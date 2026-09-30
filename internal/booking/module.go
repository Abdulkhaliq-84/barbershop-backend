// Package booking is the appointments module: when customers can book, and
// (from M5.3) their bookings (docs/architecture/domain-model.md §3.5).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). booking combines business (branches, barbers), catalog (the menu)
// and scheduling (working windows) through adapters/acl.
package booking

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Logger     *slog.Logger
	Business   *business.Module   // bookable branches and their barbers
	Catalog    *catalog.Module    // the menu: who performs what, for how long, at what price
	Scheduling *scheduling.Module // working windows
}

// Module is the wired booking module.
type Module struct {
	http *httpapi.Handlers
}

// New wires the repositories, use cases and HTTP handlers.
func New(d Deps) *Module {
	availability := app.NewAvailabilityHandlers(
		acl.NewBranches(d.Business), acl.NewMenus(d.Catalog), acl.NewSchedules(d.Scheduling),
		postgres.NewAppointments(d.Pool), d.Clock,
	)
	return &Module{http: httpapi.NewHandlers(availability, d.Logger)}
}

// HTTP returns the handlers for the booking API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }
