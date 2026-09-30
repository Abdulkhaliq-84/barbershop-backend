package acl

import (
	"context"
	"errors"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ app.UserDirectory = (*IAMUsers)(nil)

// IAMUsers answers user questions from the iam module.
type IAMUsers struct {
	iam *iam.Module
}

// NewIAMUsers wraps the iam module.
func NewIAMUsers(m *iam.Module) *IAMUsers { return &IAMUsers{iam: m} }

// PhoneOf returns the user's sign-in phone.
func (u *IAMUsers) PhoneOf(ctx context.Context, user shared.UserID) (shared.PhoneNumber, error) {
	phone, err := u.iam.PhoneOf(ctx, user)
	if errors.Is(err, iam.ErrUnknownUser) {
		return shared.PhoneNumber{}, domain.ErrNotFound
	}
	return phone, err
}
