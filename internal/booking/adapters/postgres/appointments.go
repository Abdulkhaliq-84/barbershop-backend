// Package postgres is booking's storage: appointments in the booking schema.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
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

// ByCustomer returns one of the customer's appointments, or
// domain.ErrNotFound — also for someone else's.
func (r *Appointments) ByCustomer(ctx context.Context, customer shared.UserID, id domain.AppointmentID) (*domain.Appointment, error) {
	return load(ctx, sqlcgen.New(r.pool), customer, id)
}

func load(ctx context.Context, q *sqlcgen.Queries, customer shared.UserID, id domain.AppointmentID) (*domain.Appointment, error) {
	row, err := q.CustomerAppointment(ctx, sqlcgen.CustomerAppointmentParams{CustomerID: customer.UUID(), ID: id.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load appointment: %w", err)
	}
	rows, err := q.AppointmentItems(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("load appointment items: %w", err)
	}
	items := make([]domain.Item, 0, len(rows))
	for _, it := range rows {
		name, err := shared.NewLocalizedText(it.NameAr, it.NameEn)
		if err != nil {
			return nil, fmt.Errorf("stored item name: %w", err)
		}
		price, err := shared.NewMoney(it.PriceAmount, shared.Currency(it.PriceCurrency))
		if err != nil {
			return nil, fmt.Errorf("stored item price: %w", err)
		}
		items = append(items, domain.Item{
			Service: shared.IDFromUUID[shared.ServiceTag](it.ServiceID), Name: name,
			Duration: time.Duration(it.DurationMinutes) * time.Minute, Price: price,
		})
	}
	price, err := shared.NewMoney(row.PriceAmount, shared.Currency(row.PriceCurrency))
	if err != nil {
		return nil, fmt.Errorf("stored price: %w", err)
	}
	return domain.Rehydrate(domain.Snapshot{
		ID: id, Business: shared.IDFromUUID[shared.BusinessTag](row.BusinessID), Branch: shared.IDFromUUID[shared.BranchTag](row.BranchID),
		Barber: shared.IDFromUUID[shared.StaffTag](row.StaffID), Customer: customer, Items: items,
		Start: row.StartsAt, End: row.EndsAt, BusyUntil: row.BusyUntil, Price: price,
		Status: domain.Status(row.Status), Source: domain.Source(row.Source), Assignment: domain.Assignment(row.Assignment),
		Note: row.CustomerNote, PendingUntil: row.PendingUntil, Version: int(row.Version), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}), nil
}

// toInt16 and toInt32 convert, refusing values the columns can't hold.
func toInt16(n int) (int16, error) {
	if n < math.MinInt16 || n > math.MaxInt16 {
		return 0, fmt.Errorf("booking: %d doesn't fit a smallint", n)
	}
	return int16(n), nil
}

func toInt32(n int) (int32, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("booking: %d doesn't fit an integer", n)
	}
	return int32(n), nil
}
