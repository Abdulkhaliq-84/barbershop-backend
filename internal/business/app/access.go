package app

import (
	"context"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// AccessHandler answers other modules' authorization questions: catalog and
// scheduling keep data per branch, but only business knows who works where.
type AccessHandler struct {
	staff    domain.Staff
	branches domain.Branches
}

// NewAccessHandler wires the use case.
func NewAccessHandler(staff domain.Staff, branches domain.Branches) *AccessHandler {
	return &AccessHandler{staff: staff, branches: branches}
}

// Branch checks that actor may work on branch of business with at least
// the role need, in this order:
//
//  1. actor is active staff of the business, with the role — else ErrNotFound
//     (strangers learn nothing) or ErrForbidden;
//  2. the branch is the business's — else ErrNotFound, so another shop's
//     branch ID is "not found" like one that doesn't exist;
//  3. actor works at the branch (the owner works at all) — else ErrForbidden.
func (h *AccessHandler) Branch(ctx context.Context, actor shared.UserID, business shared.BusinessID, branch shared.BranchID, need domain.Role) error {
	member, err := authorize(ctx, h.staff, actor, business, need)
	if err != nil {
		return err
	}
	if _, err := h.branches.ByID(ctx, business, branch); err != nil {
		return fmt.Errorf("access: %w", err)
	}
	return member.AuthorizeBranch(need, branch)
}
