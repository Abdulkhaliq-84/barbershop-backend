// Package catalog is the service-menu module: what each branch sells, for
// how long and at what price (docs/architecture/domain-model.md §3.3,
// ADR-0020).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). catalog asks business who may work on a branch, through
// adapters/acl.
package catalog

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Logger   *slog.Logger
	Business *business.Module // who may work on a branch (through adapters/acl)
}

// Module is the wired catalog module.
type Module struct {
	http *httpapi.Handlers
}

// New wires the repository, use cases and HTTP handlers.
func New(d Deps) *Module {
	services := app.NewServiceHandlers(postgres.NewServices(d.Pool), acl.NewBusinessAccess(d.Business), d.Clock)
	return &Module{http: httpapi.NewHandlers(services, d.Logger)}
}

// HTTP returns the handlers for the catalog API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }
