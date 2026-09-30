package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// bookings records the attempt the use case hands the repository.
type bookings struct {
	got    app.BookingAttempt
	stored *domain.Appointment // what Replay finds, if set
}

func (b *bookings) Book(_ context.Context, a app.BookingAttempt) (*domain.Appointment, bool, error) {
	b.got = a
	if len(a.Drafts) == 0 {
		return nil, false, domain.ErrSlotUnavailable // what the repository says when nobody is free
	}
	return a.Drafts[0], false, nil
}

func (b *bookings) Replay(context.Context, shared.UserID, uuid.UUID, []byte) (*domain.Appointment, bool, error) {
	return b.stored, b.stored != nil, nil
}

func (b *bookings) ByCustomer(context.Context, shared.UserID, domain.AppointmentID) (*domain.Appointment, error) {
	return nil, domain.ErrNotFound
}

func TestBook(t *testing.T) {
	t.Parallel()
	riyadh, _ := time.LoadLocation("Asia/Riyadh")
	at := func(d, h, m int) time.Time { return time.Date(2029, 10, d, h, m, 0, 0, riyadh) }
	span := func(from, to time.Time) shared.Interval {
		i, err := shared.NewInterval(from, to)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	ali, sara, omar := shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	haircut := shared.NewID[shared.ServiceTag]()
	name, _ := shared.NewLocalizedText("قص", "Cut")
	s := &shop{
		branch: app.Branch{
			BusinessID: shared.NewID[shared.BusinessTag](), ID: shared.NewID[shared.BranchTag](), Location: riyadh,
			Policy: app.Policy{MinLead: time.Hour, HorizonDays: 7, SlotInterval: 15 * time.Minute, Buffer: 10 * time.Minute, AutoConfirm: true, MaxActiveBookings: 2},
		},
		barbers: []app.Barber{{ID: ali, Name: "Ali"}, {ID: sara, Name: "Sara"}, {ID: omar, Name: "Omar"}},
		menu: []app.MenuItem{{ID: haircut, Name: name, Performers: []app.Performer{
			{Staff: ali, Duration: 30 * time.Minute, Price: shared.Halalas(6000)},
			{Staff: sara, Duration: 30 * time.Minute, Price: shared.Halalas(7000)},
			{Staff: omar, Duration: 30 * time.Minute, Price: shared.Halalas(5000)},
		}}},
		windows: map[shared.StaffID][]shared.Interval{
			ali: {span(at(4, 9, 0), at(4, 18, 0))}, sara: {span(at(4, 9, 0), at(4, 18, 0))}, omar: {span(at(4, 9, 0), at(4, 10, 0))},
		},
		// Ali has two hours booked that day, Sara one: Sara is less busy.
		busy: map[shared.StaffID][]shared.Interval{
			ali:  {span(at(4, 13, 0), at(4, 15, 0))},
			sara: {span(at(4, 14, 0), at(4, 15, 0))},
		},
	}
	repo := &bookings{}
	h := app.NewBookHandlers(app.NewAvailabilityHandlers(s, s, scheduleSide{s}, busySide{s}, clock.NewFake(at(3, 20, 0))), repo)
	ctx := t.Context()
	cmd := app.BookAppointment{
		Customer: shared.NewID[shared.UserTag](), IdempotencyKey: uuid.New(), Branch: s.branch.ID,
		Start: at(4, 11, 0), Services: []shared.ServiceID{haircut}, Note: "  قصير  ",
	}

	// Any barber at 11:00: Omar has stopped by then; Sara first (fewer
	// minutes booked that day), then Ali.
	a, _, err := h.Book(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	var order []shared.StaffID
	for _, d := range repo.got.Drafts {
		order = append(order, d.Barber())
	}
	if !slices.Equal(order, []shared.StaffID{sara, ali}) || a.Barber() != sara {
		t.Fatalf("drafts for %v, booked %v; want Sara then Ali", order, a.Barber())
	}
	snap := repo.got.Drafts[0].Snapshot()
	if snap.Price != shared.Halalas(7000) || snap.Note != "قصير" || snap.Assignment != domain.AnyBarber || snap.Items[0].Name.Ar() != "قص" || !snap.BusyUntil.Equal(at(4, 11, 40)) {
		t.Errorf("draft = %+v", snap)
	}
	// The limit: two upcoming bookings allowed.
	if repo.got.Allow(1) != nil || !errors.Is(repo.got.Allow(2), domain.ErrTooManyBookings) {
		t.Error("Allow doesn't apply MaxActiveBookings")
	}
	// The last check re-reads the barber's windows for exactly the busy time.
	s.asked = nil
	if ok, err := repo.got.StillWorking(ctx, repo.got.Drafts[0]); err != nil || !ok || !s.loadedAt[0].Equal(at(4, 11, 0)) || !s.loadedAt[1].Equal(at(4, 11, 40)) {
		t.Errorf("still working: %v, %v, asked %v", ok, err, s.loadedAt)
	}
	s.windows[sara] = []shared.Interval{span(at(4, 9, 0), at(4, 11, 30))} // her hours changed meanwhile
	if ok, _ := repo.got.StillWorking(ctx, repo.got.Drafts[0]); ok {
		t.Error("still working after her hours were cut")
	}
	s.windows[sara] = []shared.Interval{span(at(4, 9, 0), at(4, 18, 0))}
	// The same request hashes the same (a retry); another start doesn't.
	first := repo.got.RequestHash
	if _, _, err := h.Book(ctx, cmd); err != nil || !slices.Equal(repo.got.RequestHash, first) {
		t.Errorf("a retry hashes differently: %v", err)
	}
	later := cmd
	later.Start = at(4, 11, 15)
	if _, _, _ = h.Book(ctx, later); slices.Equal(repo.got.RequestHash, first) {
		t.Error("another start hashes the same")
	}
	// Omar by name at 9:00: requested, only him.
	early := cmd
	early.Start, early.Barber = at(4, 9, 0), &omar
	if _, _, err := h.Book(ctx, early); err != nil || len(repo.got.Drafts) != 1 || repo.got.Drafts[0].Barber() != omar || repo.got.Drafts[0].Snapshot().Assignment != domain.RequestedBarber {
		t.Errorf("Omar at 9:00: %v, %d drafts", err, len(repo.got.Drafts))
	}

	// A retry after the lead time has passed still gets its appointment:
	// the key is looked up before the rules.
	repo.stored, repo.got = a, app.BookingAttempt{}
	retry := cmd
	retry.Start = at(3, 20, 15)
	if got, replayed, err := h.Book(ctx, retry); err != nil || !replayed || got != a || repo.got.Key != uuid.Nil {
		t.Errorf("retry: %v, replayed %v, %v; attempted %v", got, replayed, err, repo.got.Key)
	}
	repo.stored = nil

	for name, tt := range map[string]struct {
		change func(*app.BookAppointment)
		want   error
	}{
		"off the grid":        {func(c *app.BookAppointment) { c.Start = at(4, 11, 5) }, domain.ErrInvalidStart},
		"within the lead":     {func(c *app.BookAppointment) { c.Start = at(3, 20, 45) }, domain.ErrInvalidStart},
		"in the past":         {func(c *app.BookAppointment) { c.Start = at(3, 9, 0) }, domain.ErrInvalidStart},
		"past the horizon":    {func(c *app.BookAppointment) { c.Start = at(11, 10, 0) }, domain.ErrInvalidStart},
		"nobody free":         {func(c *app.BookAppointment) { c.Start = at(4, 14, 0) }, domain.ErrSlotUnavailable},
		"Omar after his day":  {func(c *app.BookAppointment) { c.Barber = &omar }, domain.ErrSlotUnavailable},
		"not their barber":    {func(c *app.BookAppointment) { c.Barber = new(shared.NewID[shared.StaffTag]()) }, domain.ErrBarberUnavailable},
		"no services":         {func(c *app.BookAppointment) { c.Services = nil }, domain.ErrNoServices},
		"not on the menu":     {func(c *app.BookAppointment) { c.Services = []shared.ServiceID{shared.NewID[shared.ServiceTag]()} }, domain.ErrServiceUnavailable},
		"an unbookable place": {func(c *app.BookAppointment) { c.Branch = shared.NewID[shared.BranchTag]() }, domain.ErrNotFound},
	} {
		c := cmd
		tt.change(&c)
		if _, _, err := h.Book(ctx, c); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}
}
