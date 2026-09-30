// Package outbox delivers domain events between modules (ADR-0009, ADR-0019).
//
// A module saves its events in the same transaction as the change they
// describe (PublishTx), as River jobs: one per event. The worker role (Run)
// fans each event out — one delivery job per subscriber, in one transaction
// with marking the event done — and works the deliveries, calling each
// subscriber's handler. If the publishing transaction rolls back, the events
// vanish with it; if a handler fails, River retries that delivery alone,
// with backoff. Delivery is at least once, so handlers must be idempotent.
//
// Fanning out in the worker, not at publish time, keeps events safe while
// releases roll out: the api and the worker may run different versions for
// a while, and a subscriber either side doesn't know yet waits (see
// deliveryWorker) instead of being dropped.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	pool   *pgxpool.Pool
	client *river.Client[pgx.Tx] // inserts jobs; Run starts its own client to work them
	logger *slog.Logger

	mu       sync.RWMutex
	handlers map[string]Handler  // subscriber name → handler
	byType   map[string][]string // event type → subscriber names, in subscription order
}

// New returns a bus on pool. Subscribe everything before serving requests or
// calling Run.
func New(pool *pgxpool.Pool, logger *slog.Logger) (*Bus, error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Schema: Schema, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("outbox: %w", err)
	}
	return &Bus{pool: pool, client: client, logger: logger, handlers: map[string]Handler{}, byType: map[string][]string{}}, nil
}

// riverConns is how many pool connections River keeps for itself while it
// works: the LISTEN connection, fetching, completing, and leader duties.
const riverConns = 4

// maxWorkers is how many handlers run at once: what the pool has left after
// River's own connections, so a busy worker never waits for a connection
// while holding a job. A handler uses one connection at a time.
func maxWorkers(pool *pgxpool.Pool) int {
	return max(1, min(10, int(pool.Config().MaxConns)-riverConns))
}

// Subscribe registers handler under a unique, stable name (it is stored in
// every pending job; a renamed subscriber's queued deliveries wait a day for
// a worker that has the old name, then are dropped).
func (b *Bus) Subscribe(name, eventType string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, dup := b.handlers[name]; dup {
		panic("outbox: subscriber " + name + " registered twice")
	}
	b.handlers[name] = handler
	b.byType[eventType] = append(b.byType[eventType], name)
}

// PublishTx saves events inside tx, one job each. The worker fans them out
// to their subscribers.
func (b *Bus) PublishTx(ctx context.Context, tx pgx.Tx, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	b.mu.RLock()
	jobs := make([]river.InsertManyParams, 0, len(events))
	for _, e := range events {
		// The subscribers this release knows travel with the event, so an
		// older worker still delivers to one it doesn't have yet.
		jobs = append(jobs, river.InsertManyParams{Args: eventArgs{Event: e, Subscribers: slices.Clone(b.byType[e.Type])}})
	}
	b.mu.RUnlock()
	if _, err := b.client.InsertManyTx(ctx, tx, jobs); err != nil {
		return fmt.Errorf("outbox: publish: %w", err)
	}
	return nil
}

// Run works jobs until ctx is cancelled (SIGTERM), then stops: running
// handlers get up to stopTimeout to finish, and any still running after
// that are cancelled — River retries them later.
func (b *Bus) Run(ctx context.Context, stopTimeout time.Duration) error {
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, &eventWorker{bus: b}); err != nil {
		return err
	}
	if err := river.AddWorkerSafely(workers, &deliveryWorker{bus: b}); err != nil {
		return err
	}
	client, err := river.NewClient(riverpgxv5.New(b.pool), &river.Config{
		Schema:          Schema,
		Logger:          b.logger,
		Workers:         workers,
		Queues:          map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: maxWorkers(b.pool)}},
		SoftStopTimeout: stopTimeout, // then River cancels the handlers still running
	})
	if err != nil {
		return fmt.Errorf("outbox: %w", err)
	}
	// River would treat a cancelled Start context as a stop request; stopping
	// explicitly below keeps a SIGTERM during startup from being a startup
	// error.
	riverCtx := context.WithoutCancel(ctx)
	if err := client.Start(riverCtx); err != nil {
		return fmt.Errorf("outbox: start: %w", err)
	}
	b.logger.InfoContext(ctx, "outbox worker started")
	select {
	case <-ctx.Done():
	case <-client.Stopped():
		return errors.New("outbox: the worker stopped by itself")
	}
	// A handler that ignores its cancelled context can't be stopped; give up
	// waiting for it hardStopTimeout after the soft stop. Its job is retried
	// once River notices it was abandoned.
	stopCtx, cancel := context.WithTimeout(riverCtx, stopTimeout+hardStopTimeout)
	defer cancel()
	if err := client.Stop(stopCtx); err != nil {
		return fmt.Errorf("outbox: stop: %w", err)
	}
	b.logger.InfoContext(riverCtx, "outbox worker stopped")
	return nil
}

// hardStopTimeout bounds the wait for cancelled handlers to return.
const hardStopTimeout = 5 * time.Second

// eventArgs is one published event, not yet fanned out.
type eventArgs struct {
	Event       Event    `json:"event"`
	Subscribers []string `json:"subscribers"` // as the publishing release knew them
}

func (eventArgs) Kind() string { return "outbox_event" }

type eventWorker struct {
	river.WorkerDefaults[eventArgs]
	bus *Bus
}

// Work creates one delivery job per subscriber — the publisher's and this
// release's — and marks the event done, in one transaction: either every
// subscriber gets its delivery or the fan-out is retried.
func (w *eventWorker) Work(ctx context.Context, job *river.Job[eventArgs]) error {
	e := job.Args.Event
	w.bus.mu.RLock()
	names := slices.Clone(job.Args.Subscribers)
	for _, name := range w.bus.byType[e.Type] {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	w.bus.mu.RUnlock()
	return pgx.BeginFunc(ctx, w.bus.pool, func(tx pgx.Tx) error {
		if len(names) > 0 {
			deliveries := make([]river.InsertManyParams, 0, len(names))
			for _, name := range names {
				deliveries = append(deliveries, river.InsertManyParams{Args: deliveryArgs{Handler: name, Event: e}})
			}
			if _, err := w.bus.client.InsertManyTx(ctx, tx, deliveries); err != nil {
				return fmt.Errorf("outbox: fan out %s: %w", e.Type, err)
			}
		}
		if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
			return fmt.Errorf("outbox: fan out %s: %w", e.Type, err)
		}
		return nil
	})
}

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
	if ok {
		return handler(ctx, job.Args.Event)
	}
	// A subscriber this release doesn't have: most likely a newer release is
	// rolling out and its workers will take this delivery. Snoozing doesn't
	// use up attempts. After a day it's a subscriber that was removed.
	if time.Since(job.CreatedAt) < unknownSubscriberWait {
		return river.JobSnooze(unknownSubscriberSnooze)
	}
	w.bus.logger.WarnContext(ctx, "outbox delivery dropped: no such subscriber", slog.String("subscriber", job.Args.Handler), slog.String("event_type", job.Args.Event.Type))
	return river.JobCancel(fmt.Errorf("outbox: no subscriber %q", job.Args.Handler))
}

// How long a delivery to an unknown subscriber waits for a worker that
// knows it, checking every minute.
const (
	unknownSubscriberWait   = 24 * time.Hour
	unknownSubscriberSnooze = time.Minute
)
