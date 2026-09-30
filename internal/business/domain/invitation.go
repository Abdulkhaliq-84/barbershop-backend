package domain

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// InvitationTag marks invitation IDs (only this module uses them).
type InvitationTag struct{}

// InvitationID identifies an invitation.
type InvitationID = shared.ID[InvitationTag]

// Invitation rules.
const (
	InvitationTTL    = 7 * 24 * time.Hour // long enough for a barber to see an SMS, short enough to go stale
	MaxStaffNameLen  = 80
	MaxStaffBranches = 50
)

// InvitationStatus is where an invitation is. "Expired" is not stored: a
// pending invitation past its expiry simply can't be accepted.
type InvitationStatus string

// Invitation statuses.
const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationRevoked  InvitationStatus = "revoked"
)

// Invitation asks a person, identified by phone, to join a business as a
// manager or barber at some branches. It is accepted with a secret token
// sent to that phone, by a user signed in with that phone.
type Invitation struct {
	id         InvitationID
	business   shared.BusinessID
	phone      shared.PhoneNumber
	name       string
	role       Role
	branches   []shared.BranchID
	tokenHash  []byte
	status     InvitationStatus
	invitedBy  shared.UserID
	createdAt  time.Time
	expiresAt  time.Time
	acceptedAt *time.Time
	acceptedBy shared.UserID
}

// StaffInvite is what an owner fills in to invite someone.
type StaffInvite struct {
	Phone    shared.PhoneNumber
	Name     string
	Role     Role
	Branches []shared.BranchID
}

// NewInvitation checks an invite and creates a pending invitation. Owners
// aren't invited: a business has exactly one, from registration.
func NewInvitation(id InvitationID, business shared.BusinessID, in StaffInvite, tokenHash []byte, by shared.UserID, now time.Time) (*Invitation, error) {
	if in.Role != RoleManager && in.Role != RoleBarber {
		return nil, ErrInvalidInviteRole
	}
	if in.Phone.IsZero() {
		return nil, shared.ErrInvalidPhoneNumber
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrStaffNameRequired
	}
	if utf8.RuneCountInString(name) > MaxStaffNameLen {
		return nil, ErrTextTooLong
	}
	branches := slices.Clone(in.Branches)
	slices.SortFunc(branches, func(a, b shared.BranchID) int { return strings.Compare(a.String(), b.String()) })
	branches = slices.Compact(branches)
	if len(branches) == 0 || len(branches) > MaxStaffBranches {
		return nil, ErrStaffBranchRequired
	}
	now = dbTime(now)
	return &Invitation{
		id: id, business: business, phone: in.Phone, name: name, role: in.Role, branches: branches,
		tokenHash: slices.Clone(tokenHash), status: InvitationPending, invitedBy: by,
		createdAt: now, expiresAt: now.Add(InvitationTTL),
	}, nil
}

// IsOpen reports whether the invitation can still be accepted.
func (i *Invitation) IsOpen(now time.Time) bool {
	return i.status == InvitationPending && now.Before(i.expiresAt)
}

// Accept turns the invitation into a staff member for user. Callers first
// check that user signed in with the invited phone: holding the token alone
// is not enough, in case the SMS was seen by someone else.
func (i *Invitation) Accept(staffID shared.StaffID, user shared.UserID, now time.Time) (*StaffMember, error) {
	if !i.IsOpen(now) {
		return nil, ErrInvitationInvalid
	}
	at := dbTime(now)
	i.status, i.acceptedAt, i.acceptedBy = InvitationAccepted, &at, user
	return &StaffMember{
		id: staffID, business: i.business, user: user, role: i.role, active: true, createdAt: at,
		displayName: i.name, branches: slices.Clone(i.branches),
	}, nil
}

// Revoke withdraws a pending invitation, expired or not.
func (i *Invitation) Revoke() error {
	if i.status != InvitationPending {
		return ErrInvitationClosed
	}
	i.status = InvitationRevoked
	return nil
}

// RehydrateInvitation rebuilds an invitation loaded from storage.
func RehydrateInvitation(id InvitationID, business shared.BusinessID, in StaffInvite, tokenHash []byte, status InvitationStatus, by shared.UserID, createdAt, expiresAt time.Time, acceptedAt *time.Time, acceptedBy shared.UserID) *Invitation {
	return &Invitation{
		id: id, business: business, phone: in.Phone, name: in.Name, role: in.Role, branches: in.Branches,
		tokenHash: tokenHash, status: status, invitedBy: by, createdAt: createdAt, expiresAt: expiresAt,
		acceptedAt: acceptedAt, acceptedBy: acceptedBy,
	}
}

// ID returns the invitation ID.
func (i *Invitation) ID() InvitationID { return i.id }

// BusinessID returns the inviting business.
func (i *Invitation) BusinessID() shared.BusinessID { return i.business }

// Phone returns the invited phone number.
func (i *Invitation) Phone() shared.PhoneNumber { return i.phone }

// Name returns the name the owner gave the invitee.
func (i *Invitation) Name() string { return i.name }

// Role returns the offered role.
func (i *Invitation) Role() Role { return i.role }

// Branches returns the branches the invitee will work at.
func (i *Invitation) Branches() []shared.BranchID { return slices.Clone(i.branches) }

// TokenHash returns the SHA-256 of the invitation token.
func (i *Invitation) TokenHash() []byte { return slices.Clone(i.tokenHash) }

// Status returns the stored status.
func (i *Invitation) Status() InvitationStatus { return i.status }

// InvitedBy returns the owner who sent it.
func (i *Invitation) InvitedBy() shared.UserID { return i.invitedBy }

// CreatedAt returns when it was sent.
func (i *Invitation) CreatedAt() time.Time { return i.createdAt }

// ExpiresAt returns when it stops working.
func (i *Invitation) ExpiresAt() time.Time { return i.expiresAt }

// AcceptedAt returns when it was accepted, or nil.
func (i *Invitation) AcceptedAt() *time.Time { return i.acceptedAt }

// AcceptedBy returns who accepted it (zero until accepted).
func (i *Invitation) AcceptedBy() shared.UserID { return i.acceptedBy }
