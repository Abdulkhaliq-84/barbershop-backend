package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// StaffRef names one staff member at one branch, on behalf of Actor.
type StaffRef struct {
	BranchRef
	StaffID shared.StaffID
}

// SetSchedule replaces a staff member's schedule at a branch.
type SetSchedule struct {
	StaffRef
	ExpectedVersion int // 0 the first time
	Weekly          []domain.WeeklyInterval
	Overrides       map[domain.Date][]domain.DayInterval
}

// AddTimeOff records time off for a staff member.
type AddTimeOff struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	StaffID    shared.StaffID
	From, To   time.Time
	Reason     string
}

// TimeOffRef names one stretch of time off, on behalf of Actor.
type TimeOffRef struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	StaffID    shared.StaffID
	TimeOffID  domain.TimeOffID
}

// ScheduleHandlers are the barber-schedule and time-off use cases. Who may
// do what:
//
//	read a schedule        anyone working at the branch
//	set a schedule         the person themselves, or the owner / a manager of the branch
//	time off (all of it)   the person themselves, the owner, or a manager of one of their branches
type ScheduleHandlers struct {
	schedules domain.Schedules
	timeOff   domain.TimeOffs
	access    Access
	clock     clock.Clock
}

// NewScheduleHandlers wires the use cases.
func NewScheduleHandlers(schedules domain.Schedules, timeOff domain.TimeOffs, access Access, clk clock.Clock) *ScheduleHandlers {
	return &ScheduleHandlers{schedules: schedules, timeOff: timeOff, access: access, clock: clk}
}

// Schedule returns a staff member's schedule at a branch (empty, version 0,
// if never set).
func (h *ScheduleHandlers) Schedule(ctx context.Context, ref StaffRef) (*domain.BarberSchedule, error) {
	if err := h.canRead(ctx, ref); err != nil {
		return nil, err
	}
	s, err := h.schedules.Get(ctx, ref.BusinessID, ref.BranchID, ref.StaffID)
	if err != nil {
		return nil, fmt.Errorf("schedule: %w", err)
	}
	return s, nil
}

// SetSchedule replaces the schedule under the version check. The weekly
// template may not overlap the person's template at another branch.
func (h *ScheduleHandlers) SetSchedule(ctx context.Context, cmd SetSchedule) (*domain.BarberSchedule, error) {
	if err := h.canWrite(ctx, cmd.StaffRef); err != nil {
		return nil, err
	}
	weekly, err := domain.NewWeeklyHours(cmd.Weekly)
	if err != nil {
		return nil, err
	}
	var saved *domain.BarberSchedule
	err = h.schedules.Update(ctx, cmd.BusinessID, cmd.BranchID, cmd.StaffID, cmd.ExpectedVersion, func(s *domain.BarberSchedule, elsewhere []domain.WeeklyHours) error {
		if err := s.Set(weekly, cmd.Overrides, elsewhere, h.clock.Now()); err != nil {
			return err
		}
		saved = s
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("set schedule: %w", err)
	}
	return saved, nil
}

// TimeOff lists the person's time off that hasn't ended yet.
func (h *ScheduleHandlers) TimeOff(ctx context.Context, actor shared.UserID, business shared.BusinessID, staff shared.StaffID) ([]*domain.TimeOff, error) {
	if err := h.canManageTimeOff(ctx, actor, business, staff); err != nil {
		return nil, err
	}
	list, err := h.timeOff.List(ctx, business, staff, h.clock.Now())
	if err != nil {
		return nil, fmt.Errorf("time off: %w", err)
	}
	return list, nil
}

// AddTimeOff records time off; it may not overlap other time off.
func (h *ScheduleHandlers) AddTimeOff(ctx context.Context, cmd AddTimeOff) (*domain.TimeOff, error) {
	if err := h.canManageTimeOff(ctx, cmd.Actor, cmd.BusinessID, cmd.StaffID); err != nil {
		return nil, err
	}
	t, err := domain.NewTimeOff(shared.NewID[domain.TimeOffTag](), cmd.BusinessID, cmd.StaffID, cmd.From, cmd.To, cmd.Reason, h.clock.Now())
	if err != nil {
		return nil, err
	}
	if err := h.timeOff.Add(ctx, t); err != nil {
		return nil, fmt.Errorf("add time off: %w", err)
	}
	return t, nil
}

// DeleteTimeOff removes a stretch of time off.
func (h *ScheduleHandlers) DeleteTimeOff(ctx context.Context, ref TimeOffRef) error {
	if err := h.canManageTimeOff(ctx, ref.Actor, ref.BusinessID, ref.StaffID); err != nil {
		return err
	}
	if err := h.timeOff.Delete(ctx, ref.BusinessID, ref.StaffID, ref.TimeOffID); err != nil {
		return fmt.Errorf("delete time off: %w", err)
	}
	return nil
}

// canRead: the caller works at the branch, and so does the person.
func (h *ScheduleHandlers) canRead(ctx context.Context, ref StaffRef) error {
	if err := h.access.Branch(ctx, ref.Actor, ref.BusinessID, ref.BranchID, RoleBarber); err != nil {
		return err
	}
	target, err := h.access.StaffMember(ctx, ref.BusinessID, ref.StaffID)
	if err != nil {
		return err
	}
	if !target.WorksAt(ref.BranchID) {
		return domain.ErrNotFound // no schedule for someone who doesn't work here
	}
	return nil
}

// canWrite: canRead, and the caller is the person or manages the branch.
func (h *ScheduleHandlers) canWrite(ctx context.Context, ref StaffRef) error {
	if err := h.canRead(ctx, ref); err != nil {
		return err
	}
	me, err := h.access.MemberOf(ctx, ref.Actor, ref.BusinessID)
	if err != nil {
		return err
	}
	if me.ID == ref.StaffID {
		return nil
	}
	return h.access.Branch(ctx, ref.Actor, ref.BusinessID, ref.BranchID, RoleManager)
}

// canManageTimeOff: the person themselves, the owner, or a manager who
// shares a branch with them.
func (h *ScheduleHandlers) canManageTimeOff(ctx context.Context, actor shared.UserID, business shared.BusinessID, staff shared.StaffID) error {
	me, err := h.access.MemberOf(ctx, actor, business)
	if err != nil {
		return err
	}
	target, err := h.access.StaffMember(ctx, business, staff)
	if err != nil {
		return err
	}
	switch {
	case me.ID == target.ID, me.Owner:
		return nil
	case me.Manager && me.sharesBranchWith(target):
		return nil
	}
	return domain.ErrForbidden
}
