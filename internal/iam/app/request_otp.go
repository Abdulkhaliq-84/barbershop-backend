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

// RequestOTP asks for a login code to be sent to Phone (raw user input).
type RequestOTP struct {
	Phone string
}

// OTPRequested tells the client how long the code lives and when it may ask again.
type OTPRequested struct {
	ExpiresIn   time.Duration
	ResendAfter time.Duration
}

// RequestOTPHandler sends one-time codes, enforcing the resend cooldown and
// the hourly cap per phone number.
type RequestOTPHandler struct {
	challenges domain.OTPChallenges
	codes      CodeGenerator
	hasher     CodeHasher
	sender     OTPSender
	clock      clock.Clock
	policy     domain.OTPPolicy
}

// NewRequestOTPHandler wires the handler's dependencies.
func NewRequestOTPHandler(challenges domain.OTPChallenges, codes CodeGenerator, hasher CodeHasher, sender OTPSender, clk clock.Clock, policy domain.OTPPolicy) *RequestOTPHandler {
	return &RequestOTPHandler{challenges: challenges, codes: codes, hasher: hasher, sender: sender, clock: clk, policy: policy}
}

// Handle sends a new code. The result is the same whether or not the number
// is registered, so it can't be used to find out who has an account.
func (h *RequestOTPHandler) Handle(ctx context.Context, cmd RequestOTP) (OTPRequested, error) {
	phone, err := shared.NewPhoneNumber(cmd.Phone)
	if err != nil {
		return OTPRequested{}, err
	}
	now := h.clock.Now()

	latest, err := h.challenges.Latest(ctx, phone)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		// first code for this number
	case err != nil:
		return OTPRequested{}, fmt.Errorf("request otp: load latest: %w", err)
	default:
		if wait := latest.CreatedAt().Add(h.policy.ResendCooldown).Sub(now); wait > 0 {
			return OTPRequested{}, &domain.RetryLaterError{Reason: domain.ErrOTPCooldown, After: wait}
		}
	}

	sent, err := h.challenges.CountSince(ctx, phone, now.Add(-time.Hour))
	if err != nil {
		return OTPRequested{}, fmt.Errorf("request otp: count: %w", err)
	}
	if sent >= h.policy.MaxPerHour {
		return OTPRequested{}, &domain.RetryLaterError{Reason: domain.ErrOTPRateLimited, After: time.Hour}
	}

	code, err := h.codes.NewCode()
	if err != nil {
		return OTPRequested{}, fmt.Errorf("request otp: generate code: %w", err)
	}
	challenge, err := domain.NewOTPChallenge(shared.NewID[domain.OTPChallengeTag](), phone, h.hasher.Hash(code), now, h.policy.TTL)
	if err != nil {
		return OTPRequested{}, fmt.Errorf("request otp: %w", err)
	}
	if err := h.challenges.Add(ctx, challenge); err != nil {
		return OTPRequested{}, fmt.Errorf("request otp: save: %w", err)
	}
	if err := h.sender.SendOTP(ctx, phone, code); err != nil {
		return OTPRequested{}, fmt.Errorf("request otp: send: %w", err)
	}
	return OTPRequested{ExpiresIn: h.policy.TTL, ResendAfter: h.policy.ResendCooldown}, nil
}
