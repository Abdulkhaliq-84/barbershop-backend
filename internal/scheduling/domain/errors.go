package domain

import "errors"

// Errors. Adapters map them to HTTP answers.
var (
	ErrNotFound        = errors.New("scheduling: not found")
	ErrForbidden       = errors.New("scheduling: not allowed")
	ErrVersionConflict = errors.New("scheduling: changed since it was read")

	ErrInvalidWeekday       = errors.New("scheduling: not a weekday")
	ErrInvalidInterval      = errors.New("scheduling: times are on 5-minute steps and last 5 minutes to 24 hours")
	ErrTooManyIntervals     = errors.New("scheduling: at most 4 intervals start on one day")
	ErrOverlappingIntervals = errors.New("scheduling: intervals overlap")

	ErrInvalidDate      = errors.New("scheduling: not a date")
	ErrTooManyOverrides = errors.New("scheduling: at most 400 date overrides")
	ErrScheduleClash    = errors.New("scheduling: overlaps this person's schedule at another branch")
	ErrInvalidTimeOff   = errors.New("scheduling: time off must end after it starts and last at most a year")
	ErrReasonTooLong    = errors.New("scheduling: reason is at most 200 characters")
	ErrTimeOffOverlaps  = errors.New("scheduling: overlaps other time off")
)
