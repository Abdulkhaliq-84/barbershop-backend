package domain

import (
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Event is something that happened to a service that other modules may
// react to. The service records events; the repository publishes them in
// the same transaction as the change (ADR-0009).
type Event interface{ isEvent() }

// ServiceSnapshot is a service as of one version: what its events carry,
// so another module (discovery) can keep its own copy. A copy applies an
// event only if its version is newer.
type ServiceSnapshot struct {
	Version   int
	Active    bool
	Details   ServiceDetails
	Offerings []Offering
}

// PriceFrom is the least a customer pays for the service: the cheapest of
// its performers' prices. ok is false while nobody performs it.
func (s ServiceSnapshot) PriceFrom() (_ shared.Money, ok bool) {
	var least shared.Money
	for i, o := range s.Offerings {
		if p := o.PriceOr(s.Details.Price); i == 0 || p.Amount() < least.Amount() {
			least = p
		}
	}
	return least, len(s.Offerings) > 0
}

// ServiceCreatedEvent is recorded when a service is added to a branch.
type ServiceCreatedEvent struct {
	Business shared.BusinessID
	Branch   shared.BranchID
	Service  ServiceID
	At       time.Time
	Snapshot ServiceSnapshot
}

func (ServiceCreatedEvent) isEvent() {}

// ServiceUpdatedEvent is recorded when a service changes: its details,
// whether it is active, or who performs it.
type ServiceUpdatedEvent struct {
	Business shared.BusinessID
	Branch   shared.BranchID
	Service  ServiceID
	At       time.Time
	Snapshot ServiceSnapshot
}

func (ServiceUpdatedEvent) isEvent() {}
