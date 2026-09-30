package app

import (
	"context"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// DisplayName is a display name as the client sent it.
type DisplayName struct {
	Ar, En string
}

// UpdateBusiness changes a business's names. Nil fields stay as they are.
type UpdateBusiness struct {
	Actor           shared.UserID
	BusinessID      shared.BusinessID
	ExpectedVersion int // the version the client last read (If-Match)
	DisplayName     *DisplayName
	LegalName       *string
}

// UpdateBusinessHandler lets the owner edit a draft or rejected business.
type UpdateBusinessHandler struct {
	businesses domain.Businesses
	staff      domain.Staff
	clock      clock.Clock
}

// NewUpdateBusinessHandler wires the handler's dependencies.
func NewUpdateBusinessHandler(businesses domain.Businesses, staff domain.Staff, clk clock.Clock) *UpdateBusinessHandler {
	return &UpdateBusinessHandler{businesses: businesses, staff: staff, clock: clk}
}

// Handle authorizes first, then renames under the version check.
func (h *UpdateBusinessHandler) Handle(ctx context.Context, cmd UpdateBusiness) (*domain.Business, error) {
	if _, err := authorize(ctx, h.staff, cmd.Actor, cmd.BusinessID, domain.RoleOwner); err != nil {
		return nil, err
	}
	var name *shared.LocalizedText
	if cmd.DisplayName != nil {
		n, err := shared.NewLocalizedText(cmd.DisplayName.Ar, cmd.DisplayName.En)
		if err != nil {
			return nil, err
		}
		name = &n
	}

	var updated *domain.Business
	err := h.businesses.Update(ctx, cmd.BusinessID, cmd.ExpectedVersion, func(b *domain.Business) error {
		displayName, legalName := b.DisplayName(), b.LegalName()
		if name != nil {
			displayName = *name
		}
		if cmd.LegalName != nil {
			legalName = *cmd.LegalName
		}
		if err := b.Rename(displayName, legalName, h.clock.Now()); err != nil {
			return err
		}
		updated = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update business: %w", err)
	}
	return updated, nil
}
