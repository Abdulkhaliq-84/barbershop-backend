package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// ChangeMine locks one of the customer's appointments, lets change act on
// it and saves the result with its events. domain.ErrNotFound for someone
// else's.
func (s *Store) ChangeMine(ctx context.Context, customer shared.UserID, id domain.AppointmentID, change func(*domain.Appointment) error) (*domain.Appointment, error) {
	return s.change(ctx, func(q *sqlcgen.Queries) (appointmentRow, error) {
		row, err := q.CustomerAppointmentForUpdate(ctx, sqlcgen.CustomerAppointmentForUpdateParams{CustomerID: pgUUID(customer.UUID()), ID: id.UUID()})
		return appointmentRow(row), err
	}, change)
}

// ChangeAtBusiness is ChangeMine for the shop: the appointment is found by
// business, so another business's ID is domain.ErrNotFound.
func (s *Store) ChangeAtBusiness(ctx context.Context, business shared.BusinessID, id domain.AppointmentID, change func(*domain.Appointment) error) (*domain.Appointment, error) {
	return s.change(ctx, func(q *sqlcgen.Queries) (appointmentRow, error) {
		row, err := q.BusinessAppointmentForUpdate(ctx, sqlcgen.BusinessAppointmentForUpdateParams{BusinessID: business.UUID(), ID: id.UUID()})
		return appointmentRow(row), err
	}, change)
}

// change runs in one transaction: the row lock makes two changes at once
// (the shop confirming as the customer cancels) take turns, so the second
// sees the first's result and the domain decides whether it still applies.
func (s *Store) change(ctx context.Context, lock func(*sqlcgen.Queries) (appointmentRow, error), change func(*domain.Appointment) error) (changed *domain.Appointment, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := lock(q)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock appointment: %w", err)
		}
		a, err := withItems(ctx, q, row)
		if err != nil {
			return err
		}
		if err := change(a); err != nil {
			return err
		}
		if err := save(ctx, q, row, a); err != nil {
			return err
		}
		changed = a
		return s.publish(ctx, tx, a.Events())
	})
	return changed, err
}

// ExpireDue expires up to limit pending bookings whose expiry is at or
// before now, oldest first, and publishes their events, in one transaction.
// It returns how many it expired. Rows locked by a change in progress are
// skipped (SKIP LOCKED): two runs, or a run and the shop confirming, never
// wait for each other, and whatever is still due is found next time.
func (s *Store) ExpireDue(ctx context.Context, now time.Time, limit int) (expired int, err error) {
	batch, err := toInt32(limit)
	if err != nil {
		return 0, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		rows, err := q.DuePendingForUpdate(ctx, sqlcgen.DuePendingForUpdateParams{Now: &now, MaxRows: batch})
		if err != nil {
			return fmt.Errorf("due pending bookings: %w", err)
		}
		var events []domain.Event
		for _, r := range rows {
			row := appointmentRow(r)
			a, err := withItems(ctx, q, row)
			if err != nil {
				return err
			}
			if err := a.Expire(now); err != nil {
				return err
			}
			if err := save(ctx, q, row, a); err != nil {
				return err
			}
			events = append(events, a.Events()...)
		}
		expired = len(rows)
		return s.publish(ctx, tx, events)
	})
	return expired, err
}

// save writes a's new status and what goes with it, if row is still the
// version that was loaded.
func save(ctx context.Context, q *sqlcgen.Queries, row appointmentRow, a *domain.Appointment) error {
	snap := a.Snapshot()
	version, err := toInt32(snap.Version)
	if err != nil {
		return err
	}
	p := sqlcgen.UpdateAppointmentStatusParams{
		ID: row.ID, ExpectedVersion: row.Version, Status: string(snap.Status), PendingUntil: snap.PendingUntil,
		Version: version, UpdatedAt: snap.UpdatedAt,
	}
	if c := snap.Cancellation; c != nil {
		p.CancelledBy, p.CancelReason, p.CancelledAt = new(string(c.By)), c.Reason, new(c.At)
	}
	n, err := q.UpdateAppointmentStatus(ctx, p)
	if err != nil {
		return fmt.Errorf("update appointment: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("update appointment %s: changed meanwhile, despite the lock", row.ID)
	}
	return nil
}
