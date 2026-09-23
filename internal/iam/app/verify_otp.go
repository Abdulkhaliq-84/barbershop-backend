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
}

// VerifyOTPHandler signs users in with a one-time code, registering numbers
// seen for the first time.
type VerifyOTPHandler struct {
	challenges domain.OTPChallenges
	users      domain.Users
	hasher     CodeHasher
	clock      clock.Clock
	policy     domain.OTPPolicy
}

// NewVerifyOTPHandler wires the handler's dependencies.
func NewVerifyOTPHandler(challenges domain.OTPChallenges, users domain.Users, hasher CodeHasher, clk clock.Clock, policy domain.OTPPolicy) *VerifyOTPHandler {
	return &VerifyOTPHandler{challenges: challenges, users: users, hasher: hasher, clock: clk, policy: policy}
}

// Handle verifies the code and returns the (possibly new) user.
func (h *VerifyOTPHandler) Handle(ctx context.Context, cmd VerifyOTP) (Login, error) {
	phone, err := shared.NewPhoneNumber(cmd.Phone)
	if err != nil {
		return Login{}, err
	}
	code, err := domain.ParseOTPCode(cmd.Code)
	if err != nil {
		return Login{}, err
	}
	now := h.clock.Now()

	// The update function returns nil even for a wrong code, so the attempt
	// counter is saved; the verification result travels out in verifyErr.
	var verifyErr error
	err = h.challenges.UpdateLatest(ctx, phone, func(c *domain.OTPChallenge) error {
		verifyErr = c.Verify(h.hasher.Hash(code), now, h.policy.MaxAttempts)
		return nil
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Login{}, domain.ErrOTPInvalid // never requested: same answer as a wrong code
	case err != nil:
		return Login{}, fmt.Errorf("verify otp: %w", err)
	case verifyErr != nil:
		return Login{}, verifyErr
	}

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
	return Login{User: user, IsNewUser: created}, nil
}
