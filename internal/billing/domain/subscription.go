package domain

import (
	"context"
	"errors"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Status is where a business stands with billing, as the owner sees it.
type Status string

// Statuses.
const (
	// StatusSetup: not approved yet, so no subscription. The business sets
	// up under the trial plan's limits; nothing is public, so nothing is
	// being sold yet.
	StatusSetup Status = "setup"
	// StatusTrialing: the free trial of the top plan is running.
	StatusTrialing Status = "trialing"
	// StatusFree: the trial ended; the business is on the Free plan.
	StatusFree Status = "free"
)

// Standing is what a business may do right now, and why.
type Standing struct {
	Plan        Plan
	Status      Status
	TrialEndsAt *time.Time // while trialing, and after (when it ended)
}

// SetupStanding is the standing of a business with no subscription yet.
func SetupStanding() Standing {
	p, _ := PlanByCode(TrialPlan)
	return Standing{Plan: p, Status: StatusSetup}
}

// Subscription is a business's plan over time. Only trials exist so far.
type Subscription struct {
	business  shared.BusinessID
	plan      PlanCode
	periodEnd time.Time
	createdAt time.Time
}

// StartTrial begins the trial of a business approved at approvedAt. The
// trial counts from approval, not from when this runs, so a late or
// repeated delivery of the approval gives the same trial.
func StartTrial(business shared.BusinessID, approvedAt time.Time) *Subscription {
	at := approvedAt.UTC().Truncate(time.Microsecond) // Postgres keeps microseconds
	return &Subscription{business: business, plan: TrialPlan, periodEnd: at.Add(TrialLength), createdAt: at}
}

// RehydrateSubscription rebuilds a subscription loaded from storage.
func RehydrateSubscription(business shared.BusinessID, plan PlanCode, periodEnd, createdAt time.Time) *Subscription {
	return &Subscription{business: business, plan: plan, periodEnd: periodEnd, createdAt: createdAt}
}

// StandingAt says what the subscription allows at now: the trial plan until
// the trial ends, then Free. Nothing is stored when it ends: the answer
// depends only on the clock, so there is no job to miss.
func (s *Subscription) StandingAt(now time.Time) Standing {
	end := s.periodEnd
	if now.Before(end) {
		p, _ := PlanByCode(s.plan)
		return Standing{Plan: p, Status: StatusTrialing, TrialEndsAt: &end}
	}
	free, _ := PlanByCode(PlanFree)
	return Standing{Plan: free, Status: StatusFree, TrialEndsAt: &end}
}

// BusinessID returns the subscribed business.
func (s *Subscription) BusinessID() shared.BusinessID { return s.business }

// Plan returns the stored plan code.
func (s *Subscription) Plan() PlanCode { return s.plan }

// PeriodEnd returns when the current period (the trial) ends.
func (s *Subscription) PeriodEnd() time.Time { return s.periodEnd }

// CreatedAt returns when it started.
func (s *Subscription) CreatedAt() time.Time { return s.createdAt }

// ErrNotFound reports a business without a subscription.
var ErrNotFound = errors.New("billing: no subscription")

// Subscriptions stores subscriptions.
type Subscriptions interface {
	// AddIfAbsent saves s unless the business already has a subscription,
	// and reports whether it saved it.
	AddIfAbsent(ctx context.Context, s *Subscription) (bool, error)
	// ForBusiness returns the business's subscription, or ErrNotFound.
	ForBusiness(ctx context.Context, business shared.BusinessID) (*Subscription, error)
}
