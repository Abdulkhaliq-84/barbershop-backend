package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Status is where a business is in its lifecycle:
//
//	draft → pending_review → active ⇄ suspended
//	              ↓    ↑
//	            rejected
//
// Only an active business can publish branches and take bookings.
type Status string

// Business statuses.
const (
	StatusDraft         Status = "draft"
	StatusPendingReview Status = "pending_review"
	StatusActive        Status = "active"
	StatusRejected      Status = "rejected"
	StatusSuspended     Status = "suspended"
)

// ParseStatus reads a stored status.
func ParseStatus(s string) (Status, error) {
	switch st := Status(s); st {
	case StatusDraft, StatusPendingReview, StatusActive, StatusRejected, StatusSuspended:
		return st, nil
	default:
		return "", ErrUnknownStatus
	}
}

// Length limits, in characters (runes): Arabic letters are 2 bytes each in
// UTF-8, so a byte limit would allow Arabic names half as long as English ones.
const (
	MaxDisplayNameLen = 100
	MaxLegalNameLen   = 200
)

// Business is a tenant: a barbershop company with one or more branches. It
// is the aggregate root for its profile and verification status.
type Business struct {
	id          shared.BusinessID
	owner       shared.UserID
	displayName shared.LocalizedText
	legalName   string
	cr          CRNumber
	status      Status
	version     int
	createdAt   time.Time
	updatedAt   time.Time
}

// Registration is what an owner provides to register a business.
type Registration struct {
	DisplayName shared.LocalizedText
	LegalName   string
	CRNumber    CRNumber
}

// RegisterBusiness creates a draft business and its owner, the first staff
// member. They are created together because a business without an owner
// could never be managed — that invariant is easiest to keep by making the
// wrong state impossible to construct.
func RegisterBusiness(id shared.BusinessID, ownerStaff shared.StaffID, owner shared.UserID, r Registration, now time.Time) (*Business, *StaffMember, error) {
	if owner.IsZero() {
		return nil, nil, ErrOwnerRequired
	}
	if r.CRNumber == (CRNumber{}) {
		return nil, nil, ErrInvalidCRNumber
	}
	if r.DisplayName.Ar() == "" {
		return nil, nil, shared.ErrArabicRequired
	}
	if utf8.RuneCountInString(r.DisplayName.Ar()) > MaxDisplayNameLen || utf8.RuneCountInString(r.DisplayName.En()) > MaxDisplayNameLen {
		return nil, nil, ErrTextTooLong
	}
	legal := strings.TrimSpace(r.LegalName)
	if legal == "" {
		return nil, nil, ErrLegalNameRequired
	}
	if utf8.RuneCountInString(legal) > MaxLegalNameLen {
		return nil, nil, ErrTextTooLong
	}

	// PostgreSQL keeps microseconds. Dropping the rest now means the time in
	// the 201 response is the same one every later read returns.
	now = now.UTC().Truncate(time.Microsecond)
	b := &Business{
		id: id, owner: owner, displayName: r.DisplayName, legalName: legal, cr: r.CRNumber,
		status: StatusDraft, version: 1, createdAt: now, updatedAt: now,
	}
	m := &StaffMember{id: ownerStaff, business: id, user: owner, role: RoleOwner, active: true, createdAt: now}
	return b, m, nil
}

// RehydrateBusiness rebuilds a business loaded from storage.
func RehydrateBusiness(id shared.BusinessID, owner shared.UserID, displayName shared.LocalizedText, legalName string, cr CRNumber, status Status, version int, createdAt, updatedAt time.Time) *Business {
	return &Business{
		id: id, owner: owner, displayName: displayName, legalName: legalName, cr: cr,
		status: status, version: version, createdAt: createdAt, updatedAt: updatedAt,
	}
}

// ID returns the business ID.
func (b *Business) ID() shared.BusinessID { return b.id }

// OwnerID returns the user who owns the business.
func (b *Business) OwnerID() shared.UserID { return b.owner }

// DisplayName returns the name customers see.
func (b *Business) DisplayName() shared.LocalizedText { return b.displayName }

// LegalName returns the name on the Commercial Registration.
func (b *Business) LegalName() string { return b.legalName }

// CRNumber returns the Commercial Registration number.
func (b *Business) CRNumber() CRNumber { return b.cr }

// Status returns the lifecycle status.
func (b *Business) Status() Status { return b.status }

// Version increases with every saved change (optimistic concurrency).
func (b *Business) Version() int { return b.version }

// CreatedAt returns when the business was registered.
func (b *Business) CreatedAt() time.Time { return b.createdAt }

// UpdatedAt returns when the business last changed.
func (b *Business) UpdatedAt() time.Time { return b.updatedAt }
