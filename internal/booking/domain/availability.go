package domain

import (
	"iter"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MaxServices is how many services one booking may combine (one barber
// performs them back to back).
const MaxServices = 5

// Candidate is a barber who could take the booking.
type Candidate struct {
	Staff shared.StaffID
	// Length is what the booking would take them: their durations for the
	// chosen services, plus the branch's buffer. Nothing else fits in it.
	Length time.Duration
	// Windows are when they can work (scheduling: opening hours ∩ their
	// schedule − time off), sorted and merged, so a booking fits inside
	// one window or not at all.
	Windows []shared.Interval
	// Busy are their active appointments, each including its buffer.
	Busy []shared.Interval
}

// SlotRules bound which start times are offered on a day.
type SlotRules struct {
	Location *time.Location
	Interval time.Duration // starts every Interval on the clock; divides an hour
	Earliest time.Time     // now + the minimum lead time
	Latest   time.Time     // exclusive: the end of the booking horizon's last day
}

// Slot is a start time and who could take it.
type Slot struct {
	Start time.Time
	Staff []shared.StaffID // in candidate order
}

// Slots returns the start times on day at which at least one candidate
// can take the booking: on the clock grid, within [Earliest, Latest), and
// for each barber listed, [start, start + Length) lies inside one of their
// working windows and overlaps none of their appointments.
//
// It is a pure function — no clock, no database — so every rule is tested
// directly (property tests and fuzzing). Slots are advisory: someone may
// take one before this customer books it. The database has the last word
// (the exclusion constraint, M5.3).
func Slots(day Day, r SlotRules, candidates []Candidate) []Slot {
	step := int(r.Interval / time.Minute)
	if step <= 0 {
		return nil
	}
	var slots []Slot
	for start := range grid(day, r.Location, step) {
		if start.Before(r.Earliest) || !start.Before(r.Latest) {
			continue
		}
		var free []shared.StaffID
		for _, c := range candidates {
			if c.CanTake(start) {
				free = append(free, c.Staff)
			}
		}
		if len(free) > 0 {
			slots = append(slots, Slot{Start: start, Staff: free})
		}
	}
	return slots
}

// grid yields the day's real instants whose local clock reads a multiple
// of step minutes after midnight, in order. It walks instants minute by
// minute rather than building clock times with time.Date: on the night the
// clocks go back, 01:00–02:00 happens twice (a barber working then can take
// both), and which of the two time.Date returns is not guaranteed.
func grid(day Day, loc *time.Location, step int) iter.Seq[time.Time] {
	return func(yield func(time.Time) bool) {
		end := day.AddDays(1).Start(loc)
		for t := day.Start(loc); t.Before(end); t = t.Add(time.Minute) {
			if OnGrid(t, loc, time.Duration(step)*time.Minute) && !yield(t) {
				return
			}
		}
	}
}

// CanTake reports whether the candidate could work [start, start+Length):
// inside one of their windows, clear of their appointments.
func (c Candidate) CanTake(start time.Time) bool {
	span, err := shared.NewInterval(start, start.Add(c.Length))
	if err != nil {
		return false // no length: nothing to book
	}
	inside := false
	for _, w := range c.Windows {
		if w.Covers(span) {
			inside = true
			break
		}
	}
	if !inside {
		return false
	}
	for _, b := range c.Busy {
		if b.Overlaps(span) {
			return false
		}
	}
	return true
}

// OnGrid reports whether t's local clock, in loc, is a whole number of
// intervals after midnight — a start time the branch offers.
func OnGrid(t time.Time, loc *time.Location, interval time.Duration) bool {
	step := int(interval / time.Minute)
	local := t.In(loc)
	return step > 0 && local.Second() == 0 && local.Nanosecond() == 0 && (local.Hour()*60+local.Minute())%step == 0
}
