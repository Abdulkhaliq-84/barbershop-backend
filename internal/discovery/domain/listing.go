// Package domain is discovery's model: the branch listings customers browse
// (docs/architecture/domain-model.md §3.6, ADR-0027).
//
// A listing is a copy. business decides everything about a branch and
// publishes events; discovery keeps the newest copy of each branch and
// answers questions about them. So Listing is plain data, not an aggregate
// with rules to protect: its one rule, "keep the newest version", lives in
// the repository's upsert, where two events at once can't break it.
package domain

import (
	"context"
	"errors"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Listing is discovery's copy of a branch, as of Version.
type Listing struct {
	Branch    shared.BranchID
	Business  shared.BusinessID
	Version   int  // the branch's version this copy is of
	Listed    bool // published: customers can find it
	Name      shared.LocalizedText
	City      shared.City
	District  string
	Address   string
	Location  shared.GeoPoint
	Phone     string // E.164; "" if none
	Timezone  string // IANA name
	UpdatedAt time.Time
}

// Service is discovery's copy of one of a branch's services, as of
// Version: only what searching needs. catalog decides everything about it.
type Service struct {
	Service   shared.ServiceID
	Branch    shared.BranchID
	Business  shared.BusinessID
	Version   int  // the service's version this copy is of
	Offered   bool // active, and someone performs it: customers can choose it
	Category  shared.Category
	PriceFrom shared.Money // the least a customer pays for it, in SAR
	UpdatedAt time.Time
}

// OpeningHours is discovery's copy of a branch's weekly opening hours, as
// of Version (the calendar's, not the branch's). Each of Open is [start,
// end) in minutes after Sunday 00:00 in the branch's own time zone; one
// past Saturday midnight ends after MinutesPerWeek. None: closed all week.
type OpeningHours struct {
	Branch    shared.BranchID
	Business  shared.BusinessID
	Version   int
	Open      [][2]int
	UpdatedAt time.Time
}

// MinutesPerWeek is how many minutes a week has.
const MinutesPerWeek = 7 * 24 * 60

// PageSize is how many listings one page holds.
const PageSize = 20

// Search errors.
var (
	ErrNoPlace       = errors.New("search: a city, a point to search near, or a name is required")
	ErrRadius        = errors.New("search: the radius is out of range")
	ErrQueryTooShort = errors.New("search: a name to search for needs at least two letters")
	ErrNotSAR        = errors.New("discovery: prices are kept in SAR")
	ErrBadHours      = errors.New("discovery: opening hours must be within two weeks of minutes, each ending after it starts")
)

// Search radius limits, in kilometres.
const (
	DefaultRadiusKm = 10
	MaxRadiusKm     = 50
)

// Near is a search around a point: listed branches within RadiusM metres,
// nearest first.
type Near struct {
	Point   shared.GeoPoint
	RadiusM float64
}

// Filter narrows a search: only City's branches (nil: any city), only
// names matching Text (normalised by ParseQuery; "": any name), only
// branches offering a service in Category (nil: any service), only those
// open at At (OpenNow). At is required: every branch found says whether
// it is open then.
type Filter struct {
	City     *shared.City
	Text     string
	Category *shared.Category
	OpenNow  bool
	At       time.Time
}

// Found is a listing a search found. PriceFrom is the least a customer
// pays for a service there (one of the category's, if the search gave
// one); OpenNow whether it is open at the filter's At. DistanceM is how far
// it is from the point searched near, in metres; Score how well its name
// matches the text searched for, from 0 to 1. Each is 0 when the search
// didn't ask for it.
type Found struct {
	Listing
	PriceFrom shared.Money
	OpenNow   bool
	DistanceM float64
	Score     float64
}

// Position is where a page ended, so the next one starts after it: the
// last listing's branch ID and its sort key: NameAr when browsing a city
// by name, DistanceM when searching near a point, Score when searching by
// name.
type Position struct {
	NameAr    string
	DistanceM float64
	Score     float64
	Branch    shared.BranchID
}

// Listings stores discovery's copies of branches and their services.
// Searches find only listed branches that offer a service (in the filter's
// category, if it has one): a branch with nothing a customer can choose
// isn't shown.
type Listings interface {
	// Keep saves l unless the stored copy is of the same or a newer version,
	// and reports whether it saved it.
	Keep(ctx context.Context, l Listing) (bool, error)
	// KeepService saves s the same way.
	KeepService(ctx context.Context, s Service) (bool, error)
	// KeepHours saves h the same way.
	KeepHours(ctx context.Context, h OpeningHours) (bool, error)
	// InCity returns up to limit listed branches in f.City (required) that
	// pass f, by Arabic name then ID, starting after the given position
	// (nil: from the start). f.Text is not used: a name search is Matching.
	InCity(ctx context.Context, f Filter, after *Position, limit int) ([]Found, error)
	// Near returns up to limit listed branches within near's radius that
	// pass f, nearest first then by ID, starting after the given position
	// (nil: from the start).
	Near(ctx context.Context, near Near, f Filter, after *Position, limit int) ([]Found, error)
	// Matching returns up to limit listed branches whose names match
	// f.Text (required) and pass f, best match first then by ID, starting
	// after the given position (nil: from the start).
	Matching(ctx context.Context, f Filter, after *Position, limit int) ([]Found, error)
}
