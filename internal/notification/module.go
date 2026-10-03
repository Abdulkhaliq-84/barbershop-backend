// Package notification tells people what happened, on their phones
// (docs/architecture/domain-model.md §3.8, ADR-0033).
//
// It keeps the devices that receive pushes, a copy of what a message says
// about each branch (its name and time zone, from business's events), and
// the log of what was sent. main subscribes it to booking's and business's
// events; it depends on no other module.
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; domain, app and adapters are private (lint
// rules).
package notification

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/adapters/push"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Logger *slog.Logger
	// Push delivers notifications; nil = the development console sender
	// (PUSH_PROVIDER=console, refused in production).
	Push app.PushSender
}

// Module is the wired notification module.
type Module struct {
	uc   *app.Handlers
	http *httpapi.Handlers
}

// New wires the module.
func New(d Deps) *Module {
	sender := d.Push
	if sender == nil {
		sender = push.NewConsole(d.Logger)
	}
	uc := app.NewHandlers(postgres.NewStore(d.Pool), sender, d.Clock)
	return &Module{uc: uc, http: httpapi.NewHandlers(uc, d.Logger)}
}

// HTTP returns the handlers for the notification API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// Branch is a branch as of Version, as business's events describe it. Its
// fields match business.BranchChanged one for one, so main converts one to
// the other with a plain conversion. Messages use its name and time zone.
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

// KeepBranch updates notification's copy of a branch, published or not
// (its bookings still need its name). An older version than the copy's
// changes nothing.
func (m *Module) KeepBranch(ctx context.Context, b Branch) error {
	return m.uc.KeepBranch(ctx, domain.Branch{ID: b.BranchID, Version: b.Version, Name: b.Name, Timezone: b.Timezone})
}

// AppointmentChanged is something that happened to an appointment, as
// booking's events describe it. Its fields match booking.AppointmentChanged
// one for one, so main converts one to the other with a plain conversion.
type AppointmentChanged struct {
	EventID       uuid.UUID
	AppointmentID shared.AppointmentID
	BusinessID    shared.BusinessID
	BranchID      shared.BranchID
	BarberID      shared.StaffID
	CustomerID    shared.UserID // zero for a walk-in
	What          string        // booked, confirmed, rejected, cancelled, completed, no_show or expired
	Status        string
	CancelledBy   string
	StartsAt      time.Time
	At            time.Time
}

// AppointmentChanged pushes the customer what they should hear about it, if
// anything. Safe to call twice for one event: a device already reached is
// skipped.
func (m *Module) AppointmentChanged(ctx context.Context, a AppointmentChanged) error {
	return m.uc.Notify(ctx, app.AppointmentChange{
		Event: a.EventID, Appointment: a.AppointmentID, Branch: a.BranchID, Customer: a.CustomerID,
		What: a.What, Status: a.Status, CancelledBy: a.CancelledBy, StartsAt: a.StartsAt,
	})
}
