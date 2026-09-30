package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/semaphore"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// EventPublisher saves events inside the caller's transaction (the outbox).
type EventPublisher interface {
	PublishTx(ctx context.Context, tx pgx.Tx, events ...outbox.Event) error
}

// Store is booking's storage for writes: appointments and their events.
type Store struct {
	*Appointments
	events EventPublisher
	// inFlight lets at most half the pool's connections run a booking
	// transaction. Each one holds its connection while StillWorking reads
	// the barber's hours on another: if bookings held every connection,
	// none of them could get that second one, and all would wait forever.
	inFlight *semaphore.Weighted
}

// NewStore returns the store.
func NewStore(appointments *Appointments, events EventPublisher) *Store {
	return &Store{
		Appointments: appointments, events: events,
		inFlight: semaphore.NewWeighted(max(1, int64(appointments.pool.Config().MaxConns)/2)),
	}
}

var _ app.Bookings = (*Store)(nil)

const exclusionViolation = "23P01"

// Book saves the first of attempt.Drafts whose barber is still free, in one
// transaction:
//
//  1. claim the customer's Idempotency-Key — or, if a request with it
//     already booked, return that appointment (replayed = true);
//  2. lock the customer at the branch and check their limit of upcoming
//     bookings, so two requests at once can't both take the last place;
//  3. for each draft, in a savepoint: lock its barber's working time
//     (database.LockStaff), check they still work then, and insert. The
//     exclusion constraint refuses a barber who was booked meanwhile
//     (23P01); rolling back the savepoint releases their lock too, so the
//     next barber is tried holding only one lock at a time.
//
// Nothing saved means nobody was free: domain.ErrSlotUnavailable, and the
// key is released with the rolled-back transaction.
//
// StillWorking runs inside the transaction and reads on another connection:
// at most half the pool books at once, so that connection is always there.
func (s *Store) Book(ctx context.Context, attempt app.BookingAttempt) (booked *domain.Appointment, replayed bool, err error) {
	if err := s.inFlight.Acquire(ctx, 1); err != nil {
		return nil, false, err
	}
	defer s.inFlight.Release(1)
	customer, branch := attempt.Customer, attempt.Branch
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		claimed, err := q.ClaimIdempotencyKey(ctx, sqlcgen.ClaimIdempotencyKeyParams{
			CustomerID: customer.UUID(), Key: attempt.Key, RequestHash: attempt.RequestHash, CreatedAt: attempt.Now,
		})
		if err != nil {
			return fmt.Errorf("claim idempotency key: %w", err)
		}
		if claimed == 0 {
			var ok bool
			booked, ok, err = replay(ctx, q, customer, attempt.Key, attempt.RequestHash)
			replayed = ok
			return err
		}
		if len(attempt.Drafts) == 0 {
			return domain.ErrSlotUnavailable
		}
		if err := q.LockCustomerAtBranch(ctx, sqlcgen.LockCustomerAtBranchParams{CustomerID: customer.String(), BranchID: branch.String()}); err != nil {
			return fmt.Errorf("lock customer: %w", err)
		}
		active, err := q.CountActiveBookings(ctx, sqlcgen.CountActiveBookingsParams{CustomerID: customer.UUID(), BranchID: branch.UUID(), Now: attempt.Now})
		if err != nil {
			return fmt.Errorf("count bookings: %w", err)
		}
		if err := attempt.Allow(int(active)); err != nil {
			return err
		}
		for _, draft := range attempt.Drafts {
			ok, err := s.try(ctx, tx, draft, attempt.StillWorking)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := q.SettleIdempotencyKey(ctx, sqlcgen.SettleIdempotencyKeyParams{
				AppointmentID: pgUUID(draft.ID().UUID()), CustomerID: customer.UUID(), Key: attempt.Key,
			}); err != nil {
				return fmt.Errorf("settle idempotency key: %w", err)
			}
			booked = draft
			return s.publish(ctx, tx, draft.Events())
		}
		return domain.ErrSlotUnavailable
	})
	return booked, replayed, err
}

// try saves draft in a savepoint, if its barber still works then and is
// free. false: they aren't; the savepoint is rolled back, lock and all.
func (s *Store) try(ctx context.Context, tx pgx.Tx, draft *domain.Appointment, stillWorking func(context.Context, *domain.Appointment) (bool, error)) (bool, error) {
	sp, err := tx.Begin(ctx) // a savepoint
	if err != nil {
		return false, fmt.Errorf("savepoint: %w", err)
	}
	defer func() { _ = sp.Rollback(ctx) }() // after Commit, a no-op
	if err := database.LockStaff(ctx, sp, draft.Barber().UUID()); err != nil {
		return false, err
	}
	working, err := stillWorking(ctx, draft)
	if err != nil || !working {
		return false, err
	}
	err = insert(ctx, sqlcgen.New(sp), draft)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == exclusionViolation && pgErr.ConstraintName == "appointments_no_overlap" {
		return false, nil // booked by someone else meanwhile
	}
	if err != nil {
		return false, err
	}
	if err := sp.Commit(ctx); err != nil { // releases the savepoint; the transaction goes on
		return false, fmt.Errorf("release savepoint: %w", err)
	}
	return true, nil
}

// Replay returns what an earlier request with the customer's key booked, if
// it asked for the same thing; nothing if the key is new.
func (s *Store) Replay(ctx context.Context, customer shared.UserID, key uuid.UUID, requestHash []byte) (*domain.Appointment, bool, error) {
	return replay(ctx, sqlcgen.New(s.pool), customer, key, requestHash)
}

func replay(ctx context.Context, q *sqlcgen.Queries, customer shared.UserID, key uuid.UUID, requestHash []byte) (*domain.Appointment, bool, error) {
	row, err := q.IdempotencyKey(ctx, sqlcgen.IdempotencyKeyParams{CustomerID: customer.UUID(), Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load idempotency key: %w", err)
	}
	if !bytes.Equal(row.RequestHash, requestHash) || !row.AppointmentID.Valid {
		return nil, false, domain.ErrIdempotencyReused
	}
	a, err := load(ctx, q, customer, shared.IDFromUUID[domain.AppointmentTag](row.AppointmentID.Bytes))
	return a, err == nil, err
}

func insert(ctx context.Context, q *sqlcgen.Queries, a *domain.Appointment) error {
	s := a.Snapshot()
	version, err := toInt32(s.Version)
	if err != nil {
		return err
	}
	if err := q.InsertAppointment(ctx, sqlcgen.InsertAppointmentParams{
		ID: s.ID.UUID(), BusinessID: s.Business.UUID(), BranchID: s.Branch.UUID(), StaffID: s.Barber.UUID(),
		CustomerID: s.Customer.UUID(), Status: string(s.Status), Source: string(s.Source), Assignment: string(s.Assignment),
		StartsAt: s.Start, EndsAt: s.End, BusyUntil: s.BusyUntil,
		PriceAmount: s.Price.Amount(), PriceCurrency: string(s.Price.Currency()),
		CustomerNote: s.Note, PendingUntil: s.PendingUntil, Version: version, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("insert appointment: %w", err)
	}
	for i, it := range s.Items {
		position, err := toInt16(i + 1)
		if err != nil {
			return err
		}
		minutes, err := toInt16(int(it.Duration / time.Minute))
		if err != nil {
			return err
		}
		if err := q.InsertAppointmentItem(ctx, sqlcgen.InsertAppointmentItemParams{
			AppointmentID: s.ID.UUID(), Position: position, ServiceID: it.Service.UUID(),
			NameAr: it.Name.Ar(), NameEn: it.Name.En(), DurationMinutes: minutes,
			PriceAmount: it.Price.Amount(), PriceCurrency: string(it.Price.Currency()),
		}); err != nil {
			return fmt.Errorf("insert appointment item: %w", err)
		}
	}
	return nil
}

// publish hands the appointment's events to the outbox inside tx.
func (s *Store) publish(ctx context.Context, tx pgx.Tx, recorded []domain.Event) error {
	out := make([]outbox.Event, 0, len(recorded))
	for _, e := range recorded {
		var (
			ev  outbox.Event
			err error
		)
		switch e := e.(type) {
		case domain.AppointmentBooked:
			a := e.Appointment
			ev, err = outbox.NewEvent(events.TypeAppointmentBooked, a.CreatedAt, events.AppointmentBooked{
				AppointmentID: a.ID.UUID(), BusinessID: a.Business.UUID(), BranchID: a.Branch.UUID(),
				BarberID: a.Barber.UUID(), CustomerID: a.Customer.UUID(), Status: string(a.Status),
				StartsAt: a.Start, EndsAt: a.End,
			})
		default:
			err = fmt.Errorf("no contract for event %T", e)
		}
		if err != nil {
			return err
		}
		out = append(out, ev)
	}
	if err := s.events.PublishTx(ctx, tx, out...); err != nil {
		return fmt.Errorf("publish events: %w", err)
	}
	return nil
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
