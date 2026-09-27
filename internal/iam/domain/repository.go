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
	OTPStore
	// WithPhoneLock serializes all operations for a normalized phone, including
	// its first request, and commits fn atomically. Errors roll back everything.
	WithPhoneLock(ctx context.Context, phone shared.PhoneNumber, fn func(OTPStore) error) error
}

// OTPStore is the transaction-scoped view supplied by WithPhoneLock.
type OTPStore interface {
	LoadGuard(ctx context.Context, phone shared.PhoneNumber) (*OTPGuard, error)
	SaveGuard(ctx context.Context, phone shared.PhoneNumber, guard *OTPGuard) error
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
	// ByID returns the user with id, or ErrNotFound.
	ByID(ctx context.Context, id shared.UserID) (*User, error)
}

// Sessions stores sessions and their refresh tokens.
type Sessions interface {
	// Start saves a new session together with its first refresh token.
	Start(ctx context.Context, s *Session, first *RefreshToken) error
	// Rotate finds the refresh token with tokenHash, locks it and its session,
	// calls fn and saves — in one transaction — the session, the presented
	// token and the next token fn returns (if not nil). An error from fn
	// rolls everything back, so a refusal that must still be saved (reuse
	// ends the session) is recorded by fn and not returned as its error.
	// Returns ErrNotFound when no token has that hash.
	Rotate(ctx context.Context, tokenHash []byte, fn func(*Session, *RefreshToken) (next *RefreshToken, err error)) error
	// Update locks the session with id, calls fn and saves the session.
	// Returns ErrNotFound when there is no such session.
	Update(ctx context.Context, id SessionID, fn func(*Session) error) error
}
