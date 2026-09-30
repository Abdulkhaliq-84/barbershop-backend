package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var (
	_ domain.Schedules = (*Schedules)(nil)
	_ domain.TimeOffs  = (*TimeOffs)(nil)
)

// Schedules implements domain.Schedules.
type Schedules struct {
	pool *pgxpool.Pool
}

// NewSchedules returns the repository.
func NewSchedules(pool *pgxpool.Pool) *Schedules { return &Schedules{pool: pool} }

type scheduleKey struct {
	business, branch, staff uuid.UUID
}

func keyOf(business shared.BusinessID, branch shared.BranchID, staff shared.StaffID) scheduleKey {
	return scheduleKey{business.UUID(), branch.UUID(), staff.UUID()}
}

// Get returns the schedule, or a new empty one.
func (r *Schedules) Get(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff shared.StaffID) (*domain.BarberSchedule, error) {
	q := sqlcgen.New(r.pool)
	k := keyOf(business, branch, staff)
	row, err := q.ScheduleByKey(ctx, sqlcgen.ScheduleByKeyParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NewBarberSchedule(business, branch, staff), nil
	}
	if err != nil {
		return nil, fmt.Errorf("load schedule: %w", err)
	}
	return loadSchedule(ctx, q, k, int(row.Version), row.UpdatedAt)
}

// Update takes the person's schedule lock, locks this schedule, checks the
// version, loads their weekly hours at other branches, calls fn and saves.
func (r *Schedules) Update(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff shared.StaffID, expectedVersion int, fn func(*domain.BarberSchedule, []domain.WeeklyHours) error) error {
	if expectedVersion < 0 || expectedVersion > math.MaxInt32 {
		return domain.ErrVersionConflict
	}
	k := keyOf(business, branch, staff)
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		// One person's schedules change one at a time across branches (two
		// branches can't both give them the same hours), and never while
		// they are being booked (database.LockStaff).
		if err := database.LockStaff(ctx, tx, k.staff); err != nil {
			return fmt.Errorf("lock schedules: %w", err)
		}
		s := domain.NewBarberSchedule(business, branch, staff)
		row, err := q.ScheduleForUpdate(ctx, sqlcgen.ScheduleForUpdateParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("lock schedule: %w", err)
		default:
			if s, err = loadSchedule(ctx, q, k, int(row.Version), row.UpdatedAt); err != nil {
				return err
			}
		}
		if s.Version() != expectedVersion {
			return domain.ErrVersionConflict
		}
		elsewhere, err := hoursElsewhere(ctx, q, k)
		if err != nil {
			return err
		}
		if err := fn(s, elsewhere); err != nil {
			return err
		}
		return saveSchedule(ctx, q, k, s, expectedVersion)
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "barber_schedules_pkey" {
		return domain.ErrVersionConflict
	}
	return err
}

func loadSchedule(ctx context.Context, q *sqlcgen.Queries, k scheduleKey, version int, updatedAt time.Time) (*domain.BarberSchedule, error) {
	hourRows, err := q.BarberHours(ctx, sqlcgen.BarberHoursParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff})
	if err != nil {
		return nil, fmt.Errorf("load hours: %w", err)
	}
	intervals := make([]domain.WeeklyInterval, 0, len(hourRows))
	for _, h := range hourRows {
		intervals = append(intervals, domain.WeeklyInterval{Day: time.Weekday(h.Weekday), Start: int(h.StartMinute), Minutes: int(h.DurationMinutes)})
	}
	weekly, err := domain.NewWeeklyHours(intervals)
	if err != nil {
		return nil, fmt.Errorf("stored hours: %w", err)
	}
	overrideRows, err := q.OverrideHours(ctx, sqlcgen.OverrideHoursParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff})
	if err != nil {
		return nil, fmt.Errorf("load overrides: %w", err)
	}
	overrides := map[domain.Date][]domain.DayInterval{}
	for _, o := range overrideRows {
		date := domain.DateOf(o.OnDate)
		if _, ok := overrides[date]; !ok {
			overrides[date] = []domain.DayInterval{}
		}
		if o.StartMinute.Valid {
			overrides[date] = append(overrides[date], domain.DayInterval{Start: int(o.StartMinute.Int16), Minutes: int(o.DurationMinutes.Int16)})
		}
	}
	return domain.RehydrateBarberSchedule(shared.IDFromUUID[shared.BusinessTag](k.business), shared.IDFromUUID[shared.BranchTag](k.branch),
		shared.IDFromUUID[shared.StaffTag](k.staff), weekly, overrides, version, updatedAt), nil
}

func hoursElsewhere(ctx context.Context, q *sqlcgen.Queries, k scheduleKey) ([]domain.WeeklyHours, error) {
	rows, err := q.BarberHoursElsewhere(ctx, sqlcgen.BarberHoursElsewhereParams{BusinessID: k.business, StaffID: k.staff, BranchID: k.branch})
	if err != nil {
		return nil, fmt.Errorf("load hours at other branches: %w", err)
	}
	byBranch := map[uuid.UUID][]domain.WeeklyInterval{}
	var order []uuid.UUID
	for _, h := range rows {
		if _, seen := byBranch[h.BranchID]; !seen {
			order = append(order, h.BranchID)
		}
		byBranch[h.BranchID] = append(byBranch[h.BranchID], domain.WeeklyInterval{Day: time.Weekday(h.Weekday), Start: int(h.StartMinute), Minutes: int(h.DurationMinutes)})
	}
	out := make([]domain.WeeklyHours, 0, len(order))
	for _, b := range order {
		w, err := domain.NewWeeklyHours(byBranch[b])
		if err != nil {
			return nil, fmt.Errorf("stored hours: %w", err)
		}
		out = append(out, w)
	}
	return out, nil
}

func saveSchedule(ctx context.Context, q *sqlcgen.Queries, k scheduleKey, s *domain.BarberSchedule, expectedVersion int) error {
	version, err := toInt32(s.Version())
	if err != nil {
		return err
	}
	expected, err := toInt32(expectedVersion)
	if err != nil {
		return err
	}
	if expectedVersion == 0 {
		err = q.InsertSchedule(ctx, sqlcgen.InsertScheduleParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff, Version: version, UpdatedAt: s.UpdatedAt()})
	} else {
		var n int64
		n, err = q.UpdateSchedule(ctx, sqlcgen.UpdateScheduleParams{
			Version: version, UpdatedAt: s.UpdatedAt(), BusinessID: k.business, BranchID: k.branch, StaffID: k.staff,
			ExpectedVersion: expected,
		})
		if err == nil && n == 0 {
			return domain.ErrVersionConflict
		}
	}
	if err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}
	if err := q.DeleteBarberHours(ctx, sqlcgen.DeleteBarberHoursParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff}); err != nil {
		return fmt.Errorf("clear hours: %w", err)
	}
	for _, i := range s.Weekly().Intervals() {
		day, start, minutes, err := weeklyRow(i)
		if err != nil {
			return err
		}
		if err := q.InsertBarberHour(ctx, sqlcgen.InsertBarberHourParams{
			BusinessID: k.business, BranchID: k.branch, StaffID: k.staff, Weekday: day, StartMinute: start, DurationMinutes: minutes,
		}); err != nil {
			return fmt.Errorf("insert hours: %w", err)
		}
	}
	if err := q.DeleteOverrideHours(ctx, sqlcgen.DeleteOverrideHoursParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff}); err != nil {
		return fmt.Errorf("clear override hours: %w", err)
	}
	if err := q.DeleteOverrides(ctx, sqlcgen.DeleteOverridesParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff}); err != nil {
		return fmt.Errorf("clear overrides: %w", err)
	}
	for date, intervals := range s.Overrides() {
		if err := q.InsertOverride(ctx, sqlcgen.InsertOverrideParams{BusinessID: k.business, BranchID: k.branch, StaffID: k.staff, OnDate: date.AsTime()}); err != nil {
			return fmt.Errorf("insert override: %w", err)
		}
		for _, i := range intervals {
			start, err := toInt16(i.Start)
			if err != nil {
				return err
			}
			minutes, err := toInt16(i.Minutes)
			if err != nil {
				return err
			}
			if err := q.InsertOverrideHour(ctx, sqlcgen.InsertOverrideHourParams{
				BusinessID: k.business, BranchID: k.branch, StaffID: k.staff, OnDate: date.AsTime(), StartMinute: start, DurationMinutes: minutes,
			}); err != nil {
				return fmt.Errorf("insert override hours: %w", err)
			}
		}
	}
	return nil
}

// TimeOffs implements domain.TimeOffs.
type TimeOffs struct {
	pool *pgxpool.Pool
}

// NewTimeOffs returns the repository.
func NewTimeOffs(pool *pgxpool.Pool) *TimeOffs { return &TimeOffs{pool: pool} }

const exclusionViolation = "23P01"

// Add saves time off; the exclusion constraint refuses an overlap.
func (r *TimeOffs) Add(ctx context.Context, t *domain.TimeOff) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		// Not while they are being booked: a booking checks their hours
		// under the same lock (database.LockStaff).
		if err := database.LockStaff(ctx, tx, t.StaffID().UUID()); err != nil {
			return err
		}
		return sqlcgen.New(tx).InsertTimeOff(ctx, sqlcgen.InsertTimeOffParams{
			ID: t.ID().UUID(), BusinessID: t.BusinessID().UUID(), StaffID: t.StaffID().UUID(),
			StartsAt: t.Span().Start(), EndsAt: t.Span().End(), Reason: t.Reason(), CreatedAt: t.CreatedAt(),
		})
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == exclusionViolation && pgErr.ConstraintName == "time_off_no_overlap" {
		return domain.ErrTimeOffOverlaps
	}
	if err != nil {
		return fmt.Errorf("insert time off: %w", err)
	}
	return nil
}

// List returns the person's time off ending after since, by start.
func (r *TimeOffs) List(ctx context.Context, business shared.BusinessID, staff shared.StaffID, since time.Time) ([]*domain.TimeOff, error) {
	rows, err := sqlcgen.New(r.pool).TimeOffByStaff(ctx, sqlcgen.TimeOffByStaffParams{BusinessID: business.UUID(), StaffID: staff.UUID(), EndsAt: since})
	if err != nil {
		return nil, fmt.Errorf("list time off: %w", err)
	}
	out := make([]*domain.TimeOff, 0, len(rows))
	for _, row := range rows {
		span, err := shared.NewInterval(row.StartsAt, row.EndsAt)
		if err != nil {
			return nil, fmt.Errorf("stored time off: %w", err)
		}
		out = append(out, domain.RehydrateTimeOff(shared.IDFromUUID[domain.TimeOffTag](row.ID), business, staff, span, row.Reason, row.CreatedAt))
	}
	return out, nil
}

// Delete removes one, or ErrNotFound.
func (r *TimeOffs) Delete(ctx context.Context, business shared.BusinessID, staff shared.StaffID, id domain.TimeOffID) error {
	n, err := sqlcgen.New(r.pool).DeleteTimeOff(ctx, sqlcgen.DeleteTimeOffParams{BusinessID: business.UUID(), StaffID: staff.UUID(), ID: id.UUID()})
	if err != nil {
		return fmt.Errorf("delete time off: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
