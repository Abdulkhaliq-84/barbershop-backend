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

// Position is where a page of a city's listings ended: the last one's
// Arabic name and branch ID. The next page starts after it.
type Position struct {
	NameAr string
	Branch shared.BranchID
}

// Listings stores discovery's copies.
type Listings interface {
	// Keep saves l unless the stored copy is of the same or a newer version,
	// and reports whether it saved it.
	Keep(ctx context.Context, l Listing) (bool, error)
	// InCity returns up to limit listed branches in city, by Arabic name
	// then ID, starting after the given position (nil: from the start).
	InCity(ctx context.Context, city shared.City, after *Position, limit int) ([]Listing, error)
}
