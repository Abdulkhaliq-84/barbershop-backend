package domain

import "time"

// OTPGuard tracks failures across challenges; resending cannot reset it.
type OTPGuard struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

// RehydrateOTPGuard restores persisted security state.
func RehydrateOTPGuard(failures int, windowStart, lockedUntil time.Time) *OTPGuard {
	return &OTPGuard{failures: failures, windowStart: windowStart, lockedUntil: lockedUntil}
}

// Check rejects attempts during lockout without extending it.
func (g *OTPGuard) Check(now time.Time) error {
	if now.Before(g.lockedUntil) {
		return &RetryLaterError{Reason: ErrOTPLocked, After: g.lockedUntil.Sub(now)}
	}
	return nil
}

// Fail counts a failed verification and may start a lockout.
func (g *OTPGuard) Fail(now time.Time, policy OTPPolicy) error {
	if err := g.Check(now); err != nil {
		return err
	}
	if !now.Before(g.windowStart.Add(policy.FailureWindow)) || (!g.lockedUntil.IsZero() && !now.Before(g.lockedUntil)) {
		g.Reset()
	}
	if g.failures == 0 {
		g.windowStart = now
	}
	g.failures++
	if g.failures >= policy.MaxFailures {
		g.lockedUntil = now.Add(policy.Lockout)
	}
	return g.Check(now)
}

// Reset clears failures after successful verification.
func (g *OTPGuard) Reset() { *g = OTPGuard{} }

// Failures returns the persisted failure count.
func (g *OTPGuard) Failures() int { return g.failures }

// WindowStart returns the start of the failure window.
func (g *OTPGuard) WindowStart() time.Time { return g.windowStart }

// LockedUntil returns the lockout deadline (zero when unlocked).
func (g *OTPGuard) LockedUntil() time.Time { return g.lockedUntil }
