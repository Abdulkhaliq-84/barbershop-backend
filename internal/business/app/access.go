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

// StaffAtBranch checks that every one of staff is active staff of business
// working at branch (the owner works at all of them). ErrNotFound names no
// one in particular: the caller already knows who they asked about.
func (h *AccessHandler) StaffAtBranch(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) error {
	if len(staff) == 0 {
		return nil
	}
	members, err := h.staff.List(ctx, business)
	if err != nil {
		return fmt.Errorf("staff at branch: %w", err)
	}
	working := make(map[shared.StaffID]bool, len(members))
	for _, m := range members {
		if m.IsActive() && m.WorksAt(branch) {
			working[m.ID()] = true
		}
	}
	for _, id := range staff {
		if !working[id] {
			return domain.ErrNotFound
		}
	}
	return nil
}
