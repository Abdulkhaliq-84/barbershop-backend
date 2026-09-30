package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// BranchStatus says whether customers can find and book a branch.
type BranchStatus string

// Branch statuses. Publishing (it needs an active business, services and
// barbers with working hours) arrives in a later milestone.
const (
	BranchDraft       BranchStatus = "draft"
	BranchPublished   BranchStatus = "published"
	BranchUnpublished BranchStatus = "unpublished"
)

// ParseBranchStatus reads a stored status.
func ParseBranchStatus(s string) (BranchStatus, error) {
	switch st := BranchStatus(s); st {
	case BranchDraft, BranchPublished, BranchUnpublished:
		return st, nil
	default:
		return "", ErrUnknownStatus
	}
}

// CityCode names a city, e.g. "riyadh". Customers search by city, so cities
// are codes from a fixed list the app shows, not free text that would spell
// Riyadh five ways. (The list itself becomes reference data in M6.)
type CityCode string

var cityCodePattern = regexp.MustCompile(`^[a-z][a-z_]{1,39}$`)

// ParseCityCode validates a city code.
func ParseCityCode(s string) (CityCode, error) {
	if !cityCodePattern.MatchString(s) {
		return "", ErrInvalidCityCode
	}
	return CityCode(s), nil
}

// Length limits for branch text, in characters.
const (
	MaxDistrictLen = 80
	MaxAddressLen  = 200
)

// DefaultTimezone is where almost every branch is.
const DefaultTimezone = "Asia/Riyadh"

// BranchProfile is who and where a branch is. The zero PhoneNumber means
// "no phone".
type BranchProfile struct {
	Name     shared.LocalizedText
	City     CityCode
	District string
	Address  string
	Location shared.GeoPoint
	Phone    shared.PhoneNumber
	Timezone string // IANA name: the branch's opening hours are in its local time
}

// validate checks every field and returns the profile with text trimmed.
func (p BranchProfile) validate() (BranchProfile, error) {
	if p.Name.Ar() == "" {
		return p, shared.ErrArabicRequired
	}
	if utf8.RuneCountInString(p.Name.Ar()) > MaxDisplayNameLen || utf8.RuneCountInString(p.Name.En()) > MaxDisplayNameLen {
		return p, ErrTextTooLong
	}
	if _, err := ParseCityCode(string(p.City)); err != nil {
		return p, err
	}
	p.District = strings.TrimSpace(p.District)
	p.Address = strings.TrimSpace(p.Address)
	if p.Address == "" {
		return p, ErrAddressRequired
	}
	if utf8.RuneCountInString(p.District) > MaxDistrictLen || utf8.RuneCountInString(p.Address) > MaxAddressLen {
		return p, ErrTextTooLong
	}
	// Re-validate: a zero GeoPoint is (0, 0), in the Atlantic, never a shop here.
	if _, err := shared.NewGeoPoint(p.Location.Lat(), p.Location.Lng()); err != nil || p.Location == (shared.GeoPoint{}) {
		return p, shared.ErrInvalidCoordinates
	}
	if err := checkTimezone(p.Timezone); err != nil {
		return p, err
	}
	return p, nil
}

// checkTimezone accepts IANA zone names only. "" and "Local" would mean the
// server's zone, which is wherever the server happens to run.
func checkTimezone(name string) error {
	if name == "" || name == "Local" {
		return ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(name); err != nil {
		return ErrInvalidTimezone
	}
	return nil
}

// Branch is one shop of a business: where it is and how it takes bookings.
// It is its own aggregate (not part of Business) so editing one branch
// never locks the whole business.
type Branch struct {
	id        shared.BranchID
	business  shared.BusinessID
	profile   BranchProfile
	policy    BookingPolicy
	status    BranchStatus
	version   int
	createdAt time.Time
	updatedAt time.Time
	events    []Event
}

// NewBranch creates a draft branch.
func NewBranch(id shared.BranchID, business shared.BusinessID, p BranchProfile, policy BookingPolicy, now time.Time) (*Branch, error) {
	p, err := p.validate()
	if err != nil {
		return nil, err
	}
	if policy == (BookingPolicy{}) {
		policy = DefaultBookingPolicy()
	}
	now = dbTime(now)
	return &Branch{
		id: id, business: business, profile: p, policy: policy,
		status: BranchDraft, version: 1, createdAt: now, updatedAt: now,
	}, nil
}

// Edit replaces the profile and the booking policy. Callers that change only
// some fields start from Profile() and Policy(). Bumps the version.
func (b *Branch) Edit(p BranchProfile, policy BookingPolicy, now time.Time) error {
	p, err := p.validate()
	if err != nil {
		return err
	}
	if policy == (BookingPolicy{}) {
		return &PolicyError{Rule: "a booking policy is required"}
	}
	b.profile, b.policy = p, policy
	b.touch(now)
	return nil
}

// BranchReadiness is whether a customer could book at a branch. Catalog and
// scheduling know the parts; main puts them together (the business module
// can't ask them itself: they depend on it).
type BranchReadiness struct {
	OpeningHours   bool // the branch has opening hours
	OfferedService bool // an active service that at least one barber performs
	BookableBarber bool // one of those barbers has a weekly schedule here
}

// NotReadyError lists what a branch still needs before it can be published,
// in words safe to show the owner.
type NotReadyError struct {
	Missing []string
}

func (e *NotReadyError) Error() string {
	return "branch: not ready to publish: " + strings.Join(e.Missing, "; ")
}

// Unwrap lets errors.Is(err, ErrBranchNotReady) match.
func (e *NotReadyError) Unwrap() error { return ErrBranchNotReady }

// Publish shows the branch to customers: the business must be active, and
// the branch able to take a booking. Publishing again is refused.
func (b *Branch) Publish(business Status, r BranchReadiness, now time.Time) error {
	if business != StatusActive {
		return ErrBusinessNotActive
	}
	if b.status == BranchPublished {
		return ErrInvalidStateTransition
	}
	var missing []string
	if !r.OpeningHours {
		missing = append(missing, "set the branch's opening hours")
	}
	switch {
	case !r.OfferedService:
		missing = append(missing, "add a service and choose who performs it")
	case !r.BookableBarber:
		missing = append(missing, "give a barber who performs a service a weekly schedule")
	}
	if len(missing) > 0 {
		return &NotReadyError{Missing: missing}
	}
	b.status = BranchPublished
	b.touch(now)
	b.events = append(b.events, BranchPublishedEvent{Business: b.business, Branch: b.id, At: b.updatedAt})
	return nil
}

// Unpublish hides a published branch from customers. Bookings already made
// stay; nobody can make new ones.
func (b *Branch) Unpublish(now time.Time) error {
	if b.status != BranchPublished {
		return ErrInvalidStateTransition
	}
	b.status = BranchUnpublished
	b.touch(now)
	b.events = append(b.events, BranchUnpublishedEvent{Business: b.business, Branch: b.id, At: b.updatedAt})
	return nil
}

// Events returns what happened to the branch since it was loaded, for the
// repository to publish together with the change.
func (b *Branch) Events() []Event { return b.events }

// touch records a saved change: a new version and time.
func (b *Branch) touch(now time.Time) {
	b.version++
	b.updatedAt = dbTime(now)
}

// RehydrateBranch rebuilds a branch loaded from storage.
func RehydrateBranch(id shared.BranchID, business shared.BusinessID, p BranchProfile, policy BookingPolicy, status BranchStatus, version int, createdAt, updatedAt time.Time) *Branch {
	return &Branch{
		id: id, business: business, profile: p, policy: policy,
		status: status, version: version, createdAt: createdAt, updatedAt: updatedAt,
	}
}

// ID returns the branch ID.
func (b *Branch) ID() shared.BranchID { return b.id }

// BusinessID returns the business the branch belongs to.
func (b *Branch) BusinessID() shared.BusinessID { return b.business }

// Profile returns who and where the branch is.
func (b *Branch) Profile() BranchProfile { return b.profile }

// Policy returns how the branch takes bookings.
func (b *Branch) Policy() BookingPolicy { return b.policy }

// Status returns whether customers can see the branch.
func (b *Branch) Status() BranchStatus { return b.status }

// Version increases with every saved change.
func (b *Branch) Version() int { return b.version }

// CreatedAt returns when the branch was created.
func (b *Branch) CreatedAt() time.Time { return b.createdAt }

// UpdatedAt returns when the branch last changed.
func (b *Branch) UpdatedAt() time.Time { return b.updatedAt }
