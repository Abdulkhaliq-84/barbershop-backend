package app

import (
	"context"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// GetBusiness asks for one business, on behalf of Actor.
type GetBusiness struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
}

// GetBusinessHandler returns a business to its owner.
type GetBusinessHandler struct {
	businesses domain.Businesses
	staff      domain.Staff
}

// NewGetBusinessHandler wires the handler's dependencies.
func NewGetBusinessHandler(businesses domain.Businesses, staff domain.Staff) *GetBusinessHandler {
	return &GetBusinessHandler{businesses: businesses, staff: staff}
}

// Handle authorizes first, then loads.
func (h *GetBusinessHandler) Handle(ctx context.Context, q GetBusiness) (*domain.Business, error) {
	if _, err := authorize(ctx, h.staff, q.Actor, q.BusinessID, domain.RoleOwner); err != nil {
		return nil, err
	}
	b, err := h.businesses.ByID(ctx, q.BusinessID)
	if err != nil {
		return nil, fmt.Errorf("get business: %w", err)
	}
	return b, nil
}

// ListMyMembershipsHandler lists where the signed-in user works. It needs no
// authorization beyond being signed in: it only ever reads the caller's own
// rows (the user ID comes from the access token, never from the request).
type ListMyMembershipsHandler struct {
	memberships MembershipReader
}

// NewListMyMembershipsHandler wires the handler's dependencies.
func NewListMyMembershipsHandler(memberships MembershipReader) *ListMyMembershipsHandler {
	return &ListMyMembershipsHandler{memberships: memberships}
}

// Handle returns the caller's memberships.
func (h *ListMyMembershipsHandler) Handle(ctx context.Context, actor shared.UserID) ([]MembershipView, error) {
	views, err := h.memberships.ForUser(ctx, actor)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	return views, nil
}
