package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Windows is what booking calls, with no caller to authorize: the checks
// that the branch is the business's and that everyone asked about works
// there are all that stands between it and another business's schedules.
func TestWindows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	biz, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	ali, sara, stranger := shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}

	cal := domain.NewBranchCalendar(biz, branch)
	cal.SetOpeningHours(weekly(t, domain.WeeklyInterval{Day: time.Thursday, Start: 9 * 60, Minutes: 12 * 60}), time.Now())
	aliSchedule := domain.NewBarberSchedule(biz, branch, ali)
	if err := aliSchedule.Set(weekly(t, domain.WeeklyInterval{Day: time.Thursday, Start: 8 * 60, Minutes: 6 * 60}), nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	thursday := time.Date(2030, 10, 3, 0, 0, 0, 0, riyadh)
	offStart := thursday.Add(11 * time.Hour)
	off, err := domain.NewTimeOff(shared.NewID[domain.TimeOffTag](), biz, ali, offStart, offStart.Add(time.Hour), "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stores := &fakeStores{
		calendar:  cal,
		schedules: map[shared.StaffID]*domain.BarberSchedule{ali: aliSchedule},
		timeOff:   map[shared.StaffID][]*domain.TimeOff{ali: {off}},
	}
	access := &fakeAccess{business: biz, branch: branch, loc: riyadh, staff: []shared.StaffID{ali, sara}}
	h := NewWindowsHandlers(stores, scheduleStore{stores}, timeOffStore{stores}, access, nil)

	// Ali: 09:00 (opening) to 14:00 (his shift ends), minus 11:00–12:00.
	got, err := h.Windows(ctx, biz, branch, []shared.StaffID{ali, sara}, thursday, thursday.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	want := []shared.Interval{span(t, thursday, 9, 11), span(t, thursday, 12, 14)}
	if len(got) != 2 || !equalIntervals(got[ali], want) || len(got[sara]) != 0 {
		t.Errorf("windows = %v, want ali %v and nothing for sara", got, want)
	}

	for name, tt := range map[string]struct {
		business shared.BusinessID
		branch   shared.BranchID
		staff    []shared.StaffID
		from, to time.Time
		want     error
	}{
		"someone who doesn't work there": {biz, branch, []shared.StaffID{ali, stranger}, thursday, thursday.Add(time.Hour), domain.ErrNotFound},
		"another business's branch":      {shared.NewID[shared.BusinessTag](), branch, []shared.StaffID{ali}, thursday, thursday.Add(time.Hour), domain.ErrNotFound},
		"to before from":                 {biz, branch, []shared.StaffID{ali}, thursday, thursday.Add(-time.Hour), domain.ErrInvalidRange},
		"an empty range":                 {biz, branch, []shared.StaffID{ali}, thursday, thursday, domain.ErrInvalidRange},
		"just over 62 days":              {biz, branch, []shared.StaffID{ali}, thursday, thursday.Add(MaxWindowsRange + time.Minute), domain.ErrInvalidRange},
	} {
		stores.loads = 0
		if _, err := h.Windows(ctx, tt.business, tt.branch, tt.staff, tt.from, tt.to); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
		if stores.loads != 0 {
			t.Errorf("%s: loaded %d schedules before refusing", name, stores.loads)
		}
	}
	if _, err := h.Windows(ctx, biz, branch, []shared.StaffID{ali}, thursday, thursday.Add(MaxWindowsRange)); err != nil {
		t.Errorf("exactly 62 days: %v", err)
	}
}

func weekly(t *testing.T, intervals ...domain.WeeklyInterval) domain.WeeklyHours {
	t.Helper()
	w, err := domain.NewWeeklyHours(intervals)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func span(t *testing.T, day time.Time, fromHour, toHour int) shared.Interval {
	t.Helper()
	i, err := shared.NewInterval(day.Add(time.Duration(fromHour)*time.Hour), day.Add(time.Duration(toHour)*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func equalIntervals(a, b []shared.Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Start().Equal(b[i].Start()) || !a[i].End().Equal(b[i].End()) {
			return false
		}
	}
	return true
}

// fakeStores holds one branch's calendar, schedules and time off.
type fakeStores struct {
	calendar  *domain.BranchCalendar
	schedules map[shared.StaffID]*domain.BarberSchedule
	timeOff   map[shared.StaffID][]*domain.TimeOff
	loads     int
}

func (f *fakeStores) Get(context.Context, shared.BusinessID, shared.BranchID) (*domain.BranchCalendar, error) {
	return f.calendar, nil
}

func (f *fakeStores) Update(context.Context, shared.BusinessID, shared.BranchID, int, func(*domain.BranchCalendar) error) error {
	panic("not used")
}

// scheduleStore and timeOffStore adapt fakeStores to the other two ports
// (Go has no overloading: each port has its own Get/Update).
type scheduleStore struct{ *fakeStores }

func (f scheduleStore) Get(_ context.Context, business shared.BusinessID, branch shared.BranchID, staff shared.StaffID) (*domain.BarberSchedule, error) {
	f.loads++
	if s, ok := f.schedules[staff]; ok {
		return s, nil
	}
	return domain.NewBarberSchedule(business, branch, staff), nil
}

func (scheduleStore) Update(context.Context, shared.BusinessID, shared.BranchID, shared.StaffID, int, func(*domain.BarberSchedule, []domain.WeeklyHours) error) error {
	panic("not used")
}

type timeOffStore struct{ *fakeStores }

func (timeOffStore) Add(context.Context, *domain.TimeOff) error { panic("not used") }

func (f timeOffStore) List(_ context.Context, _ shared.BusinessID, staff shared.StaffID, _ time.Time) ([]*domain.TimeOff, error) {
	return f.timeOff[staff], nil
}

func (timeOffStore) Delete(context.Context, shared.BusinessID, shared.StaffID, domain.TimeOffID) error {
	panic("not used")
}

// fakeAccess knows one business, one branch and who works there.
type fakeAccess struct {
	Access   // the calls Windows never makes panic on the nil interface
	business shared.BusinessID
	branch   shared.BranchID
	loc      *time.Location
	staff    []shared.StaffID
}

func (f *fakeAccess) StaffAtBranch(_ context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) error {
	if business != f.business || branch != f.branch {
		return domain.ErrNotFound
	}
	for _, s := range staff {
		if !slices.Contains(f.staff, s) {
			return domain.ErrNotFound
		}
	}
	return nil
}

func (f *fakeAccess) BranchLocation(_ context.Context, business shared.BusinessID, branch shared.BranchID) (*time.Location, error) {
	if business != f.business || branch != f.branch {
		return nil, domain.ErrNotFound
	}
	return f.loc, nil
}
