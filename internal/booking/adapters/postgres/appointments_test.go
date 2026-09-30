package postgres_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2029, 10, 4, 7, 0, 0, 0, time.UTC)

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return pool
}

// insert writes an appointment row directly: booking's own code for it
// arrives in M5.3. during is [start, end + buffer).
func insert(t *testing.T, pool *pgxpool.Pool, staff shared.StaffID, status string, start, end, busyUntil time.Time) error {
	t.Helper()
	pending, cancelled, by := (*time.Time)(nil), (*time.Time)(nil), (*string)(nil)
	switch status {
	case "pending":
		pending = &busyUntil
	case "cancelled":
		cancelled, by = &start, new("customer")
	}
	_, err := pool.Exec(t.Context(), `
		INSERT INTO booking.appointments (id, business_id, branch_id, staff_id, customer_id, status, source, assignment,
			starts_at, ends_at, during, price_amount, price_currency, pending_until, cancelled_by, cancelled_at, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'customer_app', 'any_barber', $7, $8, tstzrange($7, $9, '[)'), 6000, 'SAR', $10, $11, $12, 1, $13, $13)`,
		shared.NewID[shared.StaffTag]().UUID(), shared.NewID[shared.BusinessTag]().UUID(), shared.NewID[shared.BranchTag]().UUID(),
		staff.UUID(), shared.NewID[shared.UserTag]().UUID(), status, start, end, busyUntil, pending, by, cancelled, t0)
	return err
}

func TestBusyAndNoDoubleBooking(t *testing.T) {
	t.Parallel()
	pool := migrated(t)
	ali, sara := shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	at := func(h, m int) time.Time { return t0.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }

	for _, a := range []struct {
		staff        shared.StaffID
		status       string
		start, end   time.Time
		busyUntil    time.Time
		wantConflict bool
	}{
		{ali, "confirmed", at(1, 0), at(1, 30), at(1, 40), false},
		{ali, "pending", at(2, 0), at(2, 30), at(2, 30), false},
		{ali, "confirmed", at(1, 40), at(2, 0), at(2, 0), false},  // touches both: [) ranges don't overlap
		{ali, "confirmed", at(1, 35), at(1, 50), at(1, 50), true}, // inside the first one's buffer
		{ali, "pending", at(2, 15), at(2, 45), at(2, 45), true},   // a pending one holds its time too
		{ali, "cancelled", at(1, 0), at(1, 30), at(1, 30), false}, // cancelled ones don't count
		{sara, "confirmed", at(1, 0), at(1, 30), at(1, 30), false},
		{ali, "completed", at(5, 0), at(5, 30), at(5, 30), false},
	} {
		err := insert(t, pool, a.staff, a.status, a.start, a.end, a.busyUntil)
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if conflict := ok && pgErr.Code == "23P01" && pgErr.ConstraintName == "appointments_no_overlap"; conflict != a.wantConflict {
			t.Errorf("%s %s–%s: err %v, want conflict %v", a.status, a.start.Format("15:04"), a.end.Format("15:04"), err, a.wantConflict)
		}
	}

	busy, err := postgres.NewAppointments(pool).Busy(t.Context(), []shared.StaffID{ali, sara}, at(1, 30), at(6, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Ali: 01:00–01:40 (with its buffer) overlaps the range; the pending
	// and the touching ones; not the cancelled or completed ones.
	var got []string
	for _, b := range busy[ali] {
		got = append(got, b.Start().Format("15:04")+"–"+b.End().Format("15:04"))
	}
	want := []string{"08:00–08:40", "08:40–09:00", "09:00–09:30"}
	if len(got) != len(want) {
		t.Fatalf("ali busy = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ali busy = %v, want %v", got, want)
		}
	}
	if len(busy[sara]) != 0 { // 01:00–01:30 ends where the range starts
		t.Errorf("sara busy = %v", busy[sara])
	}
}
