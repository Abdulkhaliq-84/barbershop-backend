package clock_test

import (
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
)

func TestFake(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	c := clock.NewFake(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now() = %v, want %v", c.Now(), start)
	}

	c.Advance(5 * time.Minute)
	if got := c.Now(); !got.Equal(start.Add(5 * time.Minute)) {
		t.Errorf("after Advance: %v", got)
	}

	riyadh := time.FixedZone("AST", 3*60*60)
	c.Set(time.Date(2026, 10, 1, 19, 0, 0, 0, riyadh))
	if got := c.Now(); got.Location() != time.UTC || !got.Equal(start) {
		t.Errorf("Set should store UTC: got %v", got)
	}
}

func TestSystemIsUTC(t *testing.T) {
	t.Parallel()

	if loc := (clock.System{}).Now().Location(); loc != time.UTC {
		t.Errorf("System.Now() location = %v, want UTC", loc)
	}
}
