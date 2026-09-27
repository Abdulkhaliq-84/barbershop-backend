package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func newSession(t *testing.T) (*domain.Session, *domain.RefreshToken) {
	t.Helper()
	s, err := domain.NewSession(shared.NewID[domain.SessionTag](), shared.NewID[shared.UserTag](), t0)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := domain.NewRefreshToken([]byte("first"), s.ID(), t0, domain.DefaultTokenPolicy().RefreshTTL)
	if err != nil {
		t.Fatal(err)
	}
	return s, tk
}

func TestSessionRotate(t *testing.T) {
	t.Parallel()
	p := domain.DefaultTokenPolicy()
	later := t0.Add(time.Hour)

	tests := []struct {
		name       string
		prepare    func(*domain.Session, *domain.RefreshToken)
		at         time.Time
		wantErr    error
		wantReason domain.RevokeReason
	}{
		{name: "fresh token", at: later},
		{name: "expired", at: t0.Add(p.RefreshTTL), wantErr: domain.ErrRefreshTokenInvalid},
		{
			name:    "signed out",
			prepare: func(s *domain.Session, _ *domain.RefreshToken) { s.Revoke(t0, domain.RevokedLogout) },
			at:      later, wantErr: domain.ErrRefreshTokenInvalid, wantReason: domain.RevokedLogout,
		},
		{
			name: "already used: the session ends",
			prepare: func(s *domain.Session, tk *domain.RefreshToken) {
				if _, err := s.Rotate(tk, []byte("second"), t0.Add(time.Minute), p); err != nil {
					t.Fatal(err)
				}
			},
			at: later, wantErr: domain.ErrRefreshTokenReused, wantReason: domain.RevokedReuse,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, tk := newSession(t)
			if tt.prepare != nil {
				tt.prepare(s, tk)
			}
			next, err := s.Rotate(tk, []byte("next"), tt.at, p)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Rotate() error = %v, want %v", err, tt.wantErr)
			}
			if s.RevokeReason() != tt.wantReason {
				t.Errorf("RevokeReason() = %q, want %q", s.RevokeReason(), tt.wantReason)
			}
			if tt.wantErr != nil {
				if next != nil {
					t.Error("a refused rotation returned a token")
				}
				return
			}
			if tk.UsedAt() == nil || !tk.UsedAt().Equal(tt.at) || !s.LastRefreshedAt().Equal(tt.at) {
				t.Errorf("used at %v, last refreshed %v; want %v", tk.UsedAt(), s.LastRefreshedAt(), tt.at)
			}
			if string(next.Hash()) != "next" || next.SessionID() != s.ID() || !next.ExpiresAt().Equal(tt.at.Add(p.RefreshTTL)) {
				t.Errorf("next token = %+v", next)
			}
		})
	}
}

func TestSessionRotateRejectsAnotherSessionsToken(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	_, foreign := newSession(t)
	if _, err := s.Rotate(foreign, []byte("next"), t0, domain.DefaultTokenPolicy()); err == nil {
		t.Fatal("rotated a token of another session")
	}
}

func TestSessionRevokeKeepsFirstReason(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	s.Revoke(t0, domain.RevokedReuse)
	s.Revoke(t0.Add(time.Hour), domain.RevokedLogout)
	if s.RevokeReason() != domain.RevokedReuse || !s.RevokedAt().Equal(t0) {
		t.Fatalf("revoked %q at %v, want reuse at %v", s.RevokeReason(), s.RevokedAt(), t0)
	}
}

func TestNewSessionAndTokenValidate(t *testing.T) {
	t.Parallel()
	if _, err := domain.NewSession(domain.SessionID{}, shared.NewID[shared.UserTag](), t0); err == nil {
		t.Error("session without id accepted")
	}
	if _, err := domain.NewSession(shared.NewID[domain.SessionTag](), shared.UserID{}, t0); err == nil {
		t.Error("session without user accepted")
	}
	id := shared.NewID[domain.SessionTag]()
	for name, create := range map[string]func() (*domain.RefreshToken, error){
		"no hash": func() (*domain.RefreshToken, error) { return domain.NewRefreshToken(nil, id, t0, time.Hour) },
		"no session": func() (*domain.RefreshToken, error) {
			return domain.NewRefreshToken([]byte("h"), domain.SessionID{}, t0, time.Hour)
		},
		"zero ttl": func() (*domain.RefreshToken, error) { return domain.NewRefreshToken([]byte("h"), id, t0, 0) },
	} {
		if _, err := create(); err == nil {
			t.Errorf("%s: NewRefreshToken() error = nil", name)
		}
	}
}
