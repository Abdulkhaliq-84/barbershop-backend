// Package app holds billing's use cases. They are called by other modules
// (through billing's root package) and by event handlers, never by HTTP
// directly: business-mode screens reach billing through the business
// module, which checks the caller's membership first.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers are billing's use cases.
type Handlers struct {
	subs  domain.Subscriptions
	clock clock.Clock
}

// NewHandlers wires the use cases.
func NewHandlers(subs domain.Subscriptions, clk clock.Clock) *Handlers {
	return &Handlers{subs: subs, clock: clk}
}

// StartTrial starts the trial of a newly approved business. Idempotent: a
// business that already has a subscription keeps it, so the approval event
// may be delivered any number of times.
func (h *Handlers) StartTrial(ctx context.Context, business shared.BusinessID, approvedAt time.Time) error {
	if _, err := h.subs.AddIfAbsent(ctx, domain.StartTrial(business, approvedAt)); err != nil {
		return fmt.Errorf("start trial: %w", err)
	}
	return nil
}

// Standing says what the business may do now.
func (h *Handlers) Standing(ctx context.Context, business shared.BusinessID) (domain.Standing, error) {
	s, err := h.subs.ForBusiness(ctx, business)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.SetupStanding(), nil
	}
	if err != nil {
		return domain.Standing{}, fmt.Errorf("standing: %w", err)
	}
	return s.StandingAt(h.clock.Now()), nil
}
