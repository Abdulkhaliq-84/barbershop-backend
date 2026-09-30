package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Approving publishes business.approved in the approval's own transaction:
// a failed approval publishes nothing.
func TestApprovalPublishesItsEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migratedDB(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	bus.Subscribe("test.listener", events.TypeBusinessApproved, func(context.Context, outbox.Event) error { return nil })
	store := postgres.NewStore(pool, bus)
	id := registered(t, store, "1010123456")
	makeReady(t, store, id)
	if err := submit(t, store, id, 1, t0); err != nil {
		t.Fatal(err)
	}
	jobs := func() []string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT args::text FROM river.river_job ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	if n := len(jobs()); n != 0 {
		t.Fatalf("jobs after submit = %d, want 0 (nobody listens to submissions)", n)
	}

	admin := shared.NewID[shared.UserTag]()
	approve := func(version int) error {
		return store.Update(ctx, id, version, func(b *domain.Business) error { return b.Approve(admin, t0.Add(time.Hour)) })
	}
	// A stale version is refused before anything is published.
	if err := approve(1); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale approve: %v", err)
	}
	// An approval whose transaction fails later publishes nothing either.
	err = store.Update(ctx, id, 2, func(b *domain.Business) error {
		if err := b.Approve(admin, t0.Add(time.Hour)); err != nil {
			return err
		}
		return errors.New("something after the approval failed")
	})
	if err == nil || len(jobs()) != 0 {
		t.Fatalf("failed approval: err=%v jobs=%d", err, len(jobs()))
	}

	if err := approve(2); err != nil {
		t.Fatal(err)
	}
	got := jobs()
	if len(got) != 1 {
		t.Fatalf("jobs = %v, want one event", got)
	}
	var job struct {
		Subscribers []string     `json:"subscribers"`
		Event       outbox.Event `json:"event"`
	}
	var payload events.BusinessApproved
	if err := json.Unmarshal([]byte(got[0]), &job); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(job.Event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	// Same transaction, proven by Postgres itself: xmin is the ID of the
	// transaction that wrote the row.
	var businessTx, jobTx string
	if err := pool.QueryRow(ctx, `SELECT xmin::text FROM business.businesses WHERE id = $1`, id.UUID()).Scan(&businessTx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT xmin::text FROM river.river_job`).Scan(&jobTx); err != nil {
		t.Fatal(err)
	}
	if businessTx != jobTx {
		t.Errorf("approval written by transaction %s, its event by %s: they must commit together", businessTx, jobTx)
	}
	b, _ := store.ByID(ctx, id)
	if !slices.Equal(job.Subscribers, []string{"test.listener"}) || job.Event.Type != "business.approved" || payload.BusinessID != id.UUID() ||
		payload.OwnerID != b.OwnerID().UUID() || !payload.ApprovedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("job = %+v, payload = %+v", job, payload)
	}
}
