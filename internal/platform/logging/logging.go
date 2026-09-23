// Package logging builds the service's structured logger on top of the
// standard library's log/slog (no third-party logger needed).
package logging

import (
	"io"
	"log/slog"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
)

// New returns a logger that writes JSON (production) or human-readable text
// (local development) to w. Every record carries the service name and
// environment so logs from several services can be told apart.
func New(w io.Writer, cfg config.Log, env string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level}

	var h slog.Handler
	if cfg.Format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h).With(
		slog.String("service", "barbershop-backend"),
		slog.String("env", env),
	)
}
