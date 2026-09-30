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
	logger       *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(availability *app.AvailabilityHandlers, logger *slog.Logger) *Handlers {
	return &Handlers{availability: availability, logger: logger}
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
			Price: apigen.Money{Amount: o.Price.Amount(), Currency: apigen.MoneyCurrency(o.Price.Currency())},
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
	switch {
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
	return httpx.APIProblem(ctx, status, code, detail), apigen.ProblemResponseHeaders{}
}
