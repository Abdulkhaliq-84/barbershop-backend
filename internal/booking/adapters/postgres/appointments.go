// Package postgres is booking's storage: appointments in the booking schema.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Appointments is the appointment repository.
type Appointments struct {
	pool *pgxpool.Pool
}

// NewAppointments returns the repository.
func NewAppointments(pool *pgxpool.Pool) *Appointments { return &Appointments{pool: pool} }

// Busy returns each barber's active appointments (with their buffers)
// overlapping [from, to).
func (r *Appointments) Busy(ctx context.Context, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, error) {
	ids := make([]uuid.UUID, 0, len(staff))
	for _, s := range staff {
		ids = append(ids, s.UUID())
	}
	rows, err := sqlcgen.New(r.pool).BusyIntervals(ctx, sqlcgen.BusyIntervalsParams{Staff: ids, FromTime: from, ToTime: to})
	if err != nil {
		return nil, fmt.Errorf("busy intervals: %w", err)
	}
	out := make(map[shared.StaffID][]shared.Interval, len(staff))
	for _, row := range rows {
		span, err := shared.NewInterval(row.BusyFrom, row.BusyTo)
		if err != nil {
			return nil, fmt.Errorf("busy intervals: %w", err)
		}
		id := shared.IDFromUUID[shared.StaffTag](row.StaffID)
		out[id] = append(out[id], span)
	}
	return out, nil
}
