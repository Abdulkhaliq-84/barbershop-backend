// Package app holds scheduling's use cases. Each starts by asking business
// whether the caller may work on the branch (Access), then loads, changes
// and saves through the domain's repository ports.
package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Role is how much a caller must be allowed to do at a branch.
type Role string

// Roles, as business defines them.
const (
	RoleManager Role = "manager" // the owner, or a manager of the branch
	RoleBarber  Role = "barber"  // anyone working at the branch
)

// Access asks business whether actor may work on branch (through
// adapters/acl): domain.ErrNotFound for strangers and for a branch that isn't
// the business's, domain.ErrForbidden for staff without the role there.
type Access interface {
	Branch(ctx context.Context, actor shared.UserID, business shared.BusinessID, branch shared.BranchID, need Role) error
	// MemberOf returns actor's own membership of business; ErrNotFound if
	// they aren't active staff there.
	MemberOf(ctx context.Context, actor shared.UserID, business shared.BusinessID) (Member, error)
	// StaffMember returns an active staff member of business, or ErrNotFound.
	StaffMember(ctx context.Context, business shared.BusinessID, staff shared.StaffID) (Member, error)
}

// Member is a staff member, as business describes them.
type Member struct {
	ID       shared.StaffID
	Owner    bool
	Manager  bool
	Branches []shared.BranchID // empty for the owner, who works at all
}

// WorksAt reports whether the member works at branch.
func (m Member) WorksAt(branch shared.BranchID) bool {
	return m.Owner || slices.Contains(m.Branches, branch)
}

// sharesBranchWith reports whether m works at any branch o works at.
func (m Member) sharesBranchWith(o Member) bool {
	return slices.ContainsFunc(o.Branches, m.WorksAt)
}

// BranchRef names a branch, on behalf of Actor.
type BranchRef struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
}

// SetOpeningHours replaces a branch's weekly opening hours.
type SetOpeningHours struct {
	BranchRef
	ExpectedVersion int // 0 the first time
	Intervals       []domain.WeeklyInterval
}

// CalendarHandlers are the branch-calendar use cases. Who may do what:
//
//	read hours    anyone working at the branch (and the owner)
//	set hours     the owner, or a manager of the branch
type CalendarHandlers struct {
	calendars domain.Calendars
	access    Access
	clock     clock.Clock
}

// NewCalendarHandlers wires the use cases.
func NewCalendarHandlers(calendars domain.Calendars, access Access, clk clock.Clock) *CalendarHandlers {
	return &CalendarHandlers{calendars: calendars, access: access, clock: clk}
}

// OpeningHours returns the branch's calendar (closed, version 0, if its
// hours were never set).
func (h *CalendarHandlers) OpeningHours(ctx context.Context, ref BranchRef) (*domain.BranchCalendar, error) {
	if err := h.access.Branch(ctx, ref.Actor, ref.BusinessID, ref.BranchID, RoleBarber); err != nil {
		return nil, err
	}
	cal, err := h.calendars.Get(ctx, ref.BusinessID, ref.BranchID)
	if err != nil {
		return nil, fmt.Errorf("opening hours: %w", err)
	}
	return cal, nil
}

// SetOpeningHours replaces the weekly hours under the version check.
func (h *CalendarHandlers) SetOpeningHours(ctx context.Context, cmd SetOpeningHours) (*domain.BranchCalendar, error) {
	if err := h.access.Branch(ctx, cmd.Actor, cmd.BusinessID, cmd.BranchID, RoleManager); err != nil {
		return nil, err
	}
	hours, err := domain.NewWeeklyHours(cmd.Intervals)
	if err != nil {
		return nil, err
	}
	var saved *domain.BranchCalendar
	err = h.calendars.Update(ctx, cmd.BusinessID, cmd.BranchID, cmd.ExpectedVersion, func(c *domain.BranchCalendar) error {
		c.SetOpeningHours(hours, h.clock.Now())
		saved = c
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("set opening hours: %w", err)
	}
	return saved, nil
}
