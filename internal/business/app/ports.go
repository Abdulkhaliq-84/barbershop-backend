// Package app holds the business use cases. Each business-mode use case
// starts with the same step — is the caller staff of this business, with a
// role that allows this? (authorize) — then loads, changes and saves through
// small interfaces (ports). It knows nothing about HTTP or SQL.
package app

import (
	"context"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MembershipView is one row of "the businesses I work in". It is a read
// model: shaped for the screen, built by one query, never saved back.
type MembershipView struct {
	StaffID     shared.StaffID
	Role        domain.Role
	BusinessID  shared.BusinessID
	DisplayName shared.LocalizedText
	Status      domain.Status
}

// MembershipReader answers "where does this user work?".
type MembershipReader interface {
	// ForUser lists the active memberships of user, newest business first.
	ForUser(ctx context.Context, user shared.UserID) ([]MembershipView, error)
}

// PlanStanding is the business's plan as billing reports it.
type PlanStanding struct {
	Plan        string // plan code: free, pro
	PlanName    shared.LocalizedText
	Status      string // setup, trialing, free
	TrialEndsAt *time.Time
	Limits      domain.Limits
}

// Plans answers "what does this business's plan allow?" (billing, through
// adapters/acl).
type Plans interface {
	Standing(ctx context.Context, business shared.BusinessID) (PlanStanding, error)
}

// Readiness tells whether a branch could take a booking: whether it has
// opening hours, a service someone performs, and a barber with a schedule.
// Catalog and scheduling know; main asks them (they depend on this module,
// so it can't).
type Readiness interface {
	BranchReadiness(ctx context.Context, business shared.BusinessID, branch shared.BranchID) (domain.BranchReadiness, error)
}
