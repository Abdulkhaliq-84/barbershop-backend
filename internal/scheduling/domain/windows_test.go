package domain_test

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func mustLoc(t testing.TB, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func weekly(t testing.TB, intervals ...domain.WeeklyInterval) domain.WeeklyHours {
	t.Helper()
	w, err := domain.NewWeeklyHours(intervals)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func scheduleOf(t testing.TB, w domain.WeeklyHours, overrides map[domain.Date][]domain.DayInterval) *domain.BarberSchedule {
	t.Helper()
	s := domain.NewBarberSchedule(shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), shared.NewID[shared.StaffTag]())
	if err := s.Set(w, overrides, nil, t0); err != nil {
		t.Fatal(err)
	}
	return s
}

func span(t testing.TB, start, end time.Time) shared.Interval {
	t.Helper()
	s, err := shared.NewInterval(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWorkingWindowsExamples(t *testing.T) {
	t.Parallel()
	riyadh := mustLoc(t, "Asia/Riyadh")
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, riyadh) }
	// 1 October 2026 is a Thursday. The branch opens Thursday 16:00–02:00;
	// the barber works Thursday 18:00–03:00.
	in := domain.WindowsInput{
		Location: riyadh,
		Opening:  weekly(t, iv(time.Thursday, "16:00", 600)),
		Schedule: scheduleOf(t, weekly(t, iv(time.Thursday, "18:00", 540)), nil),
	}
	thursday := domain.Date{Year: 2026, Month: time.October, Day: 1}

	tests := []struct {
		name     string
		change   func(*domain.WindowsInput)
		from, to time.Time
		want     [][2]time.Time
	}{
		{
			"the night shift, where both overlap", nil, at(1, 0, 0), at(3, 0, 0),
			[][2]time.Time{{at(1, 18, 0), at(2, 2, 0)}},
		},
		{
			"from Friday midnight: last night's tail", nil, at(2, 0, 0), at(3, 0, 0),
			[][2]time.Time{{at(2, 0, 0), at(2, 2, 0)}},
		},
		{
			"clipped to the range", nil, at(1, 20, 0), at(1, 21, 0),
			[][2]time.Time{{at(1, 20, 0), at(1, 21, 0)}},
		},
		{"time off in the middle", func(in *domain.WindowsInput) {
			in.TimeOff = []shared.Interval{span(t, at(1, 20, 0), at(1, 21, 0))}
		}, at(1, 0, 0), at(3, 0, 0), [][2]time.Time{{at(1, 18, 0), at(1, 20, 0)}, {at(1, 21, 0), at(2, 2, 0)}}},
		{"Friday closed cuts the tail after midnight", func(in *domain.WindowsInput) {
			in.Closed = map[domain.Date]bool{thursday.AddDays(1): true}
		}, at(1, 0, 0), at(3, 0, 0), [][2]time.Time{{at(1, 18, 0), at(2, 0, 0)}}},
		{"a day off overrides Thursday", func(in *domain.WindowsInput) {
			in.Schedule = scheduleOf(t, weekly(t, iv(time.Thursday, "18:00", 540)), map[domain.Date][]domain.DayInterval{thursday: {}})
		}, at(1, 0, 0), at(3, 0, 0), nil},
		{"the next Thursday still works", func(in *domain.WindowsInput) {
			in.Schedule = scheduleOf(t, weekly(t, iv(time.Thursday, "18:00", 540)), map[domain.Date][]domain.DayInterval{thursday: {}})
		}, at(8, 0, 0), at(9, 12, 0), [][2]time.Time{{at(8, 18, 0), at(9, 2, 0)}}},
		{"an empty range", nil, at(2, 0, 0), at(2, 0, 0), nil},
	}
	for _, tt := range tests {
		input := in
		if tt.change != nil {
			tt.change(&input)
		}
		got := domain.WorkingWindows(input, tt.from, tt.to)
		if len(got) != len(tt.want) {
			t.Errorf("%s: windows = %v, want %v", tt.name, got, tt.want)
			continue
		}
		for i, w := range tt.want {
			if !got[i].Start().Equal(w[0]) || !got[i].End().Equal(w[1]) {
				t.Errorf("%s: window %d = %v–%v, want %v–%v", tt.name, i, got[i].Start(), got[i].End(), w[0], w[1])
			}
		}
	}
}

// Wall-clock hours across a daylight-saving change: London falls back on
// 25 October 2026 at 02:00 BST → 01:00 GMT, so 00:00–04:00 lasts five hours.
func TestWorkingWindowsAcrossDST(t *testing.T) {
	t.Parallel()
	london := mustLoc(t, "Europe/London")
	allWeek := func(start string, minutes int) domain.WeeklyHours {
		var ivs []domain.WeeklyInterval
		for d := time.Sunday; d <= time.Saturday; d++ {
			ivs = append(ivs, iv(d, start, minutes))
		}
		return weekly(t, ivs...)
	}
	in := domain.WindowsInput{Location: london, Opening: allWeek("00:00", 240), Schedule: scheduleOf(t, allWeek("00:00", 240), nil)}
	day := time.Date(2026, 10, 25, 0, 0, 0, 0, london)
	got := domain.WorkingWindows(in, day, day.Add(24*time.Hour))
	if len(got) != 1 || got[0].Duration() != 5*time.Hour || !got[0].End().Equal(time.Date(2026, 10, 25, 4, 0, 0, 0, london)) {
		t.Fatalf("fall-back day = %v", got)
	}
}

// The property test compares WorkingWindows with an independent, slow
// oracle — "is this instant working time?" answered from the definitions —
// at the middle of every 5-minute step, for random schedules in four zones,
// including daylight-saving changes and a +05:45 offset.
func TestWorkingWindowsMatchesOracle(t *testing.T) {
	t.Parallel()
	zones := []string{"Asia/Riyadh", "Europe/London", "America/New_York", "Asia/Kathmandu"}
	rng := rand.New(rand.NewPCG(20261001, 42))
	for n := range 300 {
		loc := mustLoc(t, zones[n%len(zones)])
		// Late October to early November 2026: both DST changes.
		base := time.Date(2026, 10, 22+rng.IntN(12), 0, 0, 0, 0, loc)
		from := base.Add(time.Duration(rng.IntN(48)) * 30 * time.Minute)
		to := from.Add(time.Duration(1+rng.IntN(96)) * time.Hour)
		in := randomInput(t, rng, loc, base)
		checkAgainstOracle(t, n, in, from, to)
	}
}

func randomWeekly(rng *rand.Rand) domain.WeeklyHours {
	for {
		var ivs []domain.WeeklyInterval
		for d := time.Sunday; d <= time.Saturday; d++ {
			for range rng.IntN(3) {
				ivs = append(ivs, domain.WeeklyInterval{Day: d, Start: rng.IntN(288) * 5, Minutes: (1 + rng.IntN(180)) * 5})
			}
		}
		if w, err := domain.NewWeeklyHours(ivs); err == nil {
			return w
		}
	}
}

func randomInput(t testing.TB, rng *rand.Rand, loc *time.Location, base time.Time) domain.WindowsInput {
	start := domain.DateOf(base)
	overrides := map[domain.Date][]domain.DayInterval{}
	for range rng.IntN(3) {
		d := start.AddDays(rng.IntN(6) - 1)
		var day []domain.DayInterval
		if rng.IntN(2) == 0 {
			day = append(day, domain.DayInterval{Start: rng.IntN(288) * 5, Minutes: (1 + rng.IntN(200)) * 5})
		}
		overrides[d] = day
	}
	in := domain.WindowsInput{
		Location: loc,
		Opening:  randomWeekly(rng),
		Schedule: scheduleOf(t, randomWeekly(rng), overrides),
		Closed:   map[domain.Date]bool{},
	}
	if rng.IntN(3) == 0 {
		in.Closed[start.AddDays(rng.IntN(5))] = true
	}
	for range rng.IntN(4) {
		s := base.UTC().Truncate(5 * time.Minute).Add(time.Duration(rng.IntN(5*288)) * 5 * time.Minute)
		in.TimeOff = append(in.TimeOff, span(t, s, s.Add(time.Duration(1+rng.IntN(120))*5*time.Minute)))
	}
	return in
}

func checkAgainstOracle(t *testing.T, n int, in domain.WindowsInput, from, to time.Time) {
	t.Helper()
	got := domain.WorkingWindows(in, from, to)
	for i, w := range got {
		if w.Start().Before(from) || w.End().After(to) {
			t.Fatalf("case %d: window %v outside [%v, %v)", n, w, from, to)
		}
		if i > 0 && !got[i-1].End().Before(w.Start()) {
			t.Fatalf("case %d: windows %v and %v overlap or touch", n, got[i-1], w)
		}
	}
	in5 := func(tt time.Time) bool {
		return slices.ContainsFunc(got, func(w shared.Interval) bool { return !tt.Before(w.Start()) && tt.Before(w.End()) })
	}
	for tt := from.UTC().Truncate(5 * time.Minute).Add(150 * time.Second); tt.Before(to); tt = tt.Add(5 * time.Minute) {
		if tt.Before(from) {
			continue
		}
		if want := oracle(in, tt); in5(tt) != want {
			t.Fatalf("case %d (%s): at %v (local %v) got working=%v, oracle says %v", n, in.Location, tt, tt.In(in.Location), !want, want)
		}
	}
}

// oracle answers "is t working time?" straight from the definitions.
func oracle(in domain.WindowsInput, t time.Time) bool {
	today := domain.DateOf(t.In(in.Location))
	if in.Closed[today] {
		return false
	}
	for _, off := range in.TimeOff {
		if !t.Before(off.Start()) && t.Before(off.End()) {
			return false
		}
	}
	covered := func(d domain.Date, intervals []domain.DayInterval) bool {
		for _, i := range intervals {
			if s, e := d.At(i.Start, in.Location), d.At(i.Start+i.Minutes, in.Location); !t.Before(s) && t.Before(e) {
				return true
			}
		}
		return false
	}
	openOn := func(d domain.Date) []domain.DayInterval {
		var out []domain.DayInterval
		for _, i := range in.Opening.Intervals() {
			if i.Day == d.Weekday() {
				out = append(out, domain.DayInterval{Start: i.Start, Minutes: i.Minutes})
			}
		}
		return out
	}
	open, works := false, false
	for _, d := range []domain.Date{today.AddDays(-1), today} {
		open = open || covered(d, openOn(d))
		works = works || covered(d, in.Schedule.IntervalsStarting(d))
	}
	return open && works
}

// FuzzWorkingWindows lets `go test -fuzz` search for inputs that break the
// oracle; the seeds run as ordinary tests.
func FuzzWorkingWindows(f *testing.F) {
	for _, seed := range []uint64{1, 2, 3, 20261025, 20261101} {
		f.Add(seed)
	}
	zones := []string{"Asia/Riyadh", "Europe/London", "America/New_York", "Asia/Kathmandu"}
	f.Fuzz(func(t *testing.T, seed uint64) {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		loc := mustLoc(t, zones[seed%uint64(len(zones))])
		base := time.Date(2026, 10, 22+rng.IntN(12), 0, 0, 0, 0, loc)
		from := base.Add(time.Duration(rng.IntN(48)) * 30 * time.Minute)
		checkAgainstOracle(t, int(seed%1000), randomInput(t, rng, loc, base), from, from.Add(time.Duration(1+rng.IntN(72))*time.Hour))
	})
}

// Back-to-back intervals are one window: a split listing 09:00–12:00 and
// 12:00–15:00 is open from 09:00 to 15:00 without a gap.
func TestWorkingWindowsMergeTouchingIntervals(t *testing.T) {
	t.Parallel()
	riyadh := mustLoc(t, "Asia/Riyadh")
	in := domain.WindowsInput{
		Location: riyadh,
		Opening:  weekly(t, iv(time.Sunday, "09:00", 180), iv(time.Sunday, "12:00", 180)),
		Schedule: scheduleOf(t, weekly(t, iv(time.Sunday, "10:00", 120), iv(time.Sunday, "12:00", 120)), nil),
	}
	sunday := time.Date(2026, 10, 4, 0, 0, 0, 0, riyadh)
	got := domain.WorkingWindows(in, sunday, sunday.Add(24*time.Hour))
	if len(got) != 1 || !got[0].Start().Equal(sunday.Add(10*time.Hour)) || !got[0].End().Equal(sunday.Add(14*time.Hour)) {
		t.Fatalf("windows = %v, want one 10:00–14:00", got)
	}
}
