package httpapi

import (
	"context"
	"errors"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// errReasonNotAllowed: a reason was sent with an action other than cancel.
var errReasonNotAllowed = errors.New("booking: a reason goes with cancel only")

// actions maps the API's actions to the domain's.
var actions = map[string]domain.Action{
	"confirm": domain.ActionConfirm, "reject": domain.ActionReject, "cancel": domain.ActionCancel,
	"complete": domain.ActionComplete, "no-show": domain.ActionNoShow,
}

// CancelMyAppointment handles POST /v1/me/appointments/{appointment_id}/cancel.
func (h *Handlers) CancelMyAppointment(ctx context.Context, req apigen.CancelMyAppointmentRequestObject) (apigen.CancelMyAppointmentResponseObject, error) {
	fail := func(err error) (apigen.CancelMyAppointmentResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.CancelMyAppointmentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	v, err := h.manage.CancelMine(ctx, p.UserID, shared.IDFromUUID[domain.AppointmentTag](req.AppointmentId), reasonOf(req.Body))
	if err != nil {
		return fail(err)
	}
	return apigen.CancelMyAppointment200JSONResponse(toAPIAppointment(v)), nil
}

// ActOnAppointment handles POST /v1/businesses/{business_id}/appointments/{appointment_id}/{action}.
func (h *Handlers) ActOnAppointment(ctx context.Context, req apigen.ActOnAppointmentRequestObject) (apigen.ActOnAppointmentResponseObject, error) {
	fail := func(err error) (apigen.ActOnAppointmentResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.ActOnAppointmentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	action, ok := actions[req.Action]
	if !ok { // the spec's enum already refused it
		return fail(domain.ErrNotFound)
	}
	reason := reasonOf(req.Body)
	if req.Body != nil && req.Body.Reason != nil && action != domain.ActionCancel {
		return fail(errReasonNotAllowed)
	}
	v, err := h.manage.Act(ctx, app.StaffAction{
		Actor: p.UserID, Business: shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		Appointment: shared.IDFromUUID[domain.AppointmentTag](req.AppointmentId), Action: action, Reason: reason,
	})
	if err != nil {
		return fail(err)
	}
	return apigen.ActOnAppointment200JSONResponse(toAPIAppointment(v)), nil
}

// ListBranchAppointments handles GET /v1/businesses/{business_id}/branches/{branch_id}/appointments.
func (h *Handlers) ListBranchAppointments(ctx context.Context, req apigen.ListBranchAppointmentsRequestObject) (apigen.ListBranchAppointmentsResponseObject, error) {
	fail := func(err error) (apigen.ListBranchAppointmentsResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.ListBranchAppointmentsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	day, err := h.manage.Day(ctx, app.DayQuery{
		Actor: p.UserID, Business: shared.IDFromUUID[shared.BusinessTag](req.BusinessId),
		Branch: shared.IDFromUUID[shared.BranchTag](req.BranchId), Day: domain.DayOf(req.Params.Date.Time),
	})
	if err != nil {
		return fail(err)
	}
	out := apigen.ListBranchAppointments200JSONResponse{
		BranchId: req.BranchId, Date: req.Params.Date, TimeZone: day.Location.String(),
		Appointments: make([]apigen.Appointment, 0, len(day.Appointments)),
	}
	for _, v := range day.Appointments {
		out.Appointments = append(out.Appointments, toAPIAppointment(v))
	}
	return out, nil
}

func reasonOf(body *apigen.CancelRequest) string {
	if body == nil || body.Reason == nil {
		return ""
	}
	return *body.Reason
}
