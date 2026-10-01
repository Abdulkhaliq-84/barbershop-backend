package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func setSchedule(repo *postgres.Schedules, business shared.BusinessID, branch shared.BranchID, staff shared.StaffID, version int,
	weekly domain.WeeklyHours, overrides map[domain.Date][]domain.DayInterval,
) error {
	return repo.Update(context.Background(), business, branch, staff, version, func(s *domain.BarberSchedule, elsewhere []domain.WeeklyHours) error {
		return s.Set(weekly, overrides, elsewhere, t0)
	})
}

func TestScheduleRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewSchedules(migrated(t))
	business, branch, staff := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), shared.NewID[shared.StaffTag]()

	s, err := repo.Get(ctx, business, branch, staff)
	if err != nil || s.Version() != 0 || !s.Weekly().IsClosed() || len(s.Overrides()) != 0 {
		t.Fatalf("new schedule = %+v, %v", s, err)
	}
	weekly := hours(t, domain.WeeklyInterval{Day: time.Thursday, Start: 16 * 60, Minutes: 600}, domain.WeeklyInterval{Day: time.Sunday, Start: 540, Minutes: 480})
	dayOff, _ := domain.ParseDate("2026-10-08")
	short, _ := domain.ParseDate("2026-10-11")
	overrides := map[domain.Date][]domain.DayInterval{dayOff: {}, short: {{Start: 600, Minutes: 120}, {Start: 780, Minutes: 60}}}
	if err := setSchedule(repo, business, branch, staff, 0, weekly, overrides); err != nil {
		t.Fatal(err)
	}
	s, err = repo.Get(ctx, business, branch, staff)
	if err != nil || s.Version() != 1 || !s.UpdatedAt().Equal(t0) {
		t.Fatalf("saved = %+v, %v", s, err)
	}
	if got := s.Weekly().Intervals(); len(got) != 2 || got[0].Day != time.Sunday || got[1].Minutes != 600 {
		t.Errorf("weekly = %+v", got)
	}
	got := s.Overrides()
	if off, ok := got[dayOff]; !ok || len(off) != 0 {
		t.Errorf("day off = %+v (present %v)", off, ok)
	}
	if o := got[short]; len(o) != 2 || o[0].Start != 600 || o[1].Start != 780 {
		t.Errorf("short day = %+v", o)
	}
	// Replacing drops the old overrides; stale versions are refused.
	if err := setSchedule(repo, business, branch, staff, 0, weekly, nil); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale: %v", err)
	}
	if err := setSchedule(repo, business, branch, staff, 1, weekly, nil); err != nil {
		t.Fatal(err)
	}
	if s, _ := repo.Get(ctx, business, branch, staff); len(s.Overrides()) != 0 || s.Version() != 2 {
		t.Errorf("after replace = %+v", s.Overrides())
	}
}

// One person can't be scheduled at two branches at the same time.
func TestScheduleClashAcrossBranches(t *testing.T) {
	t.Parallel()
	repo := postgres.NewSchedules(migrated(t))
	business, a, b, staff := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), shared.NewID[shared.BranchTag](), shared.NewID[shared.StaffTag]()
	if err := setSchedule(repo, business, a, staff, 0, hours(t, domain.WeeklyInterval{Day: time.Thursday, Start: 16 * 60, Minutes: 600}), nil); err != nil {
		t.Fatal(err)
	}
	// Friday 01:00 is inside Thursday's night shift at branch a.
	if err := setSchedule(repo, business, b, staff, 0, hours(t, domain.WeeklyInterval{Day: time.Friday, Start: 60, Minutes: 60}), nil); !errors.Is(err, domain.ErrScheduleClash) {
		t.Fatalf("clash: %v", err)
	}
	if err := setSchedule(repo, business, b, staff, 0, hours(t, domain.WeeklyInterval{Day: time.Friday, Start: 120, Minutes: 60}), nil); err != nil {
		t.Fatalf("right after the shift: %v", err)
	}
	// Another person at the same hours is fine.
	if err := setSchedule(repo, business, b, shared.NewID[shared.StaffTag](), 0, hours(t, domain.WeeklyInterval{Day: time.Friday, Start: 60, Minutes: 60}), nil); err != nil {
		t.Errorf("another person: %v", err)
	}
}

func TestTimeOff(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewTimeOffs(migrated(t))
	business, staff := shared.NewID[shared.BusinessTag](), shared.NewID[shared.StaffTag]()
	add := func(who shared.StaffID, from, to time.Time) (*domain.TimeOff, error) {
		off, err := domain.NewTimeOff(shared.NewID[domain.TimeOffTag](), business, who, from, to, "إجازة", t0)
		if err != nil {
			t.Fatal(err)
		}
		return off, repo.Add(ctx, off)
	}
	day := time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC)
	first, err := add(staff, day, day.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := add(staff, day.Add(time.Hour), day.Add(3*time.Hour)); !errors.Is(err, domain.ErrTimeOffOverlaps) {
		t.Fatalf("overlap: %v", err)
	}
	if _, err := add(staff, day.Add(2*time.Hour), day.Add(3*time.Hour)); err != nil {
		t.Fatalf("touching: %v", err)
	}
	if _, err := add(shared.NewID[shared.StaffTag](), day, day.Add(2*time.Hour)); err != nil {
		t.Fatalf("someone else, same time: %v", err)
	}
	list, err := repo.List(ctx, business, staff, day.Add(-time.Hour))
	if err != nil || len(list) != 2 || list[0].ID() != first.ID() || list[0].Reason() != "إجازة" || !list[0].Span().Start().Equal(day) {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if list, _ := repo.List(ctx, business, staff, day.Add(2*time.Hour)); len(list) != 1 {
		t.Errorf("since the first ended: %d", len(list))
	}
	// Deleting is scoped by business and staff.
	if err := repo.Delete(ctx, shared.NewID[shared.BusinessTag](), staff, first.ID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete from another business: %v", err)
	}
	if err := repo.Delete(ctx, business, staff, first.ID()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, business, staff, first.ID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

// A booking checks a barber's hours holding database.LockStaff: changing
// their schedule or adding their time off waits until it commits, so no
// appointment lands in hours that were just taken away.
func TestChangesWaitForBookings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	schedules, timeOffs := postgres.NewSchedules(pool), postgres.NewTimeOffs(pool)
	business, branch, staff := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), shared.NewID[shared.StaffTag]()
	off, err := domain.NewTimeOff(shared.NewID[domain.TimeOffTag](), business, staff, t0.Add(24*time.Hour), t0.Add(26*time.Hour), "", t0)
	if err != nil {
		t.Fatal(err)
	}
	queued := dbtest.OthersQueued(t, pool, 2)
	changes := make(chan error, 2)
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { // the booking
		if err := database.LockStaff(ctx, tx, staff.UUID()); err != nil {
			return err
		}
		go func() {
			changes <- setSchedule(schedules, business, branch, staff, 0, hours(t, domain.WeeklyInterval{Day: time.Thursday, Start: 9 * 60, Minutes: 60}), nil)
		}()
		go func() { changes <- timeOffs.Add(ctx, off) }()
		queued() // both wait for the booking
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-changes; err != nil {
			t.Error(err)
		}
	}
}
