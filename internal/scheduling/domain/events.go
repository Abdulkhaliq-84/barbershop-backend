package domain

import (
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Event is something that happened in scheduling that other modules may
// react to. The aggregate records events; the repository publishes them in
// the same transaction as the change (ADR-0009).
type Event interface{ isEvent() }

// OpeningHoursChangedEvent is recorded when a branch's weekly opening
// hours are set. It carries them as of Version, so another module
// (discovery) can keep its own copy.
type OpeningHoursChangedEvent struct {
	Business shared.BusinessID
	Branch   shared.BranchID
	At       time.Time
	Version  int
	Hours    WeeklyHours
}

func (OpeningHoursChangedEvent) isEvent() {}
