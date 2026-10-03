package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// appointment is a customer's booking at a branch, starting at starts.
func appointment(starts time.Time, status domain.AppointmentStatus, confirmed time.Time) domain.Appointment {
	return domain.Appointment{
		ID: shared.NewID[shared.AppointmentTag](), Customer: shared.NewID[shared.UserTag](), Branch: shared.NewID[shared.BranchTag](),
		StartsAt: starts, Status: status, ConfirmedAt: confirmed,
	}
}

// Events arrive twice and in any order: a status only moves forward, and
// the first confirmation's time stays.
func TestKeepAppointment(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()
	a := appointment(t0.Add(26*time.Hour), domain.AppointmentPending, time.Time{})
	if _, err := s.Appointment(ctx, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("before any event: %v, want ErrNotFound", err)
	}
	confirmed := a
	confirmed.Status, confirmed.ConfirmedAt = domain.AppointmentConfirmed, t0
	again := confirmed
	again.ConfirmedAt = t0.Add(time.Hour) // the same confirmation, delivered again later
	closed := a
	closed.Status, closed.ConfirmedAt = domain.AppointmentClosed, t0.Add(2*time.Hour) // a later time: the first stays
	for _, c := range []struct {
		name   string
		event  domain.Appointment
		status domain.AppointmentStatus
	}{
		{"booked", a, domain.AppointmentPending},
		{"confirmed", confirmed, domain.AppointmentConfirmed},
		{"confirmed again", again, domain.AppointmentConfirmed},
		{"pending, late", a, domain.AppointmentConfirmed},
		{"cancelled", closed, domain.AppointmentClosed},
		{"confirmed, late", confirmed, domain.AppointmentClosed},
	} {
		if err := s.KeepAppointment(ctx, c.event); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, err := s.Appointment(ctx, a.ID)
		if err != nil || got.Status != c.status {
			t.Errorf("%s: %+v, %v; want %s", c.name, got, err, c.status)
		}
	}
	got, _ := s.Appointment(ctx, a.ID)
	if !got.ConfirmedAt.Equal(t0) || got.Customer != a.Customer || got.Branch != a.Branch || !got.StartsAt.Equal(a.StartsAt) {
		t.Errorf("kept %+v; want the first confirmation's time and the booking's details", got)
	}

	// Confirmed at once (booked by the shop, or a branch that confirms
	// itself): confirmed from the start.
	direct := appointment(t0.Add(26*time.Hour), domain.AppointmentConfirmed, t0)
	if err := s.KeepAppointment(ctx, direct); err != nil {
		t.Fatal(err)
	}
	// The table keeps the rules: a confirmed copy has its time, a pending
	// one has none.
	if err := s.KeepAppointment(ctx, appointment(t0, domain.AppointmentConfirmed, time.Time{})); err == nil {
		t.Error("confirmed with no confirmation time was kept")
	}
	if err := s.KeepAppointment(ctx, appointment(t0, domain.AppointmentPending, t0)); err == nil {
		t.Error("pending with a confirmation time was kept")
	}
}

// reminders lists the queued reminder events, oldest first.
func reminders(t *testing.T, pool *pgxpool.Pool) []events.ReminderDue {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT args->'event'->'payload' FROM river.river_job
		WHERE kind = 'outbox_event' AND args->'event'->>'type' = $1 ORDER BY id`, events.TypeReminderDue)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (events.ReminderDue, error) {
		var raw []byte
		if err := r.Scan(&raw); err != nil {
			return events.ReminderDue{}, err
		}
		var e events.ReminderDue
		return e, json.Unmarshal(raw, &e)
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClaimReminders(t *testing.T) {
	s, pool := newStore(t)
	ctx := t.Context()
	now := t0
	longAgo := now.Add(-48 * time.Hour)
	due := map[string]domain.Appointment{
		"in half an hour":            appointment(now.Add(30*time.Minute), domain.AppointmentConfirmed, longAgo),
		"in exactly an hour":         appointment(now.Add(time.Hour), domain.AppointmentConfirmed, longAgo),
		"confirmed exactly 1h ahead": appointment(now.Add(50*time.Minute), domain.AppointmentConfirmed, now.Add(-10*time.Minute)),
		"in a minute":                appointment(now.Add(time.Minute), domain.AppointmentConfirmed, longAgo),
	}
	notDue := map[string]domain.Appointment{
		"in an hour and a minute": appointment(now.Add(61*time.Minute), domain.AppointmentConfirmed, longAgo),
		"started":                 appointment(now, domain.AppointmentConfirmed, longAgo),
		"started a minute ago":    appointment(now.Add(-time.Minute), domain.AppointmentConfirmed, longAgo),
		"booked within the hour":  appointment(now.Add(40*time.Minute), domain.AppointmentConfirmed, now.Add(-10*time.Minute)),
		"pending":                 appointment(now.Add(30*time.Minute), domain.AppointmentPending, time.Time{}),
		"cancelled":               appointment(now.Add(30*time.Minute), domain.AppointmentClosed, longAgo),
	}
	for name, a := range due {
		if err := s.KeepAppointment(ctx, a); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, a := range notDue {
		if err := s.KeepAppointment(ctx, a); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Closed after being confirmed: the copy is closed, not due.
	cancelled := notDue["cancelled"]
	cancelled.Status = domain.AppointmentConfirmed
	if err := s.KeepAppointment(ctx, cancelled); err != nil {
		t.Fatal(err)
	}

	// A batch of 3, then the rest, then nothing: each queued once.
	if n, err := s.ClaimReminders(ctx, now, 3); err != nil || n != 3 {
		t.Fatalf("first batch: %d, %v; want 3", n, err)
	}
	if n, err := s.ClaimReminders(ctx, now, 3); err != nil || n != 1 {
		t.Fatalf("second batch: %d, %v; want 1", n, err)
	}
	if n, err := s.ClaimReminders(ctx, now, 3); err != nil || n != 0 {
		t.Fatalf("again: %d, %v; want nothing left", n, err)
	}
	queued := reminders(t, pool)
	if len(queued) != len(due) {
		t.Fatalf("%d reminders queued, want %d", len(queued), len(due))
	}
	byID := map[uuid.UUID]events.ReminderDue{}
	for _, r := range queued {
		byID[r.AppointmentID] = r
	}
	for name, a := range due {
		r, ok := byID[a.ID.UUID()]
		if !ok || r.CustomerID != a.Customer.UUID() || r.BranchID != a.Branch.UUID() || !r.StartsAt.Equal(a.StartsAt) {
			t.Errorf("%s: queued %+v (%v)", name, r, ok)
		}
	}
	// Soonest first, so a backlog reminds the most urgent.
	if !slices.IsSortedFunc(queued[:3], func(a, b events.ReminderDue) int { return a.StartsAt.Compare(b.StartsAt) }) {
		t.Errorf("first batch not by start: %+v", queued[:3])
	}

	// Ten minutes later, the one starting in an hour and a minute is due.
	if n, err := s.ClaimReminders(ctx, now.Add(10*time.Minute), 3); err != nil || n != 1 {
		t.Errorf("ten minutes later: %d, %v; want 1", n, err)
	}
	for _, l := range []int{0, -1} {
		if _, err := s.ClaimReminders(ctx, now, l); err == nil {
			t.Errorf("limit %d: want an error", l)
		}
	}
}

// failingPublisher can't queue anything.
type failingPublisher struct{}

func (failingPublisher) PublishTx(context.Context, pgx.Tx, ...outbox.Event) error {
	return errors.New("outbox down")
}

// Marking and queueing are one transaction: if queueing fails, nothing is
// marked, and the next run tries again.
func TestClaimRemindersAllOrNothing(t *testing.T) {
	good, pool := newStore(t)
	broken := postgres.NewStore(pool, failingPublisher{})
	ctx := t.Context()
	a := appointment(t0.Add(30*time.Minute), domain.AppointmentConfirmed, t0.Add(-24*time.Hour))
	if err := good.KeepAppointment(ctx, a); err != nil {
		t.Fatal(err)
	}
	if n, err := broken.ClaimReminders(ctx, t0, 10); err == nil || n != 0 {
		t.Errorf("outbox down: %d, %v; want an error", n, err)
	}
	if n, err := good.ClaimReminders(ctx, t0, 10); err != nil || n != 1 || len(reminders(t, pool)) != 1 {
		t.Errorf("next run: %d, %v; want the reminder queued now", n, err)
	}
}

// Workers claiming at once never queue the same reminder twice.
func TestClaimRemindersConcurrently(t *testing.T) {
	s, pool := newStore(t)
	ctx := t.Context()
	for i := range 40 {
		a := appointment(t0.Add(time.Duration(i+1)*time.Minute), domain.AppointmentConfirmed, t0.Add(-24*time.Hour))
		if err := s.KeepAppointment(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 4 {
		wg.Go(func() {
			for {
				n, err := s.ClaimReminders(ctx, t0, 5)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				total += n
				mu.Unlock()
				if n == 0 {
					return
				}
			}
		})
	}
	wg.Wait()
	queued := reminders(t, pool)
	seen := map[uuid.UUID]bool{}
	for _, r := range queued {
		if seen[r.AppointmentID] {
			t.Errorf("%s queued twice", r.AppointmentID)
		}
		seen[r.AppointmentID] = true
	}
	if total != 40 || len(queued) != 40 {
		t.Errorf("claimed %d, queued %d; want 40 each", total, len(queued))
	}
}
