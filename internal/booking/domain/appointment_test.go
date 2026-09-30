package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestBook(t *testing.T) {
	t.Parallel()
	now := time.Date(2029, 10, 3, 17, 0, 0, 123456789, time.UTC)
	start := time.Date(2029, 10, 4, 7, 0, 0, 0, time.UTC)
	cut, _ := shared.NewLocalizedText("قص", "Cut")
	beard, _ := shared.NewLocalizedText("لحية", "")
	b := domain.Booking{
		ID: shared.NewID[domain.AppointmentTag](), Business: shared.NewID[shared.BusinessTag](), Branch: shared.NewID[shared.BranchTag](),
		Barber: shared.NewID[shared.StaffTag](), Customer: shared.NewID[shared.UserTag](), Start: start,
		Items: []domain.Item{
			{Service: shared.NewID[shared.ServiceTag](), Name: cut, Duration: 30 * time.Minute, Price: shared.Halalas(6000)},
			{Service: shared.NewID[shared.ServiceTag](), Name: beard, Duration: 20 * time.Minute, Price: shared.Halalas(3500)},
		},
		Buffer: 10 * time.Minute, Assignment: domain.AnyBarber, Note: "قصير من الجوانب", AutoConfirm: true,
	}

	a, err := domain.Book(b, now)
	if err != nil {
		t.Fatal(err)
	}
	s := a.Snapshot()
	// Back to back: 50 minutes, then the 10-minute buffer holds the barber.
	if !s.End.Equal(start.Add(50*time.Minute)) || !s.BusyUntil.Equal(start.Add(time.Hour)) || !a.Busy().End().Equal(start.Add(time.Hour)) {
		t.Errorf("end %s, busy until %s", s.End, s.BusyUntil)
	}
	if s.Price != shared.Halalas(9500) || s.Status != domain.StatusConfirmed || s.PendingUntil != nil || s.Source != domain.SourceCustomerApp || s.Version != 1 {
		t.Errorf("snapshot = %+v", s)
	}
	if !s.CreatedAt.Equal(now.Truncate(time.Microsecond)) {
		t.Errorf("created at %s: Postgres keeps microseconds", s.CreatedAt)
	}
	if ev, ok := a.Events()[0].(domain.AppointmentBooked); len(a.Events()) != 1 || !ok || ev.Appointment.ID != b.ID {
		t.Errorf("events = %+v", a.Events())
	}

	// A branch that confirms bookings itself: pending until the expiry.
	b.AutoConfirm, b.PendingExpiry = false, 15*time.Minute
	a, err = domain.Book(b, now)
	if err != nil {
		t.Fatal(err)
	}
	if s := a.Snapshot(); s.Status != domain.StatusPending || s.PendingUntil == nil || !s.PendingUntil.Equal(now.Truncate(time.Microsecond).Add(15*time.Minute)) {
		t.Errorf("pending = %s until %v", s.Status, s.PendingUntil)
	}

	for name, tt := range map[string]struct {
		change func(*domain.Booking)
		want   error
	}{
		"no services":    {func(b *domain.Booking) { b.Items = nil }, domain.ErrNoServices},
		"six services":   {func(b *domain.Booking) { b.Items = make([]domain.Item, 6) }, domain.ErrNoServices},
		"a long note":    {func(b *domain.Booking) { b.Note = strings.Repeat("ح", 301) }, domain.ErrNoteTooLong},
		"300 characters": {func(b *domain.Booking) { b.Note = strings.Repeat("ح", 300) }, nil},
	} {
		c := b
		tt.change(&c)
		if _, err := domain.Book(c, now); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}
}

func TestOnGrid(t *testing.T) {
	t.Parallel()
	riyadh := mustLoc(t, "Asia/Riyadh")
	for at, want := range map[time.Time]bool{
		time.Date(2029, 10, 4, 10, 0, 0, 0, riyadh):   true,
		time.Date(2029, 10, 4, 10, 45, 0, 0, riyadh):  true,
		time.Date(2029, 10, 4, 10, 7, 0, 0, riyadh):   false,
		time.Date(2029, 10, 4, 10, 15, 1, 0, riyadh):  false, // a second off
		time.Date(2029, 10, 4, 7, 15, 0, 0, time.UTC): true,  // 10:15 in Riyadh
	} {
		if got := domain.OnGrid(at, riyadh, 15*time.Minute); got != want {
			t.Errorf("%s: %v, want %v", at, got, want)
		}
	}
}
