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
