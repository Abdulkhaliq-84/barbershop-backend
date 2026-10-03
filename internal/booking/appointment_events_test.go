package booking

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestDecodeAppointmentChanged(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	starts := at.Add(26 * time.Hour)
	appt, biz, branch, barber, customer := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	event := func(eventType string, payload any) outbox.Event {
		t.Helper()
		e, err := outbox.NewEvent(eventType, at, payload)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	booked := event(events.TypeAppointmentBooked, events.AppointmentBooked{
		AppointmentID: appt, BusinessID: biz, BranchID: branch, BarberID: barber, CustomerID: &customer,
		Source: "customer_app", Status: "pending", StartsAt: starts, EndsAt: starts.Add(30 * time.Minute),
	})
	got, err := decodeAppointmentChanged(booked)
	if err != nil {
		t.Fatal(err)
	}
	want := AppointmentChanged{
		EventID: booked.ID, AppointmentID: shared.IDFromUUID[shared.AppointmentTag](appt),
		BusinessID: shared.IDFromUUID[shared.BusinessTag](biz), BranchID: shared.IDFromUUID[shared.BranchTag](branch),
		BarberID: shared.IDFromUUID[shared.StaffTag](barber), CustomerID: shared.IDFromUUID[shared.UserTag](customer),
		What: "booked", Status: "pending", StartsAt: starts, At: at,
	}
	if got != want {
		t.Errorf("booked: %+v\nwant    %+v", got, want)
	}

	// A walk-in the shop cancelled: no customer, and who cancelled it.
	cancelled := event(events.TypeAppointmentCancelled, events.AppointmentStatusChanged{
		AppointmentID: appt, BusinessID: biz, BranchID: branch, BarberID: barber, From: "confirmed", Status: "cancelled",
		StartsAt: starts, EndsAt: starts.Add(30 * time.Minute), ChangedAt: at, CancelledBy: "staff", Reason: "closed early",
	})
	got, err = decodeAppointmentChanged(cancelled)
	if err != nil {
		t.Fatal(err)
	}
	if got.What != "cancelled" || got.Status != "cancelled" || got.CancelledBy != "staff" || !got.CustomerID.IsZero() || got.EventID != cancelled.ID {
		t.Errorf("cancelled: %+v", got)
	}

	for _, typ := range []string{
		events.TypeAppointmentConfirmed, events.TypeAppointmentRejected, events.TypeAppointmentCompleted,
		events.TypeAppointmentNoShow, events.TypeAppointmentExpired,
	} {
		e := event(typ, events.AppointmentStatusChanged{AppointmentID: appt, BranchID: branch, Status: "x", StartsAt: starts})
		if got, err := decodeAppointmentChanged(e); err != nil || "booking.appointment_"+got.What != typ {
			t.Errorf("%s: what %q, %v", typ, got.What, err)
		}
	}

	// A broken payload is an error: the outbox retries it.
	for name, p := range map[string]events.AppointmentStatusChanged{
		"no appointment": {BranchID: branch, Status: "confirmed", StartsAt: starts},
		"no branch":      {AppointmentID: appt, Status: "confirmed", StartsAt: starts},
		"no status":      {AppointmentID: appt, BranchID: branch, StartsAt: starts},
		"no start":       {AppointmentID: appt, BranchID: branch, Status: "confirmed"},
	} {
		if _, err := decodeAppointmentChanged(event(events.TypeAppointmentConfirmed, p)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := decodeAppointmentChanged(outbox.Event{Type: events.TypeAppointmentConfirmed, Payload: []byte(`{"appointment_id":`)}); err == nil {
		t.Error("broken JSON was accepted")
	}
}
