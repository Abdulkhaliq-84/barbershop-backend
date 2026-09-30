package app

import (
	"context"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// PlanHandler shows the owner their plan. Billing owns the plan; this
// module serves it because only this module knows who may see it.
type PlanHandler struct {
	staff domain.Staff
	plans Plans
}

// NewPlanHandler wires the use case.
func NewPlanHandler(staff domain.Staff, plans Plans) *PlanHandler {
	return &PlanHandler{staff: staff, plans: plans}
}

// Handle returns the business's plan, limits and trial, to its owner.
func (h *PlanHandler) Handle(ctx context.Context, actor shared.UserID, business shared.BusinessID) (PlanStanding, error) {
	if _, err := authorize(ctx, h.staff, actor, business, domain.RoleOwner); err != nil {
		return PlanStanding{}, err
	}
	s, err := h.plans.Standing(ctx, business)
	if err != nil {
		return PlanStanding{}, fmt.Errorf("plan: %w", err)
	}
	return s, nil
}
