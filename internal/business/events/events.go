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
