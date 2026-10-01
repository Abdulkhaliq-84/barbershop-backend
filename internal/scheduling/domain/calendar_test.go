package domain_test

import (
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Setting the hours records one event carrying them as of the new version.
func TestOpeningHoursEvents(t *testing.T) {
	t.Parallel()
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	cal := domain.NewBranchCalendar(business, branch)
	if len(cal.Events()) != 0 {
		t.Fatal("a new calendar has events")
	}
	week, err := domain.NewWeeklyHours([]domain.WeeklyInterval{{Day: time.Thursday, Start: 16 * 60, Minutes: 600}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 9, 0, 0, 123456789, time.UTC)
	cal.SetOpeningHours(week, at)
	cal.SetOpeningHours(domain.WeeklyHours{}, at.Add(time.Hour))
	events := cal.Events()
	if len(events) != 2 {
		t.Fatalf("%d events, want 2", len(events))
	}
	first, ok1 := events[0].(domain.OpeningHoursChangedEvent)
	second, ok2 := events[1].(domain.OpeningHoursChangedEvent)
	if !ok1 || first.Business != business || first.Branch != branch || first.Version != 1 ||
		!first.At.Equal(at.Truncate(time.Microsecond)) || len(first.Hours.Intervals()) != 1 || first.Hours.Intervals()[0].Start != 16*60 {
		t.Errorf("first = %+v", events[0])
	}
	if !ok2 || second.Version != 2 || !second.Hours.IsClosed() {
		t.Errorf("second = %+v", events[1])
	}
}
