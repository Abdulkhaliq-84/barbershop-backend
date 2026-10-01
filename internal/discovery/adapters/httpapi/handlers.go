// Package httpapi adapts discovery's use cases to the generated API. Every
// discovery operation is public: it shows what owners chose to publish.
package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers implement discovery's operations of the API.
type Handlers struct {
	uc     *app.Handlers
	logger *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(uc *app.Handlers, logger *slog.Logger) *Handlers {
	return &Handlers{uc: uc, logger: logger}
}

// ListCities handles GET /v1/cities.
func (h *Handlers) ListCities(context.Context, apigen.ListCitiesRequestObject) (apigen.ListCitiesResponseObject, error) {
	cities := shared.Cities()
	out := apigen.ListCities200JSONResponse{Data: make([]apigen.City, 0, len(cities))}
	for _, c := range cities {
		out.Data = append(out.Data, toAPICity(c))
	}
	return out, nil
}

// SearchBranches handles GET /v1/branches.
func (h *Handlers) SearchBranches(ctx context.Context, req apigen.SearchBranchesRequestObject) (apigen.SearchBranchesResponseObject, error) {
	fail := func(err error) (apigen.SearchBranchesResponseObject, error) {
		problem := h.problem(ctx, err)
		return apigen.SearchBranchesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status}, nil
	}
	city, err := shared.ParseCity(req.Params.City)
	if err != nil {
		return fail(err)
	}
	var after *domain.Position
	if req.Params.Cursor != nil {
		if after, err = decodeCursor(*req.Params.Cursor); err != nil {
			return fail(err)
		}
	}
	page, err := h.uc.BrowseCity(ctx, city, after)
	if err != nil {
		return fail(err)
	}
	out := apigen.SearchBranches200JSONResponse{Data: make([]apigen.BranchListing, 0, len(page.Listings))}
	for _, l := range page.Listings {
		out.Data = append(out.Data, apigen.BranchListing{
			Id: l.Branch.UUID(), Name: toAPIText(l.Name), City: toAPICity(l.City),
			District: l.District, Address: l.Address,
			Location: apigen.GeoPoint{Latitude: l.Location.Lat(), Longitude: l.Location.Lng()},
		})
	}
	if page.Next != nil {
		out.NextCursor = new(encodeCursor(*page.Next))
	}
	return out, nil
}

func toAPICity(c shared.City) apigen.City {
	return apigen.City{Code: c.Code(), Name: toAPIText(c.Name())}
}

func toAPIText(t shared.LocalizedText) apigen.LocalizedText {
	out := apigen.LocalizedText{Ar: t.Ar()}
	if t.En() != "" {
		out.En = new(t.En())
	}
	return out
}

// errBadCursor reports a cursor this API didn't issue.
var errBadCursor = errors.New("cursor: not one this API issued")

// A cursor is opaque to clients: base64url of "<branch_id>|<arabic name>"
// (the ID first: it never contains "|", a name might). It is not signed:
// it only says where to continue, and every page is still only listed
// branches of the city asked for.
func encodeCursor(p domain.Position) string {
	return base64.RawURLEncoding.EncodeToString([]byte(p.Branch.String() + "|" + p.NameAr))
}

func decodeCursor(s string) (*domain.Position, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errBadCursor
	}
	id, name, ok := strings.Cut(string(raw), "|")
	if !ok || name == "" {
		return nil, errBadCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, errBadCursor
	}
	return &domain.Position{NameAr: name, Branch: shared.IDFromUUID[shared.BranchTag](u)}, nil
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: they are logged and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) apigen.Problem {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	switch {
	case errors.Is(err, shared.ErrUnknownCity):
		status, code, detail = http.StatusUnprocessableEntity, "unknown_city", "city: not one of the cities in GET /v1/cities"
	case errors.Is(err, errBadCursor):
		status, code, detail = http.StatusBadRequest, "validation_failed", "cursor: pass next_cursor from the previous page"
	default:
		h.logger.ErrorContext(ctx, "discovery request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail)
}
