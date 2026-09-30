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
	// UpdateWithReadiness is Update for submission: fn also receives the
	// document and branch counts, read under the same lock. Returns
	// ErrCRNumberClaimed when another submitted business holds the CR number.
	UpdateWithReadiness(ctx context.Context, id shared.BusinessID, expectedVersion int, fn func(*Business, Readiness) error) error
}

// Staff stores staff members.
type Staff interface {
	// Membership returns user's staff record in business, or ErrNotFound
	// when the user doesn't work there (or the business doesn't exist).
	Membership(ctx context.Context, business shared.BusinessID, user shared.UserID) (*StaffMember, error)
	// List returns the business's staff, oldest first.
	List(ctx context.Context, business shared.BusinessID) ([]*StaffMember, error)
}

// Invitations stores staff invitations.
type Invitations interface {
	// Invite saves inv and revokes any other pending invitation for the same
	// phone in the business (inviting again = resending). Returns
	// ErrUnknownBranch if a branch isn't the business's. allow gets the seats
	// in use — active managers and barbers plus open invitations, not
	// counting the one being replaced — under a lock on the business.
	Invite(ctx context.Context, inv *Invitation, allow func(seats int) error) error
	// Pending returns the business's pending invitations, newest first.
	Pending(ctx context.Context, business shared.BusinessID) ([]*Invitation, error)
	// Update locks the invitation inside business, calls fn and saves it.
	// ErrNotFound when there is none.
	Update(ctx context.Context, business shared.BusinessID, id InvitationID, fn func(*Invitation) error) error
	// Accept locks the invitation whose token hashes to tokenHash, calls fn
	// and saves the invitation and the staff member fn returns — together.
	// ErrInvitationInvalid when no invitation has that hash; ErrAlreadyStaff
	// when the user already works at the business.
	Accept(ctx context.Context, tokenHash []byte, fn func(*Invitation) (*StaffMember, error)) error
}

// Branches stores branches. Every method takes the business: a branch is
// only ever found inside its own business, so a branch ID from another shop
// is simply "not found".
type Branches interface {
	// Add saves a new branch if allow accepts the number of branches the
	// business already has, counted under a lock on the business so two
	// adds can't both take the last place.
	Add(ctx context.Context, b *Branch, allow func(existing int) error) error
	// ByID returns the branch, or ErrNotFound.
	ByID(ctx context.Context, business shared.BusinessID, id shared.BranchID) (*Branch, error)
	// List returns the business's branches, oldest first.
	List(ctx context.Context, business shared.BusinessID) ([]*Branch, error)
	// Bookable returns a published branch of an active business, found by
	// ID alone (customers don't know the business), or ErrNotFound.
	Bookable(ctx context.Context, id shared.BranchID) (*Branch, error)
	// Update locks the branch, checks its version (ErrVersionConflict), calls
	// fn and saves — in one transaction. ErrNotFound when there is no such
	// branch in the business.
	Update(ctx context.Context, business shared.BusinessID, id shared.BranchID, expectedVersion int, fn func(*Branch) error) error
}
