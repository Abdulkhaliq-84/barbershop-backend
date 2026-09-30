// Package scheduling is the "when can work happen" module: branch opening
// hours, barber schedules, time off, and the working windows they add up to
// (docs/architecture/domain-model.md §3.4, ADR-0021).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). scheduling asks business who may work on a branch, through
// adapters/acl.
package scheduling

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
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
	http    *httpapi.Handlers
	windows *app.WindowsHandlers
}

// Errors WorkingWindows returns, re-exported so callers never import
// scheduling/domain.
var (
	// ErrNotFound: the branch isn't the business's, or a staff member
	// doesn't work there.
	ErrNotFound = domain.ErrNotFound
	// ErrInvalidRange: to isn't after from, or the range is over 62 days.
	ErrInvalidRange = domain.ErrInvalidRange
)

// WorkingWindows returns when each staff member can work at the branch
// within [from, to): opening hours ∩ their schedule, minus time off — real
// instants, sorted and merged. Booking (M5) calls it after its own checks;
// it authorizes no caller itself.
func (m *Module) WorkingWindows(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, error) {
	w, err := m.windows.Windows(ctx, business, branch, staff, from, to)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrNotFound
	}
	return w, err
}

// New wires the repositories, use cases and HTTP handlers.
func New(d Deps) *Module {
	access := acl.NewBusinessAccess(d.Business)
	calendarStore, scheduleStore, timeOffStore := postgres.NewCalendars(d.Pool), postgres.NewSchedules(d.Pool), postgres.NewTimeOffs(d.Pool)
	calendars := app.NewCalendarHandlers(calendarStore, access, d.Clock)
	schedules := app.NewScheduleHandlers(scheduleStore, timeOffStore, access, d.Clock)
	windows := app.NewWindowsHandlers(calendarStore, scheduleStore, timeOffStore, access, schedules)
	return &Module{windows: windows, http: httpapi.NewHandlers(calendars, schedules, windows, d.Logger)}
}

// HTTP returns the handlers for the scheduling API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }
