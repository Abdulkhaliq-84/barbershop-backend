package domain

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// ServiceTag marks service IDs. Booking refers to services too, so the
// marker lives in shared.
type ServiceTag = shared.ServiceTag

// ServiceID identifies a service.
type ServiceID = shared.ServiceID

// Service rules.
const (
	MaxNameLen        = 80
	MaxDescriptionLen = 500
	MinDuration       = 5 * time.Minute
	MaxDuration       = 8 * time.Hour
	DurationStep      = 5 * time.Minute
	MaxPriceHalalas   = 10_000_000 // 100,000 SAR: a typo guard, not a business limit
	MaxSortOrder      = 1000
)

// Description is optional text in both languages; empty means none.
type Description struct {
	Ar, En string
}

// ServiceDetails is everything an owner or manager edits about a service.
type ServiceDetails struct {
	Category    CategoryCode
	Name        shared.LocalizedText
	Description Description
	Duration    time.Duration
	Price       shared.Money // VAT-inclusive, as customers pay it
	SortOrder   int
}

// check validates details and returns them tidied (trimmed text).
func (d ServiceDetails) check() (ServiceDetails, error) {
	if _, err := ParseCategory(string(d.Category)); err != nil {
		return d, err
	}
	if utf8.RuneCountInString(d.Name.Ar()) > MaxNameLen || utf8.RuneCountInString(d.Name.En()) > MaxNameLen {
		return d, ErrNameTooLong
	}
	d.Description = Description{Ar: strings.TrimSpace(d.Description.Ar), En: strings.TrimSpace(d.Description.En)}
	if utf8.RuneCountInString(d.Description.Ar) > MaxDescriptionLen || utf8.RuneCountInString(d.Description.En) > MaxDescriptionLen {
		return d, ErrTextTooLong
	}
	if !validDuration(d.Duration) {
		return d, ErrInvalidDuration
	}
	if !validPrice(d.Price) {
		return d, ErrInvalidPrice
	}
	if d.SortOrder < 0 || d.SortOrder > MaxSortOrder {
		return d, ErrInvalidSort
	}
	return d, nil
}

// Service is something a branch sells: a haircut, a beard trim, a package.
// It is never deleted — appointments will point at it — only deactivated.
type Service struct {
	id        ServiceID
	business  shared.BusinessID
	branch    shared.BranchID
	details   ServiceDetails
	offerings []Offering // who performs it; empty until the manager assigns someone
	active    bool
	version   int
	createdAt time.Time
	updatedAt time.Time
}

// NewService checks details and creates an active service at branch.
func NewService(id ServiceID, business shared.BusinessID, branch shared.BranchID, d ServiceDetails, now time.Time) (*Service, error) {
	d, err := d.check()
	if err != nil {
		return nil, err
	}
	now = dbTime(now)
	return &Service{id: id, business: business, branch: branch, details: d, active: true, version: 1, createdAt: now, updatedAt: now}, nil
}

// Edit replaces the details and the active flag.
func (s *Service) Edit(d ServiceDetails, active bool, now time.Time) error {
	d, err := d.check()
	if err != nil {
		return err
	}
	s.details, s.active = d, active
	s.version++
	s.updatedAt = dbTime(now)
	return nil
}

// SetOfferings replaces who performs the service. The caller has checked
// that every staff member works at the service's branch.
func (s *Service) SetOfferings(offerings []Offering, now time.Time) error {
	checked, err := checkOfferings(offerings)
	if err != nil {
		return err
	}
	s.offerings = checked
	s.version++
	s.updatedAt = dbTime(now)
	return nil
}

// RehydrateService rebuilds a service loaded from storage.
func RehydrateService(id ServiceID, business shared.BusinessID, branch shared.BranchID, d ServiceDetails, offerings []Offering, active bool, version int, createdAt, updatedAt time.Time) *Service {
	return &Service{
		id: id, business: business, branch: branch, details: d, offerings: slices.Clone(offerings),
		active: active, version: version, createdAt: createdAt, updatedAt: updatedAt,
	}
}

// Offerings returns who performs the service, by staff ID.
func (s *Service) Offerings() []Offering { return slices.Clone(s.offerings) }

// ID returns the service ID.
func (s *Service) ID() ServiceID { return s.id }

// BusinessID returns the business that sells it.
func (s *Service) BusinessID() shared.BusinessID { return s.business }

// BranchID returns the branch that sells it.
func (s *Service) BranchID() shared.BranchID { return s.branch }

// Details returns what the service is and costs.
func (s *Service) Details() ServiceDetails { return s.details }

// IsActive reports whether customers can book it.
func (s *Service) IsActive() bool { return s.active }

// Version increases on every change (the If-Match check).
func (s *Service) Version() int { return s.version }

// CreatedAt returns when it was added.
func (s *Service) CreatedAt() time.Time { return s.createdAt }

// UpdatedAt returns when it last changed.
func (s *Service) UpdatedAt() time.Time { return s.updatedAt }

// dbTime keeps what Postgres keeps: UTC, microseconds.
func dbTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// Services stores services. Every method takes the business and branch: a
// service is only ever found inside its own branch.
type Services interface {
	// Add saves a new service.
	Add(ctx context.Context, s *Service) error
	// List returns the branch's services, by sort order then age.
	List(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]*Service, error)
	// Update locks the service, checks its version (ErrVersionConflict),
	// calls fn and saves — in one transaction. ErrNotFound when the branch
	// has no such service.
	Update(ctx context.Context, business shared.BusinessID, branch shared.BranchID, id ServiceID, expectedVersion int, fn func(*Service) error) error
}
