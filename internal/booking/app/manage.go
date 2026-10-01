package app

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Role is a staff member's role, as booking needs it.
type Role string

// Roles.
const (
	RoleOwner   Role = "owner"
	RoleManager Role = "manager"
	RoleBarber  Role = "barber"
)

// Member is the caller as staff of a business.
type Member struct {
	Staff    shared.StaffID
	Role     Role
	Branches []shared.BranchID // where a manager or barber works; the owner works at all
}

func (m Member) worksAt(branch shared.BranchID) bool {
	return m.Role == RoleOwner || slices.Contains(m.Branches, branch)
}

// mayActOn: staff act on appointments at the branches they work at, and a
// barber only on their own.
func (m Member) mayActOn(a *domain.Appointment) error {
	if !m.worksAt(a.Branch()) || (m.Role == RoleBarber && a.Barber() != m.Staff) {
		return domain.ErrForbidden
	}
	return nil
}

// Staff says who the caller is at a business (business).
type Staff interface {
	// MemberOf returns actor's active membership of business, or
	// domain.ErrNotFound.
	MemberOf(ctx context.Context, actor shared.UserID, business shared.BusinessID) (Member, error)
	// BranchLocation returns the branch's time zone, or domain.ErrNotFound
	// if it isn't the business's.
	BranchLocation(ctx context.Context, business shared.BusinessID, branch shared.BranchID) (*time.Location, error)
}

// Changes changes appointments and lists a branch's (booking's own
// repository).
type Changes interface {
	// ChangeMine locks one of the customer's appointments, applies change
	// and saves it with its events; domain.ErrNotFound for someone else's.
	ChangeMine(ctx context.Context, customer shared.UserID, id domain.AppointmentID, change func(*domain.Appointment) error) (*domain.Appointment, error)
	// ChangeAtBusiness is ChangeMine for the shop: found by business.
	ChangeAtBusiness(ctx context.Context, business shared.BusinessID, id domain.AppointmentID, change func(*domain.Appointment) error) (*domain.Appointment, error)
	// Day returns the branch's appointments starting in [from, to), by
	// start; only barber's if barber isn't nil.
	Day(ctx context.Context, business shared.BusinessID, branch shared.BranchID, from, to time.Time, barber *shared.StaffID) ([]*domain.Appointment, error)
}

// ManageHandlers are what happens to appointments after booking: the
// customer cancels, the shop confirms, rejects, cancels, completes or marks
// a no-show (ADR-0025), and the shop sees its day.
type ManageHandlers struct {
	staff    Staff
	branches Branches
	changes  Changes
	clock    clock.Clock
}

// NewManageHandlers wires the use cases.
func NewManageHandlers(staff Staff, branches Branches, changes Changes, clk clock.Clock) *ManageHandlers {
	return &ManageHandlers{staff: staff, branches: branches, changes: changes, clock: clk}
}

// CancelMine cancels one of the customer's own appointments, within the
// deadline it was booked with.
func (h *ManageHandlers) CancelMine(ctx context.Context, customer shared.UserID, id domain.AppointmentID, reason string) (AppointmentView, error) {
	a, err := h.changes.ChangeMine(ctx, customer, id, func(a *domain.Appointment) error {
		return a.CancelByCustomer(reason, h.clock.Now()) // now, once the appointment is locked
	})
	if err != nil {
		return AppointmentView{}, err
	}
	return view(ctx, h.branches, a)
}

// StaffAction is something the shop does to an appointment.
type StaffAction struct {
	Actor       shared.UserID
	Business    shared.BusinessID
	Appointment domain.AppointmentID
	Action      domain.Action
	Reason      string // cancel only
}

// Act applies a staff action. Membership comes first: a stranger to the
// business gets domain.ErrNotFound, whatever the appointment ID. Then the
// appointment is locked and the member checked against it: a barber acts
// only on their own appointments, anyone only at the branches they work at
// (domain.ErrForbidden).
func (h *ManageHandlers) Act(ctx context.Context, cmd StaffAction) (AppointmentView, error) {
	member, err := h.staff.MemberOf(ctx, cmd.Actor, cmd.Business)
	if err != nil {
		return AppointmentView{}, err
	}
	a, err := h.changes.ChangeAtBusiness(ctx, cmd.Business, cmd.Appointment, func(a *domain.Appointment) error {
		if err := member.mayActOn(a); err != nil {
			return err
		}
		now := h.clock.Now()
		switch cmd.Action {
		case domain.ActionConfirm:
			return a.Confirm(now)
		case domain.ActionReject:
			return a.Reject(now)
		case domain.ActionCancel:
			return a.CancelByStaff(cmd.Reason, now)
		case domain.ActionComplete:
			return a.Complete(now)
		case domain.ActionNoShow:
			return a.MarkNoShow(now)
		}
		return fmt.Errorf("booking: unknown action %q", cmd.Action)
	})
	if err != nil {
		return AppointmentView{}, err
	}
	return view(ctx, h.branches, a)
}

// DayQuery asks for a branch's appointments on one of its days.
type DayQuery struct {
	Actor    shared.UserID
	Business shared.BusinessID
	Branch   shared.BranchID
	Day      domain.Day // in the branch's time zone
}

// BranchDay is a branch's day as the shop sees it.
type BranchDay struct {
	Location     *time.Location
	Appointments []AppointmentView // every status, by start
}

// Day returns the appointments starting on q.Day: a barber's own, or all
// of them for the owner and the branch's managers. Checked like
// business's branch access: a stranger to the business, or a branch that
// isn't the business's, is domain.ErrNotFound; staff of another branch
// domain.ErrForbidden.
func (h *ManageHandlers) Day(ctx context.Context, q DayQuery) (BranchDay, error) {
	member, err := h.staff.MemberOf(ctx, q.Actor, q.Business)
	if err != nil {
		return BranchDay{}, err
	}
	loc, err := h.staff.BranchLocation(ctx, q.Business, q.Branch)
	if err != nil {
		return BranchDay{}, err
	}
	if !member.worksAt(q.Branch) {
		return BranchDay{}, domain.ErrForbidden
	}
	var barber *shared.StaffID
	if member.Role == RoleBarber {
		barber = &member.Staff
	}
	list, err := h.changes.Day(ctx, q.Business, q.Branch, q.Day.Start(loc), q.Day.AddDays(1).Start(loc), barber)
	if err != nil {
		return BranchDay{}, fmt.Errorf("branch day: %w", err)
	}
	all, err := views(ctx, h.branches, q.Business, q.Branch, list)
	if err != nil {
		return BranchDay{}, err
	}
	return BranchDay{Location: loc, Appointments: all}, nil
}
