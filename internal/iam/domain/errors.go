// Package domain holds the iam (identity & access) rules: one-time login
// codes and users. It is pure Go — no database, no HTTP — so every rule is
// tested with plain unit tests.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// Domain errors. The HTTP adapter maps each one to a stable API error code.
var (
	ErrNotFound           = errors.New("not found")
	ErrInvalidOTPCode     = errors.New("otp: code must be 6 digits")
	ErrOTPInvalid         = errors.New("otp: wrong or unknown code")
	ErrOTPExpired         = errors.New("otp: code expired")
	ErrOTPTooManyAttempts = errors.New("otp: too many attempts")
	ErrOTPCooldown        = errors.New("otp: wait before requesting another code")
	ErrOTPRateLimited     = errors.New("otp: too many codes requested")
	ErrUserBlocked        = errors.New("user is blocked")
)

// RetryLaterError wraps an error that goes away with time and says how long
// to wait. errors.Is still matches the wrapped reason:
//
//	errors.Is(err, ErrOTPCooldown) // true for RetryLaterError{Reason: ErrOTPCooldown}
type RetryLaterError struct {
	Reason error
	After  time.Duration
}

func (e *RetryLaterError) Error() string {
	return fmt.Sprintf("%v (retry after %s)", e.Reason, e.After.Round(time.Second))
}

// Unwrap lets errors.Is / errors.As see the reason.
func (e *RetryLaterError) Unwrap() error { return e.Reason }
