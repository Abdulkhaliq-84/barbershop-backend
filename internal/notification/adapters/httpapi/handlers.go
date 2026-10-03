// Package httpapi exposes notification's use cases over HTTP: the signed-in
// user registers and removes the devices that receive their pushes.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers serve /v1/me/devices.
type Handlers struct {
	uc     *app.Handlers
	logger *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(uc *app.Handlers, logger *slog.Logger) *Handlers {
	return &Handlers{uc: uc, logger: logger}
}

// RegisterDevice handles POST /v1/me/devices.
func (h *Handlers) RegisterDevice(ctx context.Context, req apigen.RegisterDeviceRequestObject) (apigen.RegisterDeviceResponseObject, error) {
	fail := func(err error) (apigen.RegisterDeviceResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.RegisterDevicedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	d, err := h.uc.RegisterDevice(ctx, app.RegisterDevice{
		User: p.UserID, Token: req.Body.Token, Platform: string(req.Body.Platform), Locale: shared.ParseLanguage(string(req.Body.Locale)),
	})
	if err != nil {
		return fail(err)
	}
	return apigen.RegisterDevice200JSONResponse{
		Id: d.ID.UUID(), Platform: apigen.DevicePlatform(d.Platform), Locale: apigen.DeviceLocale(d.Locale),
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}, nil
}

// RemoveDevice handles DELETE /v1/me/devices/{device_id}.
func (h *Handlers) RemoveDevice(ctx context.Context, req apigen.RemoveDeviceRequestObject) (apigen.RemoveDeviceResponseObject, error) {
	fail := func(err error) (apigen.RemoveDeviceResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.RemoveDevicedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	if err := h.uc.RemoveDevice(ctx, p.UserID, shared.IDFromUUID[domain.DeviceTag](req.DeviceId)); err != nil {
		return fail(err)
	}
	return apigen.RemoveDevice204Response{}, nil
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: logged, and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) (apigen.Problem, apigen.ProblemResponseHeaders) {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	var headers apigen.ProblemResponseHeaders
	switch {
	case errors.Is(err, httpx.ErrNoPrincipal):
		status, code, detail = http.StatusUnauthorized, "unauthorized", "a valid access token is required"
		headers.WWWAuthenticate = new(httpx.BearerChallenge)
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrBadToken):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "token: the push service's registration token"
	case errors.Is(err, domain.ErrBadPlatform):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "platform: ios or android"
	default:
		h.logger.ErrorContext(ctx, "notification request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail), headers
}
