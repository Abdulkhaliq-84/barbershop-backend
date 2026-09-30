package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
)

func iv(day time.Weekday, start string, minutes int) domain.WeeklyInterval {
	t, err := time.Parse("15:04", start)
	if err != nil {
		panic(err)
	}
	return domain.WeeklyInterval{Day: day, Start: t.Hour()*60 + t.Minute(), Minutes: minutes}
}

func TestWeeklyHours(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []domain.WeeklyInterval
		want error
	}{
		{"closed all week", nil, nil},
		{"split shift", []domain.WeeklyInterval{iv(time.Sunday, "09:00", 180), iv(time.Sunday, "16:00", 360)}, nil},
		{"touching, not overlapping", []domain.WeeklyInterval{iv(time.Monday, "09:00", 60), iv(time.Monday, "10:00", 60)}, nil},
		{"Thursday night into Friday", []domain.WeeklyInterval{iv(time.Thursday, "16:00", 600), iv(time.Friday, "02:00", 60)}, nil},
		{"all day, every day", func() []domain.WeeklyInterval {
			var all []domain.WeeklyInterval
			for d := time.Sunday; d <= time.Saturday; d++ {
				all = append(all, iv(d, "00:00", 1440))
			}
			return all
		}(), nil},
		{"overlap on one day", []domain.WeeklyInterval{iv(time.Monday, "09:00", 120), iv(time.Monday, "10:00", 60)}, domain.ErrOverlappingIntervals},
		{"past midnight into the next day's opening", []domain.WeeklyInterval{iv(time.Thursday, "16:00", 600), iv(time.Friday, "01:00", 60)}, domain.ErrOverlappingIntervals},
		{"Saturday night into Sunday morning", []domain.WeeklyInterval{iv(time.Saturday, "20:00", 360), iv(time.Sunday, "01:00", 60)}, domain.ErrOverlappingIntervals},
		{"the same interval twice", []domain.WeeklyInterval{iv(time.Monday, "09:00", 60), iv(time.Monday, "09:00", 60)}, domain.ErrOverlappingIntervals},
		{"not a weekday", []domain.WeeklyInterval{{Day: 7, Start: 0, Minutes: 60}}, domain.ErrInvalidWeekday},
		{"off the 5-minute grid", []domain.WeeklyInterval{{Day: time.Monday, Start: 541, Minutes: 60}}, domain.ErrInvalidInterval},
		{"odd length", []domain.WeeklyInterval{{Day: time.Monday, Start: 540, Minutes: 61}}, domain.ErrInvalidInterval},
		{"empty", []domain.WeeklyInterval{{Day: time.Monday, Start: 540}}, domain.ErrInvalidInterval},
		{"longer than a day", []domain.WeeklyInterval{{Day: time.Monday, Start: 0, Minutes: 1445}}, domain.ErrInvalidInterval},
		{"starts at midnight's end", []domain.WeeklyInterval{{Day: time.Monday, Start: 1440, Minutes: 60}}, domain.ErrInvalidInterval},
		{"five on one day", []domain.WeeklyInterval{
			iv(time.Monday, "01:00", 30), iv(time.Monday, "03:00", 30), iv(time.Monday, "05:00", 30),
			iv(time.Monday, "07:00", 30), iv(time.Monday, "09:00", 30),
		}, domain.ErrTooManyIntervals},
	}
	for _, tt := range tests {
		_, err := domain.NewWeeklyHours(tt.in)
		if (tt.want == nil && err != nil) || (tt.want != nil && !errors.Is(err, tt.want)) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestWeeklyHoursAreSorted(t *testing.T) {
	t.Parallel()
	w, err := domain.NewWeeklyHours([]domain.WeeklyInterval{iv(time.Friday, "16:00", 60), iv(time.Sunday, "09:00", 60), iv(time.Sunday, "08:00", 30)})
	if err != nil {
		t.Fatal(err)
	}
	got := w.Intervals()
	if got[0] != iv(time.Sunday, "08:00", 30) || got[1] != iv(time.Sunday, "09:00", 60) || got[2] != iv(time.Friday, "16:00", 60) {
		t.Errorf("order = %+v", got)
	}
	got[0].Minutes = 999 // a copy
	if w.Intervals()[0].Minutes != 30 || w.IsClosed() {
		t.Error("Intervals returned the hours' own slice")
	}
	if (domain.WeeklyHours{}).IsClosed() != true {
		t.Error("zero value is not closed")
	}
}

func TestClock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		opens, closes int
		wantMinutes   int
		wantCloses    int // as Clock reports it
	}{
		{"daytime", 9 * 60, 13 * 60, 240, 13 * 60},
		{"past midnight", 16 * 60, 2 * 60, 600, 2 * 60},
		{"until midnight as 24:00", 18 * 60, 24 * 60, 360, 24 * 60},
		{"until midnight as 00:00", 18 * 60, 0, 360, 24 * 60},
		{"all day", 0, 0, 1440, 24 * 60},
		{"all day as 24:00", 0, 24 * 60, 1440, 24 * 60},
		{"a full day from the afternoon", 16 * 60, 16 * 60, 1440, 16 * 60},
	}
	for _, tt := range tests {
		i := domain.IntervalFromClock(time.Thursday, tt.opens, tt.closes)
		if i.Day != time.Thursday || i.Start != tt.opens || i.Minutes != tt.wantMinutes {
			t.Errorf("%s: interval = %+v, want %d minutes", tt.name, i, tt.wantMinutes)
		}
		if o, c := i.Clock(); o != tt.opens || c != tt.wantCloses {
			t.Errorf("%s: Clock = %d–%d, want %d–%d", tt.name, o, c, tt.opens, tt.wantCloses)
		}
	}
}
