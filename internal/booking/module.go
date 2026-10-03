// Package booking is the appointments module: when customers can book,
// their bookings, and what happens to them after (docs/architecture/domain-model.md §3.5).
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules). booking combines business (branches, barbers), catalog (the menu)
// and scheduling (working windows) through adapters/acl. It publishes events
// through the outbox; OnAppointmentChanged is how other modules (wired in
// main) subscribe to them.
package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Logger     *slog.Logger
	Business   *business.Module   // bookable branches and their barbers
	Catalog    *catalog.Module    // the menu: who performs what, for how long, at what price
	Scheduling *scheduling.Module // working windows
	Events     *outbox.Bus        // where booking's events are published, and its tasks scheduled
}

// Module is the wired booking module.
type Module struct {
	http *httpapi.Handlers
}

// New wires the repositories, use cases and HTTP handlers.
func New(d Deps) *Module {
	appointments := postgres.NewAppointments(d.Pool)
	availability := app.NewAvailabilityHandlers(
		acl.NewBranches(d.Business), acl.NewMenus(d.Catalog), acl.NewSchedules(d.Scheduling),
		appointments, d.Clock,
	)
	store := postgres.NewStore(appointments, d.Events)
	staff := acl.NewStaff(d.Business)
	book := app.NewBookHandlers(availability, store, staff)
	manage := app.NewManageHandlers(staff, acl.NewBranches(d.Business), store, d.Clock)
	// The worker role expires the bookings the shop didn't answer in time.
	d.Events.Every("booking.expire_pending", time.Minute, app.NewExpirer(store, d.Clock).Run)
	return &Module{http: httpapi.NewHandlers(availability, book, manage, d.Logger)}
}

// HTTP returns the handlers for the booking API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// AppointmentChanged is something that happened to an appointment: it was
// booked, or moved to a new status.
type AppointmentChanged struct {
	EventID       uuid.UUID // the event's own ID: what to handle once
	AppointmentID shared.AppointmentID
	BusinessID    shared.BusinessID
	BranchID      shared.BranchID
	BarberID      shared.StaffID
	CustomerID    shared.UserID // zero for a walk-in the shop booked
	What          string        // booked, confirmed, rejected, cancelled, completed, no_show or expired
	Status        string        // the status now: pending, confirmed, …
	CancelledBy   string        // customer or staff, when cancelled
	StartsAt      time.Time
	At            time.Time // when it happened
}

// OnAppointmentChanged subscribes fn to every appointment event, under a
// stable name per event: name+".booked", name+".confirmed" and so on. An
// event can come more than once, and events about one appointment in any
// order: use EventID to handle each once.
func OnAppointmentChanged(bus *outbox.Bus, name string, fn func(ctx context.Context, e AppointmentChanged) error) {
	handle := func(ctx context.Context, e outbox.Event) error {
		a, err := decodeAppointmentChanged(e)
		if err != nil {
			return err
		}
		return fn(ctx, a)
	}
	for _, t := range []string{
		events.TypeAppointmentBooked, events.TypeAppointmentConfirmed, events.TypeAppointmentRejected,
		events.TypeAppointmentCancelled, events.TypeAppointmentCompleted, events.TypeAppointmentNoShow,
		events.TypeAppointmentExpired,
	} {
		bus.Subscribe(name+"."+what(t), t, handle)
	}
}

// what is an event type's last part: booking.appointment_no_show → no_show.
func what(eventType string) string {
	return strings.TrimPrefix(eventType, "booking.appointment_")
}

// decodeAppointmentChanged reads any appointment event. The booked and the
// status-changed payloads share the fields it reads.
func decodeAppointmentChanged(e outbox.Event) (AppointmentChanged, error) {
	var p events.AppointmentStatusChanged
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return AppointmentChanged{}, fmt.Errorf("decode %s %s: %w", e.Type, e.ID, err)
	}
	if p.AppointmentID == uuid.Nil || p.BranchID == uuid.Nil || p.Status == "" || p.StartsAt.IsZero() {
		return AppointmentChanged{}, fmt.Errorf("decode %s %s: incomplete", e.Type, e.ID)
	}
	a := AppointmentChanged{
		EventID:       e.ID,
		AppointmentID: shared.IDFromUUID[shared.AppointmentTag](p.AppointmentID),
		BusinessID:    shared.IDFromUUID[shared.BusinessTag](p.BusinessID),
		BranchID:      shared.IDFromUUID[shared.BranchTag](p.BranchID),
		BarberID:      shared.IDFromUUID[shared.StaffTag](p.BarberID),
		What:          what(e.Type), Status: p.Status, CancelledBy: p.CancelledBy,
		StartsAt: p.StartsAt, At: e.OccurredAt,
	}
	if p.CustomerID != nil {
		a.CustomerID = shared.IDFromUUID[shared.UserTag](*p.CustomerID)
	}
	return a, nil
}
