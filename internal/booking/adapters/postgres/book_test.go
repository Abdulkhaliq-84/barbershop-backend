package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// events records what was published, and in which transaction.
type events struct {
	mu  sync.Mutex
	got []outbox.Event
}

func (e *events) PublishTx(_ context.Context, _ pgx.Tx, evs ...outbox.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, evs...)
	return nil
}

type shop struct {
	business shared.BusinessID
	branch   shared.BranchID
	start    time.Time
}

func newShop() shop {
	return shop{shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), t0.Add(3 * time.Hour)}
}

func (s shop) draft(t *testing.T, barber shared.StaffID, customer shared.UserID) *domain.Appointment {
	t.Helper()
	name, _ := shared.NewLocalizedText("قص", "Cut")
	a, err := domain.Book(domain.Booking{
		ID: shared.NewID[domain.AppointmentTag](), Business: s.business, Branch: s.branch, Barber: barber, Customer: customer,
		Start: s.start, Buffer: 10 * time.Minute, Assignment: domain.AnyBarber, AutoConfirm: true,
		Items: []domain.Item{{Service: shared.NewID[shared.ServiceTag](), Name: name, Duration: 30 * time.Minute, Price: shared.Halalas(6000)}},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func attempt(key uuid.UUID, hash byte, drafts ...*domain.Appointment) app.BookingAttempt {
	s := drafts[0].Snapshot()
	return app.BookingAttempt{
		Customer: s.Customer, Branch: s.Branch,
		Key: key, RequestHash: make32(hash), Now: t0, Drafts: drafts,
		Allow:        func(int) error { return nil },
		StillWorking: func(context.Context, *domain.Appointment) (bool, error) { return true, nil },
	}
}

func make32(b byte) []byte {
	out := make([]byte, 32)
	out[0] = b
	return out
}

func store(pool *pgxpool.Pool) (*postgres.Store, *events) {
	ev := &events{}
	return postgres.NewStore(postgres.NewAppointments(pool), ev), ev
}

// Twenty customers want the same barber at the same time: one gets it.
func TestOneBookingWinsTheRace(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	s, ev := store(pool)
	sh, ali := newShop(), shared.NewID[shared.StaffTag]()
	drafts := make([]*domain.Appointment, 20)
	for i := range drafts {
		drafts[i] = sh.draft(t, ali, shared.NewID[shared.UserTag]())
	}
	var booked, taken atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, d := range drafts {
		wg.Go(func() {
			<-start
			_, _, err := s.Book(t.Context(), attempt(uuid.New(), 1, d))
			switch {
			case err == nil:
				booked.Add(1)
			case errors.Is(err, domain.ErrSlotUnavailable):
				taken.Add(1)
			default:
				t.Errorf("Book: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if booked.Load() != 1 || taken.Load() != 19 || len(ev.got) != 1 {
		t.Fatalf("booked %d, refused %d, events %d; want 1, 19, 1", booked.Load(), taken.Load(), len(ev.got))
	}
}

// "Any barber": the first choice was just taken, so the next one gets it;
// one who no longer works then is skipped.
func TestBookFallsBackToTheNextBarber(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	s, _ := store(pool)
	sh := newShop()
	ali, sara, omar := shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	if _, _, err := s.Book(t.Context(), attempt(uuid.New(), 1, sh.draft(t, ali, shared.NewID[shared.UserTag]()))); err != nil {
		t.Fatal(err)
	}
	customer := shared.NewID[shared.UserTag]()
	a := attempt(uuid.New(), 2, sh.draft(t, ali, customer), sh.draft(t, sara, customer), sh.draft(t, omar, customer))
	a.StillWorking = func(_ context.Context, d *domain.Appointment) (bool, error) { return d.Barber() != sara, nil } // Sara's hours changed
	got, replayed, err := s.Book(t.Context(), a)
	if err != nil || replayed || got.Barber() != omar {
		t.Fatalf("booked %v (replayed %v), %v; want Omar", got, replayed, err)
	}
	// Only Omar's draft was saved, with its items.
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM booking.appointments a JOIN booking.appointment_items i ON i.appointment_id = a.id WHERE a.customer_id = $1`, customer.UUID()).Scan(&n); err != nil || n != 1 {
		t.Errorf("customer's appointment items = %d, %v", n, err)
	}
	// Nobody left: slot unavailable, and the key is free again.
	b := attempt(uuid.New(), 3, sh.draft(t, ali, customer))
	if _, _, err := s.Book(t.Context(), b); !errors.Is(err, domain.ErrSlotUnavailable) {
		t.Errorf("all taken: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM booking.idempotency_keys WHERE key = $1`, b.Key).Scan(&n); err != nil || n != 0 {
		t.Errorf("a failed booking kept its key: %d, %v", n, err)
	}
}

// A retry with the same key — even at the same moment — gets the one
// appointment; the key with a different request is refused.
func TestIdempotencyKey(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	s, _ := store(pool)
	sh, customer, key := newShop(), shared.NewID[shared.UserTag](), uuid.New()
	barbers := []shared.StaffID{shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()}

	type result struct {
		a        *domain.Appointment
		replayed bool
		err      error
	}
	results := make(chan result, len(barbers))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, b := range barbers { // each retry happens to draft a different barber
		d := sh.draft(t, b, customer)
		wg.Go(func() {
			<-start
			a, replayed, err := s.Book(t.Context(), attempt(key, 7, d))
			results <- result{a, replayed, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var first *domain.Appointment
	var replays int
	for r := range results {
		if r.err != nil {
			t.Fatalf("Book: %v", r.err)
		}
		if first == nil {
			first = r.a
		} else if r.a.ID() != first.ID() {
			t.Fatalf("two appointments for one key: %s and %s", first.ID(), r.a.ID())
		}
		if r.replayed {
			replays++
		}
	}
	if replays != 2 {
		t.Errorf("replays = %d, want 2", replays)
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM booking.appointments WHERE customer_id = $1`, customer.UUID()).Scan(&n); err != nil || n != 1 {
		t.Fatalf("appointments = %d, %v", n, err)
	}
	// The replay is the stored appointment, items and all.
	if first.Snapshot().Items[0].Name.Ar() != "قص" || first.Snapshot().Price != shared.Halalas(6000) {
		t.Errorf("replayed = %+v", first.Snapshot())
	}
	if _, _, err := s.Book(t.Context(), attempt(key, 8, sh.draft(t, barbers[0], customer))); !errors.Is(err, domain.ErrIdempotencyReused) {
		t.Errorf("same key, another request: %v", err)
	}
	// Another customer's key of the same value is theirs alone.
	if _, replayed, err := s.Book(t.Context(), attempt(key, 7, sh.draft(t, shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag]()))); err != nil || replayed {
		t.Errorf("another customer, same key: replayed %v, %v", replayed, err)
	}

	// Replay, the lookup before any rule is checked.
	if a, ok, err := s.Replay(t.Context(), customer, key, make32(7)); err != nil || !ok || a.ID() != first.ID() {
		t.Errorf("Replay: %v, %v, %v", a, ok, err)
	}
	if _, ok, err := s.Replay(t.Context(), customer, key, make32(8)); ok || !errors.Is(err, domain.ErrIdempotencyReused) {
		t.Errorf("Replay, another request: %v, %v", ok, err)
	}
	if a, ok, err := s.Replay(t.Context(), customer, uuid.New(), make32(7)); err != nil || ok || a != nil {
		t.Errorf("Replay, a new key: %v, %v, %v", a, ok, err)
	}

	// Nobody looked free — the first request just took the time. The key
	// still finds it; a new key is refused and not kept.
	empty := app.BookingAttempt{Customer: customer, Branch: sh.branch, Key: key, RequestHash: make32(7), Now: t0}
	if a, replayed, err := s.Book(t.Context(), empty); err != nil || !replayed || a.ID() != first.ID() {
		t.Errorf("no drafts, known key: %v, %v, %v", a, replayed, err)
	}
	empty.Key = uuid.New()
	if _, _, err := s.Book(t.Context(), empty); !errors.Is(err, domain.ErrSlotUnavailable) {
		t.Errorf("no drafts, new key: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM booking.idempotency_keys WHERE key = $1`, empty.Key).Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused request kept its key: %d, %v", n, err)
	}
}

// The limit is checked against the customer's upcoming bookings, under a
// lock on them: two requests at once can't both take the last place.
func TestBookingLimit(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	s, _ := store(pool)
	sh, customer := newShop(), shared.NewID[shared.UserTag]()
	limit := func(active int) error {
		if active >= 1 {
			return domain.ErrTooManyBookings
		}
		return nil
	}
	var booked, refused atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 5 {
		a := attempt(uuid.New(), 1, sh.draft(t, shared.NewID[shared.StaffTag](), customer)) // different barbers: no clash
		a.Allow = limit
		wg.Go(func() {
			<-start
			switch _, _, err := s.Book(t.Context(), a); {
			case err == nil:
				booked.Add(1)
			case errors.Is(err, domain.ErrTooManyBookings):
				refused.Add(1)
			default:
				t.Errorf("Book: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if booked.Load() != 1 || refused.Load() != 4 {
		t.Fatalf("booked %d, refused %d; want 1 and 4", booked.Load(), refused.Load())
	}
}

// While a booking checks the barber's hours, their working time is locked:
// scheduling can't take hours away (database.LockStaff) until it commits —
// and the appointment and its event commit together.
func TestBookHoldsTheBarbersLock(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	s, ev := store(pool)
	sh, ali := newShop(), shared.NewID[shared.StaffTag]()
	queued := dbtest.OthersQueued(t, pool, 1)
	scheduling := make(chan error, 1)
	a := attempt(uuid.New(), 1, sh.draft(t, ali, shared.NewID[shared.UserTag]()))
	checked := false
	a.StillWorking = func(context.Context, *domain.Appointment) (bool, error) {
		checked = true
		go func() { // scheduling adding time off for Ali, meanwhile
			scheduling <- pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
				return database.LockStaff(t.Context(), tx, ali.UUID())
			})
		}()
		queued() // …waits for this booking's lock
		return true, nil
	}
	if _, _, err := s.Book(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("booked without checking the barber still works then")
	}
	if err := <-scheduling; err != nil {
		t.Fatal(err)
	}
	if len(ev.got) != 1 || ev.got[0].Type != "booking.appointment_booked" {
		t.Errorf("events = %+v", ev.got)
	}
}

// Each booking reads the barber's hours on a second connection while its
// transaction holds one. With a pool of two, ten bookings at once must all
// finish rather than each hold one connection and wait for another.
func TestBookingsLeaveAConnection(t *testing.T) {
	t.Parallel()
	cfg := migrated(t).Config()
	cfg.MaxConns = 2
	small, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(small.Close)
	s, _ := store(small)
	sh := newShop()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 10 {
		a := attempt(uuid.New(), 1, sh.draft(t, shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag]()))
		a.StillWorking = func(ctx context.Context, _ *domain.Appointment) (bool, error) {
			var one int
			err := small.QueryRow(ctx, `SELECT 1`).Scan(&one) // as scheduling reads, on its own connection
			return err == nil, err
		}
		wg.Go(func() {
			if _, _, err := s.Book(ctx, a); err != nil {
				t.Errorf("Book: %v", err)
			}
		})
	}
	wg.Wait()
}
