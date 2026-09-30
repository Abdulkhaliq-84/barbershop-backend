package domain

import (
	"errors"
	"log/slog"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// TokenPolicy holds token lifetimes (ADR-0007, ADR-0014).
type TokenPolicy struct {
	AccessTTL  time.Duration // access tokens: short-lived, checked without a database
	RefreshTTL time.Duration // refresh tokens: long-lived, single use, revocable
}

// DefaultTokenPolicy is the policy agreed in ADR-0007.
func DefaultTokenPolicy() TokenPolicy {
	return TokenPolicy{AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour}
}

// SessionTag marks session IDs.
type SessionTag struct{}

// SessionID identifies one signed-in device.
type SessionID = shared.ID[SessionTag]

// RevokeReason says why a session ended.
type RevokeReason string

// Revoke reasons (stored in iam.sessions.revoke_reason).
const (
	RevokedLogout  RevokeReason = "logout"  // the user signed out
	RevokedReuse   RevokeReason = "reuse"   // a used refresh token came back: it was copied
	RevokedBlocked RevokeReason = "blocked" // the user was blocked
)

// Session is one signed-in device. Its refresh tokens form a chain: each
// refresh spends the presented token and issues the next one, so at most
// one token of a session is ever usable.
type Session struct {
	id              SessionID
	userID          shared.UserID
	createdAt       time.Time
	lastRefreshedAt time.Time
	revokedAt       *time.Time
	revokeReason    RevokeReason
}

// NewSession starts a session for userID.
func NewSession(id SessionID, userID shared.UserID, now time.Time) (*Session, error) {
	if id.IsZero() || userID.IsZero() {
		return nil, errors.New("session: id and user are required")
	}
	return &Session{id: id, userID: userID, createdAt: now, lastRefreshedAt: now}, nil
}

// RehydrateSession rebuilds a session loaded from storage.
func RehydrateSession(id SessionID, userID shared.UserID, createdAt, lastRefreshedAt time.Time, revokedAt *time.Time, reason RevokeReason) *Session {
	return &Session{id: id, userID: userID, createdAt: createdAt, lastRefreshedAt: lastRefreshedAt, revokedAt: revokedAt, revokeReason: reason}
}

// Rotate spends t, the refresh token the client presented, and returns the
// token that replaces it (stored as nextHash).
//
// A token that was already spent means two parties hold copies of it: a
// thief and the owner. There is no telling which one is calling, so the
// whole session ends (ErrRefreshTokenReused) and both must sign in again.
// The caller must save the session even when Rotate returns that error.
func (s *Session) Rotate(t *RefreshToken, nextHash []byte, now time.Time, p TokenPolicy) (*RefreshToken, error) {
	if t.sessionID != s.id {
		return nil, errors.New("session: refresh token belongs to another session")
	}
	switch {
	case s.IsRevoked():
		return nil, ErrRefreshTokenInvalid
	case t.usedAt != nil:
		s.Revoke(now, RevokedReuse)
		return nil, ErrRefreshTokenReused
	case !now.Before(t.expiresAt):
		return nil, ErrRefreshTokenInvalid
	}
	next, err := NewRefreshToken(nextHash, s.id, now, p.RefreshTTL)
	if err != nil {
		return nil, err
	}
	t.usedAt = new(now)
	s.lastRefreshedAt = now
	return next, nil
}

// Revoke ends the session. Revoking an ended session keeps the first reason.
func (s *Session) Revoke(now time.Time, reason RevokeReason) {
	if s.IsRevoked() {
		return
	}
	at := now
	s.revokedAt, s.revokeReason = &at, reason
}

// IsRevoked reports whether the session has ended.
func (s *Session) IsRevoked() bool { return s.revokedAt != nil }

// ID returns the session ID.
func (s *Session) ID() SessionID { return s.id }

// UserID returns the signed-in user.
func (s *Session) UserID() shared.UserID { return s.userID }

// CreatedAt returns when the user signed in.
func (s *Session) CreatedAt() time.Time { return s.createdAt }

// LastRefreshedAt returns when the session last issued tokens.
func (s *Session) LastRefreshedAt() time.Time { return s.lastRefreshedAt }

// RevokedAt returns when the session ended, or nil.
func (s *Session) RevokedAt() *time.Time { return s.revokedAt }

// RevokeReason returns why the session ended ("" while active).
func (s *Session) RevokeReason() RevokeReason { return s.revokeReason }

// LogValue logs a session without anything secret.
func (s *Session) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", s.id.String()),
		slog.String("user_id", s.userID.String()),
		slog.String("revoke_reason", string(s.revokeReason)),
	)
}

// RefreshToken is one link of a session's token chain. Only a hash of the
// token is kept; the token itself is shown to the client once.
type RefreshToken struct {
	hash      []byte
	sessionID SessionID
	createdAt time.Time
	expiresAt time.Time
	usedAt    *time.Time
}

// NewRefreshToken records a token (by its hash) that expires ttl after now.
func NewRefreshToken(hash []byte, sessionID SessionID, now time.Time, ttl time.Duration) (*RefreshToken, error) {
	switch {
	case len(hash) == 0:
		return nil, errors.New("refresh token: hash is required")
	case sessionID.IsZero():
		return nil, errors.New("refresh token: session is required")
	case ttl <= 0:
		return nil, errors.New("refresh token: ttl must be positive")
	}
	return &RefreshToken{hash: hash, sessionID: sessionID, createdAt: now, expiresAt: now.Add(ttl)}, nil
}

// RehydrateRefreshToken rebuilds a token loaded from storage.
func RehydrateRefreshToken(hash []byte, sessionID SessionID, createdAt, expiresAt time.Time, usedAt *time.Time) *RefreshToken {
	return &RefreshToken{hash: hash, sessionID: sessionID, createdAt: createdAt, expiresAt: expiresAt, usedAt: usedAt}
}

// Hash returns the stored hash of the token.
func (t *RefreshToken) Hash() []byte { return t.hash }

// SessionID returns the session the token belongs to.
func (t *RefreshToken) SessionID() SessionID { return t.sessionID }

// CreatedAt returns when the token was issued.
func (t *RefreshToken) CreatedAt() time.Time { return t.createdAt }

// ExpiresAt returns when the token stops working if unused.
func (t *RefreshToken) ExpiresAt() time.Time { return t.expiresAt }

// UsedAt returns when the token was spent, or nil.
func (t *RefreshToken) UsedAt() *time.Time { return t.usedAt }
