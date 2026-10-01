// Package httpapi exposes catalog's use cases over HTTP: translate the typed
// request into a command, call the use case, translate the result or error
// back. No rules and no authorization live here.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers serves /v1/service-categories and the branch services.
type Handlers struct {
	services *app.ServiceHandlers
	logger   *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(services *app.ServiceHandlers, logger *slog.Logger) *Handlers {
	return &Handlers{services: services, logger: logger}
}

// ListServiceCategories handles GET /v1/service-categories.
func (h *Handlers) ListServiceCategories(context.Context, apigen.ListServiceCategoriesRequestObject) (apigen.ListServiceCategoriesResponseObject, error) {
	cats := h.services.Categories()
	list := apigen.ListServiceCategories200JSONResponse{Data: make([]apigen.ServiceCategory, 0, len(cats))}
	for _, c := range cats {
		list.Data = append(list.Data, apigen.ServiceCategory{Code: c.Code(), Name: httpx.APIText(c.Name()), Icon: c.Icon()})
	}
	return list, nil
}

// ListServices handles GET /v1/businesses/{business_id}/branches/{branch_id}/services.
func (h *Handlers) ListServices(ctx context.Context, req apigen.ListServicesRequestObject) (apigen.ListServicesResponseObject, error) {
	fail := func(err error) (apigen.ListServicesResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.ListServicesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	services, err := h.services.List(ctx, branchRef(p.UserID, req.BusinessId, req.BranchId))
	if err != nil {
		return fail(err)
	}
	list := apigen.ListServices200JSONResponse{Data: make([]apigen.Service, 0, len(services))}
	for _, s := range services {
		list.Data = append(list.Data, toAPIService(s))
	}
	return list, nil
}

// CreateService handles POST /v1/businesses/{business_id}/branches/{branch_id}/services.
func (h *Handlers) CreateService(ctx context.Context, req apigen.CreateServiceRequestObject) (apigen.CreateServiceResponseObject, error) {
	fail := func(err error) (apigen.CreateServiceResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.CreateServicedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	body := req.Body
	cmd := app.CreateService{
		BranchRef: branchRef(p.UserID, req.BusinessId, req.BranchId),
		Category:  body.Category,
		Name:      app.Name{Ar: body.Name.Ar, En: deref(body.Name.En)},
		Duration:  time.Duration(body.DurationMinutes) * time.Minute,
		Price:     app.Money{Amount: body.Price.Amount, Currency: string(body.Price.Currency)},
	}
	if body.Description != nil {
		cmd.Description = domain.Description{Ar: deref(body.Description.Ar), En: deref(body.Description.En)}
	}
	if body.SortOrder != nil {
		cmd.SortOrder = *body.SortOrder
	}
	s, err := h.services.Create(ctx, cmd)
	if err != nil {
		return fail(err)
	}
	return apigen.CreateService201JSONResponse(toAPIService(s)), nil
}

// UpdateService handles PATCH /v1/businesses/{business_id}/branches/{branch_id}/services/{service_id}.
func (h *Handlers) UpdateService(ctx context.Context, req apigen.UpdateServiceRequestObject) (apigen.UpdateServiceResponseObject, error) {
	fail := func(err error) (apigen.UpdateServiceResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.UpdateServicedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	version, err := httpx.ParseIfMatch(req.Params.IfMatch, 1)
	if err != nil {
		return fail(err)
	}
	body := req.Body
	cmd := app.UpdateService{
		BranchRef:       branchRef(p.UserID, req.BusinessId, req.BranchId),
		ServiceID:       shared.IDFromUUID[domain.ServiceTag](req.ServiceId),
		ExpectedVersion: version,
		Category:        body.Category,
		SortOrder:       body.SortOrder,
		Active:          body.Active,
	}
	if body.Name != nil {
		cmd.Name = &app.Name{Ar: body.Name.Ar, En: deref(body.Name.En)}
	}
	if body.Description != nil {
		cmd.Description = &domain.Description{Ar: deref(body.Description.Ar), En: deref(body.Description.En)}
	}
	if body.DurationMinutes != nil {
		cmd.Duration = new(time.Duration(*body.DurationMinutes) * time.Minute)
	}
	if body.Price != nil {
		cmd.Price = &app.Money{Amount: body.Price.Amount, Currency: string(body.Price.Currency)}
	}
	s, err := h.services.Update(ctx, cmd)
	if err != nil {
		return fail(err)
	}
	return apigen.UpdateService200JSONResponse(toAPIService(s)), nil
}

// SetServiceOfferings handles PUT …/services/{service_id}/offerings.
func (h *Handlers) SetServiceOfferings(ctx context.Context, req apigen.SetServiceOfferingsRequestObject) (apigen.SetServiceOfferingsResponseObject, error) {
	fail := func(err error) (apigen.SetServiceOfferingsResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.SetServiceOfferingsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(httpx.ErrNoPrincipal)
	}
	version, err := httpx.ParseIfMatch(req.Params.IfMatch, 1)
	if err != nil {
		return fail(err)
	}
	cmd := app.SetOfferings{
		BranchRef:       branchRef(p.UserID, req.BusinessId, req.BranchId),
		ServiceID:       shared.IDFromUUID[domain.ServiceTag](req.ServiceId),
		ExpectedVersion: version,
		Offerings:       make([]app.OfferingInput, 0, len(req.Body.Offerings)),
	}
	for _, o := range req.Body.Offerings {
		in := app.OfferingInput{Staff: shared.IDFromUUID[shared.StaffTag](o.StaffId)}
		if o.Price != nil {
			in.Price = &app.Money{Amount: o.Price.Amount, Currency: string(o.Price.Currency)}
		}
		if o.DurationMinutes != nil {
			in.Duration = new(time.Duration(*o.DurationMinutes) * time.Minute)
		}
		cmd.Offerings = append(cmd.Offerings, in)
	}
	s, err := h.services.SetOfferings(ctx, cmd)
	if err != nil {
		return fail(err)
	}
	return apigen.SetServiceOfferings200JSONResponse(toAPIService(s)), nil
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
	case errors.Is(err, httpx.ErrBadIfMatch):
		status, code, detail = http.StatusBadRequest, "validation_failed", "If-Match: send the service version you last read"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrVersionConflict):
		status, code, detail = http.StatusPreconditionFailed, "version_conflict", "it changed since you read it; reload and try again"
	case errors.Is(err, domain.ErrUnknownCategory):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "category: use a code from GET /v1/service-categories"
	case errors.Is(err, shared.ErrArabicRequired):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "name: an Arabic name is required"
	case errors.Is(err, domain.ErrNameTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "name: at most 80 characters"
	case errors.Is(err, domain.ErrTextTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "description: at most 500 characters"
	case errors.Is(err, domain.ErrInvalidDuration):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "duration_minutes: 5 to 480, in steps of 5"
	case errors.Is(err, domain.ErrInvalidPrice):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "price: 0 to 10,000,000 halalas (100,000 SAR)"
	case errors.Is(err, domain.ErrInvalidSort):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "sort_order: 0 to 1000"
	case errors.Is(err, domain.ErrUnknownStaff):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "offerings: everyone listed must be active staff working at this branch"
	case errors.Is(err, domain.ErrDuplicateOffering):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "offerings: each staff member at most once"
	case errors.Is(err, domain.ErrTooManyOfferings):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "offerings: at most 100"
	default:
		h.logger.ErrorContext(ctx, "catalog request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail), headers
}

func branchRef(actor shared.UserID, business, branch apigen.BusinessID) app.BranchRef {
	return app.BranchRef{
		Actor:      actor,
		BusinessID: shared.IDFromUUID[shared.BusinessTag](business),
		BranchID:   shared.IDFromUUID[shared.BranchTag](branch),
	}
}

func toAPIService(s *domain.Service) apigen.Service {
	d := s.Details()
	out := apigen.Service{
		Id:              s.ID().UUID(),
		BranchId:        s.BranchID().UUID(),
		Category:        string(d.Category),
		Name:            httpx.APIText(d.Name),
		DurationMinutes: int(d.Duration / time.Minute),
		Price:           toAPIMoney(d.Price),
		Offerings:       make([]apigen.Offering, 0, len(s.Offerings())),
		Active:          s.IsActive(),
		SortOrder:       d.SortOrder,
		Version:         s.Version(),
		CreatedAt:       s.CreatedAt(),
		UpdatedAt:       s.UpdatedAt(),
	}
	for _, o := range s.Offerings() {
		off := apigen.Offering{StaffId: o.Staff.UUID()}
		if o.Price != nil {
			off.Price = new(toAPIMoney(*o.Price))
		}
		if o.Duration != nil {
			off.DurationMinutes = new(int(*o.Duration / time.Minute))
		}
		out.Offerings = append(out.Offerings, off)
	}
	if d.Description != (domain.Description{}) {
		desc := apigen.ServiceDescription{}
		if d.Description.Ar != "" {
			desc.Ar = &d.Description.Ar
		}
		if d.Description.En != "" {
			desc.En = &d.Description.En
		}
		out.Description = &desc
	}
	return out
}

func toAPIMoney(m shared.Money) apigen.Money {
	return apigen.Money{Amount: m.Amount(), Currency: apigen.MoneyCurrency(m.Currency())}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
