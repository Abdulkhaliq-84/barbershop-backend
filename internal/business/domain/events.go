package domain

import (
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Event is something that happened to an aggregate that other modules may
// react to. Aggregates record events; the repository publishes them in the
// same transaction as the change (ADR-0009).
type Event interface{ isEvent() }

// BusinessApproved is recorded when a platform admin approves a business.
type BusinessApproved struct {
	Business shared.BusinessID
	Owner    shared.UserID
	At       time.Time
}

func (BusinessApproved) isEvent() {}

// BranchPublishedEvent is recorded when the owner shows a branch to customers.
type BranchPublishedEvent struct {
	Business shared.BusinessID
	Branch   shared.BranchID
	At       time.Time
}

func (BranchPublishedEvent) isEvent() {}

// BranchUnpublishedEvent is recorded when the owner hides a branch again.
type BranchUnpublishedEvent struct {
	Business shared.BusinessID
	Branch   shared.BranchID
	At       time.Time
}

func (BranchUnpublishedEvent) isEvent() {}
