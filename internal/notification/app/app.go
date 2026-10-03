// Package app holds notification's use cases: registering devices, keeping
// the branch copies messages need, and pushing notices when something
// happens to a booking.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Push is one notification to one device. Data travels with it to the app
// (what it's about, which appointment), not shown to the user.
type Push struct {
	Device domain.Device
	Title  string
	Body   string
	Data   map[string]string
}

// PushSender delivers pushes: the console in development, FCM later (M7.2).
type PushSender interface {
	Send(ctx context.Context, p Push) error
}

// Handlers are notification's use cases.
type Handlers struct {
	store domain.Store
	push  PushSender
	clock clock.Clock
}

// NewHandlers wires the use cases.
func NewHandlers(store domain.Store, push PushSender, clk clock.Clock) *Handlers {
	return &Handlers{store: store, push: push, clock: clk}
}

// RegisterDevice is the command to register an app install for pushes.
type RegisterDevice struct {
	User     shared.UserID
	Token    string
	Platform string
	Locale   shared.Language
}

// RegisterDevice registers the caller's device, or moves it to them if
// another user had it (two accounts on one phone: only the one signed in
// gets pushes), and refreshes its language.
func (h *Handlers) RegisterDevice(ctx context.Context, cmd RegisterDevice) (domain.Device, error) {
	token, err := domain.ParseToken(cmd.Token)
	if err != nil {
		return domain.Device{}, err
	}
	platform, err := domain.ParsePlatform(cmd.Platform)
	if err != nil {
		return domain.Device{}, err
	}
	now := h.clock.Now()
	d, err := h.store.SaveDevice(ctx, domain.Device{
		ID: shared.NewID[domain.DeviceTag](), User: cmd.User, Token: token, Platform: platform,
		Locale: cmd.Locale, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return domain.Device{}, fmt.Errorf("register device: %w", err)
	}
	return d, nil
}

// RemoveDevice unregisters the caller's device (signing out on it);
// domain.ErrNotFound if it isn't theirs.
func (h *Handlers) RemoveDevice(ctx context.Context, user shared.UserID, id domain.DeviceID) error {
	if err := h.store.RemoveDevice(ctx, user, id); err != nil {
		return fmt.Errorf("remove device: %w", err)
	}
	return nil
}

// KeepBranch applies a branch event to notification's copy: an older
// version than the copy's changes nothing.
func (h *Handlers) KeepBranch(ctx context.Context, b domain.Branch) error {
	if _, err := h.store.KeepBranch(ctx, b); err != nil {
		return fmt.Errorf("keep branch %s: %w", b.ID, err)
	}
	return nil
}

// AppointmentChange is something that happened to an appointment, as
// booking's events say.
type AppointmentChange struct {
	Event       uuid.UUID // the event's ID
	Appointment shared.AppointmentID
	Branch      shared.BranchID
	Customer    shared.UserID // zero for a walk-in
	What        string
	Status      string
	CancelledBy string
	StartsAt    time.Time
}

// Notify pushes the customer what they should hear about an appointment
// change, on each of their devices, in each device's language. An event
// can arrive more than once: a device it already reached is skipped. If a
// push fails, the others are still tried, and the error makes the outbox
// retry the event later.
func (h *Handlers) Notify(ctx context.Context, c AppointmentChange) error {
	kind, ok := domain.CustomerNotice(c.What, c.Status, c.CancelledBy)
	if !ok || c.Customer.IsZero() {
		return nil
	}
	branch, err := h.store.Branch(ctx, c.Branch)
	if err != nil {
		// Usually the branch's own event hasn't arrived yet: try again later.
		return fmt.Errorf("notify %s: %w", c.Appointment, err)
	}
	devices, err := h.store.Devices(ctx, c.Customer)
	if err != nil {
		return fmt.Errorf("notify %s: %w", c.Appointment, err)
	}
	var failed []error
	for _, d := range devices {
		if err := h.pushOnce(ctx, c, kind, branch, d); err != nil {
			failed = append(failed, err)
		}
	}
	if err := errors.Join(failed...); err != nil {
		return fmt.Errorf("notify %s: %w", c.Appointment, err)
	}
	return nil
}

func (h *Handlers) pushOnce(ctx context.Context, c AppointmentChange, kind domain.Kind, branch domain.Branch, d domain.Device) error {
	done, err := h.store.Delivered(ctx, c.Event, d.ID)
	if err != nil || done {
		return err
	}
	title, body, err := domain.Message(kind, d.Locale, branch, c.StartsAt)
	if err != nil {
		return err
	}
	err = h.push.Send(ctx, Push{Device: d, Title: title, Body: body, Data: map[string]string{
		"kind": string(kind), "appointment_id": c.Appointment.String(),
	}})
	if err != nil {
		return fmt.Errorf("push to device %s: %w", d.ID, err)
	}
	return h.store.RecordDelivery(ctx, domain.Delivery{
		Event: c.Event, Device: d.ID, User: d.User, Kind: kind, Appointment: c.Appointment, SentAt: h.clock.Now(),
	})
}
