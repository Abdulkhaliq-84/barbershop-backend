// Package discovery is the search module: how customers find branches
// (docs/architecture/domain-model.md §3.6, ADR-0027).
//
// It keeps its own copy of every branch, of what each sells and of when it
// is open, a read model built only from business's, catalog's and
// scheduling's events: main subscribes it to them (KeepBranch, KeepService,
// KeepHours). It depends on no other module and
// never reads their tables, so searching can't slow down or lock those
// modules, and the copy can take whatever shape searching needs.
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules).
package discovery

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock // what "open now" means
	Logger *slog.Logger
}

// Module is the wired discovery module.
type Module struct {
	uc   *app.Handlers
	http *httpapi.Handlers
}

// New wires the module.
func New(d Deps) *Module {
	uc := app.NewHandlers(postgres.NewListings(d.Pool), d.Clock)
	return &Module{uc: uc, http: httpapi.NewHandlers(uc, d.Logger)}
}

// HTTP returns the handlers for the discovery API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// Branch is a branch as of Version, as business's events describe it. Its
// fields match business.BranchChanged one for one, so main converts one to
// the other with a plain conversion, and the compiler says so if they ever
// stop matching.
type Branch struct {
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
	Version    int
	Published  bool
	Name       shared.LocalizedText
	City       string
	District   string
	Address    string
	Location   shared.GeoPoint
	Phone      string
	Timezone   string
	At         time.Time
}

// KeepBranch updates discovery's copy of a branch. An older version than
// the copy's changes nothing, so it handles an at-least-once, unordered
// event stream.
func (m *Module) KeepBranch(ctx context.Context, b Branch) error {
	city, err := shared.ParseCity(b.City)
	if err != nil {
		return err
	}
	return m.uc.Keep(ctx, domain.Listing{
		Branch: b.BranchID, Business: b.BusinessID, Version: b.Version, Listed: b.Published,
		Name: b.Name, City: city, District: b.District, Address: b.Address, Location: b.Location,
		Phone: b.Phone, Timezone: b.Timezone, UpdatedAt: b.At,
	})
}

// Service is a branch's service as of Version, as catalog's events
// describe it. Its fields match catalog.ServiceChanged one for one, so main
// converts one to the other with a plain conversion.
type Service struct {
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
	ServiceID  shared.ServiceID
	Version    int
	Offered    bool   // active, and someone performs it
	Category   string // a shared.Categories code
	Name       shared.LocalizedText
	Duration   time.Duration
	PriceFrom  shared.Money // the least a customer pays for it
	SortOrder  int          // the menu's order, lowest first
	At         time.Time
}

// KeepService updates discovery's copy of a service. An older version than
// the copy's changes nothing, like KeepBranch.
func (m *Module) KeepService(ctx context.Context, s Service) error {
	category, err := shared.ParseCategory(s.Category)
	if err != nil {
		return err
	}
	return m.uc.KeepService(ctx, domain.Service{
		Service: s.ServiceID, Branch: s.BranchID, Business: s.BusinessID, Version: s.Version,
		Offered: s.Offered, Category: category, Name: s.Name, Duration: s.Duration,
		PriceFrom: s.PriceFrom, SortOrder: s.SortOrder, UpdatedAt: s.At,
	})
}

// OpeningHours is a branch's weekly opening hours as of Version, as
// scheduling's events describe them: each of Open is [start, end) in
// minutes after Sunday 00:00 in the branch's own time zone. Its fields
// match scheduling.OpeningHoursChanged one for one, so main converts one to
// the other with a plain conversion.
type OpeningHours struct {
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
	Version    int
	Open       [][2]int
	At         time.Time
}

// KeepHours updates discovery's copy of a branch's opening hours. An older
// version than the copy's changes nothing, like KeepBranch.
func (m *Module) KeepHours(ctx context.Context, h OpeningHours) error {
	return m.uc.KeepHours(ctx, domain.OpeningHours{
		Branch: h.BranchID, Business: h.BusinessID, Version: h.Version, Open: h.Open, UpdatedAt: h.At,
	})
}
