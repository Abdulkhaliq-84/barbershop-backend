// Package app holds the iam use cases (commands). Each one loads what it
// needs through small interfaces (ports), applies domain rules and saves the
// result. It knows nothing about HTTP, SQL or SMS providers.
package app

import (
	"context"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// CodeGenerator creates one-time codes. Production uses crypto/rand; tests
// use a fixed code so they know what to type.
type CodeGenerator interface {
	NewCode() (domain.OTPCode, error)
}

// CodeHasher turns a code into the value stored in the database. It holds a
// server secret, so the stored hash is useless without it.
type CodeHasher interface {
	Hash(code domain.OTPCode) []byte
}

// OTPSender delivers a code to a phone (SMS in production, the console in
// development).
type OTPSender interface {
	SendOTP(ctx context.Context, to shared.PhoneNumber, code domain.OTPCode) error
}

// AccessClaims is what an access token says about its holder.
type AccessClaims struct {
	UserID       shared.UserID
	SessionID    domain.SessionID
	PlatformRole domain.PlatformRole
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// AccessTokenIssuer signs access tokens (JWT in production).
type AccessTokenIssuer interface {
	Issue(claims AccessClaims) (string, error)
}

// RefreshTokenSecrets creates opaque refresh tokens and hashes presented ones.
type RefreshTokenSecrets interface {
	// New returns a fresh token for the client and the hash to store.
	New() (token string, hash []byte, err error)
	// Hash returns the stored form of a presented token, or an error when
	// the token is not even well-formed.
	Hash(token string) ([]byte, error)
}
