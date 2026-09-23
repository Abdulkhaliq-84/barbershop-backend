package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger reports whether a dependency is reachable.
//
// This interface is declared here, where it is used, not next to the
// database. *pgxpool.Pool already has a Ping(ctx) error method, so it
// satisfies Pinger without saying so — Go interfaces are implicit. Tests can
// pass a two-line fake instead of a real database.
type Pinger interface {
	Ping(ctx context.Context) error
}

// readyTimeout keeps /readyz fast even when the database hangs.
const readyTimeout = 2 * time.Second

// Health serves the liveness and readiness probes used by Docker, load
// balancers and orchestrators.
type Health struct {
	db     Pinger
	logger *slog.Logger
}

// NewHealth returns probes that check db for readiness.
func NewHealth(db Pinger, logger *slog.Logger) *Health {
	return &Health{db: db, logger: logger}
}

type healthStatus struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Live answers "is the process running?" — it never touches dependencies,
// so a database outage doesn't get the process restarted in a loop.
func (h *Health) Live(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, healthStatus{Status: "ok"})
}

// Ready answers "can this instance serve traffic right now?".
func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		h.logger.WarnContext(ctx, "readiness check failed", slog.String("check", "database"), slog.Any("error", err))
		WriteProblem(w, r, Problem{
			Status: http.StatusServiceUnavailable,
			Code:   "not_ready",
			Detail: "database unavailable",
		})
		return
	}
	WriteJSON(w, http.StatusOK, healthStatus{Status: "ok", Checks: map[string]string{"database": "ok"}})
}
