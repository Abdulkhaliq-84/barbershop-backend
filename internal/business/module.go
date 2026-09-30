// Package business is the tenancy module: businesses, their onboarding and
// their staff (docs/architecture/domain-model.md §3.2).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; the domain, app and adapters packages are the
// module's private implementation (enforced by lint rules).
package business

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Logger *slog.Logger
	Media  *media.Module // stores verification documents (through adapters/acl)
}

// Module is the wired business module.
type Module struct {
	http *httpapi.Handlers
}

// New wires the repositories, use cases and HTTP handlers.
func New(d Deps) *Module {
	store := postgres.NewStore(d.Pool)
	files := acl.NewMediaFiles(d.Media)
	return &Module{
		http: httpapi.NewHandlers(httpapi.UseCases{
			Register:    app.NewRegisterBusinessHandler(store, d.Clock),
			Get:         app.NewGetBusinessHandler(store, store),
			Update:      app.NewUpdateBusinessHandler(store, store, d.Clock),
			Branches:    app.NewBranchHandlers(store.Branches(), store, d.Clock),
			Documents:   app.NewDocumentHandlers(store, store.Documents(), store, files, d.Clock),
			Submit:      app.NewSubmitHandler(store, store, d.Clock),
			Review:      app.NewReviewHandlers(store, store.Documents(), store.Branches(), store, files, d.Clock),
			Memberships: app.NewListMyMembershipsHandler(store),
		}, d.Logger),
	}
}

// HTTP returns the handlers for the business API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }
