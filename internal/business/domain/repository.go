package domain

import (
	"context"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Businesses stores businesses. Every method is scoped by BusinessID or by
// the owner: there is no "load any business" door for a bug to walk through.
type Businesses interface {
	// Register saves a new business together with its owner, in one
	// transaction. Returns ErrAlreadyRegistered when the owner already
	// registered the same CR number.
	Register(ctx context.Context, b *Business, owner *StaffMember) error
	// ByID returns the business with id, or ErrNotFound.
	ByID(ctx context.Context, id shared.BusinessID) (*Business, error)
	// Update locks the business, checks that its version is still
	// expectedVersion (ErrVersionConflict if not), calls fn and saves the
	// result — all in one transaction. If fn returns an error nothing is
	// saved. Returns ErrNotFound when there is no such business.
	Update(ctx context.Context, id shared.BusinessID, expectedVersion int, fn func(*Business) error) error
}

// Staff stores staff members.
type Staff interface {
	// Membership returns user's staff record in business, or ErrNotFound
	// when the user doesn't work there (or the business doesn't exist).
	Membership(ctx context.Context, business shared.BusinessID, user shared.UserID) (*StaffMember, error)
}

// Branches stores branches. Every method takes the business: a branch is
// only ever found inside its own business, so a branch ID from another shop
// is simply "not found".
type Branches interface {
	// Add saves a new branch.
	Add(ctx context.Context, b *Branch) error
	// ByID returns the branch, or ErrNotFound.
	ByID(ctx context.Context, business shared.BusinessID, id shared.BranchID) (*Branch, error)
	// List returns the business's branches, oldest first.
	List(ctx context.Context, business shared.BusinessID) ([]*Branch, error)
	// Update locks the branch, checks its version (ErrVersionConflict), calls
	// fn and saves — in one transaction. ErrNotFound when there is no such
	// branch in the business.
	Update(ctx context.Context, business shared.BusinessID, id shared.BranchID, expectedVersion int, fn func(*Branch) error) error
}
