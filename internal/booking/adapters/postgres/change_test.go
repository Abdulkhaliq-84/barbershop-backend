package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
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

// The expiry job: due pending bookings expire, oldest first and a batch at
// a time; the others are left alone, and the time is free again.
func TestExpireDue(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	s, ev := store(pool)
	sh, ali := newShop(), shared.NewID[shared.StaffTag]()
	// booked(…, pending) expires an hour after t0.
	first := booked(t, s, sh, ali, shared.NewID[shared.UserTag](), t0.Add(5*time.Hour), true)
	second := booked(t, s, sh, ali, shared.NewID[shared.UserTag](), t0.Add(6*time.Hour), true)
	confirmed := booked(t, s, sh, ali, shared.NewID[shared.UserTag](), t0.Add(7*time.Hour), false)
	due := t0.Add(time.Hour)
	// The second has waited longer: it is due a minute earlier, so it goes first.
	if _, err := pool.Exec(ctx, `UPDATE booking.appointments SET pending_until = $2 WHERE id = $1`, second.ID().UUID(), due.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	if n, err := s.ExpireDue(ctx, due.Add(-time.Minute-time.Microsecond), 10); err != nil || n != 0 {
		t.Fatalf("before the expiry: %d, %v", n, err)
	}
	events := len(ev.got)
	if n, err := s.ExpireDue(ctx, due, 1); err != nil || n != 1 {
		t.Fatalf("a batch of one: %d, %v", n, err)
	}
	if got, err := s.ByCustomer(ctx, second.Customer(), second.ID()); err != nil {
		t.Fatal(err)
	} else if got.Status() != domain.StatusExpired {
		t.Errorf("the longest waiting first: second is %s", got.Status())
	}
	if n, err := s.ExpireDue(ctx, due, 10); err != nil || n != 1 {
		t.Fatalf("the rest: %d, %v", n, err)
	}
	for _, a := range []*domain.Appointment{first, second} {
		got, err := s.ByCustomer(ctx, a.Customer(), a.ID())
		if err != nil || got.Status() != domain.StatusExpired || got.Snapshot().PendingUntil != nil {
			t.Errorf("%s: %v, %v", a.ID(), got.Status(), err)
		}
	}
	if got, _ := s.ByCustomer(ctx, confirmed.Customer(), confirmed.ID()); got.Status() != domain.StatusConfirmed {
		t.Errorf("a confirmed booking became %s", got.Status())
	}
	if len(ev.got)-events != 2 || ev.got[len(ev.got)-1].Type != "booking.appointment_expired" {
		t.Errorf("events = %d, last %s", len(ev.got)-events, ev.got[len(ev.got)-1].Type)
	}
	if n, err := s.ExpireDue(ctx, due.Add(time.Hour), 10); err != nil || n != 0 {
		t.Errorf("again: %d, %v", n, err)
	}
	// The barber's time is free again.
	sh.start = t0.Add(5 * time.Hour)
	if _, _, err := s.Book(ctx, attempt(uuid.New(), 1, sh.draft(t, ali, shared.NewID[shared.UserTag]()))); err != nil {
		t.Errorf("booking the expired time: %v", err)
	}
}

// A booking the shop is confirming right now is skipped, not waited for:
// the run finds it again next time, if it is still pending.
func TestExpireDueSkipsLockedRows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	s, _ := store(pool)
	sh := newShop()
	a := booked(t, s, sh, shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag](), t0.Add(5*time.Hour), true)
	expired := make(chan int, 1)
	_, err := s.ChangeAtBusiness(ctx, sh.business, a.ID(), func(a *domain.Appointment) error {
		go func() {
			n, err := s.ExpireDue(context.WithoutCancel(ctx), t0.Add(2*time.Hour), 10)
			if err != nil {
				t.Error(err)
			}
			expired <- n
		}()
		if n := <-expired; n != 0 { // returned while the row was still locked
			t.Errorf("expired %d locked bookings", n)
		}
		return a.Confirm(t0)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A walk-in has no customer account: the shop's name for them is kept, the
// key is the staff member's, and no customer can reach it.
func TestWalkIn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	s, ev := store(pool)
	sh, ali, staffUser := newShop(), shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag]()
	name, _ := shared.NewLocalizedText("قص", "")
	d, err := domain.Book(domain.Booking{
		ID: shared.NewID[domain.AppointmentTag](), Business: sh.business, Branch: sh.branch, Barber: ali,
		Source: domain.SourceStaff, CustomerName: "أبو فهد", Start: sh.start, Assignment: domain.RequestedBarber, AutoConfirm: true,
		Items: []domain.Item{{Service: shared.NewID[shared.ServiceTag](), Name: name, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	walkIn := app.BookingAttempt{
		Requester: staffUser, Branch: sh.branch, Key: key, RequestHash: make32(3), Now: t0, Drafts: []*domain.Appointment{d},
		StillWorking: func(context.Context, *domain.Appointment) (bool, error) { return true, nil },
	}
	if _, _, err := s.Book(ctx, walkIn); err != nil {
		t.Fatal(err)
	}
	day, err := postgres.NewAppointments(pool).Day(ctx, sh.business, sh.branch, t0, t0.Add(24*time.Hour), nil)
	if err != nil || len(day) != 1 {
		t.Fatalf("the day: %d, %v", len(day), err)
	}
	if snap := day[0].Snapshot(); !snap.Customer.IsZero() || snap.CustomerName != "أبو فهد" || snap.Source != domain.SourceStaff {
		t.Errorf("stored = %+v", snap)
	}
	if last := ev.got[len(ev.got)-1]; jsonHas(t, last.Payload, "source", "staff") == false || strings.Contains(string(last.Payload), "customer_id") {
		t.Errorf("event = %s", last.Payload)
	}
	// The staff member's retry gets it back, by their key.
	if a, ok, err := s.Replay(ctx, staffUser, key, make32(3)); err != nil || !ok || a.ID() != d.ID() || a.Snapshot().CustomerName != "أبو فهد" {
		t.Errorf("replay: %v, %v, %v", a, ok, err)
	}
	// The database refuses an appointment with neither a customer nor a
	// walk-in's name.
	_, err = pool.Exec(ctx, `UPDATE booking.appointments SET customer_name = '' WHERE id = $1`, d.ID().UUID())
	if err == nil || !strings.Contains(err.Error(), "appointments_customer_check") {
		t.Errorf("no customer at all: %v", err)
	}
}
