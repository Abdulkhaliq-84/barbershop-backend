// Package events is the catalog module's event contract: the JSON other
// modules receive through the outbox (ADR-0009). Renaming or removing a
// field is a breaking change for every subscriber; add fields instead.
package events

import (
	"time"

	"github.com/google/uuid"
)

// Service is a service as of Version: what customers may see of it. Every
// service event carries it, so a subscriber can keep its own copy. Events
// can arrive more than once and out of order: apply one only if its
// Version is newer than the copy's.
type Service struct {
	Version         int           `json:"version"`
	Active          bool          `json:"active"`        // customers may choose it
	CategoryCode    string        `json:"category_code"` // a shared.Categories code
	Name            LocalizedText `json:"name"`
	DurationMinutes int           `json:"duration_minutes"` // the service's own; a performer's may differ
	Price           Money         `json:"price"`            // the service's own; VAT-inclusive
	// PriceFrom is the least a customer pays for it: the cheapest of its
	// performers' prices. Absent while nobody performs it.
	PriceFrom *Money `json:"price_from,omitempty"`
}

// LocalizedText is text in Arabic and, optionally, English.
type LocalizedText struct {
	Ar string `json:"ar"`
	En string `json:"en,omitempty"`
}

// Money is an amount in the currency's minor unit (halalas for SAR).
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// TypeServiceCreated is published when a manager adds a service to a
// branch.
const TypeServiceCreated = "catalog.service_created"

// TypeServiceUpdated is published when a service changes: its details,
// whether it is active, or who performs it.
const TypeServiceUpdated = "catalog.service_updated"

// ServiceChanged is the payload of both service events.
type ServiceChanged struct {
	BusinessID uuid.UUID `json:"business_id"`
	BranchID   uuid.UUID `json:"branch_id"`
	ServiceID  uuid.UUID `json:"service_id"`
	ChangedAt  time.Time `json:"changed_at"`
	Service    Service   `json:"service"`
}
