package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func TestDate(t *testing.T) {
	t.Parallel()
	d, err := domain.ParseDate("2026-10-01")
	if err != nil || d.String() != "2026-10-01" || d.Weekday() != time.Thursday {
		t.Fatalf("date = %v %v, %v", d, d.Weekday(), err)
	}
	if d.AddDays(31).String() != "2026-11-01" || d.AddDays(-1).String() != "2026-09-30" {
		t.Errorf("AddDays: %v, %v", d.AddDays(31), d.AddDays(-1))
	}
	same, _ := domain.ParseDate("2026-10-01")
	if d.Compare(d.AddDays(1)) != -1 || d.AddDays(1).Compare(d) != 1 || d.Compare(same) != 0 {
		t.Error("Compare")
	}
	for _, bad := range []string{"2026-02-30", "01/10/2026", "", "2026-13-01"} {
		if _, err := domain.ParseDate(bad); !errors.Is(err, domain.ErrInvalidDate) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	riyadh, _ := time.LoadLocation("Asia/Riyadh")
	// 25 hours after midnight is 01:00 the next day, on the wall clock.
	if got := d.At(25*60, riyadh); !got.Equal(time.Date(2026, 10, 2, 1, 0, 0, 0, riyadh)) {
		t.Errorf("At = %v", got)
	}
}

func TestBarberSchedule(t *testing.T) {
	t.Parallel()
	s := domain.NewBarberSchedule(shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), shared.NewID[shared.StaffTag]())
	weekly, _ := domain.NewWeeklyHours([]domain.WeeklyInterval{iv(time.Thursday, "16:00", 600), iv(time.Sunday, "09:00", 480)})
	holiday, _ := domain.ParseDate("2026-10-08") // a Thursday
	longDay, _ := domain.ParseDate("2026-10-11") // a Sunday
	overrides := map[domain.Date][]domain.DayInterval{
		holiday: {},
		longDay: {{Start: 13 * 60, Minutes: 60}, {Start: 8 * 60, Minutes: 240}},
	}
	if err := s.Set(weekly, overrides, nil, t0); err != nil {
		t.Fatal(err)
	}
	if s.Version() != 1 || !s.UpdatedAt().Equal(t0) {
		t.Errorf("schedule = %+v", s)
	}
	thursday, _ := domain.ParseDate("2026-10-01")
	if got := s.IntervalsStarting(thursday); len(got) != 1 || got[0] != (domain.DayInterval{Start: 16 * 60, Minutes: 600}) {
		t.Errorf("an ordinary Thursday = %+v", got)
	}
	if got := s.IntervalsStarting(holiday); len(got) != 0 {
		t.Errorf("the day off = %+v", got)
	}
	if got := s.IntervalsStarting(longDay); len(got) != 2 || got[0].Start != 8*60 {
		t.Errorf("the override, sorted = %+v", got)
	}
	monday, _ := domain.ParseDate("2026-10-05")
	if got := s.IntervalsStarting(monday); len(got) != 0 {
		t.Errorf("a Monday off = %+v", got)
	}

	for name, tt := range map[string]struct {
		overrides map[domain.Date][]domain.DayInterval
		elsewhere []domain.WeeklyHours
		want      error
	}{
		"overlap within a date": {map[domain.Date][]domain.DayInterval{holiday: {{Start: 600, Minutes: 120}, {Start: 660, Minutes: 60}}}, nil, domain.ErrOverlappingIntervals},
		"off the grid":          {map[domain.Date][]domain.DayInterval{holiday: {{Start: 601, Minutes: 60}}}, nil, domain.ErrInvalidInterval},
		"31 February":           {map[domain.Date][]domain.DayInterval{{Year: 2026, Month: time.February, Day: 31}: {}}, nil, domain.ErrInvalidDate},
		"clashes with another branch": {nil, []domain.WeeklyHours{func() domain.WeeklyHours {
			w, _ := domain.NewWeeklyHours([]domain.WeeklyInterval{iv(time.Friday, "01:00", 60)}) // inside Thursday's night shift
			return w
		}()}, domain.ErrScheduleClash},
	} {
		if err := s.Set(weekly, tt.overrides, tt.elsewhere, t0); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.want)
		}
	}
	tooMany := map[domain.Date][]domain.DayInterval{}
	for i := range domain.MaxOverrides + 1 {
		tooMany[thursday.AddDays(i)] = nil
	}
	if err := s.Set(weekly, tooMany, nil, t0); !errors.Is(err, domain.ErrTooManyOverrides) {
		t.Errorf("too many overrides: %v", err)
	}
	if s.Version() != 1 {
		t.Error("refused schedules changed the version")
	}
	// Not overlapping another branch: fine.
	other, _ := domain.NewWeeklyHours([]domain.WeeklyInterval{iv(time.Friday, "02:00", 60), iv(time.Monday, "09:00", 60)})
	if err := s.Set(weekly, nil, []domain.WeeklyHours{other}, t0); err != nil {
		t.Errorf("no clash: %v", err)
	}
}

func TestWeeklyOverlaps(t *testing.T) {
	t.Parallel()
	w := func(i ...domain.WeeklyInterval) domain.WeeklyHours { h, _ := domain.NewWeeklyHours(i); return h }
	sat := w(iv(time.Saturday, "22:00", 240)) // into Sunday 02:00
	for name, tt := range map[string]struct {
		a, b domain.WeeklyHours
		want bool
	}{
		"same time":                 {w(iv(time.Monday, "09:00", 60)), w(iv(time.Monday, "09:30", 60)), true},
		"touching":                  {w(iv(time.Monday, "09:00", 60)), w(iv(time.Monday, "10:00", 60)), false},
		"Saturday night on Sunday":  {sat, w(iv(time.Sunday, "01:00", 60)), true},
		"Sunday after the night":    {sat, w(iv(time.Sunday, "02:00", 60)), false},
		"closed never overlaps":     {domain.WeeklyHours{}, sat, false},
		"different days, same hour": {w(iv(time.Monday, "09:00", 60)), w(iv(time.Tuesday, "09:00", 60)), false},
	} {
		if got := tt.a.Overlaps(tt.b); got != tt.want || tt.b.Overlaps(tt.a) != tt.want {
			t.Errorf("%s: Overlaps = %v, want %v (both ways)", name, got, tt.want)
		}
	}
}

func TestTimeOff(t *testing.T) {
	t.Parallel()
	business, staff := shared.NewID[shared.BusinessTag](), shared.NewID[shared.StaffTag]()
	from := time.Date(2026, 10, 5, 10, 0, 0, 123456789, time.FixedZone("AST", 3*3600))
	off, err := domain.NewTimeOff(shared.NewID[domain.TimeOffTag](), business, staff, from, from.Add(2*time.Hour), "  موعد طبيب  ", t0)
	if err != nil {
		t.Fatal(err)
	}
	if off.Reason() != "موعد طبيب" || off.Span().Start().Location() != time.UTC || off.Span().Duration() != 2*time.Hour ||
		!off.Span().Start().Equal(from.Truncate(time.Microsecond)) {
		t.Errorf("time off = %+v", off)
	}
	for name, tt := range map[string]struct {
		to     time.Time
		reason string
		want   error
	}{
		"ends before it starts": {from.Add(-time.Hour), "", domain.ErrInvalidTimeOff},
		"no length":             {from, "", domain.ErrInvalidTimeOff},
		"longer than a year":    {from.Add(367 * 24 * time.Hour), "", domain.ErrInvalidTimeOff},
		"long reason":           {from.Add(time.Hour), strings.Repeat("س", 201), domain.ErrReasonTooLong},
	} {
		if _, err := domain.NewTimeOff(shared.NewID[domain.TimeOffTag](), business, staff, from, tt.to, tt.reason, t0); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.want)
		}
	}
}
