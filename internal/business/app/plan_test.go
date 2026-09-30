package app_test

import (
	"errors"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestBranchLimit(t *testing.T) {
	t.Parallel()
	f := newBranchFixture(t)
	ctx := t.Context()
	f.plans.set(domain.Limits{MaxBranches: 1, MaxStaff: 3})
	if _, err := f.branches.Create(ctx, createCmd(f.owner, f.business)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.branches.Create(ctx, createCmd(f.owner, f.business)); !errors.Is(err, domain.ErrBranchLimitReached) {
		t.Fatalf("second branch on a 1-branch plan: error = %v", err)
	}
	if list, _ := f.branches.List(ctx, f.owner, f.business); len(list) != 1 {
		t.Errorf("branches = %d, want 1", len(list))
	}
}

func TestStaffLimit(t *testing.T) {
	t.Parallel()
	f := newStaffFixture(t)
	ctx := t.Context()
	f.plans.set(domain.Limits{MaxBranches: 1, MaxStaff: 1})
	if _, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "barber")); err != nil {
		t.Fatal(err)
	}
	// The pending invitation holds the only seat...
	if _, err := f.staff.Invite(ctx, f.inviteCmd("0559999999", "barber")); !errors.Is(err, domain.ErrStaffLimitReached) {
		t.Fatalf("second person: error = %v, want ErrStaffLimitReached", err)
	}
	// ...but resending to the same phone replaces it, so it still fits.
	if _, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "manager")); err != nil {
		t.Fatalf("resend: %v", err)
	}
}

func TestPlanHandler(t *testing.T) {
	t.Parallel()
	f := newStaffFixture(t)
	ctx := t.Context()
	h := app.NewPlanHandler(f.store, f.plans)
	s, err := h.Handle(ctx, f.owner, f.business)
	if err != nil || s.Plan != "pro" || s.Status != "setup" || s.Limits.MaxBranches != 5 {
		t.Fatalf("owner: %+v, %v", s, err)
	}
	manager, stranger := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	f.store.addStaff(f.business, manager, domain.RoleManager, true, f.branch)
	if _, err := h.Handle(ctx, manager, f.business); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("manager: error = %v, want ErrForbidden", err)
	}
	if _, err := h.Handle(ctx, stranger, f.business); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: error = %v, want ErrNotFound", err)
	}
}
