package app

import (
	"context"
	"fmt"
	"slices"
	"time"

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

// StaffAtBranch checks that branch is the business's and that every one of
// staff is active staff of business working at it (the owner works at all
// of them). ErrNotFound names no one in particular: the caller already knows
// who they asked about. The branch check matters when no staff caller was
// authorized first (a customer booking, M5): the owner "works at" any
// branch ID, so without it another business's branch would pass.
func (h *AccessHandler) StaffAtBranch(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) error {
	if _, err := h.branches.ByID(ctx, business, branch); err != nil {
		return fmt.Errorf("staff at branch: %w", err)
	}
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

// Member is a staff member as other modules see them.
type Member struct {
	ID       shared.StaffID
	Role     domain.Role
	Branches []shared.BranchID // where a manager or barber works; empty for the owner, who works at all
}

// WorksAt reports whether the member works at branch.
func (m Member) WorksAt(branch shared.BranchID) bool {
	return m.Role == domain.RoleOwner || slices.Contains(m.Branches, branch)
}

// MemberOf returns actor's own active membership of business, or ErrNotFound.
func (h *AccessHandler) MemberOf(ctx context.Context, actor shared.UserID, business shared.BusinessID) (Member, error) {
	m, err := h.staff.Membership(ctx, business, actor)
	if err != nil {
		return Member{}, fmt.Errorf("member of: %w", err)
	}
	if !m.IsActive() {
		return Member{}, domain.ErrNotFound
	}
	return Member{ID: m.ID(), Role: m.Role(), Branches: m.Branches()}, nil
}

// StaffMember returns an active staff member of business, or ErrNotFound.
func (h *AccessHandler) StaffMember(ctx context.Context, business shared.BusinessID, staff shared.StaffID) (Member, error) {
	members, err := h.staff.List(ctx, business)
	if err != nil {
		return Member{}, fmt.Errorf("staff member: %w", err)
	}
	for _, m := range members {
		if m.ID() == staff && m.IsActive() {
			return Member{ID: m.ID(), Role: m.Role(), Branches: m.Branches()}, nil
		}
	}
	return Member{}, domain.ErrNotFound
}

// BranchLocation returns the branch's time zone. ErrNotFound if the branch
// isn't the business's.
func (h *AccessHandler) BranchLocation(ctx context.Context, business shared.BusinessID, branch shared.BranchID) (*time.Location, error) {
	b, err := h.branches.ByID(ctx, business, branch)
	if err != nil {
		return nil, fmt.Errorf("branch location: %w", err)
	}
	loc, err := time.LoadLocation(b.Profile().Timezone)
	if err != nil {
		return nil, fmt.Errorf("branch location %q: %w", b.Profile().Timezone, err)
	}
	return loc, nil
}
