// Package httpapi adapts discovery's use cases to the generated API. Every
// discovery operation is public: it shows what owners chose to publish.
package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
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
	s, err := searchOf(req.Params)
	if err != nil {
		return fail(err)
	}
	var after *domain.Position
	if req.Params.Cursor != nil {
		if after, err = decodeCursor(*req.Params.Cursor, s.Near != nil); err != nil {
			return fail(err)
		}
	}
	page, err := h.uc.Search(ctx, s, after)
	if err != nil {
		return fail(err)
	}
	out := apigen.SearchBranches200JSONResponse{Data: make([]apigen.BranchListing, 0, len(page.Found))}
	for _, f := range page.Found {
		item := apigen.BranchListing{
			Id: f.Branch.UUID(), Name: toAPIText(f.Name), City: toAPICity(f.City),
			District: f.District, Address: f.Address,
			Location: apigen.GeoPoint{Latitude: f.Location.Lat(), Longitude: f.Location.Lng()},
		}
		if s.Near != nil {
			item.DistanceM = new(int(math.Round(f.DistanceM)))
		}
		out.Data = append(out.Data, item)
	}
	if page.Next != nil {
		out.NextCursor = new(encodeCursor(*page.Next, s.Near != nil))
	}
	return out, nil
}

// Errors in the search's parameters that the spec can't express.
var (
	errHalfAPlace     = errors.New("lat and lng: pass both")
	errRadiusAlone    = errors.New("radius_km: only with lat and lng")
	errBadCoordinates = errors.New("lat, lng: not a place on Earth")
)

// searchOf reads the search from the query parameters.
func searchOf(p apigen.SearchBranchesParams) (app.Search, error) {
	var s app.Search
	if p.City != nil {
		city, err := shared.ParseCity(*p.City)
		if err != nil {
			return app.Search{}, err
		}
		s.City = &city
	}
	switch {
	case p.Lat != nil && p.Lng != nil:
		point, err := shared.NewGeoPoint(*p.Lat, *p.Lng)
		if err != nil {
			return app.Search{}, errBadCoordinates
		}
		radius := domain.DefaultRadiusKm
		if p.RadiusKm != nil {
			radius = *p.RadiusKm
		}
		s.Near = &domain.Near{Point: point, RadiusM: float64(radius) * 1000}
	case p.Lat != nil || p.Lng != nil:
		return app.Search{}, errHalfAPlace
	case p.RadiusKm != nil:
		return app.Search{}, errRadiusAlone
	}
	return s, nil
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

// A cursor is opaque to clients: base64url of "n|<branch_id>|<arabic name>"
// when browsing by name, "d|<branch_id>|<distance>" near a place (the ID
// before the name: it never contains "|", a name might). The distance is
// written so it reads back exactly, so the next page starts exactly after
// the last branch shown. A cursor is not signed: it only says where to
// continue, and every page is still only listed branches.
func encodeCursor(p domain.Position, near bool) string {
	key := "n|" + p.Branch.String() + "|" + p.NameAr
	if near {
		key = "d|" + p.Branch.String() + "|" + strconv.FormatFloat(p.DistanceM, 'g', -1, 64)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeCursor(s string, near bool) (*domain.Position, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errBadCursor
	}
	want := "n|"
	if near {
		want = "d|"
	}
	rest, ours := strings.CutPrefix(string(raw), want)
	id, key, ok := strings.Cut(rest, "|")
	if !ours || !ok || key == "" {
		return nil, errBadCursor // not ours, or from the other kind of search
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, errBadCursor
	}
	p := &domain.Position{Branch: shared.IDFromUUID[shared.BranchTag](u)}
	if !near {
		p.NameAr = key
		return p, nil
	}
	if p.DistanceM, err = strconv.ParseFloat(key, 64); err != nil || p.DistanceM < 0 || math.IsInf(p.DistanceM, 0) || math.IsNaN(p.DistanceM) {
		return nil, errBadCursor
	}
	return p, nil
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: they are logged and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) apigen.Problem {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	switch {
	case errors.Is(err, shared.ErrUnknownCity):
		status, code, detail = http.StatusUnprocessableEntity, "unknown_city", "city: not one of the cities in GET /v1/cities"
	case errors.Is(err, errBadCursor):
		status, code, detail = http.StatusBadRequest, "validation_failed", "cursor: pass next_cursor from the previous page, with the same search"
	case errors.Is(err, domain.ErrNoPlace):
		status, code, detail = http.StatusBadRequest, "validation_failed", "pass city, or lat and lng"
	case errors.Is(err, domain.ErrRadius):
		status, code, detail = http.StatusBadRequest, "validation_failed", "radius_km: 1 to 50"
	case errors.Is(err, errHalfAPlace), errors.Is(err, errRadiusAlone), errors.Is(err, errBadCoordinates):
		status, code, detail = http.StatusBadRequest, "validation_failed", err.Error()
	default:
		h.logger.ErrorContext(ctx, "discovery request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail)
}
