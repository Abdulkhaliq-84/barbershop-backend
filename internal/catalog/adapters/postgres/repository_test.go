package postgres_test

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func newService(t *testing.T, business shared.BusinessID, branch shared.BranchID, sort int, at time.Time) *domain.Service {
	t.Helper()
	name, _ := shared.NewLocalizedText("قص شعر", "Haircut")
	s, err := domain.NewService(shared.NewID[domain.ServiceTag](), business, branch, domain.ServiceDetails{
		Category: "haircut", Name: name, Description: domain.Description{Ar: "قص وتصفيف"},
		Duration: 30 * time.Minute, Price: shared.Halalas(6000), SortOrder: sort,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoundTripAndOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewServices(migrated(t))
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	first := newService(t, business, branch, 5, t0)
	second := newService(t, business, branch, 1, t0.Add(time.Minute))
	third := newService(t, business, branch, 5, t0.Add(2*time.Minute))
	for _, s := range []*domain.Service{first, second, third} {
		if err := repo.Add(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.List(ctx, business, branch)
	if err != nil || len(list) != 3 {
		t.Fatalf("List = %d, %v", len(list), err)
	}
	// Sort order first, then the older one.
	if list[0].ID() != second.ID() || list[1].ID() != first.ID() || list[2].ID() != third.ID() {
		t.Errorf("order = %v, %v, %v", list[0].ID(), list[1].ID(), list[2].ID())
	}
	got := list[1]
	if got.Details() != first.Details() || got.IsActive() != first.IsActive() || got.Version() != 1 ||
		!got.CreatedAt().Equal(first.CreatedAt()) || got.BusinessID() != business || got.BranchID() != branch {
		t.Errorf("round trip = %+v, want %+v", got.Details(), first.Details())
	}
}

// A service is only found inside its own branch and business.
func TestScopedByBusinessAndBranch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewServices(migrated(t))
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	s := newService(t, business, branch, 0, t0)
	if err := repo.Add(ctx, s); err != nil {
		t.Fatal(err)
	}
	otherBusiness, otherBranch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	for name, where := range map[string][2]any{
		"other branch":   {business, otherBranch},
		"other business": {otherBusiness, branch},
	} {
		biz, br := where[0].(shared.BusinessID), where[1].(shared.BranchID)
		if list, _ := repo.List(ctx, biz, br); len(list) != 0 {
			t.Errorf("%s: listed %d", name, len(list))
		}
		err := repo.Update(ctx, biz, br, s.ID(), 1, func(*domain.Service) error { return nil })
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: update error = %v", name, err)
		}
	}
}

func TestUpdateAndVersion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := postgres.NewServices(migrated(t))
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	s := newService(t, business, branch, 0, t0)
	if err := repo.Add(ctx, s); err != nil {
		t.Fatal(err)
	}
	edit := func(version int, price int64) error {
		return repo.Update(ctx, business, branch, s.ID(), version, func(svc *domain.Service) error {
			d := svc.Details()
			d.Price = shared.Halalas(price)
			return svc.Edit(d, false, t0.Add(time.Hour))
		})
	}
	if err := edit(1, 7500); err != nil {
		t.Fatal(err)
	}
	if err := edit(1, 9900); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale: %v", err)
	}
	for _, v := range []int{0, -1, 1 << 40} {
		if err := edit(v, 1); !errors.Is(err, domain.ErrVersionConflict) {
			t.Errorf("version %d: %v", v, err)
		}
	}
	list, _ := repo.List(ctx, business, branch)
	if got := list[0]; got.Version() != 2 || got.IsActive() || got.Details().Price.Amount() != 7500 || !got.UpdatedAt().Equal(t0.Add(time.Hour)) {
		t.Errorf("after update = %+v", got)
	}
}

func TestParallelUpdatesOfTheSameVersion(t *testing.T) {
	t.Parallel()
	repo := postgres.NewServices(migrated(t))
	business, branch := shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag]()
	s := newService(t, business, branch, 0, t0)
	if err := repo.Add(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	var (
		wg               sync.WaitGroup
		start            = make(chan struct{})
		saved, conflicts atomic.Int32
	)
	for i := range 2 {
		wg.Go(func() {
			<-start
			err := repo.Update(t.Context(), business, branch, s.ID(), 1, func(svc *domain.Service) error {
				time.Sleep(20 * time.Millisecond) // overlap the transactions
				d := svc.Details()
				d.Price = shared.Halalas(int64(1000 * (i + 1)))
				return svc.Edit(d, true, t0)
			})
			switch {
			case err == nil:
				saved.Add(1)
			case errors.Is(err, domain.ErrVersionConflict):
				conflicts.Add(1)
			default:
				t.Errorf("Update: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if saved.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf("saved=%d conflicts=%d, want 1 and 1", saved.Load(), conflicts.Load())
	}
}

// The database refuses what the domain refuses, for rows written any other way.
func TestSchemaConstraints(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	repo := postgres.NewServices(pool)
	s := newService(t, shared.NewID[shared.BusinessTag](), shared.NewID[shared.BranchTag](), 0, t0)
	if err := repo.Add(ctx, s); err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]string{
		"services_duration_minutes_check": "duration_minutes = 32",
		"services_price_amount_check":     "price_amount = -1",
		"services_price_currency_check":   "price_currency = 'USD'",
		"services_category_code_check":    "category_code = 'massage'",
		"services_name_ar_check":          "name_ar = ''",
	} {
		_, err := pool.Exec(ctx, `UPDATE catalog.services SET `+set+` WHERE id = $1`, s.ID().UUID())
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != name {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}
