// Package billing is the plans module: what each business's plan allows,
// and its subscription (docs/architecture/domain-model.md §3.7, ADR-0019).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). billing depends on no other module: it learns about approved
// businesses from events that main subscribes it to.
package billing

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Logger *slog.Logger
}

// Module is the wired billing module.
type Module struct {
	uc *app.Handlers
}

// New wires the module.
func New(d Deps) *Module {
	return &Module{uc: app.NewHandlers(postgres.NewSubscriptions(d.Pool), d.Clock)}
}

// Statuses a Standing can have.
const (
	StatusSetup    = "setup"    // not approved yet: sets up under the trial plan's limits
	StatusTrialing = "trialing" // free trial of the top plan
	StatusFree     = "free"     // trial over, on the Free plan
)

// Standing is what a business may do now.
type Standing struct {
	Plan        string // plan code: free, pro
	PlanName    shared.LocalizedText
	Status      string
	TrialEndsAt *time.Time
	MaxBranches int
	MaxStaff    int // managers and barbers; the owner is free
}

// Standing returns the business's current plan and limits.
func (m *Module) Standing(ctx context.Context, business shared.BusinessID) (Standing, error) {
	s, err := m.uc.Standing(ctx, business)
	if err != nil {
		return Standing{}, err
	}
	return Standing{
		Plan: string(s.Plan.Code), PlanName: s.Plan.Name, Status: string(s.Status), TrialEndsAt: s.TrialEndsAt,
		MaxBranches: s.Plan.MaxBranches, MaxStaff: s.Plan.MaxStaff,
	}, nil
}

// StartTrial starts the free trial of a business approved at approvedAt.
// Idempotent, so it can handle an at-least-once event.
func (m *Module) StartTrial(ctx context.Context, business shared.BusinessID, approvedAt time.Time) error {
	return m.uc.StartTrial(ctx, business, approvedAt)
}
