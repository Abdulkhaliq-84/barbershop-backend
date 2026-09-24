package domain

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// OTPPolicy holds the limits of one-time codes (domain-model §3.1).
type OTPPolicy struct {
	TTL            time.Duration // how long a code works
	ResendCooldown time.Duration // minimum gap between two codes for a number
	MaxAttempts    int           // wrong guesses allowed per code
	MaxPerHour     int           // codes a number may request per hour
}

// DefaultOTPPolicy is the policy agreed in the plan.
func DefaultOTPPolicy() OTPPolicy {
	return OTPPolicy{TTL: 5 * time.Minute, ResendCooldown: time.Minute, MaxAttempts: 5, MaxPerHour: 5}
}

// OTPCode is a 6-digit one-time code, digits normalised to 0-9.
type OTPCode struct{ digits string }

// ParseOTPCode accepts exactly six digits, Western or Arabic-Indic.
func ParseOTPCode(raw string) (OTPCode, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		d, ok := shared.WesternDigit(r)
		if !ok {
			return OTPCode{}, ErrInvalidOTPCode
		}
		b.WriteRune(d)
	}
	if b.Len() != 6 {
		return OTPCode{}, ErrInvalidOTPCode
	}
	return OTPCode{digits: b.String()}, nil
}

// Digits returns the code, for hashing and for the SMS text.
func (c OTPCode) Digits() string { return c.digits }

// LogValue keeps codes out of logs even if someone logs one by mistake.
func (c OTPCode) LogValue() slog.Value { return slog.StringValue("******") }

// OTPChallengeTag marks OTP challenge IDs.
type OTPChallengeTag struct{}

// OTPChallengeID identifies one code sent to one phone number.
type OTPChallengeID = shared.ID[OTPChallengeTag]

// OTPChallenge is one code sent to one phone number. It stores only a hash
// of the code, counts wrong guesses and can be used once.
type OTPChallenge struct {
	id         OTPChallengeID
	phone      shared.PhoneNumber
	codeHash   []byte
	attempts   int
	createdAt  time.Time
	expiresAt  time.Time
	consumedAt *time.Time
}

// NewOTPChallenge starts a challenge that expires ttl after now.
func NewOTPChallenge(id OTPChallengeID, phone shared.PhoneNumber, codeHash []byte, now time.Time, ttl time.Duration) (*OTPChallenge, error) {
	switch {
	case id.IsZero(), phone.IsZero():
		return nil, errors.New("otp challenge: id and phone are required")
	case len(codeHash) == 0:
		return nil, errors.New("otp challenge: code hash is required")
	case ttl <= 0:
		return nil, errors.New("otp challenge: ttl must be positive")
	}
	return &OTPChallenge{id: id, phone: phone, codeHash: codeHash, createdAt: now, expiresAt: now.Add(ttl)}, nil
}

// RehydrateOTPChallenge rebuilds a challenge loaded from storage. It skips
// the "new challenge" checks: stored data was valid when it was saved.
func RehydrateOTPChallenge(id OTPChallengeID, phone shared.PhoneNumber, codeHash []byte, attempts int, createdAt, expiresAt time.Time, consumedAt *time.Time) *OTPChallenge {
	return &OTPChallenge{id: id, phone: phone, codeHash: codeHash, attempts: attempts, createdAt: createdAt, expiresAt: expiresAt, consumedAt: consumedAt}
}

// Verify checks a guess (as a hash) and consumes the challenge if it matches.
//
// Every guess counts, right or wrong: the caller must save the challenge
// even when Verify returns an error, or an attacker could guess forever.
func (c *OTPChallenge) Verify(guessHash []byte, now time.Time, maxAttempts int) error {
	switch {
	case c.consumedAt != nil:
		return ErrOTPInvalid // already used; don't reveal that it once existed
	case !now.Before(c.expiresAt):
		return ErrOTPExpired
	case c.attempts >= maxAttempts:
		return ErrOTPTooManyAttempts
	}

	c.attempts++
	// Constant-time comparison: the time taken doesn't leak how many bytes matched.
	if subtle.ConstantTimeCompare(c.codeHash, guessHash) != 1 {
		if c.attempts >= maxAttempts {
			return ErrOTPTooManyAttempts
		}
		return ErrOTPInvalid
	}
	consumed := now
	c.consumedAt = &consumed
	return nil
}

// ID returns the challenge ID.
func (c *OTPChallenge) ID() OTPChallengeID { return c.id }

// Phone returns the number the code was sent to.
func (c *OTPChallenge) Phone() shared.PhoneNumber { return c.phone }

// CodeHash returns the stored hash of the code.
func (c *OTPChallenge) CodeHash() []byte { return c.codeHash }

// Attempts returns how many guesses were made.
func (c *OTPChallenge) Attempts() int { return c.attempts }

// CreatedAt returns when the code was sent.
func (c *OTPChallenge) CreatedAt() time.Time { return c.createdAt }

// ExpiresAt returns when the code stops working.
func (c *OTPChallenge) ExpiresAt() time.Time { return c.expiresAt }

// ConsumedAt returns when the code was used, or nil.
func (c *OTPChallenge) ConsumedAt() *time.Time { return c.consumedAt }
