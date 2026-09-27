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
}

// Staff stores staff members.
type Staff interface {
	// Membership returns user's staff record in business, or ErrNotFound
	// when the user doesn't work there (or the business doesn't exist).
	Membership(ctx context.Context, business shared.BusinessID, user shared.UserID) (*StaffMember, error)
}
