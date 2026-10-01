package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func hours(t *testing.T, intervals ...domain.WeeklyInterval) domain.WeeklyHours {
	t.Helper()
	w, err := domain.NewWeeklyHours(intervals)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func set(repo *postgres.Calendars, business shared.BusinessID, branch shared.BranchID, version int, w domain.WeeklyHours) error {
	return repo.Update(context.Background(), business, branch, version, func(c *domain.BranchCalendar) error {
		c.SetOpeningHours(w, t0)
		return nil
	})
}

func TestCalendarLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewCalendars(migrated(t), discardEvents{})
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()

	// Never set: closed, version 0.
	cal, err := repo.Get(ctx, business, branch)
	if err != nil || cal.Version() != 0 || !cal.OpeningHours().IsClosed() || !cal.UpdatedAt().IsZero() {
		t.Fatalf("new calendar = %+v, %v", cal, err)
	}
	week := hours(t,
		domain.WeeklyInterval{Day: time.Thursday, Start: 16 * 60, Minutes: 600},
		domain.WeeklyInterval{Day: time.Sunday, Start: 9 * 60, Minutes: 180},
		domain.WeeklyInterval{Day: time.Sunday, Start: 16 * 60, Minutes: 360},
	)
	if err := set(repo, business, branch, 1, week); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("first save with version 1: %v", err)
	}
	if err := set(repo, business, branch, 0, week); err != nil {
		t.Fatal(err)
	}
	cal, err = repo.Get(ctx, business, branch)
	if err != nil || cal.Version() != 1 || !cal.UpdatedAt().Equal(t0) {
		t.Fatalf("saved = %+v, %v", cal, err)
	}
	got, want := cal.OpeningHours().Intervals(), week.Intervals()
	if len(got) != len(want) {
		t.Fatalf("hours = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("interval %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Replace; stale versions are refused; closing everything is allowed.
	if err := set(repo, business, branch, 0, week); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("second first-save: %v", err)
	}
	if err := set(repo, business, branch, 1, domain.WeeklyHours{}); err != nil {
		t.Fatal(err)
	}
	cal, _ = repo.Get(ctx, business, branch)
	if cal.Version() != 2 || !cal.OpeningHours().IsClosed() {
		t.Errorf("after closing = %+v", cal)
	}
	for _, v := range []int{-1, 1 << 40} {
		if err := set(repo, business, branch, v, week); !errors.Is(err, domain.ErrVersionConflict) {
			t.Errorf("version %d: %v", v, err)
		}
	}
	// Another branch, or the same branch under another business, is untouched.
	if other, _ := repo.Get(ctx, shared.NewID[shared.BusinessTag](), branch); other.Version() != 0 {
		t.Error("calendar found under another business")
	}
}

// Two first saves at once: one wins, the other is a version conflict.
func TestParallelFirstSaves(t *testing.T) {
	t.Parallel()
	repo := postgres.NewCalendars(migrated(t), discardEvents{})
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	week := hours(t, domain.WeeklyInterval{Day: time.Monday, Start: 540, Minutes: 60})
	var (
		wg               sync.WaitGroup
		start            = make(chan struct{})
		saved, conflicts atomic.Int32
		reading          sync.WaitGroup // all four have seen "no hours yet" before anyone saves
		allRead          = make(chan struct{})
	)
	reading.Add(4)
	go func() { reading.Wait(); close(allRead) }()
	for range 4 {
		wg.Go(func() {
			<-start
			err := repo.Update(t.Context(), business, branch, 0, func(c *domain.BranchCalendar) error {
				// No row, nothing to lock: the four collide only when they
				// insert. Make sure they all get that far together.
				reading.Done()
				select {
				case <-allRead:
				case <-time.After(10 * time.Second):
					t.Error("the four first saves never overlapped")
				}
				c.SetOpeningHours(week, t0)
				return nil
			})
			switch {
			case err == nil:
				saved.Add(1)
			case errors.Is(err, domain.ErrVersionConflict):
				conflicts.Add(1)
			default:
				t.Errorf("Update: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if saved.Load() != 1 || conflicts.Load() != 3 {
		t.Fatalf("saved=%d conflicts=%d, want 1 and 3", saved.Load(), conflicts.Load())
	}
}
