// Package events is the business module's event contract: the JSON other
// modules receive through the outbox (ADR-0009). Renaming or removing a
// field is a breaking change for every subscriber; add fields instead.
package events

import (
	"time"

	"github.com/google/uuid"
)

// TypeBusinessApproved is published when a platform admin approves a business.
const TypeBusinessApproved = "business.approved"

// BusinessApproved is the payload of business.approved.
type BusinessApproved struct {
	BusinessID uuid.UUID `json:"business_id"`
	OwnerID    uuid.UUID `json:"owner_id"`
	ApprovedAt time.Time `json:"approved_at"`
}

// Branch is a branch as of Version: what customers may see of it, and
// whether they may (Status). Every branch event carries it, so a subscriber
// can keep its own copy. Events can arrive more than once and out of order:
// apply one only if its Version is newer than the copy's.
type Branch struct {
	Version  int           `json:"version"`
	Status   string        `json:"status"` // draft, published or unpublished
	Name     LocalizedText `json:"name"`
	CityCode string        `json:"city_code"`
	District string        `json:"district"`
	Address  string        `json:"address"`
	Location Location      `json:"location"`
	Phone    string        `json:"phone,omitempty"` // E.164; the shop's public number
	Timezone string        `json:"timezone"`        // IANA, e.g. Asia/Riyadh
}

// LocalizedText is text in Arabic and, optionally, English.
type LocalizedText struct {
	Ar string `json:"ar"`
	En string `json:"en,omitempty"`
}

// Location is a WGS 84 point.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// TypeBranchPublished is published when the owner shows a branch to
// customers (discovery lists it from then on).
const TypeBranchPublished = "business.branch_published"

// BranchPublished is the payload of business.branch_published.
type BranchPublished struct {
	BusinessID  uuid.UUID `json:"business_id"`
	BranchID    uuid.UUID `json:"branch_id"`
	PublishedAt time.Time `json:"published_at"`
	Branch      Branch    `json:"branch"` // since M6.1
}

// TypeBranchUnpublished is published when the owner hides a branch again.
const TypeBranchUnpublished = "business.branch_unpublished"

// BranchUnpublished is the payload of business.branch_unpublished.
type BranchUnpublished struct {
	BusinessID    uuid.UUID `json:"business_id"`
	BranchID      uuid.UUID `json:"branch_id"`
	UnpublishedAt time.Time `json:"unpublished_at"`
	Branch        Branch    `json:"branch"` // since M6.1
}

// TypeBranchUpdated is published when the owner edits a branch, published
// or not.
const TypeBranchUpdated = "business.branch_updated"

// BranchUpdated is the payload of business.branch_updated.
type BranchUpdated struct {
	BusinessID uuid.UUID `json:"business_id"`
	BranchID   uuid.UUID `json:"branch_id"`
	UpdatedAt  time.Time `json:"updated_at"`
	Branch     Branch    `json:"branch"`
}
