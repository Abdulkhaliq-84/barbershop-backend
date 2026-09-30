package domain

import (
	"slices"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Role is what a staff member may do inside one business
// (docs/architecture/domain-model.md §4).
type Role string

// Staff roles, from most to least powerful.
const (
	RoleOwner   Role = "owner"   // everything: branches, staff, billing, verification
	RoleManager Role = "manager" // everything in their branches
	RoleBarber  Role = "barber"  // their own schedule and appointments
)

// rank orders roles so a check can say "manager or above".
func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleManager:
		return 2
	case RoleBarber:
		return 1
	default:
		return 0
	}
}

// ParseRole reads a stored role.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if r.rank() == 0 {
		return "", ErrUnknownRole
	}
	return r, nil
}

// StaffMember is a person working in a business, with one role. Every
// business-mode request starts by loading the caller's StaffMember and
// asking it whether the role is enough (Authorize).
type StaffMember struct {
	id          shared.StaffID
	business    shared.BusinessID
	user        shared.UserID
	role        Role
	active      bool
	createdAt   time.Time
	displayName string            // how customers and colleagues see them
	branches    []shared.BranchID // where a manager or barber works; empty for the owner
}

// RehydrateStaffMember rebuilds a staff member loaded from storage.
func RehydrateStaffMember(id shared.StaffID, business shared.BusinessID, user shared.UserID, role Role, active bool, createdAt time.Time, displayName string, branches []shared.BranchID) *StaffMember {
	return &StaffMember{
		id: id, business: business, user: user, role: role, active: active, createdAt: createdAt,
		displayName: displayName, branches: slices.Clone(branches),
	}
}

// Authorize says whether this member may do something that needs at least
// the role need. Deactivated staff may do nothing.
func (m *StaffMember) Authorize(need Role) error {
	if !m.active || m.role.rank() < need.rank() {
		return ErrForbidden
	}
	return nil
}

// AuthorizeBranch is Authorize for work on one branch: owners may act on
// every branch, managers and barbers only on the branches they work at.
func (m *StaffMember) AuthorizeBranch(need Role, branch shared.BranchID) error {
	if err := m.Authorize(need); err != nil {
		return err
	}
	if !m.WorksAt(branch) {
		return ErrForbidden
	}
	return nil
}

// WorksAt reports whether the member works at branch. The owner works at
// all of them.
func (m *StaffMember) WorksAt(branch shared.BranchID) bool {
	return m.role == RoleOwner || slices.Contains(m.branches, branch)
}

// DisplayName returns the name shown for this member.
func (m *StaffMember) DisplayName() string { return m.displayName }

// Branches returns the branches a manager or barber works at.
func (m *StaffMember) Branches() []shared.BranchID { return slices.Clone(m.branches) }

// ID returns the staff member's ID.
func (m *StaffMember) ID() shared.StaffID { return m.id }

// BusinessID returns the business they work in.
func (m *StaffMember) BusinessID() shared.BusinessID { return m.business }

// UserID returns the user behind this staff member.
func (m *StaffMember) UserID() shared.UserID { return m.user }

// Role returns their role.
func (m *StaffMember) Role() Role { return m.role }

// IsActive reports whether they still work there.
func (m *StaffMember) IsActive() bool { return m.active }

// CreatedAt returns when they joined.
func (m *StaffMember) CreatedAt() time.Time { return m.createdAt }
