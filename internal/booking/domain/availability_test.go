package domain_test

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
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

func span(t testing.TB, from, to time.Time) shared.Interval {
	t.Helper()
	i, err := shared.NewInterval(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func starts(slots []domain.Slot, loc *time.Location) []string {
	var out []string
	for _, s := range slots {
		out = append(out, s.Start.In(loc).Format("15:04"))
	}
	return out
}

// Thursday 4 October 2029 in Riyadh: a barber working 16:00 to 02:00, with
// an appointment 18:00–18:30 and a 30-minute haircut to fit.
func TestSlotsThursdayNight(t *testing.T) {
	t.Parallel()
	riyadh := mustLoc(t, "Asia/Riyadh")
	day, err := domain.ParseDay("2029-10-04")
	if err != nil {
		t.Fatal(err)
	}
	at := func(h, m int) time.Time { return time.Date(2029, 10, 4, h, m, 0, 0, riyadh) }
	ali := domain.Candidate{
		Staff:   shared.NewID[shared.StaffTag](),
		Length:  30 * time.Minute,
		Windows: []shared.Interval{span(t, at(16, 0), at(26, 0))},
		Busy:    []shared.Interval{span(t, at(18, 0), at(18, 30))},
	}
	rules := domain.SlotRules{Location: riyadh, Interval: 15 * time.Minute, Earliest: at(17, 0), Latest: at(48, 0)}
	got := starts(domain.Slots(day, rules, []domain.Candidate{ali}), riyadh)

	// From 17:00 (the lead time); nothing that would run into 18:00–18:30;
	// then every quarter hour to 23:45 — the day's last start, which ends
	// after midnight, still inside the night shift.
	want := []string{"17:00", "17:15", "17:30"}
	for m := 18*60 + 30; m < 24*60; m += 15 {
		want = append(want, time.Date(2029, 1, 1, 0, m, 0, 0, time.UTC).Format("15:04"))
	}
	if !slices.Equal(got, want) {
		t.Errorf("slots = %v\nwant    %v", got, want)
	}

	// A second barber free from 17:30 appears alongside; "any barber" is the
	// union, each start listing who's free.
	sara := domain.Candidate{Staff: shared.NewID[shared.StaffTag](), Length: 45 * time.Minute, Windows: []shared.Interval{span(t, at(17, 30), at(19, 0))}}
	slots := domain.Slots(day, rules, []domain.Candidate{ali, sara})
	byStart := map[string][]shared.StaffID{}
	for _, s := range slots {
		byStart[s.Start.In(riyadh).Format("15:04")] = s.Staff
	}
	for start, want := range map[string][]shared.StaffID{
		"17:15": {ali.Staff},
		"17:30": {ali.Staff, sara.Staff},
		"17:45": {sara.Staff}, // Ali has the 18:00 appointment
		"18:15": {sara.Staff}, // 18:15 + 45 min = 19:00, the end of Sara's window
		"18:30": {ali.Staff},  // too late for Sara's 45 minutes
	} {
		if !slices.Equal(byStart[start], want) {
			t.Errorf("%s: %v, want %v", start, byStart[start], want)
		}
	}

	// The horizon's end: nothing at or after Latest.
	rules.Latest = at(20, 0)
	if got := starts(domain.Slots(day, rules, []domain.Candidate{ali}), riyadh); got[len(got)-1] != "19:45" {
		t.Errorf("last slot before 20:00 = %v", got[len(got)-1])
	}
	// No length (nothing to book), no slots.
	ali.Length = 0
	if slots := domain.Slots(day, rules, []domain.Candidate{ali}); len(slots) != 0 {
		t.Errorf("zero length: %v", slots)
	}
}

// The grid follows the clock. The night clocks go back, 01:00–02:00 happens
// twice and both are real times a barber can work: a 25-hour day. The night
// they go forward, 01:00–02:00 doesn't exist: 23 hours.
func TestSlotsOnADaylightSavingDay(t *testing.T) {
	t.Parallel()
	london := mustLoc(t, "Europe/London")
	for date, starts := range map[string]int{
		"2029-10-28": 49, // every half hour of 25 hours, the last an hour before midnight
		"2029-03-25": 45, // of 23 hours
	} {
		day, _ := domain.ParseDay(date)
		start, end := day.Start(london), day.AddDays(1).Start(london)
		c := domain.Candidate{Staff: shared.NewID[shared.StaffTag](), Length: time.Hour, Windows: []shared.Interval{span(t, start, end)}}
		slots := domain.Slots(day, domain.SlotRules{Location: london, Interval: 30 * time.Minute, Earliest: start, Latest: end}, []domain.Candidate{c})
		for i, s := range slots {
			if local := s.Start.In(london); local.Minute()%30 != 0 {
				t.Errorf("%s: %s is off the grid", date, local)
			}
			if i > 0 && !s.Start.After(slots[i-1].Start) {
				t.Errorf("%s: %s after %s", date, s.Start, slots[i-1].Start)
			}
		}
		if last := slots[len(slots)-1].Start.In(london).Format("15:04"); last != "23:00" || len(slots) != starts {
			t.Errorf("%s: %d starts, the last %s; want %d, the last 23:00", date, len(slots), last, starts)
		}
	}
}

// The oracle: the rules, checked minute by minute — no interval
// arithmetic, so a mistake in Covers/Overlaps can't hide.
func oracle(day domain.Day, r domain.SlotRules, cands []domain.Candidate) map[time.Time][]shared.StaffID {
	free := func(c domain.Candidate, start time.Time) bool {
		if c.Length <= 0 {
			return false
		}
		for m := start; m.Before(start.Add(c.Length)); m = m.Add(time.Minute) {
			worked, busy := false, false
			for _, w := range c.Windows {
				worked = worked || w.Contains(m)
			}
			for _, b := range c.Busy {
				busy = busy || b.Contains(m)
			}
			if !worked || busy {
				return false
			}
		}
		return true
	}
	out := map[time.Time][]shared.StaffID{}
	start, end := day.Start(r.Location), day.AddDays(1).Start(r.Location)
	for t := start; t.Before(end); t = t.Add(time.Minute) {
		local := t.In(r.Location)
		if (local.Hour()*60+local.Minute())%int(r.Interval/time.Minute) != 0 || t.Before(r.Earliest) || !t.Before(r.Latest) {
			continue
		}
		for _, c := range cands {
			if free(c, t) {
				out[t] = append(out[t], c.Staff)
			}
		}
	}
	return out
}

// random builds a day's candidates on whole minutes: merged windows and
// scattered appointments, some crossing midnight at either end.
func random(rng *rand.Rand, loc *time.Location) (domain.Day, domain.SlotRules, []domain.Candidate) {
	day := domain.DayOf(time.Date(2029, time.Month(1+rng.IntN(12)), 1+rng.IntN(28), 0, 0, 0, 0, time.UTC))
	base := day.Start(loc).Add(-6 * time.Hour)
	minute := func(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }
	intervals := []time.Duration{5, 10, 15, 20, 30, 60}
	r := domain.SlotRules{
		Location: loc,
		Interval: intervals[rng.IntN(len(intervals))] * time.Minute,
		Earliest: minute(rng.IntN(36 * 60)),
		Latest:   minute(12*60 + rng.IntN(36*60)),
	}
	var cands []domain.Candidate
	for range 1 + rng.IntN(4) {
		c := domain.Candidate{Staff: shared.NewID[shared.StaffTag](), Length: time.Duration(5*(1+rng.IntN(36))) * time.Minute}
		at := rng.IntN(8 * 60)
		for range rng.IntN(4) { // separate, non-touching windows
			length := 30 + rng.IntN(10*60)
			w, _ := shared.NewInterval(minute(at), minute(at+length))
			c.Windows = append(c.Windows, w)
			at += length + 1 + rng.IntN(5*60)
		}
		for range rng.IntN(6) {
			start := rng.IntN(40 * 60)
			b, _ := shared.NewInterval(minute(start), minute(start+5+rng.IntN(120)))
			c.Busy = append(c.Busy, b)
		}
		cands = append(cands, c)
	}
	return day, r, cands
}

func checkAgainstOracle(t *testing.T, day domain.Day, r domain.SlotRules, cands []domain.Candidate) {
	t.Helper()
	want := oracle(day, r, cands)
	got := domain.Slots(day, r, cands)
	if len(got) != len(want) {
		t.Fatalf("%s %s every %s: %d slots, oracle %d", day, r.Location, r.Interval, len(got), len(want))
	}
	for _, s := range got {
		if !slices.Equal(s.Staff, want[s.Start]) {
			t.Fatalf("%s: %v, oracle %v", s.Start, s.Staff, want[s.Start])
		}
	}
}

func TestSlotsAgainstOracle(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(20291004, 7))
	for _, zone := range []string{"Asia/Riyadh", "Europe/London", "America/New_York", "Asia/Kathmandu"} {
		loc := mustLoc(t, zone)
		for range 150 {
			day, r, cands := random(rng, loc)
			checkAgainstOracle(t, day, r, cands)
		}
	}
}

// The days clocks change, where the grid is easiest to get wrong.
func TestSlotsAgainstOracleOnDaylightSavingDays(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(3, 11))
	for zone, dates := range map[string][]string{
		"Europe/London":    {"2029-03-25", "2029-10-28"},
		"America/New_York": {"2029-03-11", "2029-11-04"},
	} {
		loc := mustLoc(t, zone)
		for _, date := range dates {
			day, _ := domain.ParseDay(date)
			for range 40 {
				_, r, cands := random(rng, loc)
				// Move the random day onto the change day.
				shift := day.Start(loc).Sub(domain.DayOf(r.Earliest.In(loc)).Start(loc))
				r.Earliest, r.Latest = r.Earliest.Add(shift), r.Latest.Add(shift)
				for i := range cands {
					cands[i].Windows = moved(cands[i].Windows, shift)
					cands[i].Busy = moved(cands[i].Busy, shift)
				}
				checkAgainstOracle(t, day, r, cands)
			}
		}
	}
}

func moved(spans []shared.Interval, by time.Duration) []shared.Interval {
	out := make([]shared.Interval, 0, len(spans))
	for _, s := range spans {
		m, _ := shared.NewInterval(s.Start().Add(by), s.End().Add(by))
		out = append(out, m)
	}
	return out
}

func FuzzSlots(f *testing.F) {
	f.Add(uint64(1), uint8(0))
	f.Add(uint64(20291028), uint8(1))
	zones := []string{"Asia/Riyadh", "Europe/London", "America/New_York", "Asia/Kathmandu"}
	f.Fuzz(func(t *testing.T, seed uint64, zone uint8) {
		loc := mustLoc(t, zones[int(zone)%len(zones)])
		day, r, cands := random(rand.New(rand.NewPCG(seed, seed^0x5bd1e995)), loc)
		checkAgainstOracle(t, day, r, cands)
	})
}

// A busy branch day: eight barbers, two shifts each, twenty appointments
// each, a 15-minute grid — what one availability request computes.
func BenchmarkSlots(b *testing.B) {
	riyadh := mustLoc(b, "Asia/Riyadh")
	day, _ := domain.ParseDay("2029-10-04")
	at := func(h, m int) time.Time { return time.Date(2029, 10, 4, h, m, 0, 0, riyadh) }
	var cands []domain.Candidate
	for range 8 {
		c := domain.Candidate{
			Staff:   shared.NewID[shared.StaffTag](),
			Length:  45 * time.Minute,
			Windows: []shared.Interval{span(b, at(9, 0), at(13, 0)), span(b, at(16, 0), at(26, 0))},
		}
		for i := range 20 {
			c.Busy = append(c.Busy, span(b, at(9, i*40), at(9, i*40+30)))
		}
		cands = append(cands, c)
	}
	rules := domain.SlotRules{Location: riyadh, Interval: 15 * time.Minute, Earliest: at(0, 0), Latest: at(48, 0)}
	for b.Loop() {
		domain.Slots(day, rules, cands)
	}
}
