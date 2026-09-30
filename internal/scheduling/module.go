// Package scheduling is the "when can work happen" module: branch opening
// hours now; barber schedules, time off and working windows next
// (docs/architecture/domain-model.md §3.4, ADR-0021).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). scheduling asks business who may work on a branch, through
// adapters/acl.
package scheduling

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/app"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Logger   *slog.Logger
	Business *business.Module // who may work on a branch (through adapters/acl)
}

// Module is the wired scheduling module.
type Module struct {
	http *httpapi.Handlers
}

// New wires the repositories, use cases and HTTP handlers.
func New(d Deps) *Module {
	access := acl.NewBusinessAccess(d.Business)
	calendars := app.NewCalendarHandlers(postgres.NewCalendars(d.Pool), access, d.Clock)
	schedules := app.NewScheduleHandlers(postgres.NewSchedules(d.Pool), postgres.NewTimeOffs(d.Pool), access, d.Clock)
	return &Module{http: httpapi.NewHandlers(calendars, schedules, d.Logger)}
}

// HTTP returns the handlers for the scheduling API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }
