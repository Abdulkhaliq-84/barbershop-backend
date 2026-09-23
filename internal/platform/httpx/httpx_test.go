package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
)

// fakePinger is a hand-written fake: in Go a tiny struct usually beats a
// mocking library. It satisfies httpx.Pinger just by having the method.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newRouter(t *testing.T, db httpx.Pinger) http.Handler {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	return httpx.NewRouter(logger, httpx.NewHealth(db, logger))
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		path            string
		db              httpx.Pinger
		wantStatus      int
		wantContentType string
	}{
		{"live ignores database", "/healthz", fakePinger{err: errors.New("down")}, http.StatusOK, "application/json"},
		{"ready when database up", "/readyz", fakePinger{}, http.StatusOK, "application/json"},
		{"not ready when database down", "/readyz", fakePinger{err: errors.New("down")}, http.StatusServiceUnavailable, "application/problem+json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			router := newRouter(t, tt.db)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantContentType)
			}
		})
	}
}

func TestReadyReportsProblemCode(t *testing.T) {
	t.Parallel()

	router := newRouter(t, fakePinger{err: errors.New("connection refused")})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))

	p := decode[httpx.Problem](t, rec)
	if p.Code != "not_ready" || p.Status != http.StatusServiceUnavailable {
		t.Errorf("problem = %+v, want code not_ready / 503", p)
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Error("readiness response leaks the internal error")
	}
}

func TestUnknownRoutesReturnProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		wantStatus   int
		wantCode     string
	}{
		{http.MethodGet, "/nope", http.StatusNotFound, "not_found"},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			t.Parallel()

			router := newRouter(t, fakePinger{})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil))

			p := decode[httpx.Problem](t, rec)
			if rec.Code != tt.wantStatus || p.Code != tt.wantCode {
				t.Errorf("got %d %q, want %d %q", rec.Code, p.Code, tt.wantStatus, tt.wantCode)
			}
			if p.RequestID == "" {
				t.Error("problem has no request_id")
			}
		})
	}
}

func TestRequestID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		incoming string
		wantSame bool
	}{
		{"generated when missing", "", false},
		{"kept when well formed", "abc-123_XYZ.9", true},
		{"replaced when unsafe", "bad id\nwith newline", false},
		{"replaced when too long", strings.Repeat("a", 65), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			router := newRouter(t, fakePinger{})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
			if tt.incoming != "" {
				req.Header.Set(httpx.RequestIDHeader, tt.incoming)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			got := rec.Header().Get(httpx.RequestIDHeader)
			if got == "" {
				t.Fatal("response has no X-Request-Id")
			}
			if (got == tt.incoming) != tt.wantSame {
				t.Errorf("X-Request-Id = %q, incoming %q, wantSame %v", got, tt.incoming, tt.wantSame)
			}
		})
	}
}

func TestAccessLogAndRecover(t *testing.T) {
	t.Parallel()

	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := httpx.NewRouter(logger, httpx.NewHealth(fakePinger{}, logger))
	router.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("kaboom") })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if p := decode[httpx.Problem](t, rec); p.Code != "internal" {
		t.Errorf("problem code = %q, want internal", p.Code)
	}
	out := logs.String()
	for _, want := range []string{`"msg":"panic recovered"`, `"panic":"kaboom"`, `"msg":"http request"`, `"status":500`} {
		if !strings.Contains(out, want) {
			t.Errorf("logs missing %s\nlogs: %s", want, out)
		}
	}
}
