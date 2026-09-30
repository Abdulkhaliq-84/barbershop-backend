package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MaxWindowsRange bounds one working-windows query (booking asks for a day
// at a time; the horizon is at most 180 days, asked for in pieces).
const MaxWindowsRange = 62 * 24 * time.Hour

// WindowsQuery asks for one staff member's working windows, on behalf of
// Actor (the staff dashboard).
type WindowsQuery struct {
	StaffRef
	From, To time.Time
}

// WindowsHandlers compute working windows: when each staff member can work
// at a branch — the question booking (M5) asks before offering slots.
type WindowsHandlers struct {
	calendars domain.Calendars
	schedules domain.Schedules
	timeOff   domain.TimeOffs
	access    Access
	staff     *ScheduleHandlers // for the read rule (canRead)
}

// NewWindowsHandlers wires the use cases.
func NewWindowsHandlers(calendars domain.Calendars, schedules domain.Schedules, timeOff domain.TimeOffs, access Access, staff *ScheduleHandlers) *WindowsHandlers {
	return &WindowsHandlers{calendars: calendars, schedules: schedules, timeOff: timeOff, access: access, staff: staff}
}

// ForStaff returns one person's windows, and the branch's time zone, to
// anyone working at the branch.
func (h *WindowsHandlers) ForStaff(ctx context.Context, q WindowsQuery) ([]shared.Interval, *time.Location, error) {
	if err := h.staff.canRead(ctx, q.StaffRef); err != nil {
		return nil, nil, err
	}
	all, loc, err := h.compute(ctx, q.BusinessID, q.BranchID, []shared.StaffID{q.StaffID}, q.From, q.To)
	if err != nil {
		return nil, nil, err
	}
	return all[q.StaffID], loc, nil
}

// Windows computes working windows within [from, to) for each staff member
// at the branch. It has no caller to authorize: other modules (booking) use
// it through scheduling's root package, after their own checks. Every staff
// member must work at the branch (ErrNotFound otherwise).
func (h *WindowsHandlers) Windows(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, error) {
	all, _, err := h.compute(ctx, business, branch, staff, from, to)
	return all, err
}

func (h *WindowsHandlers) compute(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, *time.Location, error) {
	if !to.After(from) || to.Sub(from) > MaxWindowsRange {
		return nil, nil, domain.ErrInvalidRange
	}
	if err := h.access.StaffAtBranch(ctx, business, branch, staff); err != nil {
		return nil, nil, err
	}
	loc, err := h.access.BranchLocation(ctx, business, branch)
	if err != nil {
		return nil, nil, err
	}
	cal, err := h.calendars.Get(ctx, business, branch)
	if err != nil {
		return nil, nil, fmt.Errorf("working windows: %w", err)
	}
	out := make(map[shared.StaffID][]shared.Interval, len(staff))
	for _, s := range staff {
		schedule, err := h.schedules.Get(ctx, business, branch, s)
		if err != nil {
			return nil, nil, fmt.Errorf("working windows: %w", err)
		}
		offs, err := h.timeOff.List(ctx, business, s, from)
		if err != nil {
			return nil, nil, fmt.Errorf("working windows: %w", err)
		}
		spans := make([]shared.Interval, 0, len(offs))
		for _, o := range offs {
			spans = append(spans, o.Span())
		}
		out[s] = domain.WorkingWindows(domain.WindowsInput{
			Location: loc, Opening: cal.OpeningHours(), Schedule: schedule, TimeOff: spans,
			// Closed: the branch's closures, once they exist (the owner's exercise).
		}, from, to)
	}
	return out, loc, nil
}
