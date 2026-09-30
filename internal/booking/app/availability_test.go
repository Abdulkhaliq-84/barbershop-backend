package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// shop is one bookable branch with its menu, barbers, windows and
// appointments — the four ports in memory.
type shop struct {
	branch   app.Branch
	barbers  []app.Barber // who still works there
	menu     []app.MenuItem
	windows  map[shared.StaffID][]shared.Interval
	busy     map[shared.StaffID][]shared.Interval
	asked    []shared.StaffID // who windows and busy were asked about
	loadedAt [2]time.Time     // the range they were asked for
}

func (s *shop) Bookable(_ context.Context, id shared.BranchID) (app.Branch, error) {
	if id != s.branch.ID {
		return app.Branch{}, domain.ErrNotFound
	}
	return s.branch, nil
}

func (s *shop) Barbers(_ context.Context, _ shared.BusinessID, _ shared.BranchID, staff []shared.StaffID) ([]app.Barber, error) {
	var out []app.Barber
	for _, id := range staff {
		if i := slices.IndexFunc(s.barbers, func(b app.Barber) bool { return b.ID == id }); i >= 0 {
			out = append(out, s.barbers[i])
		}
	}
	return out, nil
}

func (s *shop) Menu(context.Context, shared.BusinessID, shared.BranchID) ([]app.MenuItem, error) {
	return s.menu, nil
}

type scheduleSide struct{ *shop }

func (s scheduleSide) WorkingWindows(_ context.Context, _ shared.BusinessID, _ shared.BranchID, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, error) {
	s.asked, s.loadedAt = staff, [2]time.Time{from, to}
	return s.windows, nil
}

type busySide struct{ *shop }

func (s busySide) Busy(context.Context, []shared.StaffID, time.Time, time.Time) (map[shared.StaffID][]shared.Interval, error) {
	return s.busy, nil
}

func TestAvailability(t *testing.T) {
	t.Parallel()
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	at := func(d, h, m int) time.Time { return time.Date(2029, 10, d, h, m, 0, 0, riyadh) }
	span := func(from, to time.Time) shared.Interval {
		i, err := shared.NewInterval(from, to)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	ali, sara, gone := shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	haircut, beard, kids := shared.NewID[shared.ServiceTag](), shared.NewID[shared.ServiceTag](), shared.NewID[shared.ServiceTag]()
	name, _ := shared.NewLocalizedText("قص", "Cut")
	s := &shop{
		branch: app.Branch{
			BusinessID: shared.NewID[shared.BusinessTag](), ID: shared.NewID[shared.BranchTag](), Location: riyadh,
			Policy: app.Policy{MinLead: 30 * time.Minute, HorizonDays: 7, SlotInterval: 30 * time.Minute, Buffer: 10 * time.Minute},
		},
		barbers: []app.Barber{{ID: ali, Name: "علي"}, {ID: sara, Name: "Sara"}},
		menu: []app.MenuItem{
			{ID: haircut, Name: name, Performers: []app.Performer{
				{Staff: ali, Duration: 30 * time.Minute, Price: shared.Halalas(6000)},
				{Staff: sara, Duration: 40 * time.Minute, Price: shared.Halalas(8000)},
				{Staff: gone, Duration: 30 * time.Minute, Price: shared.Halalas(5000)},
			}},
			{ID: beard, Name: name, Performers: []app.Performer{
				{Staff: ali, Duration: 20 * time.Minute, Price: shared.Halalas(3000)},
				{Staff: gone, Duration: 20 * time.Minute, Price: shared.Halalas(3000)},
			}},
			{ID: kids, Name: name},
		},
		windows: map[shared.StaffID][]shared.Interval{
			ali:  {span(at(4, 10, 0), at(4, 12, 0))},
			sara: {span(at(4, 10, 0), at(4, 11, 0))},
		},
		busy: map[shared.StaffID][]shared.Interval{ali: {span(at(4, 10, 30), at(4, 11, 0))}},
	}
	now := at(3, 20, 0)
	h := app.NewAvailabilityHandlers(s, s, scheduleSide{s}, busySide{s}, clock.NewFake(now))
	thursday, _ := domain.ParseDay("2029-10-04")
	ctx := t.Context()

	// Haircut and beard: only Ali does both (Gone left the branch). 50
	// minutes plus the 10-minute buffer; he's busy 10:30–11:00.
	a, err := h.Query(ctx, app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{haircut, beard}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Offers) != 1 || a.Offers[0].ID != ali || a.Offers[0].Name != "علي" || a.Offers[0].Duration != 50*time.Minute || a.Offers[0].Price != shared.Halalas(9000) {
		t.Fatalf("offers = %+v", a.Offers)
	}
	var got []string
	for _, sl := range a.Slots {
		got = append(got, sl.Start.In(riyadh).Format("15:04"))
	}
	if !slices.Equal(got, []string{"11:00"}) { // 11:00 + 60 min = 12:00: fits; 10:00 runs into 10:30
		t.Errorf("slots = %v", got)
	}
	// Windows and appointments were asked for around the day, long enough
	// for a booking starting at 23:59.
	if !s.loadedAt[0].Equal(at(4, 0, 0)) || !s.loadedAt[1].Equal(at(5, 1, 0)) || !slices.Equal(s.asked, []shared.StaffID{ali}) {
		t.Errorf("loaded %v for %v", s.loadedAt, s.asked)
	}

	// A haircut, any barber: Ali and Sara, each slot listing who's free.
	// Sara's 40 minutes (+10) fit at 10:00; Ali's 30 (+10) would run into
	// his 10:30, so he starts at 11:00.
	a, err = h.Query(ctx, app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{haircut}})
	if err != nil || len(a.Offers) != 2 {
		t.Fatalf("haircut: %+v, %v", a.Offers, err)
	}
	if len(a.Slots) != 2 || !a.Slots[0].Start.Equal(at(4, 10, 0)) || !slices.Equal(a.Slots[0].Staff, []shared.StaffID{sara}) ||
		!a.Slots[1].Start.Equal(at(4, 11, 0)) || !slices.Equal(a.Slots[1].Staff, []shared.StaffID{ali}) {
		t.Errorf("any barber = %+v", a.Slots)
	}
	// Just Sara.
	a, err = h.Query(ctx, app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{haircut}, Barber: &sara})
	if err != nil || len(a.Offers) != 1 || a.Offers[0].ID != sara || len(a.Slots) != 1 {
		t.Errorf("Sara: %+v %+v, %v", a.Offers, a.Slots, err)
	}

	// Days with no slots, not errors: before today, past the horizon — and
	// nothing loaded for them.
	for _, day := range []string{"2029-10-02", "2029-10-11"} {
		d, _ := domain.ParseDay(day)
		s.asked = nil
		if a, err := h.Query(ctx, app.AvailabilityQuery{Branch: s.branch.ID, Day: d, Services: []shared.ServiceID{haircut}}); err != nil || len(a.Slots) != 0 || len(a.Offers) != 2 || s.asked != nil {
			t.Errorf("%s: %d slots, asked about %v, %v", day, len(a.Slots), s.asked, err)
		}
	}
	// A service nobody performs: offers none, no error.
	if a, err := h.Query(ctx, app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{kids}}); err != nil || len(a.Offers) != 0 || len(a.Slots) != 0 {
		t.Errorf("kids: %+v, %v", a, err)
	}

	for name, tt := range map[string]struct {
		q    app.AvailabilityQuery
		want error
	}{
		"no services":        {app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday}, domain.ErrNoServices},
		"a service twice":    {app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{haircut, haircut}}, domain.ErrNoServices},
		"six services":       {app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: make([]shared.ServiceID, 6)}, domain.ErrNoServices},
		"not on the menu":    {app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{shared.NewID[shared.ServiceTag]()}}, domain.ErrServiceUnavailable},
		"Sara doesn't shave": {app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{haircut, beard}, Barber: &sara}, domain.ErrBarberUnavailable},
		"left the branch":    {app.AvailabilityQuery{Branch: s.branch.ID, Day: thursday, Services: []shared.ServiceID{haircut}, Barber: &gone}, domain.ErrBarberUnavailable},
		"unknown branch":     {app.AvailabilityQuery{Branch: shared.NewID[shared.BranchTag](), Day: thursday, Services: []shared.ServiceID{haircut}}, domain.ErrNotFound},
	} {
		if _, err := h.Query(ctx, tt.q); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}
}
