package httpapi

import (
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
)

// A week shows as the owners' API shows it: seven days, Sunday first, each
// interval on the day it starts.
func TestToAPIWeek(t *testing.T) {
	t.Parallel()
	const day = 1440
	got := toAPIWeek([][2]int{
		{0*day + 9*60, 0*day + 21*60},            // Sunday 09:00–21:00
		{4*day + 16*60, 4*day + 26*60},           // Thursday 16:00 to Friday 02:00
		{5 * day, 6 * day},                       // all of Friday
		{6*day + 22*60, 6*day + 26*60},           // Saturday 22:00 to Sunday 02:00
		{2*day + 13*60 + 30, 2*day + 13*60 + 35}, // Tuesday, five minutes
	})
	want := map[apigen.Weekday][]apigen.TimeRange{
		"sunday":   {{Opens: "09:00", Closes: "21:00"}},
		"tuesday":  {{Opens: "13:30", Closes: "13:35"}},
		"thursday": {{Opens: "16:00", Closes: "02:00"}},
		"friday":   {{Opens: "00:00", Closes: "24:00"}},
		"saturday": {{Opens: "22:00", Closes: "02:00"}},
	}
	order := []apigen.Weekday{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	if len(got) != 7 {
		t.Fatalf("%d days", len(got))
	}
	for i, d := range got {
		if d.Weekday != order[i] || d.Intervals == nil || len(d.Intervals) != len(want[d.Weekday]) {
			t.Errorf("day %d = %+v, want %s %v", i, d, order[i], want[order[i]])
			continue
		}
		for j, r := range d.Intervals {
			if r != want[d.Weekday][j] {
				t.Errorf("%s: %+v, want %+v", d.Weekday, r, want[d.Weekday][j])
			}
		}
	}
	// Closed all week: seven empty days, not null.
	for _, d := range toAPIWeek(nil) {
		if d.Intervals == nil || len(d.Intervals) != 0 {
			t.Errorf("closed: %+v", d)
		}
	}
}
