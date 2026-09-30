package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// InvitationTokens makes the secret that travels with an invitation. Only
// its hash is stored, so a leaked database can't be used to join a shop.
type InvitationTokens interface {
	// New returns a fresh random token and its hash.
	New() (token string, hash []byte, err error)
	// Hash returns the hash New would have returned for token.
	Hash(token string) []byte
}

// InvitationSender delivers an invitation to the invitee's phone.
type InvitationSender interface {
	SendInvitation(ctx context.Context, to shared.PhoneNumber, business shared.LocalizedText, token string) error
}

// UserDirectory looks up users (iam's accounts, through adapters/acl).
type UserDirectory interface {
	// PhoneOf returns the phone the user signs in with, or ErrNotFound.
	PhoneOf(ctx context.Context, user shared.UserID) (shared.PhoneNumber, error)
}

// InviteStaff is the command to invite someone to the business.
type InviteStaff struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	Phone      string
	Name       string
	Role       string
	Branches   []shared.BranchID
}

// InvitationQuery names one invitation, on behalf of Actor.
type InvitationQuery struct {
	Actor        shared.UserID
	BusinessID   shared.BusinessID
	InvitationID domain.InvitationID
}

// StaffHandlers are the staff use cases. Who may do what:
//
//	invite, list/revoke invitations   owner (inviting within the plan's staff limit)
//	list staff                        owner and managers
//	accept                            the invited person, signed in with the invited phone
type StaffHandlers struct {
	businesses  domain.Businesses
	staff       domain.Staff
	invitations domain.Invitations
	users       UserDirectory
	plans       Plans
	tokens      InvitationTokens
	sender      InvitationSender
	clock       clock.Clock
}

// StaffDeps are the staff use cases' dependencies.
type StaffDeps struct {
	Businesses  domain.Businesses
	Staff       domain.Staff
	Invitations domain.Invitations
	Users       UserDirectory
	Plans       Plans
	Tokens      InvitationTokens
	Sender      InvitationSender
	Clock       clock.Clock
}

// NewStaffHandlers wires the staff use cases.
func NewStaffHandlers(d StaffDeps) *StaffHandlers {
	return &StaffHandlers{
		businesses: d.Businesses, staff: d.Staff, invitations: d.Invitations, users: d.Users,
		plans: d.Plans, tokens: d.Tokens, sender: d.Sender, clock: d.Clock,
	}
}

// Invite saves an invitation and texts its link to the invitee. Inviting the
// same phone again replaces the earlier invitation: that is how an owner
// resends one.
func (h *StaffHandlers) Invite(ctx context.Context, cmd InviteStaff) (*domain.Invitation, error) {
	owner, err := authorize(ctx, h.staff, cmd.Actor, cmd.BusinessID, domain.RoleOwner)
	if err != nil {
		return nil, err
	}
	phone, err := shared.NewPhoneNumber(cmd.Phone)
	if err != nil {
		return nil, err
	}
	role, err := domain.ParseRole(cmd.Role)
	if err != nil {
		return nil, domain.ErrInvalidInviteRole
	}
	token, hash, err := h.tokens.New()
	if err != nil {
		return nil, fmt.Errorf("invitation token: %w", err)
	}
	inv, err := domain.NewInvitation(shared.NewID[domain.InvitationTag](), cmd.BusinessID,
		domain.StaffInvite{Phone: phone, Name: cmd.Name, Role: role, Branches: cmd.Branches},
		hash, owner.UserID(), h.clock.Now())
	if err != nil {
		return nil, err
	}
	b, err := h.businesses.ByID(ctx, cmd.BusinessID)
	if err != nil {
		return nil, fmt.Errorf("invite: %w", err)
	}
	plan, err := h.plans.Standing(ctx, cmd.BusinessID)
	if err != nil {
		return nil, fmt.Errorf("invite: %w", err)
	}
	if err := h.invitations.Invite(ctx, inv, plan.Limits.AllowStaff); err != nil {
		return nil, fmt.Errorf("invite: %w", err)
	}
	// Sent after saving: an SMS for an invitation that failed to save would
	// be a dead link. If sending fails, inviting again resends (3.6 moves
	// this to the outbox, which retries by itself).
	if err := h.sender.SendInvitation(ctx, phone, b.DisplayName(), token); err != nil {
		return nil, fmt.Errorf("send invitation: %w", err)
	}
	return inv, nil
}

// ListInvitations returns the pending invitations.
func (h *StaffHandlers) ListInvitations(ctx context.Context, actor shared.UserID, business shared.BusinessID) ([]*domain.Invitation, error) {
	if _, err := authorize(ctx, h.staff, actor, business, domain.RoleOwner); err != nil {
		return nil, err
	}
	invs, err := h.invitations.Pending(ctx, business)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	return invs, nil
}

// Revoke withdraws a pending invitation: its link stops working.
func (h *StaffHandlers) Revoke(ctx context.Context, q InvitationQuery) error {
	if _, err := authorize(ctx, h.staff, q.Actor, q.BusinessID, domain.RoleOwner); err != nil {
		return err
	}
	if err := h.invitations.Update(ctx, q.BusinessID, q.InvitationID, (*domain.Invitation).Revoke); err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	return nil
}

// ListStaff returns the business's staff.
func (h *StaffHandlers) ListStaff(ctx context.Context, actor shared.UserID, business shared.BusinessID) ([]*domain.StaffMember, error) {
	if _, err := authorize(ctx, h.staff, actor, business, domain.RoleManager); err != nil {
		return nil, err
	}
	members, err := h.staff.List(ctx, business)
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	return members, nil
}

// Accept joins the caller to the business that invited them. The token
// alone is not enough: the caller must be signed in with the invited phone,
// so a forwarded or overseen SMS can't be used by someone else. Every
// refusal — unknown token, wrong phone, expired, revoked, used — is the same
// ErrInvitationInvalid, so the answer says nothing about which it was.
func (h *StaffHandlers) Accept(ctx context.Context, actor shared.UserID, token string) (MembershipView, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return MembershipView{}, domain.ErrInvitationInvalid
	}
	phone, err := h.users.PhoneOf(ctx, actor)
	if errors.Is(err, domain.ErrNotFound) {
		return MembershipView{}, domain.ErrInvitationInvalid
	}
	if err != nil {
		return MembershipView{}, fmt.Errorf("accept invitation: %w", err)
	}
	var member *domain.StaffMember
	err = h.invitations.Accept(ctx, h.tokens.Hash(token), func(inv *domain.Invitation) (*domain.StaffMember, error) {
		if inv.Phone() != phone {
			return nil, domain.ErrInvitationInvalid
		}
		m, err := inv.Accept(shared.NewID[shared.StaffTag](), actor, h.clock.Now())
		member = m
		return m, err
	})
	if err != nil {
		return MembershipView{}, fmt.Errorf("accept invitation: %w", err)
	}
	b, err := h.businesses.ByID(ctx, member.BusinessID())
	if err != nil {
		return MembershipView{}, fmt.Errorf("accept invitation: %w", err)
	}
	return MembershipView{
		StaffID: member.ID(), Role: member.Role(), BusinessID: b.ID(), DisplayName: b.DisplayName(), Status: b.Status(),
	}, nil
}
