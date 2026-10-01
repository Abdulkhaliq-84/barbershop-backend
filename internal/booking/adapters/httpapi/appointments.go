package httpapi

import (
	"context"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// BookAppointment handles POST /v1/appointments.
func (h *Handlers) BookAppointment(ctx context.Context, req apigen.BookAppointmentRequestObject) (apigen.BookAppointmentResponseObject, error) {
	fail := func(err error) (apigen.BookAppointmentResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.BookAppointmentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	body := req.Body
	cmd := app.BookAppointment{
		Customer: p.UserID, IdempotencyKey: req.Params.IdempotencyKey,
		Branch: shared.IDFromUUID[shared.BranchTag](body.BranchId), Start: body.StartsAt,
	}
	for _, id := range body.ServiceIds {
		cmd.Services = append(cmd.Services, shared.IDFromUUID[shared.ServiceTag](id))
	}
	if body.BarberId != nil {
		cmd.Barber = new(shared.IDFromUUID[shared.StaffTag](*body.BarberId))
	}
	if body.Note != nil {
		cmd.Note = *body.Note
	}
	a, replayed, err := h.book.Book(ctx, cmd)
	if err != nil {
		return fail(err)
	}
	v, err := h.book.View(ctx, a)
	if err != nil {
		return fail(err)
	}
	out := apigen.BookAppointment201JSONResponse{Body: toAPIAppointment(v)}
	if replayed {
		out.Headers.IdempotentReplayed = new(true)
	}
	return out, nil
}

// GetMyAppointment handles GET /v1/me/appointments/{appointment_id}.
func (h *Handlers) GetMyAppointment(ctx context.Context, req apigen.GetMyAppointmentRequestObject) (apigen.GetMyAppointmentResponseObject, error) {
	fail := func(err error) (apigen.GetMyAppointmentResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.GetMyAppointmentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	a, err := h.book.Appointment(ctx, p.UserID, shared.IDFromUUID[domain.AppointmentTag](req.AppointmentId))
	if err != nil {
		return fail(err)
	}
	v, err := h.book.View(ctx, a)
	if err != nil {
		return fail(err)
	}
	return apigen.GetMyAppointment200JSONResponse(toAPIAppointment(v)), nil
}

func toAPIAppointment(v app.AppointmentView) apigen.Appointment {
	s := v.Snapshot()
	out := apigen.Appointment{
		Id: s.ID.UUID(), BranchId: s.Branch.UUID(),
		Barber:     apigen.AppointmentBarber{Id: s.Barber.UUID(), DisplayName: v.BarberName},
		Status:     apigen.AppointmentStatus(s.Status),
		Assignment: apigen.AppointmentAssignment(s.Assignment),
		StartsAt:   s.Start.UTC(), EndsAt: s.End.UTC(), PendingUntil: s.PendingUntil,
		Items: make([]apigen.AppointmentItem, 0, len(s.Items)),
		Price: toAPIMoney(s.Price), Note: s.Note, CreatedAt: s.CreatedAt, Version: s.Version,
	}
	for _, it := range s.Items {
		out.Items = append(out.Items, apigen.AppointmentItem{
			ServiceId: it.Service.UUID(), Name: httpx.APIText(it.Name),
			DurationMinutes: int(it.Duration.Minutes()), Price: toAPIMoney(it.Price),
		})
	}
	return out
}

func toAPIMoney(m shared.Money) apigen.Money {
	return apigen.Money{Amount: m.Amount(), Currency: apigen.MoneyCurrency(m.Currency())}
}
