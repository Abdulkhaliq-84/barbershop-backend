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
	// One job per event; the worker fans them out.
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
	// Two events, two deliveries: every job ends completed.
	eventually(t, "all jobs completed", func() bool { return countJobs(t, pool, "completed") == 4 })
}

func countJobs(t *testing.T, pool *pgxpool.Pool, state string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM river.river_job WHERE state = $1`, state).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func newBus(t *testing.T, pool *pgxpool.Pool, rec *recorder, subscribers ...string) *outbox.Bus {
	t.Helper()
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range subscribers {
		bus.Subscribe(name, "test.greeted", rec.handler(name, nil))
	}
	return bus
}

// While a release rolls out, the api and the worker run different versions.
// A subscriber only the api knows waits for a worker that has it; one only
// the worker knows gets the event too.
func TestRollingReleases(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	rec := &recorder{got: map[string][]outbox.Event{}}
	newAPI := newBus(t, pool, rec, "billing", "discovery") // discovery is new
	e, _ := outbox.NewEvent("test.greeted", t0, greeting{})
	publish(t, pool, newAPI, true, e)

	// The old worker delivers to billing, and holds discovery's delivery.
	oldWorker := newBus(t, pool, rec, "billing")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- oldWorker.Run(ctx, 5*time.Second) }()
	eventually(t, "billing's delivery, discovery's snoozed", func() bool {
		return rec.count("billing") == 1 && countJobs(t, pool, "scheduled") == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// A minute later, the new worker is up.
	if _, err := pool.Exec(t.Context(), `UPDATE river.river_job SET scheduled_at = now() WHERE state = 'scheduled'`); err != nil {
		t.Fatal(err)
	}
	newWorker := newBus(t, pool, rec, "billing", "discovery", "search") // search: newer still
	runWorker(t, newWorker)
	eventually(t, "discovery's delivery", func() bool { return rec.count("discovery") == 1 })

	// An old api's event reaches the new worker's new subscribers too
	// (search only exists from this release on: this is its first event).
	oldAPI := newBus(t, pool, rec, "billing")
	e2, _ := outbox.NewEvent("test.greeted", t0, greeting{})
	publish(t, pool, oldAPI, true, e2)
	eventually(t, "everyone's delivery of the second event", func() bool {
		return rec.count("billing") == 2 && rec.count("discovery") == 2 && rec.count("search") == 1
	})
}

// A delivery for a subscriber no release has had for a day is dropped.
func TestRemovedSubscriberIsDroppedAfterADay(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	rec := &recorder{got: map[string][]outbox.Event{}}
	oldAPI := newBus(t, pool, rec, "retired")
	e, _ := outbox.NewEvent("test.greeted", t0, greeting{})
	publish(t, pool, oldAPI, true, e)
	runWorker(t, newBus(t, pool, rec))
	eventually(t, "the snoozed delivery", func() bool { return countJobs(t, pool, "scheduled") == 1 })
	if _, err := pool.Exec(t.Context(), `UPDATE river.river_job SET created_at = now() - interval '25 hours', scheduled_at = now() WHERE state = 'scheduled'`); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the delivery cancelled", func() bool { return countJobs(t, pool, "cancelled") == 1 })
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
	err = pool.QueryRow(t.Context(), `SELECT attempt FROM river.river_job WHERE kind = 'outbox_delivery' AND args->>'handler' = 'flaky'`).Scan(&attempts)
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

// Shutdown: a stop (SIGTERM) lets running handlers finish within the
// shutdown timeout instead of cancelling them at once.
func TestRunFinishesRunningHandlersOnStop(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	started, finished := make(chan struct{}), make(chan struct{})
	bus.Subscribe("slow", "test.greeted", func(ctx context.Context, _ outbox.Event) error {
		close(started)
		select {
		case <-time.After(300 * time.Millisecond):
			close(finished)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	e, _ := outbox.NewEvent("test.greeted", t0, greeting{})
	publish(t, pool, bus, true, e)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bus.Run(ctx, 5*time.Second) }()
	<-started
	cancel() // SIGTERM while the handler runs
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("the running handler was cancelled instead of allowed to finish")
	}
	var state string
	if err := pool.QueryRow(t.Context(), `SELECT state FROM river.river_job WHERE kind = 'outbox_delivery'`).Scan(&state); err != nil || state != "completed" {
		t.Errorf("job state = %q, %v; want completed", state, err)
	}
}

// A handler still running after the shutdown timeout is cancelled; its job
// stays to be retried, and Run returns cleanly.
func TestRunCancelsHandlersAfterTheTimeout(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	bus.Subscribe("stuck", "test.greeted", func(ctx context.Context, _ outbox.Event) error {
		close(started)
		<-ctx.Done() // only a cancellation ends it
		return ctx.Err()
	})
	e, _ := outbox.NewEvent("test.greeted", t0, greeting{})
	publish(t, pool, bus, true, e)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bus.Run(ctx, 200*time.Millisecond) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	var state string
	if err := pool.QueryRow(t.Context(), `SELECT state FROM river.river_job WHERE kind = 'outbox_delivery'`).Scan(&state); err != nil || state == "completed" {
		t.Errorf("job state = %q, %v; want it left for a retry", state, err)
	}
}

// A stop that arrives while the worker is still starting is a clean stop,
// not a startup error.
func TestRunStoppedDuringStartup(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	for range 5 {
		bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := bus.Run(ctx, time.Second); err != nil {
			t.Fatalf("Run with a cancelled context: %v", err)
		}
	}
}
