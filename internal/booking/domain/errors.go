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
)
