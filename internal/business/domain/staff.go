package domain

import (
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
	id        shared.StaffID
	business  shared.BusinessID
	user      shared.UserID
	role      Role
	active    bool
	createdAt time.Time
}

// RehydrateStaffMember rebuilds a staff member loaded from storage.
func RehydrateStaffMember(id shared.StaffID, business shared.BusinessID, user shared.UserID, role Role, active bool, createdAt time.Time) *StaffMember {
	return &StaffMember{id: id, business: business, user: user, role: role, active: active, createdAt: createdAt}
}

// Authorize says whether this member may do something that needs at least
// the role need. Deactivated staff may do nothing.
func (m *StaffMember) Authorize(need Role) error {
	if !m.active || m.role.rank() < need.rank() {
		return ErrForbidden
	}
	return nil
}

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
