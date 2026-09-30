// Package httpapi exposes the iam use cases over HTTP. It implements the
// iam operations of the generated apigen.StrictServerInterface: translate the
// typed request into a command, call the use case, translate the result or
// error back. No business rules live here.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// UseCases are the iam use cases the HTTP adapter calls.
type UseCases struct {
	RequestOTP *app.RequestOTPHandler
	VerifyOTP  *app.VerifyOTPHandler
	Refresh    *app.RefreshHandler
	Logout     *app.LogoutHandler
	GetMe      *app.GetMeHandler
}

// Handlers serves /v1/auth/* and /v1/me.
type Handlers struct {
	uc     UseCases
	logger *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(uc UseCases, logger *slog.Logger) *Handlers {
	return &Handlers{uc: uc, logger: logger}
}

// RequestOTP handles POST /v1/auth/otp/request.
func (h *Handlers) RequestOTP(ctx context.Context, req apigen.RequestOTPRequestObject) (apigen.RequestOTPResponseObject, error) {
	res, err := h.uc.RequestOTP.Handle(ctx, app.RequestOTP{Phone: req.Body.Phone})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.RequestOTPdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
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
	login, err := h.uc.VerifyOTP.Handle(ctx, app.VerifyOTP{Phone: req.Body.Phone, Code: req.Body.Code, Locale: locale})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.VerifyOTPdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.VerifyOTP200JSONResponse{
		User: toAPIUser(login.User), IsNewUser: login.IsNewUser, Tokens: toAPITokens(login.Tokens),
	}, nil
}

// RefreshTokens handles POST /v1/auth/refresh.
func (h *Handlers) RefreshTokens(ctx context.Context, req apigen.RefreshTokensRequestObject) (apigen.RefreshTokensResponseObject, error) {
	tokens, err := h.uc.Refresh.Handle(ctx, app.RefreshTokens{RefreshToken: req.Body.RefreshToken})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.RefreshTokensdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.RefreshTokens200JSONResponse(toAPITokens(tokens)), nil
}

// Logout handles POST /v1/auth/logout.
func (h *Handlers) Logout(ctx context.Context, _ apigen.LogoutRequestObject) (apigen.LogoutResponseObject, error) {
	err := domain.ErrUnauthenticated
	if p, ok := auth.PrincipalFrom(ctx); ok {
		err = h.uc.Logout.Handle(ctx, p.UserID, shared.IDFromUUID[domain.SessionTag](p.SessionID))
	}
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.LogoutdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.Logout204Response{}, nil
}

// GetMe handles GET /v1/me.
func (h *Handlers) GetMe(ctx context.Context, _ apigen.GetMeRequestObject) (apigen.GetMeResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		// The spec's security requirement already rejected anonymous calls;
		// this guards against a route mounted without it.
		problem, headers := h.problem(ctx, domain.ErrUnauthenticated)
		return apigen.GetMedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	user, err := h.uc.GetMe.Handle(ctx, p.UserID)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.GetMedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.GetMe200JSONResponse(toAPIUser(user)), nil
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: they are logged and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) (apigen.Problem, apigen.ProblemResponseHeaders) {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	switch {
	case errors.Is(err, shared.ErrInvalidPhoneNumber):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "phone: not a Saudi mobile number"
	case errors.Is(err, domain.ErrInvalidOTPCode):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "code: must be 6 digits"
	case errors.Is(err, domain.ErrOTPLocked):
		status, code, detail = http.StatusTooManyRequests, "otp_locked", "wait before trying again"
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
	case errors.Is(err, domain.ErrUnauthenticated):
		status, code, detail = http.StatusUnauthorized, "unauthorized", "a valid access token is required"
	case errors.Is(err, domain.ErrRefreshTokenReused):
		status, code, detail = http.StatusUnauthorized, "refresh_token_reused", "session ended; sign in again"
	case errors.Is(err, domain.ErrRefreshTokenInvalid):
		status, code, detail = http.StatusUnauthorized, "refresh_token_invalid", "sign in again"
	default:
		h.logger.ErrorContext(ctx, "iam request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}

	var headers apigen.ProblemResponseHeaders
	if later, ok := errors.AsType[*domain.RetryLaterError](err); ok {
		headers.RetryAfter = new(int(math.Ceil(later.After.Seconds())))
	}
	if code == "unauthorized" {
		headers.WWWAuthenticate = new(httpx.BearerChallenge)
	}
	return httpx.APIProblem(ctx, status, code, detail), headers
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

func toAPITokens(t app.Tokens) apigen.TokenPair {
	return apigen.TokenPair{
		AccessToken:      t.Access,
		TokenType:        apigen.Bearer,
		ExpiresIn:        int(t.AccessExpiresIn.Seconds()),
		RefreshToken:     t.Refresh,
		RefreshExpiresIn: int(t.RefreshExpiresIn.Seconds()),
	}
}
