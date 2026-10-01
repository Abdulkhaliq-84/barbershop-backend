package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var (
	start        = time.Date(2029, 10, 4, 10, 0, 0, 0, time.UTC)
	cancelBy     = start.Add(-2 * time.Hour)    // the confirmed booking's deadline
	pendingUntil = start.Add(-30 * time.Minute) // when a pending one expires
)

// in returns an appointment with the given status, as if loaded.
func in(status domain.Status) *domain.Appointment {
	cut, _ := shared.NewLocalizedText("قص", "")
	s := domain.Snapshot{
		ID: shared.NewID[domain.AppointmentTag](), Business: shared.NewID[shared.BusinessTag](), Branch: shared.NewID[shared.BranchTag](),
		Barber: shared.NewID[shared.StaffTag](), Customer: shared.NewID[shared.UserTag](),
		Items: []domain.Item{{Service: shared.NewID[shared.ServiceTag](), Name: cut, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}},
		Start: start, End: start.Add(30 * time.Minute), BusyUntil: start.Add(40 * time.Minute), Price: shared.Halalas(6000),
		Status: status, Source: domain.SourceCustomerApp, Assignment: domain.AnyBarber, CancellableUntil: cancelBy,
		Version: 3, CreatedAt: start.Add(-48 * time.Hour), UpdatedAt: start.Add(-48 * time.Hour),
	}
	if status == domain.StatusPending {
		s.PendingUntil = new(pendingUntil)
	}
	if status == domain.StatusCancelled {
		s.Cancellation = &domain.Cancellation{By: domain.ByCustomer, At: start.Add(-24 * time.Hour)}
	}
	return domain.Rehydrate(s)
}

type act struct {
	name string
	do   func(a *domain.Appointment, now time.Time) error
}

var acts = []act{
	{"confirm", func(a *domain.Appointment, now time.Time) error { return a.Confirm(now) }},
	{"reject", func(a *domain.Appointment, now time.Time) error { return a.Reject(now) }},
	{"customer cancels", func(a *domain.Appointment, now time.Time) error { return a.CancelByCustomer("", now) }},
	{"staff cancels", func(a *domain.Appointment, now time.Time) error { return a.CancelByStaff("", now) }},
	{"complete", func(a *domain.Appointment, now time.Time) error { return a.Complete(now) }},
	{"no-show", func(a *domain.Appointment, now time.Time) error { return a.MarkNoShow(now) }},
	{"expire", func(a *domain.Appointment, now time.Time) error { return a.Expire(now) }},
}

var statuses = []domain.Status{
	domain.StatusPending, domain.StatusConfirmed, domain.StatusRejected, domain.StatusExpired,
	domain.StatusCancelled, domain.StatusCompleted, domain.StatusNoShow,
}

// The edges of the lifecycle diagram (domain-model.md §3.5).
var edges = map[[2]domain.Status]bool{
	{domain.StatusPending, domain.StatusExpired}:     true,
	{domain.StatusPending, domain.StatusConfirmed}:   true,
	{domain.StatusPending, domain.StatusRejected}:    true,
	{domain.StatusPending, domain.StatusCancelled}:   true,
	{domain.StatusConfirmed, domain.StatusCancelled}: true,
	{domain.StatusConfirmed, domain.StatusCompleted}: true,
	{domain.StatusConfirmed, domain.StatusNoShow}:    true,
}

// Every action on every status at every kind of moment: a change only
// ever follows an edge of the diagram, and leaves a trace (version, event);
// a refusal changes nothing.
func TestLifecycleFollowsTheDiagram(t *testing.T) {
	t.Parallel()
	moments := []time.Time{
		start.Add(-5 * time.Hour),    // well ahead
		start.Add(-1 * time.Hour),    // inside the cancellation window, pending expired
		start.Add(10 * time.Minute),  // started
		start.Add(-90 * time.Minute), // between: still pending
	}
	for _, from := range statuses {
		for _, x := range acts {
			for _, now := range moments {
				a := in(from)
				before := a.Snapshot()
				err := x.do(a, now)
				after := a.Snapshot()
				name := fmt.Sprintf("%s: %s at start%+v", from, x.name, now.Sub(start))
				if err != nil {
					if after.Status != before.Status || after.Version != before.Version || len(a.Events()) != 0 {
						t.Errorf("%s refused (%v) but changed it", name, err)
					}
					continue
				}
				if !edges[[2]domain.Status{from, after.Status}] {
					t.Errorf("%s: %s → %s isn't in the diagram", name, from, after.Status)
				}
				ev, ok := a.Events()[0].(domain.StatusChanged)
				if after.Version != before.Version+1 || len(a.Events()) != 1 || !ok || ev.From != from || ev.Appointment.Status != after.Status {
					t.Errorf("%s: version %d → %d, events %+v", name, before.Version, after.Version, a.Events())
				}
				if after.PendingUntil != nil || !after.UpdatedAt.Equal(now) {
					t.Errorf("%s: pending until %v, updated %s", name, after.PendingUntil, after.UpdatedAt)
				}
			}
		}
	}
}

func TestLifecycle(t *testing.T) {
	t.Parallel()
	ahead, inWindow, started := start.Add(-5*time.Hour), start.Add(-time.Hour), start.Add(10*time.Minute)
	for _, tt := range []struct {
		name string
		from domain.Status
		do   func(*domain.Appointment) error
		want domain.Status // when it works
		err  error
	}{
		{"the shop confirms a pending booking", domain.StatusPending, func(a *domain.Appointment) error { return a.Confirm(ahead) }, domain.StatusConfirmed, nil},
		{"…but not once it expired", domain.StatusPending, func(a *domain.Appointment) error { return a.Confirm(pendingUntil) }, "", domain.ErrInvalidTransition},
		{"the shop rejects a pending booking", domain.StatusPending, func(a *domain.Appointment) error { return a.Reject(ahead) }, domain.StatusRejected, nil},
		{"…not a confirmed one", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.Reject(ahead) }, "", domain.ErrInvalidTransition},
		{"the customer cancels before the deadline", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.CancelByCustomer("", cancelBy.Add(-time.Minute)) }, domain.StatusCancelled, nil},
		{"…not at it", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.CancelByCustomer("", cancelBy) }, "", domain.ErrTooLateToCancel},
		{"…a pending booking until it starts", domain.StatusPending, func(a *domain.Appointment) error { return a.CancelByCustomer("", pendingUntil.Add(-time.Minute)) }, domain.StatusCancelled, nil},
		{"…not one that expired", domain.StatusPending, func(a *domain.Appointment) error { return a.CancelByCustomer("", pendingUntil.Add(time.Minute)) }, "", domain.ErrInvalidTransition},
		{"the shop cancels inside the window", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.CancelByStaff("", inWindow) }, domain.StatusCancelled, nil},
		{"…even once started", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.CancelByStaff("", started) }, domain.StatusCancelled, nil},
		{"…not twice", domain.StatusCancelled, func(a *domain.Appointment) error { return a.CancelByStaff("", ahead) }, "", domain.ErrInvalidTransition},
		{"completed once started", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.Complete(started) }, domain.StatusCompleted, nil},
		{"…at the very start", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.Complete(start) }, domain.StatusCompleted, nil},
		{"…not before", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.Complete(start.Add(-time.Minute)) }, "", domain.ErrNotStarted},
		{"…not if pending", domain.StatusPending, func(a *domain.Appointment) error { return a.Complete(ahead) }, "", domain.ErrInvalidTransition},
		{"a no-show once started", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.MarkNoShow(started) }, domain.StatusNoShow, nil},
		{"…not before", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.MarkNoShow(inWindow) }, "", domain.ErrNotStarted},
		{"…not after it was completed", domain.StatusCompleted, func(a *domain.Appointment) error { return a.MarkNoShow(started) }, "", domain.ErrInvalidTransition},
		{"the job expires a pending booking at its expiry", domain.StatusPending, func(a *domain.Appointment) error { return a.Expire(pendingUntil) }, domain.StatusExpired, nil},
		{"…not before", domain.StatusPending, func(a *domain.Appointment) error { return a.Expire(pendingUntil.Add(-time.Microsecond)) }, "", domain.ErrInvalidTransition},
		{"…nor a confirmed one", domain.StatusConfirmed, func(a *domain.Appointment) error { return a.Expire(started) }, "", domain.ErrInvalidTransition},
	} {
		a := in(tt.from)
		err := tt.do(a)
		if !errors.Is(err, tt.err) || (err == nil && a.Status() != tt.want) {
			t.Errorf("%s: %v, status %s; want %v, %s", tt.name, err, a.Status(), tt.err, tt.want)
		}
	}

	// A refusal says what the appointment is now.
	err := in(domain.StatusPending).Confirm(pendingUntil)
	if te, ok := errors.AsType[*domain.TransitionError](err); !ok || te.Status != domain.StatusExpired || te.Action != domain.ActionConfirm {
		t.Errorf("confirming an expired booking: %#v", err)
	}
}

func TestCancellation(t *testing.T) {
	t.Parallel()
	now := start.Add(-5*time.Hour + 123*time.Nanosecond)
	a := in(domain.StatusConfirmed)
	if err := a.CancelByStaff("  الحلاق مريض  ", now); err != nil {
		t.Fatal(err)
	}
	c := a.Snapshot().Cancellation
	if c == nil || c.By != domain.ByStaff || c.Reason != "الحلاق مريض" || !c.At.Equal(now.Truncate(time.Microsecond)) {
		t.Errorf("cancellation = %+v", c)
	}
	if err := in(domain.StatusConfirmed).CancelByCustomer(strings.Repeat("س", 301), now); !errors.Is(err, domain.ErrReasonTooLong) {
		t.Errorf("a long reason: %v", err)
	}
	if err := in(domain.StatusConfirmed).CancelByCustomer(strings.Repeat("س", 300), now); err != nil {
		t.Errorf("300 characters: %v", err)
	}
}

// The deadline is the start minus the branch's window when it was booked.
func TestBookKeepsTheCancellationDeadline(t *testing.T) {
	t.Parallel()
	cut, _ := shared.NewLocalizedText("قص", "")
	b := domain.Booking{
		ID: shared.NewID[domain.AppointmentTag](), Customer: shared.NewID[shared.UserTag](), Start: start, CancellationWindow: 3 * time.Hour,
		Items: []domain.Item{{Service: shared.NewID[shared.ServiceTag](), Name: cut, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}},
	}
	a, err := domain.Book(b, start.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Snapshot().CancellableUntil; !got.Equal(start.Add(-3 * time.Hour)) {
		t.Errorf("cancellable until %s", got)
	}
	b.CancellationWindow = 0
	if a, _ = domain.Book(b, start.Add(-48*time.Hour)); !a.Snapshot().CancellableUntil.Equal(start) {
		t.Errorf("no window: cancellable until %s", a.Snapshot().CancellableUntil)
	}
}

// The shop books walk-ins by name; only the shop does, and its bookings
// need no answer.
func TestWalkIn(t *testing.T) {
	t.Parallel()
	cut, _ := shared.NewLocalizedText("قص", "")
	b := domain.Booking{
		ID: shared.NewID[domain.AppointmentTag](), Start: start, Source: domain.SourceStaff, CustomerName: "  أبو فهد  ",
		Assignment: domain.RequestedBarber, AutoConfirm: false, PendingExpiry: time.Hour,
		Items: []domain.Item{{Service: shared.NewID[shared.ServiceTag](), Name: cut, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}},
	}
	a, err := domain.Book(b, start.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if s := a.Snapshot(); !s.Customer.IsZero() || s.CustomerName != "أبو فهد" || s.Source != domain.SourceStaff || s.Status != domain.StatusConfirmed || s.PendingUntil != nil {
		t.Errorf("walk-in = %+v", s)
	}
	for name, tt := range map[string]struct {
		change func(*domain.Booking)
		want   error
	}{
		"no name":             {func(b *domain.Booking) { b.CustomerName = "   " }, domain.ErrNoCustomer},
		"the app, no account": {func(b *domain.Booking) { b.Source = "" }, domain.ErrNoCustomer},
		"a long name":         {func(b *domain.Booking) { b.CustomerName = strings.Repeat("ف", 101) }, domain.ErrCustomerNameTooLong},
		"100 characters":      {func(b *domain.Booking) { b.CustomerName = strings.Repeat("ف", 100) }, nil},
	} {
		c := b
		tt.change(&c)
		if _, err := domain.Book(c, start.Add(-time.Hour)); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}
}
