package domain

import (
	"context"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// BranchCalendar is when a branch is open. Closures (holidays, renovation)
// will join it (the owner's exercise); a barber can't work while the branch
// is closed.
type BranchCalendar struct {
	business  shared.BusinessID
	branch    shared.BranchID
	hours     WeeklyHours
	version   int // 0 until first saved
	updatedAt time.Time
}

// NewBranchCalendar is a branch that hasn't set its hours: closed all week.
func NewBranchCalendar(business shared.BusinessID, branch shared.BranchID) *BranchCalendar {
	return &BranchCalendar{business: business, branch: branch}
}

// RehydrateBranchCalendar rebuilds a calendar loaded from storage.
func RehydrateBranchCalendar(business shared.BusinessID, branch shared.BranchID, hours WeeklyHours, version int, updatedAt time.Time) *BranchCalendar {
	return &BranchCalendar{business: business, branch: branch, hours: hours, version: version, updatedAt: updatedAt}
}

// SetOpeningHours replaces the weekly opening hours.
func (c *BranchCalendar) SetOpeningHours(hours WeeklyHours, now time.Time) {
	c.hours = hours
	c.version++
	c.updatedAt = now.UTC().Truncate(time.Microsecond)
}

// BusinessID returns the branch's business.
func (c *BranchCalendar) BusinessID() shared.BusinessID { return c.business }

// BranchID returns the branch.
func (c *BranchCalendar) BranchID() shared.BranchID { return c.branch }

// OpeningHours returns the weekly opening hours.
func (c *BranchCalendar) OpeningHours() WeeklyHours { return c.hours }

// Version is 0 until the calendar is first saved, then increases on every
// change (the If-Match check).
func (c *BranchCalendar) Version() int { return c.version }

// UpdatedAt returns when it last changed (zero if never saved).
func (c *BranchCalendar) UpdatedAt() time.Time { return c.updatedAt }

// Calendars stores branch calendars.
type Calendars interface {
	// Get returns the branch's calendar, or a new closed one (version 0).
	Get(ctx context.Context, business shared.BusinessID, branch shared.BranchID) (*BranchCalendar, error)
	// Update locks the calendar (or its absence), checks the version
	// (ErrVersionConflict; expect 0 for a calendar never saved), calls fn
	// and saves — in one transaction.
	Update(ctx context.Context, business shared.BusinessID, branch shared.BranchID, expectedVersion int, fn func(*BranchCalendar) error) error
}
