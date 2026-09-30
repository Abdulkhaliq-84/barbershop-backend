package app_test

import (
	"errors"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestAccessBranch(t *testing.T) {
	t.Parallel()
	f := newBranchFixture(t)
	ctx := t.Context()
	mine, err := f.branches.Create(ctx, createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.branches.Create(ctx, createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	// A branch of another business, created by its own owner.
	elsewhere := newBranchFixture(t)
	foreign, err := elsewhere.branches.Create(ctx, createCmd(elsewhere.owner, elsewhere.business))
	if err != nil {
		t.Fatal(err)
	}
	f.branchStore.branches = append(f.branchStore.branches, elsewhere.branchStore.branches...)

	manager, barber, former, stranger := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	f.store.addStaff(f.business, manager, domain.RoleManager, true, mine.ID())
	f.store.addStaff(f.business, barber, domain.RoleBarber, true, mine.ID())
	f.store.addStaff(f.business, former, domain.RoleManager, false, mine.ID())
	access := app.NewAccessHandler(f.store, f.branchStore)

	tests := []struct {
		name   string
		actor  shared.UserID
		branch shared.BranchID
		need   domain.Role
		want   error
	}{
		{"owner, any of their branches", f.owner, other.ID(), domain.RoleOwner, nil},
		{"owner, another shop's branch", f.owner, foreign.ID(), domain.RoleManager, domain.ErrNotFound},
		{"owner, made-up branch", f.owner, shared.NewID[shared.BranchTag](), domain.RoleManager, domain.ErrNotFound},
		{"manager, own branch", manager, mine.ID(), domain.RoleManager, nil},
		{"manager, other branch", manager, other.ID(), domain.RoleManager, domain.ErrForbidden},
		{"barber reads own branch", barber, mine.ID(), domain.RoleBarber, nil},
		{"barber manages", barber, mine.ID(), domain.RoleManager, domain.ErrForbidden},
		{"former manager", former, mine.ID(), domain.RoleBarber, domain.ErrNotFound},
		{"stranger", stranger, mine.ID(), domain.RoleBarber, domain.ErrNotFound},
	}
	for _, tt := range tests {
		err := access.Branch(ctx, tt.actor, f.business, tt.branch, tt.need)
		if (tt.want == nil && err != nil) || (tt.want != nil && !errors.Is(err, tt.want)) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestStaffAtBranch(t *testing.T) {
	t.Parallel()
	f := newBranchFixture(t)
	ctx := t.Context()
	mine, err := f.branches.Create(ctx, createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.branches.Create(ctx, createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	barber, elsewhere, former := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	f.store.addStaff(f.business, barber, domain.RoleBarber, true, mine.ID())
	f.store.addStaff(f.business, elsewhere, domain.RoleBarber, true, other.ID())
	f.store.addStaff(f.business, former, domain.RoleBarber, false, mine.ID())
	staffID := func(user shared.UserID) shared.StaffID {
		t.Helper()
		m, err := f.store.Membership(ctx, f.business, user)
		if err != nil {
			t.Fatal(err)
		}
		return m.ID()
	}
	access := app.NewAccessHandler(f.store, f.branchStore)

	// The owner works at every branch; a barber at their own.
	if err := access.StaffAtBranch(ctx, f.business, mine.ID(), []shared.StaffID{staffID(f.owner), staffID(barber)}); err != nil {
		t.Errorf("owner and barber: %v", err)
	}
	if err := access.StaffAtBranch(ctx, f.business, mine.ID(), nil); err != nil {
		t.Errorf("nobody: %v", err)
	}
	for name, id := range map[string]shared.StaffID{
		"barber of another branch": staffID(elsewhere),
		"former barber":            staffID(former),
		"made-up staff":            shared.NewID[shared.StaffTag](),
	} {
		err := access.StaffAtBranch(ctx, f.business, mine.ID(), []shared.StaffID{staffID(barber), id})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: error = %v, want ErrNotFound", name, err)
		}
	}
}
