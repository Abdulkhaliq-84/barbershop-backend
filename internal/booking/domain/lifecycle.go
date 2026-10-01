package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Canceller is who cancelled an appointment.
type Canceller string

// Cancellers.
const (
	ByCustomer Canceller = "customer"
	ByStaff    Canceller = "staff"
)

// Cancellation records who cancelled an appointment, why and when.
type Cancellation struct {
	By     Canceller
	Reason string // may be empty
	At     time.Time
}

// MaxReasonLen is how long a cancellation reason may be, in characters.
const MaxReasonLen = 300

// Action is something done to an appointment after it was booked.
type Action string

// Actions (domain-model.md §3.5).
const (
	ActionConfirm  Action = "confirm"
	ActionReject   Action = "reject"
	ActionCancel   Action = "cancel"
	ActionComplete Action = "complete"
	ActionNoShow   Action = "no_show"
)

// TransitionError says an action doesn't apply to an appointment in Status.
// It matches ErrInvalidTransition.
type TransitionError struct {
	Action Action
	Status Status // as it is now: a pending appointment past its expiry is expired
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("booking: can't %s a %s appointment", e.Action, e.Status)
}

// Is makes errors.Is(err, ErrInvalidTransition) true.
func (e *TransitionError) Is(target error) bool { return target == ErrInvalidTransition }

// The lifecycle (domain-model.md §3.5):
//
//	pending   → confirmed | rejected | cancelled | expired (M5.5)
//	confirmed → cancelled | completed | no_show
//
// The other statuses are final. Every change bumps the version and records
// a StatusChanged event.

// Confirm accepts a pending booking (the shop).
func (a *Appointment) Confirm(now time.Time) error {
	if s := a.current(now); s != StatusPending {
		return &TransitionError{ActionConfirm, s}
	}
	a.change(StatusConfirmed, now)
	return nil
}

// Reject turns a pending booking down (the shop).
func (a *Appointment) Reject(now time.Time) error {
	if s := a.current(now); s != StatusPending {
		return &TransitionError{ActionReject, s}
	}
	a.change(StatusRejected, now)
	return nil
}

// CancelByCustomer cancels for the customer: a pending booking until it
// starts (the shop hasn't taken it on), a confirmed one until the
// deadline it was booked with.
func (a *Appointment) CancelByCustomer(reason string, now time.Time) error {
	deadline := a.start
	switch s := a.current(now); s {
	case StatusPending:
	case StatusConfirmed:
		deadline = a.cancellableUntil
	default:
		return &TransitionError{ActionCancel, s}
	}
	if !now.Before(deadline) {
		return ErrTooLateToCancel
	}
	return a.cancel(ByCustomer, reason, now)
}

// CancelByStaff cancels for the shop, which may cancel an active
// appointment at any time.
func (a *Appointment) CancelByStaff(reason string, now time.Time) error {
	if s := a.current(now); s != StatusPending && s != StatusConfirmed {
		return &TransitionError{ActionCancel, s}
	}
	return a.cancel(ByStaff, reason, now)
}

// Complete records that it took place (the shop), once it has started.
func (a *Appointment) Complete(now time.Time) error {
	return a.finish(StatusCompleted, ActionComplete, now)
}

// MarkNoShow records that the customer didn't come (the shop), once it
// has started.
func (a *Appointment) MarkNoShow(now time.Time) error {
	return a.finish(StatusNoShow, ActionNoShow, now)
}

func (a *Appointment) finish(to Status, action Action, now time.Time) error {
	if s := a.current(now); s != StatusConfirmed {
		return &TransitionError{action, s}
	}
	if now.Before(a.start) {
		return ErrNotStarted
	}
	a.change(to, now)
	return nil
}

func (a *Appointment) cancel(by Canceller, reason string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > MaxReasonLen {
		return ErrReasonTooLong
	}
	a.cancellation = &Cancellation{By: by, Reason: reason, At: dbTime(now)}
	a.change(StatusCancelled, now)
	return nil
}

// current is the status as of now: a pending booking past its expiry is
// expired, even before the expiry job (M5.5) has written it down.
func (a *Appointment) current(now time.Time) Status {
	if a.status == StatusPending && a.pendingUntil != nil && !now.Before(*a.pendingUntil) {
		return StatusExpired
	}
	return a.status
}

func (a *Appointment) change(to Status, now time.Time) {
	from := a.status
	a.status = to
	a.pendingUntil = nil // only a pending booking has an expiry
	a.version++
	a.updatedAt = dbTime(now)
	a.events = append(a.events, StatusChanged{From: from, Appointment: a.snapshot()})
}

// dbTime is t as Postgres keeps it: UTC, to the microsecond.
func dbTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }
