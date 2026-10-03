// Package events is the notification module's own event contract: the JSON
// it queues for itself through the outbox (ADR-0009). Renaming or removing a
// field breaks reminders already queued; add fields instead.
package events

import (
	"time"

	"github.com/google/uuid"
)

// TypeReminderDue is queued when an appointment is due its reminder: one
// event per appointment, sent to the customer's devices by the module's
// own subscriber (ADR-0035).
const TypeReminderDue = "notification.reminder_due"

// ReminderDue is the payload of notification.reminder_due.
type ReminderDue struct {
	AppointmentID uuid.UUID `json:"appointment_id"`
	CustomerID    uuid.UUID `json:"customer_id"`
	BranchID      uuid.UUID `json:"branch_id"`
	StartsAt      time.Time `json:"starts_at"`
}
