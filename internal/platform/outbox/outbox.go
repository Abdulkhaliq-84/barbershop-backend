// Package outbox delivers domain events between modules (ADR-0009, ADR-0019).
//
// A module saves its events in the same transaction as the change they
// describe (PublishTx), as River jobs: one job per subscriber. The worker
// role (Run) works those jobs and calls each subscriber's handler. If the
// transaction rolls back, the events vanish with it; if a handler fails,
// River retries that handler alone, with backoff. Delivery is at least once,
// so handlers must be idempotent.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Schema is where River keeps its tables.
const Schema = "river"

// Event is a domain event on its way to other modules. Payload is the
// event's JSON contract, owned by the module that publishes it.
type Event struct {
	ID         uuid.UUID       `json:"id"`
	Type       string          `json:"type"` // "<module>.<what happened>", e.g. business.approved
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// NewEvent wraps payload (a JSON-encodable struct) in an event with a fresh ID.
func NewEvent(eventType string, occurredAt time.Time, payload any) (Event, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Event{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s: %w", eventType, err)
	}
	return Event{ID: id, Type: eventType, OccurredAt: occurredAt.UTC(), Payload: body}, nil
}

// Handler reacts to one event. It may be called more than once for the
// same event (at-least-once delivery): make it idempotent.
type Handler func(ctx context.Context, e Event) error

// Bus is the outbox: subscriptions, publishing and the worker.
type Bus struct {
	client *river.Client[pgx.Tx]
	logger *slog.Logger

	mu       sync.RWMutex
	handlers map[string]Handler  // subscriber name → handler
	byType   map[string][]string // event type → subscriber names, in subscription order
}

// New returns a bus on pool. Subscribe everything before serving requests or
// calling Run: an event is fanned out to the subscribers known when it is
// published.
func New(pool *pgxpool.Pool, logger *slog.Logger) (*Bus, error) {
	b := &Bus{logger: logger, handlers: map[string]Handler{}, byType: map[string][]string{}}
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, &deliveryWorker{bus: b}); err != nil {
		return nil, err
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:  Schema,
		Logger:  logger,
		Workers: workers,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}},
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: %w", err)
	}
	b.client = client
	return b, nil
}

// Subscribe registers handler under a unique, stable name (it is stored in
// every pending job; renaming a subscriber orphans its queued deliveries).
func (b *Bus) Subscribe(name, eventType string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, dup := b.handlers[name]; dup {
		panic("outbox: subscriber " + name + " registered twice")
	}
	b.handlers[name] = handler
	b.byType[eventType] = append(b.byType[eventType], name)
}

// PublishTx saves events inside tx: one delivery job per subscriber of each
// event's type. Events nobody subscribes to are dropped here.
func (b *Bus) PublishTx(ctx context.Context, tx pgx.Tx, events ...Event) error {
	b.mu.RLock()
	var jobs []river.InsertManyParams
	for _, e := range events {
		for _, name := range b.byType[e.Type] {
			jobs = append(jobs, river.InsertManyParams{Args: deliveryArgs{Handler: name, Event: e}})
		}
	}
	b.mu.RUnlock()
	if len(jobs) == 0 {
		return nil
	}
	if _, err := b.client.InsertManyTx(ctx, tx, jobs); err != nil {
		return fmt.Errorf("outbox: publish: %w", err)
	}
	return nil
}

// Run works delivery jobs until ctx is cancelled (SIGTERM), then stops in
// two steps: running handlers get up to shutdownTimeout to finish, and any
// still running after that are cancelled — River retries them later.
func (b *Bus) Run(ctx context.Context, shutdownTimeout time.Duration) error {
	// River treats a cancelled Start context as a hard stop that cancels
	// running jobs at once, so it gets a context the signal doesn't cancel:
	// stopping is done explicitly below. This also keeps a stop that arrives
	// during startup from turning into a startup error.
	riverCtx := context.WithoutCancel(ctx)
	if err := b.client.Start(riverCtx); err != nil {
		return fmt.Errorf("outbox: start: %w", err)
	}
	b.logger.InfoContext(ctx, "outbox worker started")
	<-ctx.Done()

	soft, cancelSoft := context.WithTimeout(riverCtx, shutdownTimeout)
	defer cancelSoft()
	err := b.client.Stop(soft)
	if err == nil {
		b.logger.InfoContext(riverCtx, "outbox worker stopped")
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("outbox: stop: %w", err)
	}
	b.logger.WarnContext(riverCtx, "outbox handlers still running after the shutdown timeout; cancelling them (they will be retried)")
	hard, cancelHard := context.WithTimeout(riverCtx, hardStopTimeout)
	defer cancelHard()
	if err := b.client.StopAndCancel(hard); err != nil {
		return fmt.Errorf("outbox: cancel running handlers: %w", err)
	}
	return nil
}

// hardStopTimeout bounds the wait for cancelled handlers to return. A
// handler that ignores its context can't be stopped; the process exits
// anyway and the job is retried after River notices it was abandoned.
const hardStopTimeout = 5 * time.Second

// deliveryArgs is one event for one subscriber.
type deliveryArgs struct {
	Handler string `json:"handler"`
	Event   Event  `json:"event"`
}

func (deliveryArgs) Kind() string { return "outbox_delivery" }

type deliveryWorker struct {
	river.WorkerDefaults[deliveryArgs]
	bus *Bus
}

// Work calls the subscriber. A returned error makes River retry this
// delivery later; the other subscribers of the event are unaffected.
func (w *deliveryWorker) Work(ctx context.Context, job *river.Job[deliveryArgs]) error {
	w.bus.mu.RLock()
	handler, ok := w.bus.handlers[job.Args.Handler]
	w.bus.mu.RUnlock()
	if !ok {
		// A subscriber removed in a later release: retrying can't help.
		return river.JobCancel(fmt.Errorf("outbox: no subscriber %q", job.Args.Handler))
	}
	return handler(ctx, job.Args.Event)
}
