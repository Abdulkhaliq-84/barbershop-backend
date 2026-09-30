package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

type greeting struct {
	Name string `json:"name"`
}

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return pool
}

// recorder collects what each subscriber received.
type recorder struct {
	mu  sync.Mutex
	got map[string][]outbox.Event
}

func (r *recorder) handler(name string, fail func(attempt int) error) outbox.Handler {
	return func(_ context.Context, e outbox.Event) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.got[name] = append(r.got[name], e)
		if fail != nil {
			return fail(len(r.got[name]))
		}
		return nil
	}
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got[name])
}

// eventually waits up to 15 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func publish(t *testing.T, pool *pgxpool.Pool, bus *outbox.Bus, commit bool, events ...outbox.Event) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.PublishTx(t.Context(), tx, events...); err != nil {
		t.Fatal(err)
	}
	if commit {
		err = tx.Commit(t.Context())
	} else {
		err = tx.Rollback(t.Context())
	}
	if err != nil {
		t.Fatal(err)
	}
}

func jobs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM river.river_job`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func runWorker(t *testing.T, bus *outbox.Bus) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bus.Run(ctx, 5*time.Second) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
}

func TestPublishAndDeliver(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{got: map[string][]outbox.Event{}}
	bus.Subscribe("a.greet", "test.greeted", rec.handler("a.greet", nil))
	bus.Subscribe("b.greet", "test.greeted", rec.handler("b.greet", nil))
	bus.Subscribe("c.other", "test.other", rec.handler("c.other", nil))

	hello, err := outbox.NewEvent("test.greeted", t0, greeting{Name: "سعد"})
	if err != nil {
		t.Fatal(err)
	}
	ignored, _ := outbox.NewEvent("test.nobody_listens", t0, greeting{})
	publish(t, pool, bus, true, hello, ignored)
	// One job per subscriber of each event; an event nobody listens to makes none.
	if n := jobs(t, pool); n != 2 {
		t.Fatalf("jobs = %d, want 2", n)
	}
	// A rolled-back transaction takes its events with it.
	lost, _ := outbox.NewEvent("test.greeted", t0, greeting{Name: "rolled back"})
	publish(t, pool, bus, false, lost)
	if n := jobs(t, pool); n != 2 {
		t.Fatalf("jobs after rollback = %d, want 2", n)
	}

	runWorker(t, bus)
	eventually(t, "both deliveries", func() bool { return rec.count("a.greet") == 1 && rec.count("b.greet") == 1 })
	got := rec.got["a.greet"][0]
	var g greeting
	if err := json.Unmarshal(got.Payload, &g); err != nil || got.ID != hello.ID || got.Type != "test.greeted" || !got.OccurredAt.Equal(t0) || g.Name != "سعد" {
		t.Errorf("delivered %+v (%v), want %+v", got, err, hello)
	}
	if rec.count("c.other") != 0 {
		t.Error("a subscriber got another type's event")
	}
}

func TestFailedDeliveryIsRetriedAlone(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{got: map[string][]outbox.Event{}}
	bus.Subscribe("flaky", "test.greeted", rec.handler("flaky", func(attempt int) error {
		if attempt == 1 {
			return errors.New("downstream unavailable")
		}
		return nil
	}))
	bus.Subscribe("steady", "test.greeted", rec.handler("steady", nil))
	e, _ := outbox.NewEvent("test.greeted", t0, greeting{Name: "x"})
	publish(t, pool, bus, true, e)
	runWorker(t, bus)

	// River retries the failed delivery after a backoff (about a second for
	// the first retry); the steady subscriber runs exactly once.
	eventually(t, "the retry", func() bool { return rec.count("flaky") == 2 })
	if rec.count("steady") != 1 {
		t.Errorf("steady ran %d times, want 1", rec.count("steady"))
	}
	var attempts int
	err = pool.QueryRow(t.Context(), `SELECT attempt FROM river.river_job WHERE args->>'handler' = 'flaky'`).Scan(&attempts)
	if err != nil || attempts != 2 {
		t.Errorf("attempts = %d, %v", attempts, err)
	}
}

func TestSubscribeTwicePanics(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	bus.Subscribe("x", "test.greeted", func(context.Context, outbox.Event) error { return nil })
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	bus.Subscribe("x", "test.other", func(context.Context, outbox.Event) error { return nil })
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	if err := outbox.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_tables WHERE schemaname = 'river' AND tablename = 'river_job'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("river_job tables = %d, %v", tables, err)
	}
}
