package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// authorize is the first line of every business-mode use case: the caller
// must be staff of the business named in the request, with at least the role
// need. Skipping it is how one shop reads another shop's data (BOLA, the #1
// API risk), so it runs before anything is loaded.
//
// A caller who isn't staff gets ErrNotFound, exactly like a business that
// doesn't exist: otherwise the difference between 403 and 404 would tell a
// stranger which IDs are real. Staff with too small a role get ErrForbidden —
// they already know the business exists.
func authorize(ctx context.Context, staff domain.Staff, actor shared.UserID, business shared.BusinessID, need domain.Role) (*domain.StaffMember, error) {
	member, err := staff.Membership(ctx, business, actor)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authorize: %w", err)
	}
	if !member.IsActive() {
		return nil, domain.ErrNotFound // former staff are strangers again
	}
	if err := member.Authorize(need); err != nil {
		return nil, err
	}
	return member, nil
}
