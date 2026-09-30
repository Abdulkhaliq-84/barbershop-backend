package domain

import (
	"slices"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// WindowsInput is everything the working-windows calculation needs about
// one staff member at one branch.
type WindowsInput struct {
	Location *time.Location // the branch's time zone
	Opening  WeeklyHours    // the branch's opening hours
	Closed   map[Date]bool  // dates the branch is closed all day (closures)
	Schedule *BarberSchedule
	TimeOff  []shared.Interval
}

// WorkingWindows returns when a staff member can work at a branch within
// [from, to): the branch's opening hours ∩ their schedule, minus closed
// dates and time off — as real instants, sorted, merged, never touching.
//
// It is a pure function: no clock, no database. Weekly hours and overrides
// are wall-clock times on dates in the branch's location; the calculation
// starts a day before from so the night before's shift, which runs past
// midnight into from's date, is included.
func WorkingWindows(in WindowsInput, from, to time.Time) []shared.Interval {
	if !to.After(from) {
		return nil
	}
	first, last := DateOf(from.In(in.Location)).AddDays(-1), DateOf(to.In(in.Location))
	var open, work, closed []shared.Interval
	for d := first; d.Compare(last) <= 0; d = d.AddDays(1) {
		for _, i := range startingOn(in.Opening, d.Weekday()) {
			open = appendSpan(open, d.At(i.Start, in.Location), d.At(i.Start+i.Minutes, in.Location))
		}
		for _, i := range in.Schedule.IntervalsStarting(d) {
			work = appendSpan(work, d.At(i.Start, in.Location), d.At(i.Start+i.Minutes, in.Location))
		}
		if in.Closed[d] {
			closed = appendSpan(closed, d.At(0, in.Location), d.AddDays(1).At(0, in.Location))
		}
	}
	windows := intersect(normalize(open), normalize(work))
	windows = subtract(windows, normalize(closed))
	windows = subtract(windows, normalize(slices.Clone(in.TimeOff)))
	return clip(windows, from, to)
}

// appendSpan adds [start, end) if it is not empty. A wall-clock interval
// inside a daylight-saving gap can come out empty or reversed.
func appendSpan(spans []shared.Interval, start, end time.Time) []shared.Interval {
	if s, err := shared.NewInterval(start, end); err == nil {
		spans = append(spans, s)
	}
	return spans
}

// normalize sorts spans and merges those that overlap or touch.
func normalize(spans []shared.Interval) []shared.Interval {
	slices.SortFunc(spans, func(a, b shared.Interval) int { return a.Start().Compare(b.Start()) })
	var out []shared.Interval
	for _, s := range spans {
		if n := len(out); n > 0 && !s.Start().After(out[n-1].End()) {
			if s.End().After(out[n-1].End()) {
				out[n-1] = mustSpan(out[n-1].Start(), s.End())
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// intersect returns what two normalized sets have in common.
func intersect(a, b []shared.Interval) []shared.Interval {
	var out []shared.Interval
	for i, j := 0, 0; i < len(a) && j < len(b); {
		start, end := later(a[i].Start(), b[j].Start()), earlier(a[i].End(), b[j].End())
		if start.Before(end) {
			out = append(out, mustSpan(start, end))
		}
		if a[i].End().Before(b[j].End()) {
			i++
		} else {
			j++
		}
	}
	return out
}

// subtract removes b (normalized) from a (normalized).
func subtract(a, b []shared.Interval) []shared.Interval {
	var out []shared.Interval
	j := 0
	for _, s := range a {
		start, end := s.Start(), s.End()
		for j < len(b) && !b[j].End().After(start) {
			j++ // b[j] ends before s starts
		}
		for k := j; k < len(b) && b[k].Start().Before(end); k++ {
			if b[k].Start().After(start) {
				out = append(out, mustSpan(start, b[k].Start()))
			}
			if b[k].End().After(start) {
				start = b[k].End()
			}
		}
		if start.Before(end) {
			out = append(out, mustSpan(start, end))
		}
	}
	return out
}

// clip keeps what lies within [from, to).
func clip(spans []shared.Interval, from, to time.Time) []shared.Interval {
	var out []shared.Interval
	for _, s := range spans {
		start, end := later(s.Start(), from), earlier(s.End(), to)
		if start.Before(end) {
			out = append(out, mustSpan(start, end))
		}
	}
	return out
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// mustSpan builds an interval the caller has already checked is non-empty.
func mustSpan(start, end time.Time) shared.Interval {
	s, err := shared.NewInterval(start, end)
	if err != nil {
		panic("scheduling: empty span " + start.String() + " – " + end.String())
	}
	return s
}
