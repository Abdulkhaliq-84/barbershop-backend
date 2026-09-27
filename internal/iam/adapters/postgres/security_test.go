package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

type testCodes struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (c *testCodes) NewCode() (domain.OTPCode, error) {
	if c.calls.Add(1) == 1 && c.entered != nil {
		close(c.entered)
		<-c.release
	}
	return domain.ParseOTPCode("482193")
}

type testHasher struct{}

func (testHasher) Hash(c domain.OTPCode) []byte { return []byte(c.Digits()) }

type testSender struct{ sent atomic.Int32 }

func (s *testSender) SendOTP(context.Context, shared.PhoneNumber, domain.OTPCode) error {
	s.sent.Add(1)
	return nil
}

// Hold the winner AFTER policy reads but BEFORE insert. Without the phone lock,
// a second independent repository can read the same state and insert a code.
// The deadline is a deadlock guard and a negative assertion: it must be unable
// to finish while the first transaction holds the lock. No scheduler lottery.
func TestOTPRequestConcurrentLimits(t *testing.T) {
	for _, scenario := range []string{"first request cooldown", "last hourly slot"} {
		t.Run(scenario, func(t *testing.T) {
			pool := migratedDB(t)
			repo := postgres.NewOTPChallenges(pool)
			clk := clock.NewFake(t0)
			policy := domain.DefaultOTPPolicy()
			p := phone(t, "0551234567")
			initial := 0
			if scenario == "last hourly slot" {
				initial = 4
				policy.ResendCooldown = 0 // isolate the hourly cap from cooldown protection
				for i := range initial {
					c, _ := domain.NewOTPChallenge(shared.NewID[domain.OTPChallengeTag](), p, []byte("old"), t0.Add(-time.Duration(i+1)*time.Minute), policy.TTL)
					if err := repo.Add(t.Context(), c); err != nil {
						t.Fatal(err)
					}
				}
			}
			codes := &testCodes{entered: make(chan struct{}), release: make(chan struct{})}
			sender := &testSender{}
			first := app.NewRequestOTPHandler(repo, codes, testHasher{}, sender, clk, policy)
			second := app.NewRequestOTPHandler(postgres.NewOTPChallenges(pool), &testCodes{}, testHasher{}, sender, clk, policy)
			done := make(chan error, 1)
			go func() { _, err := first.Handle(t.Context(), app.RequestOTP{Phone: "0551234567"}); done <- err }()
			defer func() {
				close(codes.release)
				if err := <-done; err != nil {
					t.Errorf("first request: %v", err)
				}
			}()
			select {
			case <-codes.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("first request did not reach generator")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			_, err := second.Handle(ctx, app.RequestOTP{Phone: "+966551234567"})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("second request escaped phone lock: %v", err)
			}
			// Other phones remain independent while this phone is locked.
			if _, err := second.Handle(t.Context(), app.RequestOTP{Phone: "0559876543"}); err != nil {
				t.Fatal(err)
			}
			n, err := repo.CountSince(t.Context(), p, t0.Add(-time.Hour))
			if err != nil || n != initial {
				t.Fatalf("uncommitted/competing insert escaped: count=%d err=%v", n, err)
			}
		})
	}
}

func TestOTPRequestBurst(t *testing.T) {
	t.Parallel()
	repo := postgres.NewOTPChallenges(migratedDB(t))
	sender := &testSender{}
	h := app.NewRequestOTPHandler(repo, &testCodes{}, testHasher{}, sender, clock.NewFake(t0), domain.DefaultOTPPolicy())
	var wg sync.WaitGroup
	var accepted atomic.Int32
	start := make(chan struct{})
	for range 50 {
		wg.Go(func() {
			<-start
			_, err := h.Handle(t.Context(), app.RequestOTP{Phone: "0551234567"})
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, domain.ErrOTPCooldown) {
				t.Errorf("request: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 1 || sender.sent.Load() != 1 {
		t.Fatalf("accepted=%d sent=%d; want 1", accepted.Load(), sender.sent.Load())
	}
}

func TestOTPPhoneFailuresAreAtomic(t *testing.T) {
	t.Parallel()
	pool := migratedDB(t)
	repo := postgres.NewOTPChallenges(pool)
	policy := domain.DefaultOTPPolicy()
	verify := app.NewVerifyOTPHandler(repo, postgres.NewUsers(pool), testHasher{}, clock.NewFake(t0), policy)
	var wg sync.WaitGroup
	var locked atomic.Int32
	for range 30 {
		wg.Go(func() {
			_, err := verify.Handle(t.Context(), app.VerifyOTP{Phone: "0551234567", Code: "000000"})
			if errors.Is(err, domain.ErrOTPLocked) {
				locked.Add(1)
			} else if !errors.Is(err, domain.ErrOTPInvalid) {
				t.Errorf("verify: %v", err)
			}
		})
	}
	wg.Wait()
	// No challenge exists: verifies still accrue failures, and only nine may
	// return invalid before the tenth locks. New repository observes persistence.
	if locked.Load() != 21 {
		t.Fatalf("locked=%d, want 21", locked.Load())
	}
	request := app.NewRequestOTPHandler(postgres.NewOTPChallenges(pool), &testCodes{}, testHasher{}, &testSender{}, clock.NewFake(t0), policy)
	if _, err := request.Handle(t.Context(), app.RequestOTP{Phone: "+966551234567"}); !errors.Is(err, domain.ErrOTPLocked) {
		t.Fatalf("lockout lost: %v", err)
	}
}

func TestPhoneTransactionRollback(t *testing.T) {
	t.Parallel()
	repo := postgres.NewOTPChallenges(migratedDB(t))
	p := phone(t, "0551234567")
	boom := errors.New("rollback")
	err := repo.WithPhoneLock(t.Context(), p, func(store domain.OTPStore) error {
		guard := &domain.OTPGuard{}
		_ = guard.Fail(t0, domain.DefaultOTPPolicy())
		if err := store.SaveGuard(t.Context(), p, guard); err != nil {
			return err
		}
		c, _ := domain.NewOTPChallenge(shared.NewID[domain.OTPChallengeTag](), p, []byte("hash"), t0, time.Minute)
		if err := store.Add(t.Context(), c); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	guard, err := repo.LoadGuard(t.Context(), p)
	if err != nil || guard.Failures() != 0 {
		t.Fatalf("guard not rolled back: %v", err)
	}
	if _, err := repo.Latest(t.Context(), p); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("challenge not rolled back: %v", err)
	}
}
