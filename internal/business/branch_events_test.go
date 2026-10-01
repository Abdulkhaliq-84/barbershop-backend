package business

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestDecodeBranchChanged(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	biz, branch := uuid.New(), uuid.New()
	state := events.Branch{
		Version: 3, Status: "published", Name: events.LocalizedText{Ar: "فرع العليا", En: "Olaya"},
		CityCode: "riyadh", District: "العليا", Address: "شارع العليا العام",
		Location: events.Location{Latitude: 24.6911, Longitude: 46.6851}, Phone: "+966551234567", Timezone: "Asia/Riyadh",
	}
	event := func(eventType string, payload any) outbox.Event {
		t.Helper()
		e, err := outbox.NewEvent(eventType, at, payload)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	name, _ := shared.NewLocalizedText("فرع العليا", "Olaya")
	location, _ := shared.NewGeoPoint(24.6911, 46.6851)
	want := BranchChanged{
		BusinessID: shared.IDFromUUID[shared.BusinessTag](biz), BranchID: shared.IDFromUUID[shared.BranchTag](branch),
		Version: 3, Published: true, Name: name, City: "riyadh", District: "العليا", Address: "شارع العليا العام",
		Location: location, Phone: "+966551234567", Timezone: "Asia/Riyadh", At: at,
	}
	hidden := state
	hidden.Status = "unpublished"
	wantHidden := want
	wantHidden.Published = false

	for name, tt := range map[string]struct {
		event outbox.Event
		want  BranchChanged
	}{
		"published":   {event(events.TypeBranchPublished, events.BranchPublished{BusinessID: biz, BranchID: branch, PublishedAt: at, Branch: state}), want},
		"updated":     {event(events.TypeBranchUpdated, events.BranchUpdated{BusinessID: biz, BranchID: branch, UpdatedAt: at, Branch: state}), want},
		"unpublished": {event(events.TypeBranchUnpublished, events.BranchUnpublished{BusinessID: biz, BranchID: branch, UnpublishedAt: at, Branch: hidden}), wantHidden},
	} {
		if got, ok, err := decodeBranchChanged(tt.event); err != nil || !ok || got != tt.want {
			t.Errorf("%s: %+v, %v, %v; want %+v", name, got, ok, err, tt.want)
		}
	}

	// Queued before events carried the branch: skipped, not an error.
	old := event(events.TypeBranchPublished, events.BranchPublished{BusinessID: biz, BranchID: branch, PublishedAt: at})
	if _, ok, err := decodeBranchChanged(old); ok || err != nil {
		t.Errorf("an event from before M6.1: ok %v, %v", ok, err)
	}
	// A broken payload is an error: the outbox retries it.
	bad := state
	bad.Location.Latitude = 200
	if _, _, err := decodeBranchChanged(event(events.TypeBranchUpdated, events.BranchUpdated{BranchID: branch, Branch: bad})); err == nil {
		t.Error("a location off the globe was accepted")
	}
}
