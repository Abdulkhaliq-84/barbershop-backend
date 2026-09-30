// Package acl adapts other modules' public APIs to catalog's ports: their
// types and errors stop here.
package acl

import (
	"context"
	"errors"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ app.Access = (*BusinessAccess)(nil)

// BusinessAccess asks the business module who may work on a branch.
type BusinessAccess struct {
	business *business.Module
}

// NewBusinessAccess wraps the business module.
func NewBusinessAccess(m *business.Module) *BusinessAccess { return &BusinessAccess{business: m} }

// Branch translates business's answer into catalog's errors.
func (a *BusinessAccess) Branch(ctx context.Context, actor shared.UserID, businessID shared.BusinessID, branch shared.BranchID, need app.Role) error {
	role := business.RoleBarber
	if need == app.RoleManager {
		role = business.RoleManager
	}
	err := a.business.AuthorizeBranch(ctx, actor, businessID, branch, role)
	switch {
	case errors.Is(err, business.ErrNotFound):
		return domain.ErrNotFound
	case errors.Is(err, business.ErrForbidden):
		return domain.ErrForbidden
	}
	return err
}
