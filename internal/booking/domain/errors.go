// Package domain is booking's model: when customers can book, and (from
// M5.3) their appointments. It depends on nothing but shared.
package domain

import "errors"

// Errors the booking model reports.
var (
	ErrNotFound           = errors.New("booking: not found")
	ErrInvalidDay         = errors.New("booking: not a date (YYYY-MM-DD)")
	ErrNoServices         = errors.New("booking: choose 1 to 5 different services")
	ErrServiceUnavailable = errors.New("booking: a chosen service isn't offered at this branch")
	ErrBarberUnavailable  = errors.New("booking: this barber doesn't perform all the chosen services here")
	// ErrSlotUnavailable: nobody asked for is free at that time any more
	// (someone booked first, or the hours changed).
	ErrSlotUnavailable = errors.New("booking: that time isn't free")
	// ErrInvalidStart: not a start time the branch offers — off its grid,
	// too soon, or past its booking horizon.
	ErrInvalidStart      = errors.New("booking: not a start time the branch offers")
	ErrTooManyBookings   = errors.New("booking: you already have the most upcoming bookings this branch allows")
	ErrNoteTooLong       = errors.New("booking: the note is too long")
	ErrIdempotencyReused = errors.New("booking: this Idempotency-Key was used for a different booking")
)
