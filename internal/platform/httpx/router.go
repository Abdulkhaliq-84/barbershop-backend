package httpx

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// NewRouter builds the root handler: shared middleware, operational
// endpoints, and JSON problem responses for unknown routes. Business modules
// mount their /v1 routes on it from M2 on.
func NewRouter(logger *slog.Logger, health *Health) *chi.Mux {
	r := chi.NewRouter()

	// Order matters: the request ID must exist before anything logs, and
	// AccessLog must wrap Recover so a recovered panic is logged as a 500.
	r.Use(RequestID, AccessLog(logger), Recover(logger))

	r.NotFound(notFound)
	r.MethodNotAllowed(methodNotAllowed)

	r.Get("/healthz", health.Live)
	r.Get("/readyz", health.Ready)

	return r
}

// Compile-time check that *chi.Mux is a plain http.Handler: chi is just a
// router on top of net/http, not a framework with its own request type.
var _ http.Handler = (*chi.Mux)(nil)
