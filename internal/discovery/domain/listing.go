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

// PageSize is how many listings one page holds.
const PageSize = 20

// Search errors.
var (
	ErrNoPlace       = errors.New("search: a city, a point to search near, or a name is required")
	ErrRadius        = errors.New("search: the radius is out of range")
	ErrQueryTooShort = errors.New("search: a name to search for needs at least two letters")
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
// names matching Text (normalised by ParseQuery; "": any name).
type Filter struct {
	City *shared.City
	Text string
}

// Found is a listing a search found. DistanceM is how far it is from the
// point searched near, in metres; Score how well its name matches the text
// searched for, from 0 to 1. Each is 0 when the search didn't ask for it.
type Found struct {
	Listing
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

// Listings stores discovery's copies.
type Listings interface {
	// Keep saves l unless the stored copy is of the same or a newer version,
	// and reports whether it saved it.
	Keep(ctx context.Context, l Listing) (bool, error)
	// InCity returns up to limit listed branches in city, by Arabic name
	// then ID, starting after the given position (nil: from the start).
	InCity(ctx context.Context, city shared.City, after *Position, limit int) ([]Listing, error)
	// Near returns up to limit listed branches within near's radius that
	// pass f, nearest first then by ID, starting after the given position
	// (nil: from the start).
	Near(ctx context.Context, near Near, f Filter, after *Position, limit int) ([]Found, error)
	// Matching returns up to limit listed branches whose names match
	// f.Text (required) and pass f, best match first then by ID, starting
	// after the given position (nil: from the start).
	Matching(ctx context.Context, f Filter, after *Position, limit int) ([]Found, error)
}
