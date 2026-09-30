package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MaxOfferings caps how many people one service lists (a typo guard).
const MaxOfferings = 100

// Offering says one staff member performs the service, optionally at their
// own price or duration: a senior barber may charge more, or take longer.
// Anyone on the staff may offer services — owners and managers who also cut
// hair included.
type Offering struct {
	Staff    shared.StaffID
	Price    *shared.Money  // nil: the service's price
	Duration *time.Duration // nil: the service's duration
}

// PriceOr returns the offering's price, or the service's.
func (o Offering) PriceOr(service shared.Money) shared.Money {
	if o.Price != nil {
		return *o.Price
	}
	return service
}

// DurationOr returns the offering's duration, or the service's.
func (o Offering) DurationOr(service time.Duration) time.Duration {
	if o.Duration != nil {
		return *o.Duration
	}
	return service
}

// checkOfferings validates a full set of offerings and returns it in a
// stable order (by staff ID).
func checkOfferings(offerings []Offering) ([]Offering, error) {
	if len(offerings) > MaxOfferings {
		return nil, ErrTooManyOfferings
	}
	out := slices.Clone(offerings)
	slices.SortFunc(out, func(a, b Offering) int { return strings.Compare(a.Staff.String(), b.Staff.String()) })
	for i, o := range out {
		if o.Staff.IsZero() {
			return nil, ErrUnknownStaff
		}
		if i > 0 && out[i-1].Staff == o.Staff {
			return nil, ErrDuplicateOffering
		}
		if o.Duration != nil && !validDuration(*o.Duration) {
			return nil, ErrInvalidDuration
		}
		if o.Price != nil && !validPrice(*o.Price) {
			return nil, ErrInvalidPrice
		}
	}
	return out, nil
}

func validDuration(d time.Duration) bool {
	return d >= MinDuration && d <= MaxDuration && d%DurationStep == 0
}

func validPrice(p shared.Money) bool {
	return p.Currency() == shared.SAR && !p.IsNegative() && p.Amount() <= MaxPriceHalalas
}
