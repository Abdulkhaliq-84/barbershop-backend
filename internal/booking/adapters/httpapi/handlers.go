// Package httpapi adapts booking's use cases to the generated API.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers implement booking's operations of the API.
type Handlers struct {
	availability *app.AvailabilityHandlers
	book         *app.BookHandlers
	manage       *app.ManageHandlers
	logger       *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(availability *app.AvailabilityHandlers, book *app.BookHandlers, manage *app.ManageHandlers, logger *slog.Logger) *Handlers {
	return &Handlers{availability: availability, book: book, manage: manage, logger: logger}
}

// GetAvailability handles GET /v1/branches/{branch_id}/availability.
func (h *Handlers) GetAvailability(ctx context.Context, req apigen.GetAvailabilityRequestObject) (apigen.GetAvailabilityResponseObject, error) {
	q := app.AvailabilityQuery{
		Branch: shared.IDFromUUID[shared.BranchTag](req.BranchId),
		Day:    domain.DayOf(req.Params.Date.Time),
	}
	for _, id := range req.Params.ServiceIds {
		q.Services = append(q.Services, shared.IDFromUUID[shared.ServiceTag](id))
	}
	if req.Params.BarberId != nil {
		q.Barber = new(shared.IDFromUUID[shared.StaffTag](*req.Params.BarberId))
	}
	a, err := h.availability.Query(ctx, q)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.GetAvailabilitydefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	out := apigen.GetAvailability200JSONResponse{
		BranchId: a.Branch.ID.UUID(),
		Date:     req.Params.Date,
		TimeZone: a.Branch.Location.String(),
		Barbers:  make([]apigen.BarberOffer, 0, len(a.Offers)),
		Slots:    make([]apigen.Slot, 0, len(a.Slots)),
	}
	for _, o := range a.Offers {
		out.Barbers = append(out.Barbers, apigen.BarberOffer{
			Id: o.ID.UUID(), DisplayName: o.Name, DurationMinutes: int(o.Duration.Minutes()),
			Price: toAPIMoney(o.Price),
		})
	}
	for _, s := range a.Slots {
		slot := apigen.Slot{StartsAt: s.Start.UTC(), BarberIds: make([]apigen.BranchID, 0, len(s.Staff))}
		for _, id := range s.Staff {
			slot.BarberIds = append(slot.BarberIds, id.UUID())
		}
		out.Slots = append(out.Slots, slot)
	}
	return out, nil
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: they are logged and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) (apigen.Problem, apigen.ProblemResponseHeaders) {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	var headers apigen.ProblemResponseHeaders
	switch {
	case errors.Is(err, httpx.ErrNoPrincipal):
		status, code, detail = http.StatusUnauthorized, "unauthorized", "a valid access token is required"
		headers.WWWAuthenticate = new(httpx.BearerChallenge)
	case errors.Is(err, domain.ErrSlotUnavailable):
		status, code, detail = http.StatusConflict, "slot_unavailable", "that time was just taken or is no longer free; ask for availability again"
	case errors.Is(err, domain.ErrTooManyBookings):
		status, code, detail = http.StatusConflict, "booking_limit_reached", "you already hold the most upcoming bookings this branch allows"
	case errors.Is(err, domain.ErrInvalidStart):
		status, code, detail = http.StatusUnprocessableEntity, "invalid_start", "not a start time this branch offers (its grid, lead time and horizon)"
	case errors.Is(err, domain.ErrIdempotencyReused):
		status, code, detail = http.StatusUnprocessableEntity, "idempotency_key_reused", "this Idempotency-Key was used for a different booking"
	case errors.Is(err, domain.ErrNoteTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "note: at most 300 characters"
	case errors.Is(err, domain.ErrInvalidTransition):
		status, code, detail = http.StatusConflict, "invalid_transition", transitionDetail(err)
	case errors.Is(err, domain.ErrTooLateToCancel):
		status, code, detail = http.StatusConflict, "cancellation_window_passed", "past the deadline to cancel this booking (cancellable_until)"
	case errors.Is(err, domain.ErrNotStarted):
		status, code, detail = http.StatusConflict, "appointment_not_started", "it can be completed or marked a no-show once it has started"
	case errors.Is(err, domain.ErrReasonTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "reason: at most 300 characters"
	case errors.Is(err, errReasonNotAllowed):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "reason: only with cancel"
	case errors.Is(err, domain.ErrBranchNotBookable):
		status, code, detail = http.StatusConflict, "branch_not_bookable", "the branch takes no bookings until it is published and its business is active"
	case errors.Is(err, domain.ErrNoCustomer):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "customer_name: required"
	case errors.Is(err, domain.ErrCustomerNameTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "customer_name: at most 100 characters"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrNoServices):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "service_ids: 1 to 5 different services"
	case errors.Is(err, domain.ErrServiceUnavailable):
		status, code, detail = http.StatusUnprocessableEntity, "service_unavailable", "a chosen service isn't offered at this branch"
	case errors.Is(err, domain.ErrBarberUnavailable):
		status, code, detail = http.StatusUnprocessableEntity, "barber_unavailable", "this barber doesn't perform all the chosen services here"
	default:
		h.logger.ErrorContext(ctx, "booking request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail), headers
}

// transitionDetail says what the appointment is now, in plain words.
func transitionDetail(err error) string {
	te, ok := errors.AsType[*domain.TransitionError](err)
	if !ok {
		return "the appointment can't change that way"
	}
	done := map[domain.Action]string{
		domain.ActionConfirm: "confirmed", domain.ActionReject: "rejected", domain.ActionCancel: "cancelled",
		domain.ActionComplete: "completed", domain.ActionNoShow: "marked a no-show",
	}[te.Action]
	is := string(te.Status)
	if te.Status == domain.StatusNoShow {
		is = "a no-show"
	}
	return fmt.Sprintf("the appointment is %s, so it can't be %s", is, done)
}
