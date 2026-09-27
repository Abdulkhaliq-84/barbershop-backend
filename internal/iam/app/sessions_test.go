package app_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// fakeSessions keeps plain copies and rebuilds domain objects on every call,
// so a callback that fails leaves nothing behind — like a rolled-back
// transaction.
type fakeSessions struct {
	mu       sync.Mutex
	sessions map[domain.SessionID]sessionRecord
	tokens   map[string]tokenRecord // key: string(hash)
}

type sessionRecord struct {
	userID          shared.UserID
	createdAt, last time.Time
	revokedAt       *time.Time
	reason          domain.RevokeReason
}

type tokenRecord struct {
	sessionID domain.SessionID
	createdAt time.Time
	expiresAt time.Time
	usedAt    *time.Time
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{sessions: map[domain.SessionID]sessionRecord{}, tokens: map[string]tokenRecord{}}
}

func (f *fakeSessions) Start(_ context.Context, s *domain.Session, first *domain.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveSession(s)
	f.saveToken(first)
	return nil
}

func (f *fakeSessions) Rotate(_ context.Context, hash []byte, fn func(*domain.Session, *domain.RefreshToken) (*domain.RefreshToken, error)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	tr, ok := f.tokens[string(hash)]
	if !ok {
		return domain.ErrNotFound
	}
	session := f.load(tr.sessionID)
	token := domain.RehydrateRefreshToken(hash, tr.sessionID, tr.createdAt, tr.expiresAt, tr.usedAt)
	next, err := fn(session, token)
	if err != nil {
		return err
	}
	f.saveSession(session)
	f.saveToken(token)
	if next != nil {
		f.saveToken(next)
	}
	return nil
}

func (f *fakeSessions) Update(_ context.Context, id domain.SessionID, fn func(*domain.Session) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[id]; !ok {
		return domain.ErrNotFound
	}
	s := f.load(id)
	if err := fn(s); err != nil {
		return err
	}
	f.saveSession(s)
	return nil
}

func (f *fakeSessions) load(id domain.SessionID) *domain.Session {
	r := f.sessions[id]
	return domain.RehydrateSession(id, r.userID, r.createdAt, r.last, r.revokedAt, r.reason)
}

func (f *fakeSessions) saveSession(s *domain.Session) {
	f.sessions[s.ID()] = sessionRecord{s.UserID(), s.CreatedAt(), s.LastRefreshedAt(), s.RevokedAt(), s.RevokeReason()}
}

func (f *fakeSessions) saveToken(t *domain.RefreshToken) {
	f.tokens[string(t.Hash())] = tokenRecord{t.SessionID(), t.CreatedAt(), t.ExpiresAt(), t.UsedAt()}
}

// only returns the single stored session (tests start one at a time).
func (f *fakeSessions) only(t *testing.T) (domain.SessionID, sessionRecord) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(f.sessions))
	}
	for id, r := range f.sessions {
		return id, r
	}
	return domain.SessionID{}, sessionRecord{}
}

// fakeAccess "signs" readable tokens so tests can check what went in.
type fakeAccess struct{}

func (fakeAccess) Issue(c app.AccessClaims) (string, error) {
	return fmt.Sprintf("access|%s|%s|%s|%s", c.UserID, c.SessionID, c.PlatformRole, c.ExpiresAt.Sub(c.IssuedAt)), nil
}

// fakeSecrets hands out rt-1, rt-2, … and "hashes" by prefixing.
type fakeSecrets struct {
	mu sync.Mutex
	n  int
}

func (s *fakeSecrets) New() (string, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	token := fmt.Sprintf("rt-%d", s.n)
	return token, []byte("h:" + token), nil
}

func (*fakeSecrets) Hash(token string) ([]byte, error) {
	if !strings.HasPrefix(token, "rt-") {
		return nil, errors.New("malformed")
	}
	return []byte("h:" + token), nil
}

// signIn requests and verifies a code and returns the login.
func (f *fixture) signIn(t *testing.T) app.Login {
	t.Helper()
	ctx := t.Context()
	if _, err := f.request.Handle(ctx, app.RequestOTP{Phone: "0551234567"}); err != nil {
		t.Fatal(err)
	}
	login, err := f.verify.Handle(ctx, app.VerifyOTP{Phone: "0551234567", Code: "482193"})
	if err != nil {
		t.Fatal(err)
	}
	return login
}

func TestSignInStartsSession(t *testing.T) {
	t.Parallel()
	f := newFixture()
	login := f.signIn(t)

	id, s := f.sessions.only(t)
	if s.userID != login.User.ID() || s.revokedAt != nil {
		t.Errorf("session = %+v", s)
	}
	tk := login.Tokens
	wantAccess := fmt.Sprintf("access|%s|%s|none|15m0s", login.User.ID(), id)
	if tk.Access != wantAccess || tk.AccessExpiresIn != 15*time.Minute || tk.Refresh != "rt-1" || tk.RefreshExpiresIn != 30*24*time.Hour {
		t.Errorf("tokens = %+v, want access %q", tk, wantAccess)
	}
}

func TestRefreshRotatesTheToken(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()
	login := f.signIn(t)

	f.clock.Advance(20 * time.Minute)
	next, err := f.refresh.Handle(ctx, app.RefreshTokens{RefreshToken: login.Tokens.Refresh})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if next.Refresh == login.Tokens.Refresh || next.Access == "" {
		t.Fatalf("refresh did not rotate: %+v", next)
	}
	if _, s := f.sessions.only(t); !s.last.Equal(f.clock.Now()) {
		t.Errorf("last refreshed = %v, want %v", s.last, f.clock.Now())
	}
	// The new token works; each token once.
	if _, err := f.refresh.Handle(ctx, app.RefreshTokens{RefreshToken: next.Refresh}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
}

func TestRefreshReuseEndsTheSession(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()
	login := f.signIn(t)

	next, err := f.refresh.Handle(ctx, app.RefreshTokens{RefreshToken: login.Tokens.Refresh})
	if err != nil {
		t.Fatal(err)
	}
	// Someone presents the first token again: it was copied.
	if _, err := f.refresh.Handle(ctx, app.RefreshTokens{RefreshToken: login.Tokens.Refresh}); !errors.Is(err, domain.ErrRefreshTokenReused) {
		t.Fatalf("reused token: error = %v, want ErrRefreshTokenReused", err)
	}
	if _, s := f.sessions.only(t); s.reason != domain.RevokedReuse {
		t.Fatalf("session reason = %q, want reuse", s.reason)
	}
	// The legitimate holder's newer token is dead too.
	if _, err := f.refresh.Handle(ctx, app.RefreshTokens{RefreshToken: next.Refresh}); !errors.Is(err, domain.ErrRefreshTokenInvalid) {
		t.Fatalf("token after reuse: error = %v, want ErrRefreshTokenInvalid", err)
	}
}

func TestRefreshRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token func(f *fixture, login app.Login) string
	}{
		{"malformed", func(*fixture, app.Login) string { return "not-a-refresh-token" }},
		{"unknown", func(*fixture, app.Login) string { return "rt-999" }},
		{"expired", func(f *fixture, login app.Login) string {
			f.clock.Advance(30*24*time.Hour + time.Second)
			return login.Tokens.Refresh
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture()
			login := f.signIn(t)
			_, err := f.refresh.Handle(t.Context(), app.RefreshTokens{RefreshToken: tt.token(f, login)})
			if !errors.Is(err, domain.ErrRefreshTokenInvalid) {
				t.Fatalf("error = %v, want ErrRefreshTokenInvalid", err)
			}
		})
	}
}

func TestRefreshForBlockedUserEndsTheSession(t *testing.T) {
	t.Parallel()
	f := newFixture()
	login := f.signIn(t)
	f.users.block(login.User.ID())

	if _, err := f.refresh.Handle(t.Context(), app.RefreshTokens{RefreshToken: login.Tokens.Refresh}); !errors.Is(err, domain.ErrUserBlocked) {
		t.Fatalf("error = %v, want ErrUserBlocked", err)
	}
	if _, s := f.sessions.only(t); s.reason != domain.RevokedBlocked {
		t.Fatalf("session reason = %q, want blocked", s.reason)
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()
	login := f.signIn(t)
	id, _ := f.sessions.only(t)

	// Someone else's user ID can't end this session.
	if err := f.logout.Handle(ctx, shared.NewID[shared.UserTag](), id); err != nil {
		t.Fatal(err)
	}
	if _, s := f.sessions.only(t); s.revokedAt != nil {
		t.Fatal("another user ended the session")
	}

	for range 2 { // idempotent
		if err := f.logout.Handle(ctx, login.User.ID(), id); err != nil {
			t.Fatalf("logout: %v", err)
		}
	}
	if _, s := f.sessions.only(t); s.reason != domain.RevokedLogout {
		t.Fatalf("session reason = %q, want logout", s.reason)
	}
	if _, err := f.refresh.Handle(ctx, app.RefreshTokens{RefreshToken: login.Tokens.Refresh}); !errors.Is(err, domain.ErrRefreshTokenInvalid) {
		t.Fatalf("refresh after logout: error = %v, want ErrRefreshTokenInvalid", err)
	}
	if err := f.logout.Handle(ctx, login.User.ID(), shared.NewID[domain.SessionTag]()); err != nil {
		t.Fatalf("logout of an unknown session: %v", err)
	}
}

func TestGetMe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()
	login := f.signIn(t)

	me, err := f.getMe.Handle(ctx, login.User.ID())
	if err != nil || me.ID() != login.User.ID() {
		t.Fatalf("GetMe = %v, %v", me, err)
	}
	if _, err := f.getMe.Handle(ctx, shared.NewID[shared.UserTag]()); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Errorf("unknown user: error = %v, want ErrUnauthenticated", err)
	}
	f.users.block(login.User.ID())
	if _, err := f.getMe.Handle(ctx, login.User.ID()); !errors.Is(err, domain.ErrUserBlocked) {
		t.Errorf("blocked user: error = %v, want ErrUserBlocked", err)
	}
}
