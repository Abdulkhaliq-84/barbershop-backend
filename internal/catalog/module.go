// Package catalog is the service-menu module: what each branch sells, for
// how long and at what price (docs/architecture/domain-model.md §3.3,
// ADR-0020).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). catalog asks business who may work on a branch, through
// adapters/acl. It publishes events through the outbox; OnServiceChanged is
// how other modules (wired in main) subscribe to them.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Logger   *slog.Logger
	Business *business.Module // who may work on a branch (through adapters/acl)
	Events   *outbox.Bus      // where the services' events are published
}

// Module is the wired catalog module.
type Module struct {
	http     *httpapi.Handlers
	services *app.ServiceHandlers
}

// New wires the repository, use cases and HTTP handlers.
func New(d Deps) *Module {
	services := app.NewServiceHandlers(postgres.NewServices(d.Pool, d.Events), acl.NewBusinessAccess(d.Business), d.Clock)
	return &Module{services: services, http: httpapi.NewHandlers(services, d.Logger)}
}

// ServiceID identifies a service.
type ServiceID = domain.ServiceID

// MenuItem is an active service and who performs it, for how long and at
// what price.
type MenuItem = app.MenuItem

// Performer is one barber's version of a service.
type Performer = app.Performer

// Menu returns the branch's active services with who performs them, for
// booking. It authorizes nobody: callers check the branch is bookable first.
func (m *Module) Menu(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]MenuItem, error) {
	return m.services.Menu(ctx, business, branch)
}

// PerformingStaff returns who performs at least one active service at the
// branch, in ID order — part of whether the branch can take a booking. It
// authorizes nobody: callers check first.
func (m *Module) PerformingStaff(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]shared.StaffID, error) {
	return m.services.PerformingStaff(ctx, business, branch)
}

// HTTP returns the handlers for the catalog API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// ServiceChanged is a service event (created or updated) with the service
// as it is from Version on: whether customers can choose it, what kind it
// is, and the least it costs.
type ServiceChanged struct {
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
	ServiceID  ServiceID
	Version    int
	Offered    bool         // active, and someone performs it: customers can choose it
	Category   string       // a shared.Categories code
	PriceFrom  shared.Money // the least a customer pays for it (its own price while nobody performs it)
	At         time.Time
}

// OnServiceChanged subscribes fn to every service event, under two stable
// names: name+".created" and name+".updated". An event can come more than
// once and out of order: keep the newest Version.
func OnServiceChanged(bus *outbox.Bus, name string, fn func(ctx context.Context, e ServiceChanged) error) {
	handle := func(ctx context.Context, e outbox.Event) error {
		s, err := decodeServiceChanged(e)
		if err != nil {
			return err
		}
		return fn(ctx, s)
	}
	bus.Subscribe(name+".created", events.TypeServiceCreated, handle)
	bus.Subscribe(name+".updated", events.TypeServiceUpdated, handle)
}

// decodeServiceChanged reads either service event.
func decodeServiceChanged(e outbox.Event) (ServiceChanged, error) {
	var p events.ServiceChanged
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return ServiceChanged{}, fmt.Errorf("decode %s %s: %w", e.Type, e.ID, err)
	}
	s := p.Service
	if s.Version < 1 {
		return ServiceChanged{}, fmt.Errorf("decode %s %s: no service version", e.Type, e.ID)
	}
	from := s.Price
	if s.PriceFrom != nil {
		from = *s.PriceFrom
	}
	price, err := shared.NewMoney(from.Amount, shared.Currency(from.Currency))
	if err != nil {
		return ServiceChanged{}, fmt.Errorf("decode %s %s: price: %w", e.Type, e.ID, err)
	}
	return ServiceChanged{
		BusinessID: shared.IDFromUUID[shared.BusinessTag](p.BusinessID),
		BranchID:   shared.IDFromUUID[shared.BranchTag](p.BranchID),
		ServiceID:  shared.IDFromUUID[domain.ServiceTag](p.ServiceID),
		Version:    s.Version, Offered: s.Active && s.PriceFrom != nil,
		Category: s.CategoryCode, PriceFrom: price, At: e.OccurredAt,
	}, nil
}
