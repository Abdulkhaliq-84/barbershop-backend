package postgres_test

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func migratedDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

var t0 = time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)

func phone(t *testing.T, raw string) shared.PhoneNumber {
	t.Helper()
	p, err := shared.NewPhoneNumber(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOTPChallenges(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewOTPChallenges(migratedDB(t))
	p := phone(t, "0551234567")

	if _, err := repo.Latest(ctx, p); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Latest on empty table: error = %v, want ErrNotFound", err)
	}

	first, _ := domain.NewOTPChallenge(shared.NewID[domain.OTPChallengeTag](), p, []byte("h1"), t0, 5*time.Minute)
	second, _ := domain.NewOTPChallenge(shared.NewID[domain.OTPChallengeTag](), p, []byte("h2"), t0.Add(2*time.Minute), 5*time.Minute)
	for _, c := range []*domain.OTPChallenge{first, second} {
		if err := repo.Add(ctx, c); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	latest, err := repo.Latest(ctx, p)
	if err != nil || latest.ID() != second.ID() || string(latest.CodeHash()) != "h2" || !latest.ExpiresAt().Equal(second.ExpiresAt()) {
		t.Fatalf("Latest = %+v, %v; want the second challenge", latest, err)
	}

	n, err := repo.CountSince(ctx, p, t0.Add(time.Minute))
	if err != nil || n != 1 {
		t.Errorf("CountSince = %d, %v; want 1", n, err)
	}

	// A failing verification must still persist the attempt.
	err = repo.UpdateLatest(ctx, p, func(c *domain.OTPChallenge) error {
		_ = c.Verify([]byte("nope"), t0.Add(3*time.Minute), 5)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateLatest: %v", err)
	}
	if got, _ := repo.Latest(ctx, p); got.Attempts() != 1 || got.ConsumedAt() != nil {
		t.Errorf("after a wrong guess: attempts=%d consumed=%v", got.Attempts(), got.ConsumedAt())
	}

	// An error from fn rolls the transaction back.
	boom := errors.New("boom")
	err = repo.UpdateLatest(ctx, p, func(c *domain.OTPChallenge) error {
		_ = c.Verify([]byte("h2"), t0.Add(3*time.Minute), 5)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("UpdateLatest error = %v, want boom", err)
	}
	if got, _ := repo.Latest(ctx, p); got.Attempts() != 1 || got.ConsumedAt() != nil {
		t.Errorf("rolled-back change was saved: attempts=%d consumed=%v", got.Attempts(), got.ConsumedAt())
	}
}

// Parallel guesses must not slip past the attempt limit. Without the row lock
// (SELECT … FOR UPDATE) several transactions read "attempts = 0" at once and
// each gets a free guess. 20 goroutines guess at the same moment; only the
// limit (5) may be compared against the code.
func TestOTPChallengesParallelGuessesRespectLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewOTPChallenges(migratedDB(t))
	p := phone(t, "0559876543")
	c, _ := domain.NewOTPChallenge(shared.NewID[domain.OTPChallengeTag](), p, []byte("secret"), t0, 5*time.Minute)
	if err := repo.Add(ctx, c); err != nil {
		t.Fatal(err)
	}

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		checked atomic.Int32 // guesses that were really compared
	)
	for range 20 {
		wg.Go(func() { // Go 1.25+: Add(1) + go + Done() in one call
			<-start
			err := repo.UpdateLatest(ctx, p, func(c *domain.OTPChallenge) error {
				before := c.Attempts()
				_ = c.Verify([]byte("wrong"), t0.Add(time.Minute), 5)
				if c.Attempts() > before {
					checked.Add(1)
				}
				return nil
			})
			if err != nil {
				t.Errorf("UpdateLatest: %v", err)
			}
		})
	}
	close(start) // release all goroutines at once
	wg.Wait()

	if n := checked.Load(); n != 5 {
		t.Errorf("%d of 20 parallel guesses were checked, want exactly 5 (the limit)", n)
	}
	if got, _ := repo.Latest(ctx, p); got.Attempts() != 5 {
		t.Errorf("stored attempts = %d, want 5", got.Attempts())
	}
}

func TestUsersRegisterIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewUsers(migratedDB(t))
	p := phone(t, "+966 50 111 2222")

	first, created, err := repo.Register(ctx, domain.NewUser(shared.NewID[shared.UserTag](), p, shared.English, t0))
	if err != nil || !created {
		t.Fatalf("first Register = %v, created=%v, err=%v", first, created, err)
	}
	if first.Locale() != shared.English || first.IsBlocked() || first.Phone() != p {
		t.Errorf("stored user = %+v", first)
	}

	again, created, err := repo.Register(ctx, domain.NewUser(shared.NewID[shared.UserTag](), p, shared.Arabic, t0.Add(time.Hour)))
	if err != nil || created || again.ID() != first.ID() {
		t.Fatalf("second Register = %v, created=%v, err=%v; want the first user", again, created, err)
	}
}
