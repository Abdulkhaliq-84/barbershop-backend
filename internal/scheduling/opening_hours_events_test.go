package scheduling

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestDecodeOpeningHoursChanged(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	biz, branch := uuid.New(), uuid.New()
	event := func(c events.Calendar) outbox.Event {
		t.Helper()
		e, err := outbox.NewEvent(events.TypeOpeningHoursChanged, at, events.OpeningHoursChanged{BusinessID: biz, BranchID: branch, ChangedAt: at, Calendar: c})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	// Sunday 09:00–21:00, Thursday 16:00–02:00, Saturday 22:00–02:00.
	got, err := decodeOpeningHoursChanged(event(events.Calendar{Version: 3, Hours: []events.Interval{
		{Weekday: 6, StartMinute: 1320, Minutes: 240},
		{Weekday: 0, StartMinute: 540, Minutes: 720},
		{Weekday: 4, StartMinute: 960, Minutes: 600},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{540, 1260}, {4*1440 + 960, 4*1440 + 1560}, {6*1440 + 1320, 6*1440 + 1560}}
	if got.BusinessID != shared.IDFromUUID[shared.BusinessTag](biz) || got.BranchID != shared.IDFromUUID[shared.BranchTag](branch) ||
		got.Version != 3 || !got.At.Equal(at) || !slices.Equal(got.Open, want) {
		t.Errorf("decoded %+v, want open %v", got, want)
	}
	if want[2][1] <= MinutesPerWeek {
		t.Error("Saturday night should end past the week's end")
	}
	// Closed all week: nothing open.
	if got, err := decodeOpeningHoursChanged(event(events.Calendar{Version: 4})); err != nil || len(got.Open) != 0 {
		t.Errorf("closed: %+v, %v", got, err)
	}
	// A broken payload is an error: the outbox retries it.
	for name, c := range map[string]events.Calendar{
		"no version":    {Hours: []events.Interval{{Weekday: 0, StartMinute: 540, Minutes: 60}}},
		"day 7":         {Version: 1, Hours: []events.Interval{{Weekday: 7, StartMinute: 540, Minutes: 60}}},
		"25 hours":      {Version: 1, Hours: []events.Interval{{Weekday: 0, StartMinute: 0, Minutes: 1500}}},
		"overlapping":   {Version: 1, Hours: []events.Interval{{Weekday: 0, StartMinute: 540, Minutes: 120}, {Weekday: 0, StartMinute: 600, Minutes: 60}}},
		"off the clock": {Version: 1, Hours: []events.Interval{{Weekday: 0, StartMinute: 1440, Minutes: 60}}},
	} {
		if _, err := decodeOpeningHoursChanged(event(c)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
