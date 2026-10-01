package postgres_test

import (
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
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

func TestNear(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	r := postgres.NewListings(pool)
	// Due north of a point, 0.009° of latitude is about 1 km.
	olaya, _ := shared.NewGeoPoint(24.6911, 46.6851)
	north := func(ar string, km float64) domain.Listing {
		l := listing(t, ar)
		l.Location, _ = shared.NewGeoPoint(24.6911+0.009*km, 46.6851)
		return l
	}
	jeddahCoded := north("أ", 0.5) // filed under another city, to test the filter
	jeddahCoded.City, _ = shared.ParseCity("jeddah")
	km1, km3, km9, km12 := north("ب", 1), north("ت", 3), north("ث", 9), north("ج", 12)
	tie1, tie2 := north("ح", 2), north("خ", 2) // the same place: the ID decides
	if tie2.Branch.String() < tie1.Branch.String() {
		tie1, tie2 = tie2, tie1
	}
	hidden := north("د", 0.2)
	hidden.Listed = false
	for _, l := range []domain.Listing{km9, tie2, hidden, km1, km12, jeddahCoded, tie1, km3} {
		if _, err := r.Keep(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	near := func(radiusKm float64, city *shared.City, after *domain.Position, limit int) []domain.Found {
		t.Helper()
		got, err := r.Near(ctx, domain.Near{Point: olaya, RadiusM: radiusKm * 1000}, city, after, limit)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	ids := func(found []domain.Found) []shared.BranchID {
		out := make([]shared.BranchID, len(found))
		for i, f := range found {
			out[i] = f.Branch
		}
		return out
	}

	// Within 10 km, nearest first; hidden and too-far branches aren't there.
	all := near(10, nil, nil, 20)
	want := []shared.BranchID{jeddahCoded.Branch, km1.Branch, tie1.Branch, tie2.Branch, km3.Branch, km9.Branch}
	if !slices.Equal(ids(all), want) {
		t.Fatalf("within 10 km = %v, want %v", ids(all), want)
	}
	if all[1].Listing != km1 {
		t.Errorf("listing read back = %+v, want %+v", all[1].Listing, km1)
	}
	// Distances in metres, close to the truth (a sphere, not the spheroid).
	for i, km := range []float64{0.5, 1, 2, 2, 3, 9} {
		if d := all[i].DistanceM; math.Abs(d-km*1000) > km*1000*0.01 {
			t.Errorf("%d: %.0f m, want about %.0f", i, d, km*1000)
		}
	}
	if got := near(0.6, nil, nil, 20); !slices.Equal(ids(got), want[:1]) {
		t.Errorf("within 600 m = %v", ids(got))
	}
	riyadh, _ := shared.ParseCity("riyadh")
	if got := near(10, &riyadh, nil, 20); !slices.Equal(ids(got), want[1:]) {
		t.Errorf("in riyadh = %v", ids(got))
	}

	// Pages continue exactly after the last one shown, even between two
	// branches at the same distance.
	at := func(f domain.Found) *domain.Position {
		return &domain.Position{DistanceM: f.DistanceM, Branch: f.Branch}
	}
	if got := near(10, nil, nil, 2); !slices.Equal(ids(got), want[:2]) {
		t.Errorf("first page = %v", ids(got))
	}
	if got := near(10, nil, at(all[1]), 2); !slices.Equal(ids(got), want[2:4]) {
		t.Errorf("second page = %v", ids(got))
	}
	if got := near(10, nil, at(all[2]), 2); !slices.Equal(ids(got), want[3:5]) {
		t.Errorf("after the first of two at the same place = %v", ids(got))
	}
	if got := near(10, nil, at(all[5]), 2); len(got) != 0 {
		t.Errorf("after the last = %v", ids(got))
	}
}

// The search reads the GiST index, not every row.
func TestNearUsesTheIndex(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO discovery.branch_listings
			(branch_id, business_id, version, listed, name_ar, city_code, address, latitude, longitude, timezone, updated_at)
		SELECT gen_random_uuid(), gen_random_uuid(), 1, true, 'صالون', 'riyadh', 'شارع', 17 + random() * 15, 37 + random() * 18, 'Asia/Riyadh', now()
		FROM generate_series(1, 5000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE discovery.branch_listings`); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `EXPLAIN (COSTS OFF)
		SELECT branch_id FROM discovery.branch_listings
		WHERE listed AND ST_DWithin(location, ST_MakePoint(46.6851, 24.6911)::geography, 10000, false)
		ORDER BY location <-> ST_MakePoint(46.6851, 24.6911)::geography, branch_id LIMIT 21`)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if !strings.Contains(plan.String(), "branch_listings_location_idx") {
		t.Errorf("the plan doesn't use the location index:\n%s", plan.String())
	}
}

// Branches at the same place are ordered by ID, so paging one at a time
// shows each exactly once, in that order, whatever order they were saved in.
func TestNearTies(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migrated(t)
	r := postgres.NewListings(pool)
	olaya, _ := shared.NewGeoPoint(24.6911, 46.6851)
	var same []domain.Listing
	for range 8 {
		l := listing(t, "صالون")
		l.Location, _ = shared.NewGeoPoint(24.70, 46.69)
		same = append(same, l)
	}
	slices.SortFunc(same, func(a, b domain.Listing) int { return strings.Compare(b.Branch.String(), a.Branch.String()) })
	for _, l := range same { // saved in descending ID order
		if _, err := r.Keep(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	slices.Reverse(same)
	for _, size := range []int{1, 3} {
		var seen []shared.BranchID
		var after *domain.Position
		for range 20 {
			got, err := r.Near(ctx, domain.Near{Point: olaya, RadiusM: 10_000}, nil, after, size)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range got {
				seen = append(seen, f.Branch)
			}
			if len(got) < size {
				break
			}
			last := got[len(got)-1]
			after = &domain.Position{DistanceM: last.DistanceM, Branch: last.Branch}
		}
		want := make([]shared.BranchID, len(same))
		for i, l := range same {
			want[i] = l.Branch
		}
		if !slices.Equal(seen, want) {
			t.Errorf("pages of %d = %v, want %v", size, seen, want)
		}
	}
}
