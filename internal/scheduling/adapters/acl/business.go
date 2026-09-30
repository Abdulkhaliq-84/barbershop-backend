// Package acl adapts other modules' public APIs to scheduling's ports: their
// types and errors stop here.
package acl

import (
	"context"
	"errors"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ app.Access = (*BusinessAccess)(nil)

// BusinessAccess asks the business module who may work on a branch.
type BusinessAccess struct {
	business *business.Module
}

// NewBusinessAccess wraps the business module.
func NewBusinessAccess(m *business.Module) *BusinessAccess { return &BusinessAccess{business: m} }

// Branch translates business's answer into scheduling's errors.
func (a *BusinessAccess) Branch(ctx context.Context, actor shared.UserID, businessID shared.BusinessID, branch shared.BranchID, need app.Role) error {
	role := business.RoleBarber
	if need == app.RoleManager {
		role = business.RoleManager
	}
	return translate(a.business.AuthorizeBranch(ctx, actor, businessID, branch, role))
}

// MemberOf returns actor's own membership of the business.
func (a *BusinessAccess) MemberOf(ctx context.Context, actor shared.UserID, businessID shared.BusinessID) (app.Member, error) {
	m, err := a.business.MemberOf(ctx, actor, businessID)
	return toMember(m), translate(err)
}

// StaffMember returns an active staff member of the business.
func (a *BusinessAccess) StaffMember(ctx context.Context, businessID shared.BusinessID, staff shared.StaffID) (app.Member, error) {
	m, err := a.business.StaffMember(ctx, businessID, staff)
	return toMember(m), translate(err)
}

// StaffAtBranch checks that the branch is the business's and each staff
// member works there.
func (a *BusinessAccess) StaffAtBranch(ctx context.Context, businessID shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) error {
	return translate(a.business.StaffAtBranch(ctx, businessID, branch, staff))
}

// BranchLocation returns the branch's time zone.
func (a *BusinessAccess) BranchLocation(ctx context.Context, businessID shared.BusinessID, branch shared.BranchID) (*time.Location, error) {
	loc, err := a.business.BranchLocation(ctx, businessID, branch)
	return loc, translate(err)
}

func toMember(s business.StaffInfo) app.Member {
	return app.Member{ID: s.ID, Owner: s.Role == business.RoleOwner, Manager: s.Role == business.RoleManager, Branches: s.Branches}
}

func translate(err error) error {
	switch {
	case errors.Is(err, business.ErrNotFound):
		return domain.ErrNotFound
	case errors.Is(err, business.ErrForbidden):
		return domain.ErrForbidden
	}
	return err
}
