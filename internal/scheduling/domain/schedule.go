package domain

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MaxOverrides caps date overrides per schedule (about a year of them).
const MaxOverrides = 400

// DayInterval is an interval on one particular date: Start minutes after
// midnight, lasting Minutes; it may run past midnight.
type DayInterval struct {
	Start   int
	Minutes int
}

// CheckDayIntervals validates one date's intervals (the weekly rules, on a
// single day) and returns them sorted.
func CheckDayIntervals(intervals []DayInterval) ([]DayInterval, error) {
	weekly := make([]WeeklyInterval, 0, len(intervals))
	for _, i := range intervals {
		weekly = append(weekly, WeeklyInterval{Day: time.Sunday, Start: i.Start, Minutes: i.Minutes})
	}
	if _, err := NewWeeklyHours(weekly); err != nil {
		return nil, err
	}
	out := slices.Clone(intervals)
	slices.SortFunc(out, func(a, b DayInterval) int { return cmp.Compare(a.Start, b.Start) })
	return out, nil
}

// BarberSchedule is when one staff member works at one branch: a weekly
// template, and date overrides that replace the template for a date (other
// hours, or none at all — a day off). An override replaces only the
// intervals that start on its date; the night before's shift still runs
// past midnight into it.
type BarberSchedule struct {
	business  shared.BusinessID
	branch    shared.BranchID
	staff     shared.StaffID
	weekly    WeeklyHours
	overrides map[Date][]DayInterval // an empty slice: the day off
	version   int                    // 0 until first saved
	updatedAt time.Time
}

// NewBarberSchedule is a schedule never set: works no hours.
func NewBarberSchedule(business shared.BusinessID, branch shared.BranchID, staff shared.StaffID) *BarberSchedule {
	return &BarberSchedule{business: business, branch: branch, staff: staff, overrides: map[Date][]DayInterval{}}
}

// RehydrateBarberSchedule rebuilds a schedule loaded from storage.
func RehydrateBarberSchedule(business shared.BusinessID, branch shared.BranchID, staff shared.StaffID, weekly WeeklyHours, overrides map[Date][]DayInterval, version int, updatedAt time.Time) *BarberSchedule {
	return &BarberSchedule{
		business: business, branch: branch, staff: staff, weekly: weekly, overrides: maps.Clone(overrides),
		version: version, updatedAt: updatedAt,
	}
}

// Set replaces the weekly template and all date overrides. elsewhere are
// the same person's weekly templates at their other branches: a template
// that overlaps one of them would put them in two shops at once.
func (s *BarberSchedule) Set(weekly WeeklyHours, overrides map[Date][]DayInterval, elsewhere []WeeklyHours, now time.Time) error {
	if len(overrides) > MaxOverrides {
		return ErrTooManyOverrides
	}
	for _, other := range elsewhere {
		if weekly.Overlaps(other) {
			return ErrScheduleClash
		}
	}
	checked := make(map[Date][]DayInterval, len(overrides))
	for date, intervals := range overrides {
		if date.String() != DateOf(date.midnightUTC()).String() {
			return ErrInvalidDate // e.g. 31 February
		}
		ivs, err := CheckDayIntervals(intervals)
		if err != nil {
			return err
		}
		checked[date] = ivs
	}
	s.weekly, s.overrides = weekly, checked
	s.version++
	s.updatedAt = now.UTC().Truncate(time.Microsecond)
	return nil
}

// IntervalsStarting returns the intervals that start on date: the date's
// override if it has one, else the weekly template's intervals for its weekday.
func (s *BarberSchedule) IntervalsStarting(date Date) []DayInterval {
	if o, ok := s.overrides[date]; ok {
		return slices.Clone(o)
	}
	return startingOn(s.weekly, date.Weekday())
}

func startingOn(w WeeklyHours, day time.Weekday) []DayInterval {
	var out []DayInterval
	for _, i := range w.Intervals() {
		if i.Day == day {
			out = append(out, DayInterval{Start: i.Start, Minutes: i.Minutes})
		}
	}
	return out
}

// BusinessID returns the business.
func (s *BarberSchedule) BusinessID() shared.BusinessID { return s.business }

// BranchID returns the branch.
func (s *BarberSchedule) BranchID() shared.BranchID { return s.branch }

// StaffID returns whose schedule it is.
func (s *BarberSchedule) StaffID() shared.StaffID { return s.staff }

// Weekly returns the weekly template.
func (s *BarberSchedule) Weekly() WeeklyHours { return s.weekly }

// Overrides returns the date overrides, by date.
func (s *BarberSchedule) Overrides() map[Date][]DayInterval { return maps.Clone(s.overrides) }

// Version is 0 until first saved.
func (s *BarberSchedule) Version() int { return s.version }

// UpdatedAt returns when it last changed.
func (s *BarberSchedule) UpdatedAt() time.Time { return s.updatedAt }

// Schedules stores barber schedules, one per (staff, branch).
type Schedules interface {
	// Get returns the schedule, or a new empty one (version 0).
	Get(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff shared.StaffID) (*BarberSchedule, error)
	// Update locks the person's schedules, checks this one's version, calls
	// fn with it and the person's weekly templates at their other branches,
	// and saves — in one transaction.
	Update(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff shared.StaffID, expectedVersion int, fn func(s *BarberSchedule, elsewhere []WeeklyHours) error) error
}
