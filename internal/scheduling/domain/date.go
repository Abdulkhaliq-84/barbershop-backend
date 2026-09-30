package domain

import (
	"fmt"
	"time"
)

// Date is a calendar date with no time or zone — "5 October 2026" at the
// branch, whatever the UTC offset. Date overrides and (later) closures use it.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns t's date in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// ParseDate reads YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, ErrInvalidDate
	}
	return DateOf(t), nil
}

// String formats the date as YYYY-MM-DD.
func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day) }

// Weekday returns the date's day of the week.
func (d Date) Weekday() time.Weekday { return d.midnightUTC().Weekday() }

// AddDays returns the date n days later (or earlier).
func (d Date) AddDays(n int) Date { return DateOf(d.midnightUTC().AddDate(0, 0, n)) }

// Compare returns -1, 0 or +1 as d is before, equal to or after o.
func (d Date) Compare(o Date) int { return d.midnightUTC().Compare(o.midnightUTC()) }

// At returns the instant minute minutes after midnight on this date, in loc,
// counted on the wall clock: 1500 minutes is 01:00 the next day. time.Date
// normalizes the overflow, so a date's intervals stay right across DST.
func (d Date) At(minute int, loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, minute, 0, 0, loc)
}

func (d Date) midnightUTC() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

// AsTime returns midnight UTC on the date, for storage as a SQL date.
func (d Date) AsTime() time.Time { return d.midnightUTC() }
