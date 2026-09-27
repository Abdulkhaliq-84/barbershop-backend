package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Tokens is what a client receives when it signs in or refreshes.
type Tokens struct {
	Access           string
	AccessExpiresIn  time.Duration
	Refresh          string
	RefreshExpiresIn time.Duration
}

// SessionIssuer starts sessions and issues token pairs. Sign-in and refresh
// share it so both hand out tokens the same way.
type SessionIssuer struct {
	sessions domain.Sessions
	access   AccessTokenIssuer
	secrets  RefreshTokenSecrets
	clock    clock.Clock
	policy   domain.TokenPolicy
}

// NewSessionIssuer wires the issuer's dependencies.
func NewSessionIssuer(sessions domain.Sessions, access AccessTokenIssuer, secrets RefreshTokenSecrets, clk clock.Clock, policy domain.TokenPolicy) *SessionIssuer {
	return &SessionIssuer{sessions: sessions, access: access, secrets: secrets, clock: clk, policy: policy}
}

// Start begins a session for user (one per sign-in, i.e. per device).
func (s *SessionIssuer) Start(ctx context.Context, user *domain.User) (Tokens, error) {
	now := s.clock.Now()
	session, err := domain.NewSession(shared.NewID[domain.SessionTag](), user.ID(), now)
	if err != nil {
		return Tokens{}, err
	}
	refresh, hash, err := s.secrets.New()
	if err != nil {
		return Tokens{}, fmt.Errorf("start session: new refresh token: %w", err)
	}
	first, err := domain.NewRefreshToken(hash, session.ID(), now, s.policy.RefreshTTL)
	if err != nil {
		return Tokens{}, err
	}
	if err := s.sessions.Start(ctx, session, first); err != nil {
		return Tokens{}, fmt.Errorf("start session: %w", err)
	}
	return s.tokens(user, session.ID(), refresh, now)
}

// tokens signs an access token and pairs it with the refresh token.
func (s *SessionIssuer) tokens(user *domain.User, sessionID domain.SessionID, refresh string, now time.Time) (Tokens, error) {
	access, err := s.access.Issue(AccessClaims{
		UserID:       user.ID(),
		SessionID:    sessionID,
		PlatformRole: user.PlatformRole(),
		IssuedAt:     now,
		ExpiresAt:    now.Add(s.policy.AccessTTL),
	})
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{
		Access: access, AccessExpiresIn: s.policy.AccessTTL,
		Refresh: refresh, RefreshExpiresIn: s.policy.RefreshTTL,
	}, nil
}

// RefreshTokens exchanges RefreshToken (raw client input) for a new pair.
type RefreshTokens struct {
	RefreshToken string
}

// RefreshHandler rotates refresh tokens and detects reuse.
type RefreshHandler struct {
	issuer *SessionIssuer
	users  domain.Users
}

// NewRefreshHandler wires the handler's dependencies.
func NewRefreshHandler(issuer *SessionIssuer, users domain.Users) *RefreshHandler {
	return &RefreshHandler{issuer: issuer, users: users}
}

// Handle spends the presented refresh token and returns a new pair.
func (h *RefreshHandler) Handle(ctx context.Context, cmd RefreshTokens) (Tokens, error) {
	s := h.issuer
	hash, err := s.secrets.Hash(cmd.RefreshToken)
	if err != nil {
		return Tokens{}, domain.ErrRefreshTokenInvalid
	}
	nextRefresh, nextHash, err := s.secrets.New()
	if err != nil {
		return Tokens{}, fmt.Errorf("refresh: new refresh token: %w", err)
	}
	now := s.clock.Now()

	var session *domain.Session
	var refused error
	err = s.sessions.Rotate(ctx, hash, func(sess *domain.Session, presented *domain.RefreshToken) (*domain.RefreshToken, error) {
		session = sess
		next, err := sess.Rotate(presented, nextHash, now, s.policy)
		// A refusal is not returned as the transaction error: when the token
		// was reused, the session's revocation must still be saved.
		refused = err
		return next, nil
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Tokens{}, domain.ErrRefreshTokenInvalid
	case err != nil:
		return Tokens{}, fmt.Errorf("refresh: %w", err)
	case refused != nil:
		return Tokens{}, refused
	}

	user, err := h.users.ByID(ctx, session.UserID())
	if errors.Is(err, domain.ErrNotFound) {
		return Tokens{}, domain.ErrRefreshTokenInvalid // the account is gone
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("refresh: load user: %w", err)
	}
	if user.IsBlocked() {
		if err := s.sessions.Update(ctx, session.ID(), func(sess *domain.Session) error {
			sess.Revoke(now, domain.RevokedBlocked)
			return nil
		}); err != nil {
			return Tokens{}, fmt.Errorf("refresh: revoke blocked user's session: %w", err)
		}
		return Tokens{}, domain.ErrUserBlocked
	}
	return s.tokens(user, session.ID(), nextRefresh, now)
}

// LogoutHandler ends the caller's session.
type LogoutHandler struct {
	sessions domain.Sessions
	clock    clock.Clock
}

// NewLogoutHandler wires the handler's dependencies.
func NewLogoutHandler(sessions domain.Sessions, clk clock.Clock) *LogoutHandler {
	return &LogoutHandler{sessions: sessions, clock: clk}
}

// Handle revokes sessionID if it belongs to userID. Signing out twice, or
// from a session that no longer exists, succeeds: the outcome is the same.
func (h *LogoutHandler) Handle(ctx context.Context, userID shared.UserID, sessionID domain.SessionID) error {
	err := h.sessions.Update(ctx, sessionID, func(s *domain.Session) error {
		if s.UserID() != userID {
			return domain.ErrNotFound // never touch someone else's session
		}
		s.Revoke(h.clock.Now(), domain.RevokedLogout)
		return nil
	})
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// GetMeHandler returns the signed-in user.
type GetMeHandler struct {
	users domain.Users
}

// NewGetMeHandler wires the handler's dependencies.
func NewGetMeHandler(users domain.Users) *GetMeHandler {
	return &GetMeHandler{users: users}
}

// Handle loads the caller. A blocked user gets ErrUserBlocked even while
// their access token is still valid.
func (h *GetMeHandler) Handle(ctx context.Context, userID shared.UserID) (*domain.User, error) {
	user, err := h.users.ByID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, domain.ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("get me: %w", err)
	}
	if user.IsBlocked() {
		return nil, domain.ErrUserBlocked
	}
	return user, nil
}
