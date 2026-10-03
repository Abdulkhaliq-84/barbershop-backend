package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// discardEvents is an outbox that publishes nothing.
type discardEvents struct{}

func (discardEvents) PublishTx(context.Context, pgx.Tx, ...outbox.Event) error { return nil }

// Adding and changing a service publish its events in the change's own
// transaction, each carrying the service as of its version; a change that
// fails publishes nothing.
func TestServiceEvents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	bus, err := outbox.New(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	listen := func(context.Context, outbox.Event) error { return nil }
	bus.Subscribe("test.created", events.TypeServiceCreated, listen)
	bus.Subscribe("test.updated", events.TypeServiceUpdated, listen)
	repo := postgres.NewServices(pool, bus)

	type published struct {
		event   outbox.Event
		payload events.ServiceChanged
		tx      string // the transaction that wrote the job
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
			if err := json.Unmarshal([]byte(args), &job); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(job.Event.Payload, &p.payload); err != nil {
				t.Fatal(err)
			}
			p.event = job.Event
			out = append(out, p)
		}
		return out
	}
	// The transaction that last wrote the service row.
	serviceTx := func(s *domain.Service) string {
		t.Helper()
		var tx string
		if err := pool.QueryRow(ctx, `SELECT xmin::text FROM catalog.services WHERE id = $1`, s.ID().UUID()).Scan(&tx); err != nil {
			t.Fatal(err)
		}
		return tx
	}

	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	s := newService(t, business, branch, 7, t0)
	if err := repo.Add(ctx, s); err != nil {
		t.Fatal(err)
	}
	got := jobs()
	if len(got) != 1 {
		t.Fatalf("after Add: %d jobs, want 1", len(got))
	}
	created := got[0]
	want := events.ServiceChanged{
		BusinessID: business.UUID(), BranchID: branch.UUID(), ServiceID: s.ID().UUID(), ChangedAt: t0,
		Service: events.Service{
			Version: 1, Active: true, CategoryCode: "haircut",
			Name:            events.LocalizedText{Ar: "قص شعر", En: "Haircut"},
			DurationMinutes: 30, Price: events.Money{Amount: 6000, Currency: "SAR"}, SortOrder: 7,
		},
	}
	if created.event.Type != events.TypeServiceCreated || !sameChange(created.payload, want) || created.tx != serviceTx(s) {
		t.Errorf("created: %s %+v in tx %s (service in %s)", created.event.Type, created.payload, created.tx, serviceTx(s))
	}

	// Two barbers, one cheaper than the service: the price from is theirs.
	cheaper := shared.Halalas(4500)
	err = repo.Update(ctx, business, branch, s.ID(), 1, func(svc *domain.Service) error {
		return svc.SetOfferings([]domain.Offering{
			{Staff: shared.NewID[shared.StaffTag]()},
			{Staff: shared.NewID[shared.StaffTag](), Price: &cheaper},
		}, t0.Add(time.Hour))
	})
	if err != nil {
		t.Fatal(err)
	}
	// A change that fails after the service changed publishes nothing.
	err = repo.Update(ctx, business, branch, s.ID(), 2, func(svc *domain.Service) error {
		if err := svc.Edit(svc.Details(), false, t0.Add(2*time.Hour)); err != nil {
			return err
		}
		return errors.New("something after the edit failed")
	})
	if err == nil || len(jobs()) != 2 {
		t.Fatalf("failed update: %v, %d jobs", err, len(jobs()))
	}
	// Turned off: still the same price from, but no longer active.
	err = repo.Update(ctx, business, branch, s.ID(), 2, func(svc *domain.Service) error {
		return svc.Edit(svc.Details(), false, t0.Add(3*time.Hour))
	})
	if err != nil {
		t.Fatal(err)
	}
	got = jobs()
	if len(got) != 3 {
		t.Fatalf("%d jobs, want 3", len(got))
	}
	offered, off := got[1], got[2]
	want.ChangedAt, want.Service.Version = t0.Add(time.Hour), 2
	want.Service.PriceFrom = &events.Money{Amount: 4500, Currency: "SAR"}
	if offered.event.Type != events.TypeServiceUpdated || !sameChange(offered.payload, want) {
		t.Errorf("offerings set: %s %+v", offered.event.Type, offered.payload)
	}
	want.ChangedAt, want.Service.Version, want.Service.Active = t0.Add(3*time.Hour), 3, false
	if off.event.Type != events.TypeServiceUpdated || !sameChange(off.payload, want) || off.tx != serviceTx(s) {
		t.Errorf("turned off: %s %+v in tx %s (service in %s)", off.event.Type, off.payload, off.tx, serviceTx(s))
	}
}

func sameChange(a, b events.ServiceChanged) bool {
	pa, pb := a.Service.PriceFrom, b.Service.PriceFrom
	a.Service.PriceFrom, b.Service.PriceFrom = nil, nil
	return a.ChangedAt.Equal(b.ChangedAt) && a.BusinessID == b.BusinessID && a.BranchID == b.BranchID &&
		a.ServiceID == b.ServiceID && a.Service == b.Service &&
		(pa == nil) == (pb == nil) && (pa == nil || *pa == *pb)
}
