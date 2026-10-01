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
	"github.com/jackc/pgx/v5/pgtype"
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

// Day returns the branch's appointments starting in [from, to), in every
// status, by start; only barber's if barber isn't nil.
func (r *Appointments) Day(ctx context.Context, business shared.BusinessID, branch shared.BranchID, from, to time.Time, barber *shared.StaffID) ([]*domain.Appointment, error) {
	q := sqlcgen.New(r.pool)
	p := sqlcgen.BranchDayParams{BusinessID: business.UUID(), BranchID: branch.UUID(), FromTime: from, ToTime: to}
	if barber != nil {
		p.StaffID = pgUUID(barber.UUID())
	}
	rows, err := q.BranchDay(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("branch day: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	all, err := q.AppointmentItemsOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("branch day items: %w", err)
	}
	items := make(map[uuid.UUID][]sqlcgen.BookingAppointmentItem, len(rows))
	for _, it := range all {
		items[it.AppointmentID] = append(items[it.AppointmentID], it)
	}
	out := make([]*domain.Appointment, 0, len(rows))
	for _, row := range rows {
		a, err := hydrate(appointmentRow(row), items[row.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// appointmentRow is a row of any of the appointment queries: they select
// the same columns, so their row types convert to this one.
type appointmentRow = sqlcgen.CustomerAppointmentRow

func load(ctx context.Context, q *sqlcgen.Queries, customer shared.UserID, id domain.AppointmentID) (*domain.Appointment, error) {
	row, err := q.CustomerAppointment(ctx, sqlcgen.CustomerAppointmentParams{CustomerID: pgUUID(customer.UUID()), ID: id.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load appointment: %w", err)
	}
	return withItems(ctx, q, row)
}

func withItems(ctx context.Context, q *sqlcgen.Queries, row appointmentRow) (*domain.Appointment, error) {
	items, err := q.AppointmentItems(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("load appointment items: %w", err)
	}
	return hydrate(row, items)
}

// hydrate rebuilds an appointment from its row and its items' rows.
func hydrate(row appointmentRow, rows []sqlcgen.BookingAppointmentItem) (*domain.Appointment, error) {
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
	var cancellation *domain.Cancellation
	if row.CancelledBy != nil && row.CancelledAt != nil {
		cancellation = &domain.Cancellation{By: domain.Canceller(*row.CancelledBy), Reason: row.CancelReason, At: *row.CancelledAt}
	}
	return domain.Rehydrate(domain.Snapshot{
		ID: shared.IDFromUUID[domain.AppointmentTag](row.ID), Business: shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		Branch: shared.IDFromUUID[shared.BranchTag](row.BranchID), Barber: shared.IDFromUUID[shared.StaffTag](row.StaffID),
		Customer: customerOf(row.CustomerID), CustomerName: row.CustomerName, Items: items,
		Start: row.StartsAt, End: row.EndsAt, BusyUntil: row.BusyUntil, Price: price,
		Status: domain.Status(row.Status), Source: domain.Source(row.Source), Assignment: domain.Assignment(row.Assignment),
		Note: row.CustomerNote, PendingUntil: row.PendingUntil, CancellableUntil: row.CancellableUntil, Cancellation: cancellation,
		Version: int(row.Version), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}), nil
}

// customerOf reads a nullable customer_id: no one for a walk-in.
func customerOf(id pgtype.UUID) shared.UserID {
	if !id.Valid {
		return shared.UserID{}
	}
	return shared.IDFromUUID[shared.UserTag](id.Bytes)
}

// optional is a nullable column's value: NULL for a zero ID.
func optional[T any](id shared.ID[T]) pgtype.UUID {
	if id.IsZero() {
		return pgtype.UUID{}
	}
	return pgUUID(id.UUID())
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
