package shared

import (
	"errors"
	"time"
)

// ErrInvalidInterval reports an interval whose end is not after its start.
var ErrInvalidInterval = errors.New("interval end must be after start")

// Interval is a half-open time range [start, end). Half-open means an
// appointment 16:00–16:30 and one 16:30–17:00 touch but do not overlap —
// the same rule as Postgres tstzrange, which prevents double booking.
type Interval struct {
	start time.Time
	end   time.Time
}

// NewInterval returns [start, end); end must be after start.
func NewInterval(start, end time.Time) (Interval, error) {
	if !end.After(start) {
		return Interval{}, ErrInvalidInterval
	}
	return Interval{start: start, end: end}, nil
}

// Start returns the inclusive start.
func (i Interval) Start() time.Time { return i.start }

// End returns the exclusive end.
func (i Interval) End() time.Time { return i.end }

// Duration returns end − start.
func (i Interval) Duration() time.Duration { return i.end.Sub(i.start) }

// Overlaps reports whether the two intervals share any instant.
func (i Interval) Overlaps(o Interval) bool {
	return i.start.Before(o.end) && o.start.Before(i.end)
}

// Contains reports whether t is inside the interval (start ≤ t < end).
func (i Interval) Contains(t time.Time) bool {
	return !t.Before(i.start) && t.Before(i.end)
}

// Covers reports whether o lies completely inside i — e.g. an appointment
// inside a barber's working window.
func (i Interval) Covers(o Interval) bool {
	return !o.start.Before(i.start) && !o.end.After(i.end)
}
