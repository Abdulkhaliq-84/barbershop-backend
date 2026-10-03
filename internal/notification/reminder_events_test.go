package notification

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
)

func TestDecodeReminder(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	full := events.ReminderDue{AppointmentID: uuid.New(), CustomerID: uuid.New(), BranchID: uuid.New(), StartsAt: at.Add(40 * time.Minute)}
	event := func(p events.ReminderDue) outbox.Event {
		t.Helper()
		e, err := outbox.NewEvent(events.TypeReminderDue, at, p)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := event(full)
	r, err := decodeReminder(e)
	if err != nil {
		t.Fatal(err)
	}
	if r.Event != e.ID || r.Appointment.UUID() != full.AppointmentID || r.Customer.UUID() != full.CustomerID ||
		r.Branch.UUID() != full.BranchID || !r.StartsAt.Equal(full.StartsAt) {
		t.Errorf("decoded %+v from %+v", r, full)
	}
	// A broken payload is an error: the outbox retries it (and logs it).
	for name, p := range map[string]events.ReminderDue{
		"no appointment": {CustomerID: full.CustomerID, BranchID: full.BranchID, StartsAt: full.StartsAt},
		"no customer":    {AppointmentID: full.AppointmentID, BranchID: full.BranchID, StartsAt: full.StartsAt},
		"no branch":      {AppointmentID: full.AppointmentID, CustomerID: full.CustomerID, StartsAt: full.StartsAt},
		"no start":       {AppointmentID: full.AppointmentID, CustomerID: full.CustomerID, BranchID: full.BranchID},
	} {
		if _, err := decodeReminder(event(p)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := decodeReminder(outbox.Event{Type: events.TypeReminderDue, Payload: []byte(`{"appointment_id":`)}); err == nil {
		t.Error("broken JSON was accepted")
	}
}
