package postgres_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func hashOf(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// startSession registers a user and starts a session whose first refresh
// token hashes to hashOf("first").
func startSession(t *testing.T, pool *pgxpool.Pool) (*postgres.Sessions, *domain.Session, *domain.User) {
	t.Helper()
	ctx := t.Context()
	user, _, err := postgres.NewUsers(pool).Register(ctx, domain.NewUser(shared.NewID[shared.UserTag](), phone(t, "0551234567"), shared.Arabic, t0))
	if err != nil {
		t.Fatal(err)
	}
	session, _ := domain.NewSession(shared.NewID[domain.SessionTag](), user.ID(), t0)
	first, _ := domain.NewRefreshToken(hashOf("first"), session.ID(), t0, domain.DefaultTokenPolicy().RefreshTTL)
	repo := postgres.NewSessions(pool)
	if err := repo.Start(ctx, session, first); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return repo, session, user
}

// rotate presents hashOf(presented), asking for hashOf(next) in exchange.
func rotate(t *testing.T, repo *postgres.Sessions, presented, next string, now time.Time) error {
	t.Helper()
	var refused error
	err := repo.Rotate(t.Context(), hashOf(presented), func(s *domain.Session, tk *domain.RefreshToken) (*domain.RefreshToken, error) {
		n, err := s.Rotate(tk, hashOf(next), now, domain.DefaultTokenPolicy())
		refused = err
		return n, nil
	})
	if err != nil {
		return err
	}
	return refused
}

// sessionState reads a session back through Update.
func sessionState(t *testing.T, repo *postgres.Sessions, id domain.SessionID) *domain.Session {
	t.Helper()
	var got *domain.Session
	if err := repo.Update(t.Context(), id, func(s *domain.Session) error { got = s; return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSessionsRotateAndDetectReuse(t *testing.T) {
	t.Parallel()
	repo, session, _ := startSession(t, migratedDB(t))

	if err := rotate(t, repo, "first", "second", t0.Add(time.Hour)); err != nil {
		t.Fatalf("first rotation: %v", err)
	}
	if got := sessionState(t, repo, session.ID()); !got.LastRefreshedAt().Equal(t0.Add(time.Hour)) || got.IsRevoked() {
		t.Fatalf("after rotation: last=%v revoked=%v", got.LastRefreshedAt(), got.IsRevoked())
	}
	if err := rotate(t, repo, "second", "third", t0.Add(2*time.Hour)); err != nil {
		t.Fatalf("second rotation: %v", err)
	}

	// "first" again: reuse. The revocation must be saved although the
	// request fails.
	if err := rotate(t, repo, "first", "x", t0.Add(3*time.Hour)); !errors.Is(err, domain.ErrRefreshTokenReused) {
		t.Fatalf("reuse: error = %v, want ErrRefreshTokenReused", err)
	}
	if got := sessionState(t, repo, session.ID()); got.RevokeReason() != domain.RevokedReuse {
		t.Fatalf("revoke reason = %q, want reuse", got.RevokeReason())
	}
	if err := rotate(t, repo, "third", "y", t0.Add(4*time.Hour)); !errors.Is(err, domain.ErrRefreshTokenInvalid) {
		t.Fatalf("newest token after reuse: error = %v, want ErrRefreshTokenInvalid", err)
	}
	if err := rotate(t, repo, "never-issued", "z", t0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown token: error = %v, want ErrNotFound", err)
	}
}

func TestSessionsRotateRollsBackOnError(t *testing.T) {
	t.Parallel()
	repo, session, _ := startSession(t, migratedDB(t))
	boom := errors.New("boom")

	err := repo.Rotate(t.Context(), hashOf("first"), func(s *domain.Session, tk *domain.RefreshToken) (*domain.RefreshToken, error) {
		_, _ = s.Rotate(tk, hashOf("second"), t0.Add(time.Minute), domain.DefaultTokenPolicy())
		s.Revoke(t0, domain.RevokedLogout)
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
	if got := sessionState(t, repo, session.ID()); got.IsRevoked() {
		t.Fatal("a failed callback's changes were saved")
	}
	if err := rotate(t, repo, "first", "second", t0.Add(time.Minute)); err != nil {
		t.Fatalf("token must still be unused: %v", err)
	}
}

// Several devices (or a double-tapped retry) refresh with the same token at
// the same moment. The row locks make them take turns: exactly one gets new
// tokens, the others look like reuse — which ends the session.
//
// Without the locks every caller would read "unused" and win. To make that
// failure show up reliably, the pool is warmed up first (so callers don't
// queue for connections) and each callback pauses briefly while it holds
// the token, widening the window in which an unlocked read could overlap.
func TestSessionsParallelRefreshesTakeTurns(t *testing.T) {
	t.Parallel()
	pool := migratedDB(t)
	repo, session, _ := startSession(t, pool)

	const callers = 8 // dbtest pools hold 8 connections
	warm := make([]*pgxpool.Conn, callers)
	for i := range warm {
		c, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		warm[i] = c
	}
	for _, c := range warm {
		c.Release()
	}

	var (
		wg              sync.WaitGroup
		start           = make(chan struct{})
		rotated, reused atomic.Int32
	)
	for i := range callers {
		wg.Go(func() {
			<-start
			var refused error
			err := repo.Rotate(t.Context(), hashOf("first"), func(s *domain.Session, tk *domain.RefreshToken) (*domain.RefreshToken, error) {
				next, err := s.Rotate(tk, hashOf(fmt.Sprintf("next-%d", i)), t0.Add(time.Minute), domain.DefaultTokenPolicy())
				refused = err
				time.Sleep(20 * time.Millisecond) // hold the token a moment
				return next, nil
			})
			switch {
			case err != nil:
				t.Errorf("rotate: %v", err)
			case refused == nil:
				rotated.Add(1)
			case errors.Is(refused, domain.ErrRefreshTokenReused), errors.Is(refused, domain.ErrRefreshTokenInvalid):
				reused.Add(1)
			default:
				t.Errorf("rotate refused: %v", refused)
			}
		})
	}
	close(start)
	wg.Wait()

	if rotated.Load() != 1 || reused.Load() != callers-1 {
		t.Fatalf("rotated=%d refused=%d, want exactly 1 and %d", rotated.Load(), reused.Load(), callers-1)
	}
	if got := sessionState(t, repo, session.ID()); got.RevokeReason() != domain.RevokedReuse {
		t.Fatalf("revoke reason = %q, want reuse", got.RevokeReason())
	}
}

func TestSessionsUpdate(t *testing.T) {
	t.Parallel()
	repo, session, _ := startSession(t, migratedDB(t))

	if err := repo.Update(t.Context(), session.ID(), func(s *domain.Session) error {
		s.Revoke(t0.Add(time.Minute), domain.RevokedLogout)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := sessionState(t, repo, session.ID())
	if got.RevokeReason() != domain.RevokedLogout || !got.RevokedAt().Equal(t0.Add(time.Minute)) {
		t.Fatalf("session = %v revoked at %v", got.RevokeReason(), got.RevokedAt())
	}
	if err := repo.Update(t.Context(), shared.NewID[domain.SessionTag](), func(*domain.Session) error { return nil }); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown session: error = %v, want ErrNotFound", err)
	}
}

func TestUsersByID(t *testing.T) {
	t.Parallel()
	pool := migratedDB(t)
	_, _, user := startSession(t, pool)
	users := postgres.NewUsers(pool)

	got, err := users.ByID(t.Context(), user.ID())
	if err != nil || got.Phone() != user.Phone() || got.PlatformRole() != domain.PlatformRoleNone || got.IsBlocked() {
		t.Fatalf("ByID = %+v, %v", got, err)
	}
	if _, err := users.ByID(t.Context(), shared.NewID[shared.UserTag]()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown user: error = %v, want ErrNotFound", err)
	}
}
