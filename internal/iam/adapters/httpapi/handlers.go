// Package httpapi exposes the iam use cases over HTTP. It implements the
// iam operations of the generated apigen.StrictServerInterface: translate the
// typed request into a command, call the use case, translate the result or
// error back. No business rules live here.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers serves /v1/auth/*.
type Handlers struct {
	requestOTP *app.RequestOTPHandler
	verifyOTP  *app.VerifyOTPHandler
	logger     *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(requestOTP *app.RequestOTPHandler, verifyOTP *app.VerifyOTPHandler, logger *slog.Logger) *Handlers {
	return &Handlers{requestOTP: requestOTP, verifyOTP: verifyOTP, logger: logger}
}

// RequestOTP handles POST /v1/auth/otp/request.
func (h *Handlers) RequestOTP(ctx context.Context, req apigen.RequestOTPRequestObject) (apigen.RequestOTPResponseObject, error) {
	res, err := h.requestOTP.Handle(ctx, app.RequestOTP{Phone: req.Body.Phone})
	if err != nil {
		problem, retryAfter := h.problem(ctx, err)
		return apigen.RequestOTPdefaultApplicationProblemPlusJSONResponse{
			Body: problem, StatusCode: problem.Status,
			Headers: apigen.ProblemResponseHeaders{RetryAfter: retryAfter},
		}, nil
	}
	return apigen.RequestOTP202JSONResponse{
		ExpiresInSeconds:   int(res.ExpiresIn.Seconds()),
		ResendAfterSeconds: int(res.ResendAfter.Seconds()),
	}, nil
}

// VerifyOTP handles POST /v1/auth/otp/verify.
func (h *Handlers) VerifyOTP(ctx context.Context, req apigen.VerifyOTPRequestObject) (apigen.VerifyOTPResponseObject, error) {
	locale := shared.Arabic
	if req.Params.AcceptLanguage != nil {
		locale = shared.ParseLanguage(*req.Params.AcceptLanguage)
	}
	login, err := h.verifyOTP.Handle(ctx, app.VerifyOTP{Phone: req.Body.Phone, Code: req.Body.Code, Locale: locale})
	if err != nil {
		problem, retryAfter := h.problem(ctx, err)
		return apigen.VerifyOTPdefaultApplicationProblemPlusJSONResponse{
			Body: problem, StatusCode: problem.Status,
			Headers: apigen.ProblemResponseHeaders{RetryAfter: retryAfter},
		}, nil
	}
	return apigen.VerifyOTP200JSONResponse{User: toAPIUser(login.User), IsNewUser: login.IsNewUser}, nil
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: they are logged and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) (apigen.Problem, *int) {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	switch {
	case errors.Is(err, shared.ErrInvalidPhoneNumber):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "phone: not a Saudi mobile number"
	case errors.Is(err, domain.ErrInvalidOTPCode):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "code: must be 6 digits"
	case errors.Is(err, domain.ErrOTPCooldown):
		status, code = http.StatusTooManyRequests, "otp_cooldown"
	case errors.Is(err, domain.ErrOTPRateLimited):
		status, code = http.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, domain.ErrOTPTooManyAttempts):
		status, code, detail = http.StatusTooManyRequests, "otp_too_many_attempts", "request a new code"
	case errors.Is(err, domain.ErrOTPInvalid):
		status, code = http.StatusUnauthorized, "otp_invalid"
	case errors.Is(err, domain.ErrOTPExpired):
		status, code, detail = http.StatusUnauthorized, "otp_expired", "request a new code"
	case errors.Is(err, domain.ErrUserBlocked):
		status, code = http.StatusForbidden, "user_blocked"
	default:
		h.logger.ErrorContext(ctx, "iam request failed", slog.Any("error", err))
	}

	var retryAfter *int
	var later *domain.RetryLaterError
	if errors.As(err, &later) {
		secs := int(math.Ceil(later.After.Seconds()))
		retryAfter = &secs
	}
	return httpx.APIProblem(ctx, status, code, detail), retryAfter
}

func toAPIUser(u *domain.User) apigen.User {
	user := apigen.User{
		Id:        u.ID().UUID(),
		Phone:     u.Phone().String(),
		Locale:    apigen.UserLocale(u.Locale()),
		CreatedAt: u.CreatedAt(),
	}
	if name := u.Name(); name != "" {
		user.Name = &name
	}
	return user
}
