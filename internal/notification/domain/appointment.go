package domain

import (
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// ReminderLead is how long before an appointment its customer is reminded.
const ReminderLead = time.Hour

// AppointmentStatus is where a booking is, as far as reminders care.
type AppointmentStatus string

// Appointment statuses, in the only order they move.
const (
	AppointmentPending   AppointmentStatus = "pending"   // waiting for the shop
	AppointmentConfirmed AppointmentStatus = "confirmed" // on: remind the customer
	AppointmentClosed    AppointmentStatus = "closed"    // over: rejected, cancelled, expired, completed or no-show
)

// AppointmentStatusOf maps booking's status to notification's, if it knows
// it.
func AppointmentStatusOf(bookingStatus string) (AppointmentStatus, bool) {
	switch bookingStatus {
	case "pending":
		return AppointmentPending, true
	case "confirmed":
		return AppointmentConfirmed, true
	case "rejected", "cancelled", "expired", "completed", "no_show":
		return AppointmentClosed, true
	}
	return "", false
}

// Appointment is notification's copy of a customer's booking: what a
// reminder needs. Booking's events can arrive twice and in any order, so a
// copy's status only moves forward (pending → confirmed → closed).
//
// A customer is reminded ReminderLead before StartsAt, if the booking is
// confirmed then and was confirmed at least ReminderLead before it starts:
// someone who books for half an hour from now just got "Booking
// confirmed", and needs no reminder.
type Appointment struct {
	ID          shared.AppointmentID
	Customer    shared.UserID
	Branch      shared.BranchID
	StartsAt    time.Time
	Status      AppointmentStatus
	ConfirmedAt time.Time // when it was confirmed; zero if it never was
}
