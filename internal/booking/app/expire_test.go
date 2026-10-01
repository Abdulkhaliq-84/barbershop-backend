package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
)

// dueBookings pretends due bookings remain, expiring at most limit a call.
type dueBookings struct {
	left  int
	calls int
	fail  error
}

func (d *dueBookings) ExpireDue(_ context.Context, _ time.Time, limit int) (int, error) {
	d.calls++
	if d.fail != nil {
		return 0, d.fail
	}
	n := min(d.left, limit)
	d.left -= n
	return n, nil
}

// A run keeps going while batches come back full.
func TestExpirer(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2029, 10, 4, 7, 0, 0, 0, time.UTC))
	for left, calls := range map[int]int{0: 1, 1: 1, app.ExpiryBatch: 2, 2*app.ExpiryBatch + 1: 3} {
		d := &dueBookings{left: left}
		if err := app.NewExpirer(d, clk).Run(t.Context()); err != nil || d.left != 0 || d.calls != calls {
			t.Errorf("%d due: %d left after %d calls (%v), want 0 after %d", left, d.left, d.calls, err, calls)
		}
	}
	boom := errors.New("boom")
	if err := app.NewExpirer(&dueBookings{left: 5, fail: boom}, clk).Run(t.Context()); !errors.Is(err, boom) {
		t.Errorf("a failing batch: %v", err)
	}
}
