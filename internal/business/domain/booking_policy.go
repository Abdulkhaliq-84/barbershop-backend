package domain

import (
	"errors"
	"slices"
	"time"
)

// ErrInvalidBookingPolicy reports a booking policy outside the allowed
// ranges. The concrete error is a *PolicyError naming the rule.
var ErrInvalidBookingPolicy = errors.New("invalid booking policy")

// PolicyError says which booking-policy rule was broken, in words safe to
// show to the shop owner.
type PolicyError struct {
	Rule string // e.g. "slot interval must be 5, 10, 15, 20, 30 or 60 minutes"
}

func (e *PolicyError) Error() string { return "booking policy: " + e.Rule }

// Unwrap lets errors.Is(err, ErrInvalidBookingPolicy) match.
func (e *PolicyError) Unwrap() error { return ErrInvalidBookingPolicy }

// PolicyRules are the booking policy's settings, as plain values.
type PolicyRules struct {
	MinLead            time.Duration // earliest booking: now + MinLead
	HorizonDays        int           // latest booking: today + HorizonDays
	SlotInterval       time.Duration // start times offered: every SlotInterval
	Buffer             time.Duration // cleanup time after each appointment
	CancellationWindow time.Duration // customers may cancel until start − CancellationWindow
	AutoConfirm        bool          // false: the shop confirms each booking
	PendingExpiry      time.Duration // an unconfirmed booking expires after this
	MaxActiveBookings  int           // future bookings one customer may hold here
}

// BookingPolicy is how a branch takes bookings. It is a value object: it has
// no identity, is replaced as a whole, and can't exist in an invalid state.
type BookingPolicy struct {
	rules PolicyRules
}

// slotIntervals divide an hour evenly, so slots line up on the clock.
var slotIntervals = []time.Duration{5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 20 * time.Minute, 30 * time.Minute, time.Hour}

// NewBookingPolicy validates every rule. The ranges match the database checks.
func NewBookingPolicy(r PolicyRules) (BookingPolicy, error) {
	checks := []struct {
		ok   bool
		rule string
	}{
		{wholeMinutes(r.MinLead) && r.MinLead >= 0 && r.MinLead <= 7*24*time.Hour, "minimum lead time must be 0 to 7 days, in whole minutes"},
		{r.HorizonDays >= 1 && r.HorizonDays <= 180, "booking horizon must be 1 to 180 days"},
		{slices.Contains(slotIntervals, r.SlotInterval), "slot interval must be 5, 10, 15, 20, 30 or 60 minutes"},
		{wholeMinutes(r.Buffer) && r.Buffer >= 0 && r.Buffer <= time.Hour, "buffer must be 0 to 60 minutes, in whole minutes"},
		{wholeMinutes(r.CancellationWindow) && r.CancellationWindow >= 0 && r.CancellationWindow <= 48*time.Hour, "cancellation window must be 0 to 48 hours, in whole minutes"},
		{wholeMinutes(r.PendingExpiry) && r.PendingExpiry >= 5*time.Minute && r.PendingExpiry <= 2*time.Hour, "pending expiry must be 5 to 120 minutes, in whole minutes"},
		{r.MaxActiveBookings >= 1 && r.MaxActiveBookings <= 10, "active bookings per customer must be 1 to 10"},
	}
	for _, c := range checks {
		if !c.ok {
			return BookingPolicy{}, &PolicyError{Rule: c.rule}
		}
	}
	return BookingPolicy{rules: r}, nil
}

// DefaultBookingPolicy is what a new branch starts with
// (docs/architecture/domain-model.md §3.2).
func DefaultBookingPolicy() BookingPolicy {
	return BookingPolicy{rules: PolicyRules{
		MinLead:            30 * time.Minute,
		HorizonDays:        30,
		SlotInterval:       15 * time.Minute,
		Buffer:             0,
		CancellationWindow: 2 * time.Hour,
		AutoConfirm:        true,
		PendingExpiry:      15 * time.Minute,
		MaxActiveBookings:  2,
	}}
}

// Rules returns the settings.
func (p BookingPolicy) Rules() PolicyRules { return p.rules }

func wholeMinutes(d time.Duration) bool { return d%time.Minute == 0 }
