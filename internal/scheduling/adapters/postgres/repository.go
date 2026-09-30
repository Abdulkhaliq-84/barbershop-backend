// Package postgres stores scheduling's calendars and schedules.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Calendars = (*Calendars)(nil)

const uniqueViolation = "23505"

// Calendars implements domain.Calendars.
type Calendars struct {
	pool *pgxpool.Pool
}

// NewCalendars returns the repository.
func NewCalendars(pool *pgxpool.Pool) *Calendars { return &Calendars{pool: pool} }

// Get returns the branch's calendar, or a new closed one.
func (r *Calendars) Get(ctx context.Context, business shared.BusinessID, branch shared.BranchID) (*domain.BranchCalendar, error) {
	q := sqlcgen.New(r.pool)
	row, err := q.CalendarByBranch(ctx, sqlcgen.CalendarByBranchParams{BusinessID: business.UUID(), BranchID: branch.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NewBranchCalendar(business, branch), nil
	}
	if err != nil {
		return nil, fmt.Errorf("load calendar: %w", err)
	}
	return loadCalendar(ctx, q, row)
}

// Update locks the calendar, checks the version, calls fn and saves the
// calendar and its hours. A calendar never saved is version 0: two first
// saves at once both see no row, and the second insert fails on the
// primary key — reported as a version conflict, like any lost race.
func (r *Calendars) Update(ctx context.Context, business shared.BusinessID, branch shared.BranchID, expectedVersion int, fn func(*domain.BranchCalendar) error) error {
	if expectedVersion < 0 || expectedVersion > math.MaxInt32 {
		return domain.ErrVersionConflict
	}
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		cal := domain.NewBranchCalendar(business, branch)
		row, err := q.CalendarForUpdate(ctx, sqlcgen.CalendarForUpdateParams{BusinessID: business.UUID(), BranchID: branch.UUID()})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// never saved: cal stays version 0
		case err != nil:
			return fmt.Errorf("lock calendar: %w", err)
		default:
			if cal, err = loadCalendar(ctx, q, row); err != nil {
				return err
			}
		}
		if cal.Version() != expectedVersion {
			return domain.ErrVersionConflict
		}
		if err := fn(cal); err != nil {
			return err
		}
		version, err := toInt32(cal.Version())
		if err != nil {
			return err
		}
		if expectedVersion == 0 {
			err = q.InsertCalendar(ctx, sqlcgen.InsertCalendarParams{BusinessID: business.UUID(), BranchID: branch.UUID(), Version: version, UpdatedAt: cal.UpdatedAt()})
		} else {
			var n int64
			n, err = q.UpdateCalendar(ctx, sqlcgen.UpdateCalendarParams{
				Version: version, UpdatedAt: cal.UpdatedAt(), BusinessID: business.UUID(), BranchID: branch.UUID(), ExpectedVersion: row.Version,
			})
			if err == nil && n == 0 {
				return domain.ErrVersionConflict
			}
		}
		if err != nil {
			return fmt.Errorf("save calendar: %w", err)
		}
		return saveHours(ctx, q, cal)
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "branch_calendars_pkey" {
		return domain.ErrVersionConflict
	}
	return err
}

func loadCalendar(ctx context.Context, q *sqlcgen.Queries, row sqlcgen.SchedulingBranchCalendar) (*domain.BranchCalendar, error) {
	rows, err := q.OpeningHours(ctx, sqlcgen.OpeningHoursParams{BusinessID: row.BusinessID, BranchID: row.BranchID})
	if err != nil {
		return nil, fmt.Errorf("load opening hours: %w", err)
	}
	intervals := make([]domain.WeeklyInterval, 0, len(rows))
	for _, h := range rows {
		intervals = append(intervals, domain.WeeklyInterval{Day: time.Weekday(h.Weekday), Start: int(h.StartMinute), Minutes: int(h.DurationMinutes)})
	}
	hours, err := domain.NewWeeklyHours(intervals)
	if err != nil {
		return nil, fmt.Errorf("stored opening hours: %w", err)
	}
	return domain.RehydrateBranchCalendar(shared.IDFromUUID[shared.BusinessTag](row.BusinessID), shared.IDFromUUID[shared.BranchTag](row.BranchID),
		hours, int(row.Version), row.UpdatedAt), nil
}

func saveHours(ctx context.Context, q *sqlcgen.Queries, cal *domain.BranchCalendar) error {
	business, branch := cal.BusinessID().UUID(), cal.BranchID().UUID()
	if err := q.DeleteOpeningHours(ctx, sqlcgen.DeleteOpeningHoursParams{BusinessID: business, BranchID: branch}); err != nil {
		return fmt.Errorf("clear opening hours: %w", err)
	}
	for _, i := range cal.OpeningHours().Intervals() {
		day, start, minutes, err := weeklyRow(i)
		if err != nil {
			return err
		}
		if err := q.InsertOpeningHour(ctx, sqlcgen.InsertOpeningHourParams{
			BusinessID: business, BranchID: branch, Weekday: day, StartMinute: start, DurationMinutes: minutes,
		}); err != nil {
			return fmt.Errorf("insert opening hours: %w", err)
		}
	}
	return nil
}

// weeklyRow converts an interval to smallints (the domain keeps them small).
func weeklyRow(i domain.WeeklyInterval) (day, start, minutes int16, err error) {
	if day, err = toInt16(int(i.Day)); err != nil {
		return 0, 0, 0, err
	}
	if start, err = toInt16(i.Start); err != nil {
		return 0, 0, 0, err
	}
	minutes, err = toInt16(i.Minutes)
	return day, start, minutes, err
}

func toInt16(n int) (int16, error) {
	if n < 0 || n > math.MaxInt16 {
		return 0, fmt.Errorf("value %d out of smallint range", n)
	}
	return int16(n), nil
}

func toInt32(n int) (int32, error) {
	if n < 0 || n > math.MaxInt32 {
		return 0, fmt.Errorf("value %d out of integer range", n)
	}
	return int32(n), nil
}
