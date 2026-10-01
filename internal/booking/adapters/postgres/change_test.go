package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// booked saves an appointment: pending (the shop confirms; it would expire
// an hour after t0) or confirmed.
func booked(t *testing.T, s *postgres.Store, sh shop, barber shared.StaffID, customer shared.UserID, start time.Time, pending bool) *domain.Appointment {
	t.Helper()
	name, _ := shared.NewLocalizedText("قص", "Cut")
	d, err := domain.Book(domain.Booking{
		ID: shared.NewID[domain.AppointmentTag](), Business: sh.business, Branch: sh.branch, Barber: barber, Customer: customer,
		Start: start, Buffer: 10 * time.Minute, Assignment: domain.AnyBarber,
		AutoConfirm: !pending, PendingExpiry: time.Hour, CancellationWindow: 2 * time.Hour,
		Items: []domain.Item{{Service: shared.NewID[shared.ServiceTag](), Name: name, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Book(t.Context(), attempt(uuid.New(), 1, d))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// jsonHas reports whether the JSON object has key set to the string want.
func jsonHas(t *testing.T, payload []byte, key, want string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatal(err)
	}
	return m[key] == want
}

func TestChangeAtBusiness(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	s, ev := store(pool)
	sh, barber, customer := newShop(), shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag]()
	a := booked(t, s, sh, barber, customer, t0.Add(5*time.Hour), true)
	now := t0.Add(time.Minute)

	got, err := s.ChangeAtBusiness(ctx, sh.business, a.ID(), func(a *domain.Appointment) error { return a.Confirm(now) })
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.ByCustomer(ctx, customer, a.ID())
	if err != nil {
		t.Fatal(err)
	}
	if snap := stored.Snapshot(); snap.Status != domain.StatusConfirmed || snap.PendingUntil != nil || snap.Version != 2 || !snap.UpdatedAt.Equal(now) || got.Status() != domain.StatusConfirmed {
		t.Errorf("stored = %+v", snap)
	}
	if last := ev.got[len(ev.got)-1]; last.Type != "booking.appointment_confirmed" {
		t.Errorf("event = %s", last.Type)
	}

	// Another business's appointment, or someone else's, is not found.
	if _, err := s.ChangeAtBusiness(ctx, shared.NewID[shared.BusinessTag](), a.ID(), func(*domain.Appointment) error { return nil }); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another business: %v", err)
	}
	if _, err := s.ChangeMine(ctx, shared.NewID[shared.UserTag](), a.ID(), func(*domain.Appointment) error { return nil }); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another customer: %v", err)
	}

	// A refused change saves nothing and publishes nothing.
	events := len(ev.got)
	if _, err := s.ChangeMine(ctx, customer, a.ID(), func(a *domain.Appointment) error { return a.Reject(now) }); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("rejecting a confirmed booking: %v", err)
	}
	if stored, _ := s.ByCustomer(ctx, customer, a.ID()); stored.Status() != domain.StatusConfirmed || len(ev.got) != events {
		t.Errorf("after a refusal: %s, %d events", stored.Status(), len(ev.got)-events)
	}

	// The customer cancels: who, why and when are kept, and announced.
	later := now.Add(time.Minute)
	if _, err := s.ChangeMine(ctx, customer, a.ID(), func(a *domain.Appointment) error { return a.CancelByCustomer("سفر", later) }); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.ByCustomer(ctx, customer, a.ID())
	if c := stored.Snapshot().Cancellation; c == nil || c.By != domain.ByCustomer || c.Reason != "سفر" || !c.At.Equal(later) {
		t.Errorf("cancellation = %+v", c)
	}
	if last := ev.got[len(ev.got)-1]; last.Type != "booking.appointment_cancelled" || !jsonHas(t, last.Payload, "cancelled_by", "customer") {
		t.Errorf("event = %s %s", last.Type, last.Payload)
	}
	// The deadline it was booked with comes back from storage.
	if !stored.Snapshot().CancellableUntil.Equal(a.Snapshot().CancellableUntil) {
		t.Errorf("cancellable until %s, booked with %s", stored.Snapshot().CancellableUntil, a.Snapshot().CancellableUntil)
	}
}

// A cancelled appointment no longer holds the barber's time: the same
// barber can be booked then again.
func TestCancellingFreesTheTime(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := store(migrated(t))
	sh, barber := newShop(), shared.NewID[shared.StaffTag]()
	at := t0.Add(5 * time.Hour)
	first := booked(t, s, sh, barber, shared.NewID[shared.UserTag](), at, false)

	sh.start = at
	again := attempt(uuid.New(), 1, sh.draft(t, barber, shared.NewID[shared.UserTag]()))
	if _, _, err := s.Book(ctx, again); !errors.Is(err, domain.ErrSlotUnavailable) {
		t.Fatalf("while booked: %v", err)
	}
	if _, err := s.ChangeAtBusiness(ctx, sh.business, first.ID(), func(a *domain.Appointment) error { return a.CancelByStaff("", t0) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Book(ctx, again); err != nil {
		t.Errorf("after the cancellation: %v", err)
	}
}

// Two staff acting at once take turns on the row lock: the second sees what
// the first did, and the domain refuses it.
func TestChangesTakeTurns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	s, _ := store(pool)
	sh := newShop()
	a := booked(t, s, sh, shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag](), t0.Add(5*time.Hour), true)
	queued := dbtest.OthersQueued(t, pool, 1)
	rejected := make(chan error, 1)

	_, err := s.ChangeAtBusiness(ctx, sh.business, a.ID(), func(a *domain.Appointment) error {
		go func() { // a colleague rejects it meanwhile
			_, err := s.ChangeAtBusiness(context.WithoutCancel(ctx), sh.business, a.ID(), func(a *domain.Appointment) error { return a.Reject(t0) })
			rejected <- err
		}()
		queued() // …and waits for this confirmation's lock
		return a.Confirm(t0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-rejected; !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("the second action: %v", err)
	}
}

func TestDay(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	s, _ := store(pool)
	repo := postgres.NewAppointments(pool)
	sh, ali, sara := newShop(), shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	day := t0.Truncate(24 * time.Hour) // a UTC day, for the test
	late := booked(t, s, sh, ali, shared.NewID[shared.UserTag](), day.Add(20*time.Hour), false)
	early := booked(t, s, sh, sara, shared.NewID[shared.UserTag](), day.Add(9*time.Hour), false)
	cancelled := booked(t, s, sh, ali, shared.NewID[shared.UserTag](), day.Add(12*time.Hour), false)
	if _, err := s.ChangeAtBusiness(ctx, sh.business, cancelled.ID(), func(a *domain.Appointment) error { return a.CancelByStaff("", t0) }); err != nil {
		t.Fatal(err)
	}
	booked(t, s, sh, ali, shared.NewID[shared.UserTag](), day.Add(24*time.Hour), false) // the next day
	other := newShop()                                                                  // another business's branch, same day
	booked(t, s, other, ali, shared.NewID[shared.UserTag](), day.Add(15*time.Hour), false)

	all, err := repo.Day(ctx, sh.business, sh.branch, day, day.Add(24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids := idsOf(all); !slices.Equal(ids, []domain.AppointmentID{early.ID(), cancelled.ID(), late.ID()}) {
		t.Errorf("the day = %v, want early, cancelled, late", ids)
	}
	if len(all[0].Snapshot().Items) != 1 || all[1].Status() != domain.StatusCancelled {
		t.Errorf("loaded %+v", all[0].Snapshot())
	}
	mine, err := repo.Day(ctx, sh.business, sh.branch, day, day.Add(24*time.Hour), &ali)
	if err != nil {
		t.Fatal(err)
	}
	if ids := idsOf(mine); !slices.Equal(ids, []domain.AppointmentID{cancelled.ID(), late.ID()}) {
		t.Errorf("Ali's day = %v", ids)
	}
	if none, err := repo.Day(ctx, sh.business, sh.branch, day.Add(-24*time.Hour), day, nil); err != nil || len(none) != 0 {
		t.Errorf("the day before: %d, %v", len(none), err)
	}
}

func idsOf(list []*domain.Appointment) []domain.AppointmentID {
	out := make([]domain.AppointmentID, 0, len(list))
	for _, a := range list {
		out = append(out, a.ID())
	}
	return out
}
