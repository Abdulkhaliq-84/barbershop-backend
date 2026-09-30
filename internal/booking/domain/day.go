package domain

import (
	"fmt"
	"time"
)

// Day is a calendar date in a branch's own time zone, e.g. "the 4th of
// October in Riyadh" — not an instant. Customers pick days; slots are
// instants on them.
type Day struct {
	year  int
	month time.Month
	day   int
}

// ParseDay reads YYYY-MM-DD, rejecting dates that don't exist (2026-02-30).
func ParseDay(s string) (Day, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Day{}, ErrInvalidDay
	}
	return DayOf(t), nil
}

// DayOf returns the calendar day of t, as t's location sees it.
func DayOf(t time.Time) Day {
	y, m, d := t.Date()
	return Day{year: y, month: m, day: d}
}

// String returns YYYY-MM-DD.
func (d Day) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.year, d.month, d.day) }

// Start returns the first instant of the day in loc.
func (d Day) Start(loc *time.Location) time.Time {
	return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, loc)
}

// AddDays returns the day n days later (earlier if n < 0).
func (d Day) AddDays(n int) Day {
	return DayOf(time.Date(d.year, d.month, d.day+n, 0, 0, 0, 0, time.UTC))
}

// Compare returns -1, 0 or +1 as d is before, the same as, or after o.
func (d Day) Compare(o Day) int {
	return d.Start(time.UTC).Compare(o.Start(time.UTC))
}
