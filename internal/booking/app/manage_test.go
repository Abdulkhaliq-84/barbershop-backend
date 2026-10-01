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

// staffDir knows one business: its members and branches.
type staffDir struct {
	business shared.BusinessID
	members  map[shared.UserID]app.Member
	branches map[shared.BranchID]*time.Location
}

func (s *staffDir) MemberOf(_ context.Context, actor shared.UserID, business shared.BusinessID) (app.Member, error) {
	m, ok := s.members[actor]
	if !ok || business != s.business {
		return app.Member{}, domain.ErrNotFound
	}
	return m, nil
}

func (s *staffDir) BranchLocation(_ context.Context, business shared.BusinessID, branch shared.BranchID) (*time.Location, error) {
	loc, ok := s.branches[branch]
	if !ok || business != s.business {
		return nil, domain.ErrNotFound
	}
	return loc, nil
}

// ledger keeps appointments in memory, scoped like the repository.
type ledger struct {
	all      []*domain.Appointment
	dayAsked [2]time.Time
}

func (l *ledger) ChangeMine(_ context.Context, customer shared.UserID, id domain.AppointmentID, change func(*domain.Appointment) error) (*domain.Appointment, error) {
	return l.change(func(a *domain.Appointment) bool { return a.Customer() == customer && a.ID() == id }, change)
}

func (l *ledger) ChangeAtBusiness(_ context.Context, business shared.BusinessID, id domain.AppointmentID, change func(*domain.Appointment) error) (*domain.Appointment, error) {
	return l.change(func(a *domain.Appointment) bool { return a.Business() == business && a.ID() == id }, change)
}

func (l *ledger) change(find func(*domain.Appointment) bool, change func(*domain.Appointment) error) (*domain.Appointment, error) {
	i := slices.IndexFunc(l.all, find)
	if i < 0 {
		return nil, domain.ErrNotFound
	}
	a := domain.Rehydrate(l.all[i].Snapshot()) // a refused change leaves the stored one alone
	if err := change(a); err != nil {
		return nil, err
	}
	l.all[i] = a
	return a, nil
}

func (l *ledger) Day(_ context.Context, business shared.BusinessID, branch shared.BranchID, from, to time.Time, barber *shared.StaffID) ([]*domain.Appointment, error) {
	l.dayAsked = [2]time.Time{from, to}
	var out []*domain.Appointment
	for _, a := range l.all {
		s := a.Snapshot()
		if s.Business == business && s.Branch == branch && !s.Start.Before(from) && s.Start.Before(to) && (barber == nil || s.Barber == *barber) {
			out = append(out, a)
		}
	}
	return out, nil
}

type manageFixture struct {
	h                        *app.ManageHandlers
	clk                      *clock.Fake
	l                        *ledger
	biz                      shared.BusinessID
	north, south             shared.BranchID
	ali, sara                shared.StaffID // barbers at north
	owner, manager, northMgr shared.UserID
	aliUser, saraUser        shared.UserID
	customer                 shared.UserID
	start                    time.Time
}

func newManageFixture(t *testing.T) *manageFixture {
	t.Helper()
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	f := &manageFixture{
		biz: shared.NewID[shared.BusinessTag](), north: shared.NewID[shared.BranchTag](), south: shared.NewID[shared.BranchTag](),
		ali: shared.NewID[shared.StaffTag](), sara: shared.NewID[shared.StaffTag](),
		owner: shared.NewID[shared.UserTag](), manager: shared.NewID[shared.UserTag](), northMgr: shared.NewID[shared.UserTag](),
		aliUser: shared.NewID[shared.UserTag](), saraUser: shared.NewID[shared.UserTag](), customer: shared.NewID[shared.UserTag](),
		start: time.Date(2029, 10, 4, 10, 0, 0, 0, riyadh),
		l:     &ledger{},
	}
	f.clk = clock.NewFake(f.start.Add(-24 * time.Hour))
	dir := &staffDir{
		business: f.biz,
		branches: map[shared.BranchID]*time.Location{f.north: riyadh, f.south: riyadh},
		members: map[shared.UserID]app.Member{
			f.owner:    {Staff: shared.NewID[shared.StaffTag](), Role: app.RoleOwner},
			f.manager:  {Staff: shared.NewID[shared.StaffTag](), Role: app.RoleManager, Branches: []shared.BranchID{f.south}},
			f.northMgr: {Staff: shared.NewID[shared.StaffTag](), Role: app.RoleManager, Branches: []shared.BranchID{f.north}},
			f.aliUser:  {Staff: f.ali, Role: app.RoleBarber, Branches: []shared.BranchID{f.north}},
			f.saraUser: {Staff: f.sara, Role: app.RoleBarber, Branches: []shared.BranchID{f.north}},
		},
	}
	s := &shop{barbers: []app.Barber{{ID: f.ali, Name: "Ali"}, {ID: f.sara, Name: "Sara"}}}
	f.h = app.NewManageHandlers(dir, s, f.l, f.clk)
	return f
}

// book adds an appointment with barber at the north branch, start + offset.
func (f *manageFixture) book(t *testing.T, barber shared.StaffID, offset time.Duration, pending bool) domain.AppointmentID {
	t.Helper()
	name, _ := shared.NewLocalizedText("قص", "")
	a, err := domain.Book(domain.Booking{
		ID: shared.NewID[domain.AppointmentTag](), Business: f.biz, Branch: f.north, Barber: barber, Customer: f.customer,
		Start: f.start.Add(offset), AutoConfirm: !pending, PendingExpiry: 48 * time.Hour, CancellationWindow: 2 * time.Hour,
		Items: []domain.Item{{Service: shared.NewID[shared.ServiceTag](), Name: name, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}},
	}, f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.l.all = append(f.l.all, a)
	return a.ID()
}

func TestAct(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for _, tt := range []struct {
		name  string
		actor func(f *manageFixture) shared.UserID
		on    func(f *manageFixture) shared.StaffID // whose appointment
		want  error
	}{
		{"the owner, anyone's", func(f *manageFixture) shared.UserID { return f.owner }, func(f *manageFixture) shared.StaffID { return f.sara }, nil},
		{"the branch's manager", func(f *manageFixture) shared.UserID { return f.northMgr }, func(f *manageFixture) shared.StaffID { return f.sara }, nil},
		{"a barber, their own", func(f *manageFixture) shared.UserID { return f.aliUser }, func(f *manageFixture) shared.StaffID { return f.ali }, nil},
		{"a barber, a colleague's", func(f *manageFixture) shared.UserID { return f.aliUser }, func(f *manageFixture) shared.StaffID { return f.sara }, domain.ErrForbidden},
		{"another branch's manager", func(f *manageFixture) shared.UserID { return f.manager }, func(f *manageFixture) shared.StaffID { return f.ali }, domain.ErrForbidden},
		{"not staff", func(f *manageFixture) shared.UserID { return f.customer }, func(f *manageFixture) shared.StaffID { return f.ali }, domain.ErrNotFound},
	} {
		f := newManageFixture(t)
		id := f.book(t, tt.on(f), 0, true)
		v, err := f.h.Act(ctx, app.StaffAction{Actor: tt.actor(f), Business: f.biz, Appointment: id, Action: domain.ActionConfirm})
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
			continue
		}
		if err == nil && (v.Status() != domain.StatusConfirmed || v.BarberName == "") {
			t.Errorf("%s: %s, barber %q", tt.name, v.Status(), v.BarberName)
		}
		if err != nil && f.l.all[0].Status() != domain.StatusPending {
			t.Errorf("%s: refused but changed it", tt.name)
		}
	}

	f := newManageFixture(t)
	id := f.book(t, f.ali, 0, false)
	act := func(action domain.Action, reason string) error {
		_, err := f.h.Act(ctx, app.StaffAction{Actor: f.aliUser, Business: f.biz, Appointment: id, Action: action, Reason: reason})
		return err
	}
	// Another business's ID is "not found", whoever asks.
	if _, err := f.h.Act(ctx, app.StaffAction{Actor: f.owner, Business: shared.NewID[shared.BusinessTag](), Appointment: id, Action: domain.ActionCancel}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another business: %v", err)
	}
	if err := act(domain.ActionComplete, ""); !errors.Is(err, domain.ErrNotStarted) {
		t.Errorf("completing before it starts: %v", err)
	}
	f.clk.Set(f.start.Add(5 * time.Minute))
	if err := act(domain.ActionNoShow, ""); err != nil {
		t.Errorf("a no-show once started: %v", err)
	}
	if err := act(domain.ActionCancel, "late"); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("cancelling a no-show: %v", err)
	}
	if err := act("dance", ""); err == nil {
		t.Error("an unknown action worked")
	}
}

func TestCancelMine(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newManageFixture(t)
	id := f.book(t, f.ali, 0, false)
	if _, err := f.h.CancelMine(ctx, shared.NewID[shared.UserTag](), id, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("someone else's: %v", err)
	}
	f.clk.Set(f.start.Add(-time.Hour)) // inside the two-hour window
	if _, err := f.h.CancelMine(ctx, f.customer, id, ""); !errors.Is(err, domain.ErrTooLateToCancel) {
		t.Errorf("inside the window: %v", err)
	}
	f.clk.Set(f.start.Add(-3 * time.Hour))
	v, err := f.h.CancelMine(ctx, f.customer, id, "تغيرت خططي")
	if err != nil {
		t.Fatal(err)
	}
	if c := v.Snapshot().Cancellation; v.Status() != domain.StatusCancelled || c == nil || c.By != domain.ByCustomer || v.BarberName != "Ali" {
		t.Errorf("cancelled = %+v", v.Snapshot())
	}
}

func TestDay(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newManageFixture(t)
	aliMorning := f.book(t, f.ali, 0, false)
	saraNoon := f.book(t, f.sara, 2*time.Hour, true)
	f.book(t, f.ali, 24*time.Hour, false) // tomorrow
	day := domain.DayOf(f.start)

	got, err := f.h.Day(ctx, app.DayQuery{Actor: f.northMgr, Business: f.biz, Branch: f.north, Day: day})
	if err != nil {
		t.Fatal(err)
	}
	if ids := viewIDs(got.Appointments); !slices.Equal(ids, []domain.AppointmentID{aliMorning, saraNoon}) || got.Appointments[1].BarberName != "Sara" {
		t.Errorf("the manager's day = %v", ids)
	}
	// The day is the branch's: midnight to midnight in Riyadh.
	midnight := time.Date(2029, 10, 4, 0, 0, 0, 0, f.start.Location())
	if !f.l.dayAsked[0].Equal(midnight) || !f.l.dayAsked[1].Equal(midnight.Add(24*time.Hour)) {
		t.Errorf("asked for %v", f.l.dayAsked)
	}
	got, err = f.h.Day(ctx, app.DayQuery{Actor: f.aliUser, Business: f.biz, Branch: f.north, Day: day})
	if err != nil || !slices.Equal(viewIDs(got.Appointments), []domain.AppointmentID{aliMorning}) {
		t.Errorf("Ali's own day = %v, %v", viewIDs(got.Appointments), err)
	}

	for name, tt := range map[string]struct {
		actor    shared.UserID
		business shared.BusinessID
		branch   shared.BranchID
		want     error
	}{
		"another branch's manager": {f.manager, f.biz, f.north, domain.ErrForbidden},
		"not staff":                {f.customer, f.biz, f.north, domain.ErrNotFound},
		"not the business's":       {f.owner, f.biz, shared.NewID[shared.BranchTag](), domain.ErrNotFound},
	} {
		if _, err := f.h.Day(ctx, app.DayQuery{Actor: tt.actor, Business: tt.business, Branch: tt.branch, Day: day}); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}
}

func viewIDs(views []app.AppointmentView) []domain.AppointmentID {
	out := make([]domain.AppointmentID, 0, len(views))
	for _, v := range views {
		out = append(out, v.ID())
	}
	return out
}
