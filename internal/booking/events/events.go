// Package events is the booking module's event contract: the JSON other
// modules receive through the outbox (ADR-0009). Renaming or removing a
// field is a breaking change for every subscriber; add fields instead.
package events

import (
	"time"

	"github.com/google/uuid"
)

// TypeAppointmentBooked is published when a customer books (notifications
// tell the barber, M7).
const TypeAppointmentBooked = "booking.appointment_booked"

// Lifecycle changes (ADR-0025), each with an AppointmentStatusChanged
// payload: notifications tell the customer or the barber (M7).
const (
	TypeAppointmentConfirmed = "booking.appointment_confirmed"
	TypeAppointmentRejected  = "booking.appointment_rejected"
	TypeAppointmentCancelled = "booking.appointment_cancelled"
	TypeAppointmentCompleted = "booking.appointment_completed"
	TypeAppointmentNoShow    = "booking.appointment_no_show"
)

// AppointmentBooked is the payload of booking.appointment_booked.
type AppointmentBooked struct {
	AppointmentID uuid.UUID `json:"appointment_id"`
	BusinessID    uuid.UUID `json:"business_id"`
	BranchID      uuid.UUID `json:"branch_id"`
	BarberID      uuid.UUID `json:"barber_id"` // a staff ID
	CustomerID    uuid.UUID `json:"customer_id"`
	Status        string    `json:"status"` // pending or confirmed
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
}

// AppointmentStatusChanged is the payload of the lifecycle events.
type AppointmentStatusChanged struct {
	AppointmentID uuid.UUID `json:"appointment_id"`
	BusinessID    uuid.UUID `json:"business_id"`
	BranchID      uuid.UUID `json:"branch_id"`
	BarberID      uuid.UUID `json:"barber_id"` // a staff ID
	CustomerID    uuid.UUID `json:"customer_id"`
	From          string    `json:"from"`   // the status before
	Status        string    `json:"status"` // the status now
	StartsAt      time.Time `json:"starts_at"`
	EndsAt        time.Time `json:"ends_at"`
	ChangedAt     time.Time `json:"changed_at"`
	CancelledBy   string    `json:"cancelled_by,omitempty"` // customer or staff, when cancelled
	Reason        string    `json:"reason,omitempty"`       // the cancellation's, if one was given
}
