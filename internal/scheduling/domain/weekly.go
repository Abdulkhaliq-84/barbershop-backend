// Package domain holds scheduling's rules: when work can happen — branch
// opening hours, barber schedules, time off (docs/architecture/domain-model.md §3.4).
package domain

import (
	"cmp"
	"slices"
	"time"
)

// Time-of-day rules. Times are branch-local wall-clock minutes, because
// "we open at 4 pm" must stay 4 pm whatever the UTC offset.
const (
	MinutesPerDay  = 24 * 60
	MinutesPerWeek = 7 * MinutesPerDay
	Step           = 5 // minutes: times and lengths are multiples of 5
	MaxPerDay      = 4 // intervals starting on one weekday (split shifts)
)

// WeeklyInterval is a repeating stretch of time: it starts on Day at Start
// minutes after midnight and lasts Minutes. It may run past midnight into
// the next day (Thursday 16:00 for 600 minutes ends Friday 02:00) — it still
// belongs to the day it starts on.
type WeeklyInterval struct {
	Day     time.Weekday
	Start   int // minutes after midnight, 0–1435
	Minutes int // 5–1440
}

// End returns the minute after midnight on Day at which it ends; above 1440
// means the next day.
func (i WeeklyInterval) End() int { return i.Start + i.Minutes }

// weekStart is the interval's start in minutes since Sunday 00:00.
func (i WeeklyInterval) weekStart() int { return int(i.Day)*MinutesPerDay + i.Start }

// WeeklyHours is a week of repeating intervals — opening hours or a barber's
// usual shifts. The zero value is "closed all week".
type WeeklyHours struct {
	intervals []WeeklyInterval // sorted by weekday, then start
}

// NewWeeklyHours validates intervals: each on a real weekday, on 5-minute
// steps, 5 minutes to 24 hours long; at most 4 starting per day; and no two
// overlapping — including one that runs past Saturday midnight into Sunday.
func NewWeeklyHours(intervals []WeeklyInterval) (WeeklyHours, error) {
	sorted := slices.Clone(intervals)
	slices.SortFunc(sorted, func(a, b WeeklyInterval) int { return cmp.Compare(a.weekStart(), b.weekStart()) })
	perDay := map[time.Weekday]int{}
	for _, i := range sorted {
		if i.Day < time.Sunday || i.Day > time.Saturday {
			return WeeklyHours{}, ErrInvalidWeekday
		}
		if i.Start < 0 || i.Start >= MinutesPerDay || i.Start%Step != 0 ||
			i.Minutes < Step || i.Minutes > MinutesPerDay || i.Minutes%Step != 0 {
			return WeeklyHours{}, ErrInvalidInterval
		}
		if perDay[i.Day]++; perDay[i.Day] > MaxPerDay {
			return WeeklyHours{}, ErrTooManyIntervals
		}
	}
	for n := range sorted {
		cur, next := sorted[n], sorted[(n+1)%len(sorted)]
		end, nextStart := cur.weekStart()+cur.Minutes, next.weekStart()
		if n == len(sorted)-1 {
			nextStart += MinutesPerWeek // the first interval, next week
		}
		if len(sorted) > 1 && end > nextStart {
			return WeeklyHours{}, ErrOverlappingIntervals
		}
	}
	return WeeklyHours{intervals: sorted}, nil
}

// Intervals returns the intervals by weekday, then start.
func (w WeeklyHours) Intervals() []WeeklyInterval { return slices.Clone(w.intervals) }

// IsClosed reports whether there are no intervals at all.
func (w WeeklyHours) IsClosed() bool { return len(w.intervals) == 0 }

// IntervalFromClock builds an interval from an opening and a closing time,
// in minutes after midnight. A closing time at or before the opening time is
// on the next day (16:00–02:00 is ten hours); 00:00–00:00 is all day.
// closes may be 1440 ("24:00").
func IntervalFromClock(day time.Weekday, opens, closes int) WeeklyInterval {
	minutes := ((closes-opens)%MinutesPerDay + MinutesPerDay) % MinutesPerDay
	if minutes == 0 {
		minutes = MinutesPerDay
	}
	return WeeklyInterval{Day: day, Start: opens, Minutes: minutes}
}

// Clock returns the interval's opening and closing times in minutes after
// midnight. closes is 1440 ("24:00") when it ends exactly at the day's end,
// and wraps past midnight otherwise (a ten-hour 16:00 shift closes at 02:00).
func (i WeeklyInterval) Clock() (opens, closes int) {
	end := i.End()
	if end == MinutesPerDay {
		return i.Start, MinutesPerDay
	}
	return i.Start, end % MinutesPerDay
}

// Overlaps reports whether any interval of w overlaps any interval of o on
// the weekly clock (a Saturday-night shift reaches into Sunday). Used so one
// person's schedules at two branches can't put them in both at once.
func (w WeeklyHours) Overlaps(o WeeklyHours) bool {
	for _, a := range w.intervals {
		for _, b := range o.intervals {
			as, bs := a.weekStart(), b.weekStart()
			for _, shift := range [3]int{-MinutesPerWeek, 0, MinutesPerWeek} {
				if as < bs+shift+b.Minutes && bs+shift < as+a.Minutes {
					return true
				}
			}
		}
	}
	return false
}
