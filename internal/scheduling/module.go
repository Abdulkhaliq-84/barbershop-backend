// Package scheduling is the "when can work happen" module: branch opening
// hours, barber schedules, time off, and the working windows they add up to
// (docs/architecture/domain-model.md §3.4, ADR-0021).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). scheduling asks business who may work on a branch, through
// adapters/acl. It publishes events through the outbox;
// OnOpeningHoursChanged is how other modules (wired in main) subscribe to
// them.
package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Logger   *slog.Logger
	Business *business.Module // who may work on a branch (through adapters/acl)
	Events   *outbox.Bus      // where the calendars' events are published
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

// Readiness says whether the branch has opening hours, and which of staff
// have a weekly schedule there — part of whether it can take a booking. It
// authorizes nobody: callers check first.
func (m *Module) Readiness(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) (openingHours bool, scheduled []shared.StaffID, err error) {
	return m.windows.Readiness(ctx, business, branch, staff)
}

// New wires the repositories, use cases and HTTP handlers.
func New(d Deps) *Module {
	access := acl.NewBusinessAccess(d.Business)
	calendarStore, scheduleStore, timeOffStore := postgres.NewCalendars(d.Pool, d.Events), postgres.NewSchedules(d.Pool), postgres.NewTimeOffs(d.Pool)
	calendars := app.NewCalendarHandlers(calendarStore, access, d.Clock)
	schedules := app.NewScheduleHandlers(scheduleStore, timeOffStore, access, d.Clock)
	windows := app.NewWindowsHandlers(calendarStore, scheduleStore, timeOffStore, access, schedules)
	return &Module{windows: windows, http: httpapi.NewHandlers(calendars, schedules, windows, d.Logger)}
}

// HTTP returns the handlers for the scheduling API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// OpeningHoursChanged is a branch's weekly opening hours as of Version, in
// the branch's own time zone. Each of Open is [start, end) in minutes after
// Sunday 00:00; one that runs past Saturday midnight ends after
// MinutesPerWeek. None: closed all week.
type OpeningHoursChanged struct {
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
	Version    int
	Open       [][2]int
	At         time.Time
}

// MinutesPerWeek is how many minutes a week has: Open's intervals start
// before it.
const MinutesPerWeek = domain.MinutesPerWeek

// OnOpeningHoursChanged subscribes fn, under a stable name, to changes of
// a branch's opening hours. An event can come more than once and out of
// order: keep the newest Version.
func OnOpeningHoursChanged(bus *outbox.Bus, name string, fn func(ctx context.Context, e OpeningHoursChanged) error) {
	bus.Subscribe(name, events.TypeOpeningHoursChanged, func(ctx context.Context, e outbox.Event) error {
		h, err := decodeOpeningHoursChanged(e)
		if err != nil {
			return err
		}
		return fn(ctx, h)
	})
}

// decodeOpeningHoursChanged reads the event, checking each interval as
// the domain would.
func decodeOpeningHoursChanged(e outbox.Event) (OpeningHoursChanged, error) {
	var p events.OpeningHoursChanged
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return OpeningHoursChanged{}, fmt.Errorf("decode %s %s: %w", e.Type, e.ID, err)
	}
	if p.Calendar.Version < 1 {
		return OpeningHoursChanged{}, fmt.Errorf("decode %s %s: no calendar version", e.Type, e.ID)
	}
	intervals := make([]domain.WeeklyInterval, 0, len(p.Calendar.Hours))
	for _, i := range p.Calendar.Hours {
		intervals = append(intervals, domain.WeeklyInterval{Day: time.Weekday(i.Weekday), Start: i.StartMinute, Minutes: i.Minutes})
	}
	week, err := domain.NewWeeklyHours(intervals)
	if err != nil {
		return OpeningHoursChanged{}, fmt.Errorf("decode %s %s: hours: %w", e.Type, e.ID, err)
	}
	open := make([][2]int, 0, len(intervals))
	for _, i := range week.Intervals() {
		start := int(i.Day)*domain.MinutesPerDay + i.Start
		open = append(open, [2]int{start, start + i.Minutes})
	}
	return OpeningHoursChanged{
		BusinessID: shared.IDFromUUID[shared.BusinessTag](p.BusinessID),
		BranchID:   shared.IDFromUUID[shared.BranchTag](p.BranchID),
		Version:    p.Calendar.Version, Open: open, At: e.OccurredAt,
	}, nil
}
