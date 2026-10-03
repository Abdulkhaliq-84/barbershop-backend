package domain

import (
	"cmp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// AppointmentTag marks appointment IDs. Notifications refer to them too, so
// the marker lives in shared.
type AppointmentTag = shared.AppointmentTag

// AppointmentID identifies an appointment.
type AppointmentID = shared.AppointmentID

// Status is where an appointment is in its life (domain-model.md §3.5).
type Status string

// Appointment statuses. Pending and Confirmed are "active": they hold the
// barber's time.
const (
	StatusPending   Status = "pending"
	StatusConfirmed Status = "confirmed"
	StatusRejected  Status = "rejected"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
	StatusCompleted Status = "completed"
	StatusNoShow    Status = "no_show"
)

// Source is who made the booking.
type Source string

// Booking sources.
const (
	SourceCustomerApp Source = "customer_app"
	SourceStaff       Source = "staff"
)

// Assignment is how the barber was chosen.
type Assignment string

// Assignments.
const (
	RequestedBarber Assignment = "requested_barber"
	AnyBarber       Assignment = "any_barber"
)

// MaxNoteLen is how long a customer's note may be, in characters.
const MaxNoteLen = 300

// MaxCustomerNameLen is how long a walk-in customer's name may be.
const MaxCustomerNameLen = 100

// Item is one booked service, as it was when booked: a later price change
// doesn't reach it.
type Item struct {
	Service  shared.ServiceID
	Name     shared.LocalizedText
	Duration time.Duration
	Price    shared.Money
}

// Booking is what a customer asks for, with the barber chosen and the
// branch's rules that apply.
type Booking struct {
	ID       AppointmentID
	Business shared.BusinessID
	Branch   shared.BranchID
	Barber   shared.StaffID
	// Customer is who booked in the app. A staff booking may leave it zero —
	// a walk-in without an account — and give CustomerName instead.
	Customer     shared.UserID
	CustomerName string
	Source       Source // SourceCustomerApp if empty
	Start        time.Time
	Items        []Item
	Buffer       time.Duration // after the services; the barber's, not the customer's
	Assignment   Assignment
	Note         string
	// AutoConfirm books it confirmed; otherwise it waits for the shop until
	// PendingExpiry has passed.
	AutoConfirm   bool
	PendingExpiry time.Duration
	// CancellationWindow: the customer may cancel a confirmed booking until
	// this long before it starts. The deadline is kept with the booking.
	CancellationWindow time.Duration
}

// Appointment is a booked visit: one barber, one or more services back to
// back, at one branch.
type Appointment struct {
	id           AppointmentID
	business     shared.BusinessID
	branch       shared.BranchID
	barber       shared.StaffID
	customer     shared.UserID // zero for a walk-in
	customerName string        // a walk-in's, given by the shop
	items        []Item
	start, end   time.Time // the services; the buffer follows end
	busyUntil    time.Time // end + buffer: when the barber is free again
	price        shared.Money
	status       Status
	source       Source
	assignment   Assignment
	note         string
	pendingUntil *time.Time
	// cancellableUntil is the customer's deadline to cancel once confirmed:
	// start − the branch's window when it was booked.
	cancellableUntil time.Time
	cancellation     *Cancellation
	version          int
	createdAt        time.Time
	updatedAt        time.Time
	events           []Event
}

// Book makes an appointment: a customer's in the app, confirmed at once or
// pending the shop's answer, or the shop's own (always confirmed), perhaps
// for a walk-in. Whether the barber is free is not checked here — the
// database decides that (ADR-0008).
func Book(b Booking, now time.Time) (*Appointment, error) {
	if len(b.Items) == 0 || len(b.Items) > MaxServices {
		return nil, ErrNoServices
	}
	if utf8.RuneCountInString(b.Note) > MaxNoteLen {
		return nil, ErrNoteTooLong
	}
	source := cmp.Or(b.Source, SourceCustomerApp)
	name := strings.TrimSpace(b.CustomerName)
	switch {
	case utf8.RuneCountInString(name) > MaxCustomerNameLen:
		return nil, ErrCustomerNameTooLong
	case b.Customer.IsZero() && (source != SourceStaff || name == ""):
		return nil, ErrNoCustomer // only the shop books walk-ins, by name
	}
	now = dbTime(now)
	a := &Appointment{
		id: b.ID, business: b.Business, branch: b.Branch, barber: b.Barber, customer: b.Customer, customerName: name,
		items: b.Items, start: b.Start.UTC(), status: StatusConfirmed, source: source,
		assignment: b.Assignment, note: b.Note, cancellableUntil: b.Start.UTC().Add(-max(b.CancellationWindow, 0)),
		version: 1, createdAt: now, updatedAt: now,
	}
	total := time.Duration(0)
	a.price = shared.Halalas(0)
	for _, it := range b.Items {
		total += it.Duration
		p, err := a.price.Add(it.Price)
		if err != nil {
			return nil, err
		}
		a.price = p
	}
	a.end = a.start.Add(total)
	a.busyUntil = a.end.Add(b.Buffer)
	if !b.AutoConfirm && source == SourceCustomerApp { // the shop's own bookings need no answer
		a.status = StatusPending
		a.pendingUntil = new(now.Add(b.PendingExpiry))
	}
	a.events = append(a.events, AppointmentBooked{Appointment: a.snapshot()})
	return a, nil
}

// Rehydrate rebuilds an appointment loaded from storage.
func Rehydrate(s Snapshot) *Appointment {
	return &Appointment{
		id: s.ID, business: s.Business, branch: s.Branch, barber: s.Barber, customer: s.Customer, customerName: s.CustomerName,
		items: s.Items, start: s.Start, end: s.End, busyUntil: s.BusyUntil, price: s.Price,
		status: s.Status, source: s.Source, assignment: s.Assignment, note: s.Note, pendingUntil: s.PendingUntil,
		cancellableUntil: s.CancellableUntil, cancellation: s.Cancellation,
		version: s.Version, createdAt: s.CreatedAt, updatedAt: s.UpdatedAt,
	}
}

// Snapshot is an appointment's state as plain values, for storage and events.
type Snapshot struct {
	ID           AppointmentID
	Business     shared.BusinessID
	Branch       shared.BranchID
	Barber       shared.StaffID
	Customer     shared.UserID // zero for a walk-in
	CustomerName string        // a walk-in's
	Items        []Item
	Start, End   time.Time
	BusyUntil    time.Time
	Price        shared.Money
	Status       Status
	Source       Source
	Assignment   Assignment
	Note         string
	PendingUntil *time.Time
	// CancellableUntil is the customer's deadline to cancel once confirmed.
	CancellableUntil time.Time
	Cancellation     *Cancellation // set once cancelled
	Version          int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (a *Appointment) snapshot() Snapshot {
	return Snapshot{
		ID: a.id, Business: a.business, Branch: a.branch, Barber: a.barber, Customer: a.customer, CustomerName: a.customerName,
		Items: a.items, Start: a.start, End: a.end, BusyUntil: a.busyUntil, Price: a.price,
		Status: a.status, Source: a.source, Assignment: a.assignment, Note: a.note, PendingUntil: a.pendingUntil,
		CancellableUntil: a.cancellableUntil, Cancellation: a.cancellation,
		Version: a.version, CreatedAt: a.createdAt, UpdatedAt: a.updatedAt,
	}
}

// Snapshot returns the appointment's state as plain values.
func (a *Appointment) Snapshot() Snapshot { return a.snapshot() }

// ID returns the appointment's ID.
func (a *Appointment) ID() AppointmentID { return a.id }

// Business returns the business it was booked at.
func (a *Appointment) Business() shared.BusinessID { return a.business }

// Branch returns the branch it was booked at.
func (a *Appointment) Branch() shared.BranchID { return a.branch }

// Barber returns who performs it.
func (a *Appointment) Barber() shared.StaffID { return a.barber }

// Customer returns who booked it.
func (a *Appointment) Customer() shared.UserID { return a.customer }

// Busy returns the time it holds the barber: the services and the buffer.
func (a *Appointment) Busy() shared.Interval {
	i, _ := shared.NewInterval(a.start, a.busyUntil) // Book made sure it isn't empty
	return i
}

// Status returns where the appointment is.
func (a *Appointment) Status() Status { return a.status }

// Events returns what happened since it was built or loaded, for the
// repository to publish together with the change.
func (a *Appointment) Events() []Event { return a.events }

// Event is something that happened to an appointment that other modules
// may react to (notifications, M7).
type Event interface{ isEvent() }

// AppointmentBooked is recorded when a customer, or the shop, books.
type AppointmentBooked struct {
	Appointment Snapshot
}

func (AppointmentBooked) isEvent() {}

// StatusChanged is recorded when an appointment moves on: confirmed,
// rejected, cancelled, completed or marked a no-show. Appointment is the
// state after the change.
type StatusChanged struct {
	From        Status
	Appointment Snapshot
}

func (StatusChanged) isEvent() {}
