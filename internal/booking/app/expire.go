package app

import (
	"context"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
)

// Expiries expires due pending bookings (booking's own repository).
type Expiries interface {
	// ExpireDue expires up to limit pending bookings whose expiry is at or
	// before now, and returns how many.
	ExpireDue(ctx context.Context, now time.Time, limit int) (int, error)
}

// ExpiryBatch is how many bookings one transaction expires: a run never
// holds many rows locked for long.
const ExpiryBatch = 100

// Expirer expires the pending bookings the shop didn't answer in time,
// freeing their barbers' time. The worker runs it every minute.
type Expirer struct {
	expiries Expiries
	clock    clock.Clock
}

// NewExpirer wires the task.
func NewExpirer(expiries Expiries, clk clock.Clock) *Expirer {
	return &Expirer{expiries: expiries, clock: clk}
}

// Run expires everything due, a batch at a time.
func (e *Expirer) Run(ctx context.Context) error {
	for {
		n, err := e.expiries.ExpireDue(ctx, e.clock.Now(), ExpiryBatch)
		if err != nil {
			return err
		}
		if n < ExpiryBatch {
			return nil
		}
	}
}
