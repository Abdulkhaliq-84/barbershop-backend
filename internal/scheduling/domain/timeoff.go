package domain

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// TimeOffTag marks time-off IDs.
type TimeOffTag struct{}

// TimeOffID identifies a stretch of time off.
type TimeOffID = shared.ID[TimeOffTag]

// Time-off rules.
const (
	MaxTimeOff       = 366 * 24 * time.Hour
	MaxTimeOffReason = 200
)

// TimeOff is a stretch when a staff member doesn't work — at any branch.
// Unlike weekly hours it is real instants: a doctor's appointment at 10:00
// on 5 October is that moment, whatever the branch.
type TimeOff struct {
	id        TimeOffID
	business  shared.BusinessID
	staff     shared.StaffID
	span      shared.Interval
	reason    string
	createdAt time.Time
}

// NewTimeOff checks a stretch of time off: it ends after it starts, lasts at
// most a year, and has a reason of at most 200 characters (optional).
func NewTimeOff(id TimeOffID, business shared.BusinessID, staff shared.StaffID, from, to time.Time, reason string, now time.Time) (*TimeOff, error) {
	from, to = from.UTC().Truncate(time.Microsecond), to.UTC().Truncate(time.Microsecond)
	span, err := shared.NewInterval(from, to)
	if err != nil {
		return nil, ErrInvalidTimeOff
	}
	if span.Duration() > MaxTimeOff {
		return nil, ErrInvalidTimeOff
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > MaxTimeOffReason {
		return nil, ErrReasonTooLong
	}
	return &TimeOff{id: id, business: business, staff: staff, span: span, reason: reason, createdAt: now.UTC().Truncate(time.Microsecond)}, nil
}

// RehydrateTimeOff rebuilds time off loaded from storage.
func RehydrateTimeOff(id TimeOffID, business shared.BusinessID, staff shared.StaffID, span shared.Interval, reason string, createdAt time.Time) *TimeOff {
	return &TimeOff{id: id, business: business, staff: staff, span: span, reason: reason, createdAt: createdAt}
}

// ID returns the time-off ID.
func (t *TimeOff) ID() TimeOffID { return t.id }

// BusinessID returns the business.
func (t *TimeOff) BusinessID() shared.BusinessID { return t.business }

// StaffID returns who is off.
func (t *TimeOff) StaffID() shared.StaffID { return t.staff }

// Span returns when: [start, end).
func (t *TimeOff) Span() shared.Interval { return t.span }

// Reason returns the reason ("" if none).
func (t *TimeOff) Reason() string { return t.reason }

// CreatedAt returns when it was recorded.
func (t *TimeOff) CreatedAt() time.Time { return t.createdAt }

// TimeOffs stores time off.
type TimeOffs interface {
	// Add saves t. ErrTimeOffOverlaps if it overlaps the staff member's
	// other time off.
	Add(ctx context.Context, t *TimeOff) error
	// List returns the staff member's time off ending after since, by start.
	List(ctx context.Context, business shared.BusinessID, staff shared.StaffID, since time.Time) ([]*TimeOff, error)
	// Delete removes one, or ErrNotFound.
	Delete(ctx context.Context, business shared.BusinessID, staff shared.StaffID, id TimeOffID) error
}
