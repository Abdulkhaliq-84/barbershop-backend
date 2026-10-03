// Package events is the scheduling module's event contract: the JSON other
// modules receive through the outbox (ADR-0009). Renaming or removing a
// field is a breaking change for every subscriber; add fields instead.
package events

import (
	"time"

	"github.com/google/uuid"
)

// TypeOpeningHoursChanged is published when an owner or manager sets a
// branch's weekly opening hours.
const TypeOpeningHoursChanged = "scheduling.opening_hours_changed"

// OpeningHoursChanged is the payload of scheduling.opening_hours_changed.
type OpeningHoursChanged struct {
	BusinessID uuid.UUID `json:"business_id"`
	BranchID   uuid.UUID `json:"branch_id"`
	ChangedAt  time.Time `json:"changed_at"`
	Calendar   Calendar  `json:"calendar"`
}

// Calendar is a branch's opening hours as of Version, so a subscriber can
// keep its own copy. Events can arrive more than once and out of order:
// apply one only if its Version is newer than the copy's.
type Calendar struct {
	Version int        `json:"version"`
	Hours   []Interval `json:"hours"` // by weekday, then start; none: closed all week
}

// Interval is a stretch of a week in the branch's own time zone (the
// branch's timezone, which business's branch events carry).
type Interval struct {
	Weekday     int `json:"weekday"`      // 0 Sunday … 6 Saturday
	StartMinute int `json:"start_minute"` // after local midnight, 0–1435
	Minutes     int `json:"minutes"`      // 5–1440; may run past midnight into the next day
}
