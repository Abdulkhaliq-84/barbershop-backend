package httpx_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// A path parameter that doesn't parse (a business_id that isn't a UUID)
// gets the same problem+json as every other invalid request, without
// echoing the input back.
func TestUnparseableParameterIsAProblem(t *testing.T) {
	t.Parallel()
	const input = "0551234567-not-a-uuid"
	signedIn := func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{UserID: shared.NewID[shared.UserTag]()}, nil
	}
	r := chi.NewRouter()
	// The server is never reached: parsing fails before any handler runs.
	if err := httpx.MountAPI(r, nil, slog.New(slog.DiscardHandler), signedIn); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/businesses/"+input, nil)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status %d, content type %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if strings.Contains(rec.Body.String(), input) {
		t.Fatal("the response echoes the raw input")
	}
	if p := decode[httpx.Problem](t, rec); p.Code != "validation_failed" {
		t.Fatalf("problem = %+v", p)
	}
}
