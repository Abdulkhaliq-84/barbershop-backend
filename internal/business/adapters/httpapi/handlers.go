// Package httpapi exposes the business use cases over HTTP. It implements
// the business operations of the generated apigen.StrictServerInterface:
// translate the typed request into a command, call the use case, translate
// the result or error back. No business rules — and no authorization — live
// here: the use cases check membership themselves.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// UseCases are the business use cases the HTTP adapter calls.
type UseCases struct {
	Register    *app.RegisterBusinessHandler
	Get         *app.GetBusinessHandler
	Memberships *app.ListMyMembershipsHandler
}

// Handlers serves /v1/businesses/* and /v1/me/memberships.
type Handlers struct {
	uc     UseCases
	logger *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(uc UseCases, logger *slog.Logger) *Handlers {
	return &Handlers{uc: uc, logger: logger}
}

// errNoPrincipal guards against a route mounted without the spec's security
// requirement, which would otherwise have rejected an anonymous caller.
var errNoPrincipal = errors.New("no authenticated caller")

// RegisterBusiness handles POST /v1/businesses.
func (h *Handlers) RegisterBusiness(ctx context.Context, req apigen.RegisterBusinessRequestObject) (apigen.RegisterBusinessResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.RegisterBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	body := req.Body
	b, err := h.uc.Register.Handle(ctx, app.RegisterBusiness{
		Actor:         p.UserID,
		DisplayNameAr: body.DisplayName.Ar,
		DisplayNameEn: deref(body.DisplayName.En),
		LegalName:     body.LegalName,
		CRNumber:      body.CrNumber,
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.RegisterBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.RegisterBusiness201JSONResponse(toAPIBusiness(b)), nil
}

// GetBusiness handles GET /v1/businesses/{business_id}.
func (h *Handlers) GetBusiness(ctx context.Context, req apigen.GetBusinessRequestObject) (apigen.GetBusinessResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.GetBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	b, err := h.uc.Get.Handle(ctx, app.GetBusiness{
		Actor:      p.UserID,                                              // who asks: from the token, never the request
		BusinessID: shared.IDFromUUID[shared.BusinessTag](req.BusinessId), // what they ask for: from the path
	})
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.GetBusinessdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	return apigen.GetBusiness200JSONResponse(toAPIBusiness(b)), nil
}

// ListMyMemberships handles GET /v1/me/memberships.
func (h *Handlers) ListMyMemberships(ctx context.Context, _ apigen.ListMyMembershipsRequestObject) (apigen.ListMyMembershipsResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		problem, headers := h.problem(ctx, errNoPrincipal)
		return apigen.ListMyMembershipsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	views, err := h.uc.Memberships.Handle(ctx, p.UserID)
	if err != nil {
		problem, headers := h.problem(ctx, err)
		return apigen.ListMyMembershipsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	list := apigen.ListMyMemberships200JSONResponse{Data: make([]apigen.Membership, 0, len(views))}
	for _, v := range views {
		list.Data = append(list.Data, apigen.Membership{
			StaffId: v.StaffID.UUID(),
			Role:    apigen.StaffRole(v.Role),
			Business: apigen.BusinessSummary{
				Id:          v.BusinessID.UUID(),
				DisplayName: toAPIText(v.DisplayName),
				Status:      apigen.BusinessStatus(v.Status),
			},
		})
	}
	return list, nil
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: they are logged and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) (apigen.Problem, apigen.ProblemResponseHeaders) {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	var headers apigen.ProblemResponseHeaders
	switch {
	case errors.Is(err, errNoPrincipal):
		status, code, detail = http.StatusUnauthorized, "unauthorized", "a valid access token is required"
		challenge := httpx.BearerChallenge
		headers.WWWAuthenticate = &challenge
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrAlreadyRegistered):
		status, code, detail = http.StatusConflict, "business_already_registered", "you already registered this CR number; see /v1/me/memberships"
	case errors.Is(err, domain.ErrInvalidCRNumber):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "cr_number: must be 10 digits"
	case errors.Is(err, shared.ErrArabicRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "display_name.ar: required"
	case errors.Is(err, domain.ErrLegalNameRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "legal_name: required"
	case errors.Is(err, domain.ErrTextTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "a name is too long"
	default:
		h.logger.ErrorContext(ctx, "business request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail), headers
}

func toAPIBusiness(b *domain.Business) apigen.Business {
	return apigen.Business{
		Id:          b.ID().UUID(),
		DisplayName: toAPIText(b.DisplayName()),
		LegalName:   b.LegalName(),
		CrNumber:    b.CRNumber().String(),
		Status:      apigen.BusinessStatus(b.Status()),
		Version:     b.Version(),
		CreatedAt:   b.CreatedAt(),
		UpdatedAt:   b.UpdatedAt(),
	}
}

// toAPIText returns both languages: business-mode screens edit them.
func toAPIText(t shared.LocalizedText) apigen.LocalizedText {
	text := apigen.LocalizedText{Ar: t.Ar()}
	if en := t.En(); en != "" {
		text.En = &en
	}
	return text
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
