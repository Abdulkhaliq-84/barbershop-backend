package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestStaffBook(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
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
	ali, sara := shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	haircut := shared.NewID[shared.ServiceTag]()
	name, _ := shared.NewLocalizedText("قص", "")
	s := &shop{
		branch: app.Branch{
			BusinessID: shared.NewID[shared.BusinessTag](), ID: shared.NewID[shared.BranchTag](), Location: riyadh,
			Policy: app.Policy{MinLead: time.Hour, HorizonDays: 7, SlotInterval: 15 * time.Minute, Buffer: 10 * time.Minute, AutoConfirm: false, MaxActiveBookings: 1},
		},
		barbers: []app.Barber{{ID: ali, Name: "Ali"}, {ID: sara, Name: "Sara"}},
		menu: []app.MenuItem{{ID: haircut, Name: name, Performers: []app.Performer{
			{Staff: ali, Duration: 30 * time.Minute, Price: shared.Halalas(6000)},
		}}},
		windows: map[shared.StaffID][]shared.Interval{ali: {span(at(4, 9, 0), at(4, 18, 0))}, sara: {span(at(4, 9, 0), at(4, 18, 0))}},
		busy:    map[shared.StaffID][]shared.Interval{ali: {span(at(4, 13, 0), at(4, 15, 0))}},
	}
	owner, manager, otherMgr, aliUser, stranger := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	unpublished := shared.NewID[shared.BranchTag]()
	dir := &staffDir{
		business: s.branch.BusinessID,
		branches: map[shared.BranchID]*time.Location{s.branch.ID: riyadh, unpublished: riyadh},
		members: map[shared.UserID]app.Member{
			owner:    {Staff: shared.NewID[shared.StaffTag](), Role: app.RoleOwner},
			manager:  {Staff: shared.NewID[shared.StaffTag](), Role: app.RoleManager, Branches: []shared.BranchID{s.branch.ID}},
			otherMgr: {Staff: shared.NewID[shared.StaffTag](), Role: app.RoleManager, Branches: []shared.BranchID{unpublished}},
			aliUser:  {Staff: ali, Role: app.RoleBarber, Branches: []shared.BranchID{s.branch.ID}},
		},
	}
	clk := clock.NewFake(at(4, 11, 10)) // a walk-in at 11:10
	repo := &bookings{}
	h := app.NewBookHandlers(app.NewAvailabilityHandlers(s, s, scheduleSide{s}, busySide{s}, clk), repo, dir)
	cmd := app.StaffBooking{
		Actor: manager, Business: s.branch.BusinessID, Branch: s.branch.ID, IdempotencyKey: uuid.New(),
		Start: at(4, 11, 7), Services: []shared.ServiceID{haircut}, Barber: ali, CustomerName: "أبو فهد",
	}

	// 11:07, off the grid and inside the lead time: fine for the shop. It is
	// confirmed at once, though the branch confirms app bookings by hand.
	a, _, err := h.StaffBook(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	snap := a.Snapshot()
	if snap.Source != domain.SourceStaff || snap.Status != domain.StatusConfirmed || snap.CustomerName != "أبو فهد" || !snap.Customer.IsZero() || snap.Assignment != domain.RequestedBarber || snap.Price != shared.Halalas(6000) {
		t.Errorf("walk-in = %+v", snap)
	}
	if repo.got.Requester != manager || repo.got.Allow != nil || repo.got.StillWorking == nil {
		t.Errorf("attempt: requester %v, limit %v", repo.got.Requester, repo.got.Allow != nil)
	}

	for name, tt := range map[string]struct {
		change func(*app.StaffBooking)
		want   error
	}{
		"the owner":                      {func(c *app.StaffBooking) { c.Actor = owner }, nil},
		"a barber for themselves":        {func(c *app.StaffBooking) { c.Actor = aliUser }, nil},
		"a barber for a colleague":       {func(c *app.StaffBooking) { c.Actor, c.Barber = aliUser, sara }, domain.ErrForbidden},
		"another branch's manager":       {func(c *app.StaffBooking) { c.Actor = otherMgr }, domain.ErrForbidden},
		"not staff":                      {func(c *app.StaffBooking) { c.Actor = stranger }, domain.ErrNotFound},
		"not the business's branch":      {func(c *app.StaffBooking) { c.Branch = shared.NewID[shared.BranchTag]() }, domain.ErrNotFound},
		"an unpublished branch":          {func(c *app.StaffBooking) { c.Actor, c.Branch = owner, unpublished }, domain.ErrBranchNotBookable},
		"15 minutes ago":                 {func(c *app.StaffBooking) { c.Start = at(4, 10, 55) }, nil},
		"16 minutes ago":                 {func(c *app.StaffBooking) { c.Start = at(4, 10, 54) }, domain.ErrInvalidStart},
		"not a whole minute":             {func(c *app.StaffBooking) { c.Start = at(4, 12, 0).Add(30 * time.Second) }, domain.ErrInvalidStart},
		"past the horizon":               {func(c *app.StaffBooking) { c.Start = at(12, 10, 0) }, domain.ErrInvalidStart},
		"while he is booked":             {func(c *app.StaffBooking) { c.Start = at(4, 13, 30) }, domain.ErrSlotUnavailable},
		"after his hours":                {func(c *app.StaffBooking) { c.Start = at(4, 17, 45) }, domain.ErrSlotUnavailable},
		"a barber who doesn't do it":     {func(c *app.StaffBooking) { c.Barber = sara }, domain.ErrBarberUnavailable},
		"no services":                    {func(c *app.StaffBooking) { c.Services = nil }, domain.ErrNoServices},
		"no name":                        {func(c *app.StaffBooking) { c.CustomerName = " " }, domain.ErrNoCustomer},
		"a barber who doesn't work here": {func(c *app.StaffBooking) { c.Barber = shared.NewID[shared.StaffTag]() }, domain.ErrBarberUnavailable},
	} {
		c := cmd
		c.IdempotencyKey = uuid.New()
		tt.change(&c)
		if _, _, err := h.StaffBook(ctx, c); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}

	// The key is the staff member's: what they asked before comes back.
	repo.stored = a
	if got, replayed, err := h.StaffBook(ctx, cmd); err != nil || !replayed || got != a {
		t.Errorf("a retry: %v, replayed %v, %v", got, replayed, err)
	}
}
