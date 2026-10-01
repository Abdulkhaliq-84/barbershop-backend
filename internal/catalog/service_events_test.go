package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestDecodeServiceChanged(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	biz, branch, service := uuid.New(), uuid.New(), uuid.New()
	sar := func(n int64) events.Money { return events.Money{Amount: n, Currency: "SAR"} }
	state := events.Service{
		Version: 4, Active: true, CategoryCode: "beard", Name: events.LocalizedText{Ar: "تهذيب اللحية"},
		DurationMinutes: 20, Price: sar(4000), PriceFrom: new(sar(3500)),
	}
	event := func(eventType string, s events.Service) outbox.Event {
		t.Helper()
		e, err := outbox.NewEvent(eventType, at, events.ServiceChanged{BusinessID: biz, BranchID: branch, ServiceID: service, ChangedAt: at, Service: s})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	want := ServiceChanged{
		BusinessID: shared.IDFromUUID[shared.BusinessTag](biz), BranchID: shared.IDFromUUID[shared.BranchTag](branch),
		ServiceID: shared.IDFromUUID[shared.ServiceTag](service), Version: 4, Offered: true, Category: "beard",
		PriceFrom: shared.Halalas(3500), At: at,
	}
	off := state
	off.Active = false
	wantOff := want
	wantOff.Offered = false
	nobody := state
	nobody.PriceFrom = nil
	wantNobody := want
	wantNobody.Offered, wantNobody.PriceFrom = false, shared.Halalas(4000)

	for name, tt := range map[string]struct {
		event outbox.Event
		want  ServiceChanged
	}{
		"created":            {event(events.TypeServiceCreated, state), want},
		"updated":            {event(events.TypeServiceUpdated, state), want},
		"turned off":         {event(events.TypeServiceUpdated, off), wantOff},
		"nobody performs it": {event(events.TypeServiceUpdated, nobody), wantNobody},
	} {
		if got, err := decodeServiceChanged(tt.event); err != nil || got != tt.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, tt.want)
		}
	}

	// A broken payload is an error: the outbox retries it.
	noVersion := state
	noVersion.Version = 0
	dollars := state
	dollars.PriceFrom = &events.Money{Amount: 1000, Currency: "USD"}
	for name, s := range map[string]events.Service{"no version": noVersion, "an unknown currency": dollars} {
		if _, err := decodeServiceChanged(event(events.TypeServiceUpdated, s)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
