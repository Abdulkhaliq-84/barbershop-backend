package domain

import (
	"context"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// OTPChallenges stores one-time code challenges. It is declared here, in
// the domain, and implemented by the postgres adapter: the domain says what
// it needs, the adapter decides how (ports & adapters).
type OTPChallenges interface {
	Add(ctx context.Context, c *OTPChallenge) error
	// Latest returns the most recent challenge for phone, or ErrNotFound.
	Latest(ctx context.Context, phone shared.PhoneNumber) (*OTPChallenge, error)
	// CountSince counts challenges for phone created at or after since.
	CountSince(ctx context.Context, phone shared.PhoneNumber, since time.Time) (int, error)
	// UpdateLatest locks the most recent challenge for phone, calls fn and saves
	// the result in one transaction. If fn returns an error nothing is saved.
	// Returns ErrNotFound when the phone has no challenge.
	UpdateLatest(ctx context.Context, phone shared.PhoneNumber, fn func(*OTPChallenge) error) error
}

// Users stores users.
type Users interface {
	// Register saves u unless its phone number is already registered, and
	// returns the stored user either way; created reports which happened.
	Register(ctx context.Context, u *User) (stored *User, created bool, err error)
}
