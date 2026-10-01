package postgres_test

import (
	"log/slog"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
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

// listing is a published branch in Riyadh, as of version 2.
func listing(t *testing.T, ar string) domain.Listing {
	t.Helper()
	name, err := shared.NewLocalizedText(ar, "Barbers")
	if err != nil {
		t.Fatal(err)
	}
	city, _ := shared.ParseCity("riyadh")
	at, _ := shared.NewGeoPoint(24.6911, 46.6851)
	return domain.Listing{
		Branch: shared.NewID[shared.BranchTag](), Business: shared.NewID[shared.BusinessTag](), Version: 2, Listed: true,
		Name: name, City: city, District: "العليا", Address: "شارع العليا العام", Location: at,
		Phone: "+966551234567", Timezone: "Asia/Riyadh", UpdatedAt: t0,
	}
}

func inCity(t *testing.T, r *postgres.Listings, code string, after *domain.Position, limit int) []domain.Listing {
	t.Helper()
	city, err := shared.ParseCity(code)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.InCity(t.Context(), city, after, limit)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// stored reads a copy back, listed or not.
func stored(t *testing.T, pool *pgxpool.Pool, l domain.Listing) (version int, listed bool, name string) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `SELECT version, listed, name_ar FROM discovery.branch_listings WHERE branch_id = $1`,
		l.Branch.UUID()).Scan(&version, &listed, &name); err != nil {
		t.Fatal(err)
	}
	return version, listed, name
}

func TestKeep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	r := postgres.NewListings(pool)
	keep := func(l domain.Listing) bool {
		t.Helper()
		saved, err := r.Keep(ctx, l)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}

	// Published: a copy, listed and complete.
	v2 := listing(t, "صالون الأناقة")
	if !keep(v2) {
		t.Fatal("the first event wasn't saved")
	}
	if got := inCity(t, r, "riyadh", nil, 10); len(got) != 1 || got[0] != v2 {
		t.Fatalf("listed = %+v, want %+v", got, v2)
	}

	// The same event again, or an older one, changes nothing.
	if keep(v2) {
		t.Error("the same version was saved twice")
	}
	v1 := v2
	v1.Version, v1.Listed = 1, false
	v1.Name, _ = shared.NewLocalizedText("مسودة", "")
	if keep(v1) {
		t.Error("an older version replaced a newer one")
	}
	if v, listed, name := stored(t, pool, v2); v != 2 || !listed || name != "صالون الأناقة" {
		t.Errorf("after an old event: v%d listed %v %q", v, listed, name)
	}

	// Newer: unpublished hides it; edited while hidden, it stays hidden.
	v3 := v2
	v3.Version, v3.Listed = 3, false
	v4 := v3
	v4.Version, v4.Address = 4, "طريق الملك فهد"
	if !keep(v3) || !keep(v4) {
		t.Fatal("newer versions weren't saved")
	}
	if got := inCity(t, r, "riyadh", nil, 10); len(got) != 0 {
		t.Errorf("an unpublished branch is listed: %+v", got)
	}

	// Published again in another city: listed there, not here.
	v5 := v4
	v5.Version, v5.Listed = 5, true
	v5.City, _ = shared.ParseCity("jeddah")
	keep(v5)
	if got := inCity(t, r, "jeddah", nil, 10); len(got) != 1 || got[0] != v5 {
		t.Errorf("jeddah = %+v", got)
	}
	if got := inCity(t, r, "riyadh", nil, 10); len(got) != 0 {
		t.Errorf("riyadh still lists it: %+v", got)
	}

	// Out of order: the unpublish (v7) arrives before the edit (v6). The
	// edit must not bring the branch back.
	v7 := v5
	v7.Version, v7.Listed = 7, false
	v6 := v5
	v6.Version, v6.District = 6, "الحمراء"
	keep(v7)
	keep(v6)
	if v, listed, _ := stored(t, pool, v5); v != 7 || listed {
		t.Errorf("after a late edit: v%d listed %v", v, listed)
	}
}

// Many events for one branch at once, in any order: the newest wins.
func TestKeepConcurrently(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	r := postgres.NewListings(pool)
	base := listing(t, "صالون")
	versions := rand.Perm(30)
	var wg sync.WaitGroup
	for _, v := range versions {
		l := base
		l.Version, l.Listed = v+1, v%2 == 0
		wg.Go(func() {
			if _, err := r.Keep(ctx, l); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	// Version 30 is v = 29: odd, so unpublished.
	if v, listed, _ := stored(t, pool, base); v != 30 || listed {
		t.Errorf("after all events: v%d listed %v, want v30 not listed", v, listed)
	}
}

func TestInCity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	r := postgres.NewListings(pool)
	// Code point order: أ (U+0623) < ب (U+0628) < ت (U+062A). Two share a
	// name; the branch ID breaks the tie.
	alef, ba1, ba2, ta := listing(t, "أ"), listing(t, "ب"), listing(t, "ب"), listing(t, "ت")
	if ba2.Branch.String() < ba1.Branch.String() {
		ba1, ba2 = ba2, ba1
	}
	hidden := listing(t, "آ")
	hidden.Listed = false
	jeddah := listing(t, "أ")
	jeddah.City, _ = shared.ParseCity("jeddah")
	for _, l := range []domain.Listing{ta, ba2, hidden, alef, jeddah, ba1} {
		if _, err := r.Keep(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	want := []domain.Listing{alef, ba1, ba2, ta}
	first := inCity(t, r, "riyadh", nil, 2)
	if len(first) != 2 || first[0] != want[0] || first[1] != want[1] {
		t.Fatalf("first page = %+v", first)
	}
	rest := inCity(t, r, "riyadh", &domain.Position{NameAr: first[1].Name.Ar(), Branch: first[1].Branch}, 10)
	if len(rest) != 2 || rest[0] != want[2] || rest[1] != want[3] {
		t.Errorf("next page = %+v", rest)
	}
	if got := inCity(t, r, "riyadh", &domain.Position{NameAr: ta.Name.Ar(), Branch: ta.Branch}, 10); len(got) != 0 {
		t.Errorf("after the last: %+v", got)
	}
	if got := inCity(t, r, "dammam", nil, 10); len(got) != 0 {
		t.Errorf("a city with no branches: %+v", got)
	}
}
