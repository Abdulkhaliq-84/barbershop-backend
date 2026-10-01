package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// discardEvents is an outbox that publishes nothing.
type discardEvents struct{}

func (discardEvents) PublishTx(context.Context, pgx.Tx, ...outbox.Event) error { return nil }

// Setting the hours publishes the event in the change's own transaction,
// with the hours as of the new version; a change that fails publishes
// nothing.
func TestOpeningHoursPublished(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	bus.Subscribe("test.listener", events.TypeOpeningHoursChanged, func(context.Context, outbox.Event) error { return nil })
	repo := postgres.NewCalendars(pool, bus)
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()

	type published struct {
		payload events.OpeningHoursChanged
		tx      string
	}
	jobs := func() []published {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT args::text, xmin::text FROM river.river_job ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []published
		for rows.Next() {
			var args string
			var p published
			if err := rows.Scan(&args, &p.tx); err != nil {
				t.Fatal(err)
			}
			var job struct {
				Event outbox.Event `json:"event"`
			}
			if err := json.Unmarshal([]byte(args), &job); err != nil || job.Event.Type != events.TypeOpeningHoursChanged {
				t.Fatalf("job %s: %v", args, err)
			}
			if err := json.Unmarshal(job.Event.Payload, &p.payload); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return out
	}

	// Thursday 16:00 for ten hours, and Saturday from 22:00 into Sunday.
	week := hours(t,
		domain.WeeklyInterval{Day: time.Thursday, Start: 16 * 60, Minutes: 600},
		domain.WeeklyInterval{Day: time.Saturday, Start: 22 * 60, Minutes: 240},
	)
	if err := set(repo, business, branch, 0, week); err != nil {
		t.Fatal(err)
	}
	// A change that fails after the hours were set publishes nothing.
	err = repo.Update(ctx, business, branch, 1, func(c *domain.BranchCalendar) error {
		c.SetOpeningHours(domain.WeeklyHours{}, t0)
		return errors.New("something after the change failed")
	})
	if err == nil {
		t.Fatal("the failing change succeeded")
	}
	if err := set(repo, business, branch, 1, domain.WeeklyHours{}); err != nil {
		t.Fatal(err)
	}
	got := jobs()
	if len(got) != 2 {
		t.Fatalf("%d jobs, want 2", len(got))
	}
	first, closed := got[0].payload, got[1].payload
	wantHours := []events.Interval{{Weekday: 4, StartMinute: 960, Minutes: 600}, {Weekday: 6, StartMinute: 1320, Minutes: 240}}
	if first.BusinessID != business.UUID() || first.BranchID != branch.UUID() || !first.ChangedAt.Equal(t0) ||
		first.Calendar.Version != 1 || !slices.Equal(first.Calendar.Hours, wantHours) {
		t.Errorf("first = %+v", first)
	}
	if closed.Calendar.Version != 2 || closed.Calendar.Hours == nil || len(closed.Calendar.Hours) != 0 {
		t.Errorf("closed = %+v", closed)
	}
	var calendarTx string
	if err := pool.QueryRow(ctx, `SELECT xmin::text FROM scheduling.branch_calendars WHERE branch_id = $1`, branch.UUID()).Scan(&calendarTx); err != nil {
		t.Fatal(err)
	}
	if got[1].tx != calendarTx {
		t.Errorf("calendar written by transaction %s, its event by %s: they must commit together", calendarTx, got[1].tx)
	}
}
