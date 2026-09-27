package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// VerifyOTP checks the code typed for Phone. Locale is used only when the
// number is new (from the app's Accept-Language).
type VerifyOTP struct {
	Phone  string
	Code   string
	Locale shared.Language
}

// Login is the result of a successful verification.
type Login struct {
	User      *domain.User
	IsNewUser bool
	Tokens    Tokens
}

// SessionStarter begins a signed-in session for a user.
type SessionStarter interface {
	Start(ctx context.Context, user *domain.User) (Tokens, error)
}

// VerifyOTPHandler signs users in with a one-time code, registering numbers
// seen for the first time.
type VerifyOTPHandler struct {
	challenges domain.OTPChallenges
	users      domain.Users
	hasher     CodeHasher
	sessions   SessionStarter
	clock      clock.Clock
	policy     domain.OTPPolicy
}

// NewVerifyOTPHandler wires the handler's dependencies.
func NewVerifyOTPHandler(challenges domain.OTPChallenges, users domain.Users, hasher CodeHasher, sessions SessionStarter, clk clock.Clock, policy domain.OTPPolicy) *VerifyOTPHandler {
	return &VerifyOTPHandler{challenges: challenges, users: users, hasher: hasher, sessions: sessions, clock: clk, policy: policy}
}

// Handle verifies the code and returns the (possibly new) user with the
// tokens of a new session.
func (h *VerifyOTPHandler) Handle(ctx context.Context, cmd VerifyOTP) (Login, error) {
	phone, err := shared.NewPhoneNumber(cmd.Phone)
	if err != nil {
		return Login{}, err
	}
	code, err := domain.ParseOTPCode(cmd.Code)
	if err != nil {
		return Login{}, err
	}
	var verifyErr error
	err = h.challenges.WithPhoneLock(ctx, phone, func(store domain.OTPStore) error {
		now := h.clock.Now()
		guard, err := store.LoadGuard(ctx, phone)
		if err != nil {
			return err
		}
		if err := guard.Check(now); err != nil {
			return err
		}
		// Keep business failures outside the transaction error: counters must commit.
		err = store.UpdateLatest(ctx, phone, func(c *domain.OTPChallenge) error {
			verifyErr = c.Verify(h.hasher.Hash(code), now, h.policy.MaxAttempts)
			return nil
		})
		if errors.Is(err, domain.ErrNotFound) {
			verifyErr = domain.ErrOTPInvalid
		} else if err != nil {
			return err
		}
		if verifyErr == nil {
			guard.Reset()
		} else if lockErr := guard.Fail(now, h.policy); lockErr != nil {
			verifyErr = lockErr
		}
		return store.SaveGuard(ctx, phone, guard)
	})
	if err != nil {
		return Login{}, fmt.Errorf("verify otp: %w", err)
	}
	if verifyErr != nil {
		return Login{}, verifyErr
	}
	now := h.clock.Now()

	locale := cmd.Locale
	if locale == "" {
		locale = shared.Arabic
	}
	user, created, err := h.users.Register(ctx, domain.NewUser(shared.NewID[shared.UserTag](), phone, locale, now))
	if err != nil {
		return Login{}, fmt.Errorf("verify otp: register user: %w", err)
	}
	if user.IsBlocked() {
		return Login{}, domain.ErrUserBlocked
	}
	tokens, err := h.sessions.Start(ctx, user)
	if err != nil {
		return Login{}, fmt.Errorf("verify otp: %w", err)
	}
	return Login{User: user, IsNewUser: created, Tokens: tokens}, nil
}
