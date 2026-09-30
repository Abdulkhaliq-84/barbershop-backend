package acl

import (
	"context"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ app.Plans = (*BillingPlans)(nil)

// BillingPlans reads plans and limits from the billing module.
type BillingPlans struct {
	billing *billing.Module
}

// NewBillingPlans wraps the billing module.
func NewBillingPlans(m *billing.Module) *BillingPlans { return &BillingPlans{billing: m} }

// Standing returns the business's plan in this module's terms.
func (p *BillingPlans) Standing(ctx context.Context, business shared.BusinessID) (app.PlanStanding, error) {
	s, err := p.billing.Standing(ctx, business)
	if err != nil {
		return app.PlanStanding{}, err
	}
	return app.PlanStanding{
		Plan: s.Plan, PlanName: s.PlanName, Status: s.Status, TrialEndsAt: s.TrialEndsAt,
		Limits: domain.Limits{MaxBranches: s.MaxBranches, MaxStaff: s.MaxStaff},
	}, nil
}
