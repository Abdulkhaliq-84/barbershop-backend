package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
)

func TestOTPGuard(t *testing.T) {
	t.Parallel()
	policy := domain.DefaultOTPPolicy()
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"lockout", "window expires", "success resets"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			g := &domain.OTPGuard{}
			for range policy.MaxFailures - 1 {
				if err := g.Fail(now, policy); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "window expires":
				if err := g.Fail(now.Add(policy.FailureWindow), policy); err != nil || g.Failures() != 1 {
					t.Fatalf("window not reset: %v", err)
				}
			case "success resets":
				g.Reset()
				if err := g.Fail(now, policy); err != nil || g.Failures() != 1 {
					t.Fatalf("success did not reset: %v", err)
				}
			case "lockout":
				err := g.Fail(now, policy)
				var later *domain.RetryLaterError
				if !errors.Is(err, domain.ErrOTPLocked) || !errors.As(err, &later) || later.After != policy.Lockout {
					t.Fatalf("lockout = %v", err)
				}
				deadline := g.LockedUntil()
				_ = g.Fail(now.Add(time.Minute), policy)
				if g.LockedUntil() != deadline || g.Failures() != policy.MaxFailures {
					t.Fatal("locked attempts must not extend lockout or count")
				}
				if err := g.Fail(deadline, policy); err != nil || g.Failures() != 1 {
					t.Fatalf("lock did not expire: %v", err)
				}
			}
		})
	}
}
