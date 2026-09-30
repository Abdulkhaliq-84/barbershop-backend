package httpapi

import (
	"context"
	"sort"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// GetBarberSchedule handles GET …/branches/{branch_id}/staff/{staff_id}/schedule.
func (h *Handlers) GetBarberSchedule(ctx context.Context, req apigen.GetBarberScheduleRequestObject) (apigen.GetBarberScheduleResponseObject, error) {
	fail := func(err error) (apigen.GetBarberScheduleResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.GetBarberScheduledefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(errNoPrincipal)
	}
	s, err := h.schedules.Schedule(ctx, staffRef(p.UserID, req.BusinessId, req.BranchId, req.StaffId))
	if err != nil {
		return fail(err)
	}
	return apigen.GetBarberSchedule200JSONResponse(toAPISchedule(s)), nil
}

// SetBarberSchedule handles PUT …/branches/{branch_id}/staff/{staff_id}/schedule.
func (h *Handlers) SetBarberSchedule(ctx context.Context, req apigen.SetBarberScheduleRequestObject) (apigen.SetBarberScheduleResponseObject, error) {
	fail := func(err error) (apigen.SetBarberScheduleResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.SetBarberScheduledefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(errNoPrincipal)
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return fail(err)
	}
	weekly, err := fromAPIDays(req.Body.Weekly)
	if err != nil {
		return fail(err)
	}
	overrides := map[domain.Date][]domain.DayInterval{}
	if req.Body.Overrides != nil {
		for _, o := range *req.Body.Overrides {
			date := domain.DateOf(o.Date.Time)
			intervals := []domain.DayInterval{}
			for _, r := range o.Intervals {
				opens, err := parseClock(r.Opens)
				if err != nil {
					return fail(err)
				}
				closes, err := parseClock(r.Closes)
				if err != nil {
					return fail(err)
				}
				i := domain.IntervalFromClock(time.Sunday, opens, closes)
				intervals = append(intervals, domain.DayInterval{Start: i.Start, Minutes: i.Minutes})
			}
			if _, dup := overrides[date]; dup {
				return fail(errDuplicateDate)
			}
			overrides[date] = intervals
		}
	}
	s, err := h.schedules.SetSchedule(ctx, app.SetSchedule{
		StaffRef: staffRef(p.UserID, req.BusinessId, req.BranchId, req.StaffId), ExpectedVersion: version,
		Weekly: weekly, Overrides: overrides,
	})
	if err != nil {
		return fail(err)
	}
	return apigen.SetBarberSchedule200JSONResponse(toAPISchedule(s)), nil
}

// ListTimeOff handles GET /v1/businesses/{business_id}/staff/{staff_id}/time-off.
func (h *Handlers) ListTimeOff(ctx context.Context, req apigen.ListTimeOffRequestObject) (apigen.ListTimeOffResponseObject, error) {
	fail := func(err error) (apigen.ListTimeOffResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.ListTimeOffdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(errNoPrincipal)
	}
	list, err := h.schedules.TimeOff(ctx, p.UserID, shared.IDFromUUID[shared.BusinessTag](req.BusinessId), shared.IDFromUUID[shared.StaffTag](req.StaffId))
	if err != nil {
		return fail(err)
	}
	out := apigen.ListTimeOff200JSONResponse{Data: make([]apigen.TimeOff, 0, len(list))}
	for _, t := range list {
		out.Data = append(out.Data, toAPITimeOff(t))
	}
	return out, nil
}

// AddTimeOff handles POST /v1/businesses/{business_id}/staff/{staff_id}/time-off.
func (h *Handlers) AddTimeOff(ctx context.Context, req apigen.AddTimeOffRequestObject) (apigen.AddTimeOffResponseObject, error) {
	fail := func(err error) (apigen.AddTimeOffResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.AddTimeOffdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(errNoPrincipal)
	}
	cmd := app.AddTimeOff{
		Actor: p.UserID, BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId), StaffID: shared.IDFromUUID[shared.StaffTag](req.StaffId),
		From: req.Body.StartsAt, To: req.Body.EndsAt,
	}
	if req.Body.Reason != nil {
		cmd.Reason = *req.Body.Reason
	}
	t, err := h.schedules.AddTimeOff(ctx, cmd)
	if err != nil {
		return fail(err)
	}
	return apigen.AddTimeOff201JSONResponse(toAPITimeOff(t)), nil
}

// DeleteTimeOff handles DELETE …/staff/{staff_id}/time-off/{time_off_id}.
func (h *Handlers) DeleteTimeOff(ctx context.Context, req apigen.DeleteTimeOffRequestObject) (apigen.DeleteTimeOffResponseObject, error) {
	fail := func(err error) (apigen.DeleteTimeOffResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.DeleteTimeOffdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(errNoPrincipal)
	}
	err := h.schedules.DeleteTimeOff(ctx, app.TimeOffRef{
		Actor: p.UserID, BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		StaffID: shared.IDFromUUID[shared.StaffTag](req.StaffId), TimeOffID: shared.IDFromUUID[domain.TimeOffTag](req.TimeOffId),
	})
	if err != nil {
		return fail(err)
	}
	return apigen.DeleteTimeOff204Response{}, nil
}

func toAPISchedule(s *domain.BarberSchedule) apigen.BarberSchedule {
	out := apigen.BarberSchedule{
		StaffId: s.StaffID().UUID(), BranchId: s.BranchID().UUID(),
		Weekly: toAPIWeek(s.Weekly()), Overrides: []apigen.DateHours{}, Version: s.Version(),
	}
	overrides := s.Overrides()
	dates := make([]domain.Date, 0, len(overrides))
	for d := range overrides {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Compare(dates[j]) < 0 })
	for _, d := range dates {
		ranges := []apigen.TimeRange{}
		for _, i := range overrides[d] {
			opens, closes := domain.WeeklyInterval{Start: i.Start, Minutes: i.Minutes}.Clock()
			ranges = append(ranges, apigen.TimeRange{Opens: formatClock(opens), Closes: formatClock(closes)})
		}
		out.Overrides = append(out.Overrides, apigen.DateHours{Date: openapi_types.Date{Time: d.AsTime()}, Intervals: ranges})
	}
	if !s.UpdatedAt().IsZero() {
		at := s.UpdatedAt()
		out.UpdatedAt = &at
	}
	return out
}

func toAPITimeOff(t *domain.TimeOff) apigen.TimeOff {
	out := apigen.TimeOff{
		Id: t.ID().UUID(), StaffId: t.StaffID().UUID(), StartsAt: t.Span().Start(), EndsAt: t.Span().End(), CreatedAt: t.CreatedAt(),
	}
	if r := t.Reason(); r != "" {
		out.Reason = &r
	}
	return out
}

func staffRef(actor shared.UserID, business, branch, staff apigen.BusinessID) app.StaffRef {
	return app.StaffRef{BranchRef: branchRef(actor, business, branch), StaffID: shared.IDFromUUID[shared.StaffTag](staff)}
}
