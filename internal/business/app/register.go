package app

import (
	"context"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// RegisterBusiness is the command to register a business.
type RegisterBusiness struct {
	Actor         shared.UserID // the signed-in user; becomes the owner
	DisplayNameAr string
	DisplayNameEn string
	LegalName     string
	CRNumber      string
}

// RegisterBusinessHandler registers draft businesses. Any signed-in user may
// register one — there is nothing to authorize yet: they become its owner.
type RegisterBusinessHandler struct {
	businesses domain.Businesses
	clock      clock.Clock
}

// NewRegisterBusinessHandler wires the handler's dependencies.
func NewRegisterBusinessHandler(businesses domain.Businesses, clk clock.Clock) *RegisterBusinessHandler {
	return &RegisterBusinessHandler{businesses: businesses, clock: clk}
}

// Handle registers the business and returns it.
func (h *RegisterBusinessHandler) Handle(ctx context.Context, cmd RegisterBusiness) (*domain.Business, error) {
	name, err := shared.NewLocalizedText(cmd.DisplayNameAr, cmd.DisplayNameEn)
	if err != nil {
		return nil, err
	}
	cr, err := domain.NewCRNumber(cmd.CRNumber)
	if err != nil {
		return nil, err
	}
	business, owner, err := domain.RegisterBusiness(
		shared.NewID[shared.BusinessTag](), shared.NewID[shared.StaffTag](), cmd.Actor,
		domain.Registration{DisplayName: name, LegalName: cmd.LegalName, CRNumber: cr},
		h.clock.Now(),
	)
	if err != nil {
		return nil, err
	}
	if err := h.businesses.Register(ctx, business, owner); err != nil {
		return nil, fmt.Errorf("register business: %w", err)
	}
	return business, nil
}
