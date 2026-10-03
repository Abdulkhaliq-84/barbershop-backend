package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// listings is an in-memory domain.Listings holding a city's branches in
// search order (by name, or by distance: the order is the fake's data).
type listings struct {
	all    []domain.Listing
	asked  int // the limit of the last call
	near   *domain.Near
	filter *domain.Filter       // the last search's
	kept   *domain.Service      // the last KeepService call's
	hours  *domain.OpeningHours // the last KeepHours call's
	err    error
}

// now is the fake clock's time in every test: a Thursday morning.
var now = time.Date(2026, 10, 1, 6, 30, 0, 0, time.UTC)

func (l *listings) Keep(context.Context, domain.Listing) (bool, error) { return true, l.err }

func (l *listings) KeepService(_ context.Context, s domain.Service) (bool, error) {
	l.kept = &s
	return true, l.err
}

func (l *listings) KeepHours(_ context.Context, h domain.OpeningHours) (bool, error) {
	l.hours = &h
	return true, l.err
}

// page returns up to limit branches after the given one, the i-th from
// 10·i SAR.
func (l *listings) page(after *domain.Position, limit int) []domain.Found {
	l.asked = limit
	start := 0
	if after != nil {
		for i, x := range l.all {
			if x.Branch == after.Branch {
				start = i + 1
			}
		}
	}
	var out []domain.Found
	for i, x := range l.all[start:min(start+limit, len(l.all))] {
		out = append(out, domain.Found{Listing: x, PriceFrom: shared.Halalas(int64(1000 * (start + i)))})
	}
	return out
}

func (l *listings) InCity(_ context.Context, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	l.filter = &f
	return l.page(after, limit), l.err
}

// Near returns the same branches, the i-th 100·i metres away.
func (l *listings) Near(_ context.Context, near domain.Near, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	l.near, l.filter = &near, &f
	found := l.page(after, limit)
	for i, x := range found {
		found[i].DistanceM = float64(100 * slices.Index(l.all, x.Listing))
	}
	return found, l.err
}

// Matching returns the same branches, the i-th scoring 1 - i/100.
func (l *listings) Matching(_ context.Context, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	l.filter = &f
	found := l.page(after, limit)
	for i, x := range found {
		found[i].Score = 1 - float64(slices.Index(l.all, x.Listing))/100
	}
	return found, l.err
}

func branches(t *testing.T, n int) []domain.Listing {
	t.Helper()
	out := make([]domain.Listing, n)
	for i := range out {
		name, err := shared.NewLocalizedText(fmt.Sprintf("صالون %02d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		out[i] = domain.Listing{Branch: shared.NewID[shared.BranchTag](), Name: name, Listed: true}
	}
	return out
}

func TestSearch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	riyadh, _ := shared.ParseCity("riyadh")
	inRiyadh := app.Search{City: &riyadh}

	for name, tt := range map[string]struct {
		have     int
		wantPage int
		wantNext bool
	}{
		"none":                {0, 0, false},
		"less than a page":    {7, 7, false},
		"exactly a page":      {domain.PageSize, domain.PageSize, false},
		"one more than fits":  {domain.PageSize + 1, domain.PageSize, true},
		"several pages ahead": {3 * domain.PageSize, domain.PageSize, true},
	} {
		store := &listings{all: branches(t, tt.have)}
		page, err := app.NewHandlers(store, clock.NewFake(now)).Search(ctx, inRiyadh, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Found) != tt.wantPage || (page.Next != nil) != tt.wantNext || store.asked != domain.PageSize+1 {
			t.Errorf("%s: %d listings, next %v (asked for %d)", name, len(page.Found), page.Next, store.asked)
		}
		// The next page starts after the last listing shown.
		if tt.wantNext {
			last := page.Found[len(page.Found)-1]
			if *page.Next != (domain.Position{NameAr: last.Name.Ar(), Branch: last.Branch}) {
				t.Errorf("%s: next = %+v, last shown %s", name, page.Next, last.Branch)
			}
		}
	}

	// Paging through 45 branches shows each once, in order.
	store := &listings{all: branches(t, 45)}
	h := app.NewHandlers(store, clock.NewFake(now))
	var seen []domain.Listing
	var after *domain.Position
	for range 10 {
		page, err := h.Search(ctx, inRiyadh, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range page.Found {
			seen = append(seen, f.Listing)
		}
		if after = page.Next; after == nil {
			break
		}
	}
	if len(seen) != 45 {
		t.Fatalf("saw %d of 45", len(seen))
	}
	for i := range seen {
		if seen[i] != store.all[i] {
			t.Fatalf("listing %d out of order", i)
		}
	}
}

func TestSearchNear(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	olaya, _ := shared.NewGeoPoint(24.6911, 46.6851)
	riyadh, _ := shared.ParseCity("riyadh")
	store := &listings{all: branches(t, domain.PageSize+5)}
	h := app.NewHandlers(store, clock.NewFake(now))
	near := &domain.Near{Point: olaya, RadiusM: 10_000}

	// Near a place, in a city: both reach the store; the next page starts
	// after the last distance shown.
	page, err := h.Search(ctx, app.Search{City: &riyadh, Near: near}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if *store.near != *near || store.filter.City == nil || *store.filter.City != riyadh || store.filter.Text != "" {
		t.Errorf("asked near %+v with %+v", store.near, store.filter)
	}
	last := page.Found[len(page.Found)-1]
	if len(page.Found) != domain.PageSize || page.Next == nil ||
		*page.Next != (domain.Position{DistanceM: last.DistanceM, Branch: last.Branch}) || last.DistanceM != 1900 {
		t.Errorf("page of %d, next %+v, last %+v", len(page.Found), page.Next, last)
	}
	rest, err := h.Search(ctx, app.Search{Near: near}, page.Next)
	if err != nil || len(rest.Found) != 5 || rest.Next != nil || store.filter.City != nil {
		t.Errorf("rest: %d, next %v, filter %+v, %v", len(rest.Found), rest.Next, store.filter, err)
	}
	// A name near a place: still nearest first, the name normalised.
	if _, err := h.Search(ctx, app.Search{Near: near, Text: "  الأناقة "}, nil); err != nil || store.filter.Text != "الاناقه" {
		t.Errorf("a name near a place: filter %+v, %v", store.filter, err)
	}

	for name, tt := range map[string]struct {
		search app.Search
		want   error
	}{
		"nowhere":                {app.Search{}, domain.ErrNoPlace},
		"no radius":              {app.Search{Near: &domain.Near{Point: olaya}}, domain.ErrRadius},
		"a radius of 50 km":      {app.Search{Near: &domain.Near{Point: olaya, RadiusM: 50_000}}, nil},
		"a radius over 50 km":    {app.Search{Near: &domain.Near{Point: olaya, RadiusM: 50_001}}, domain.ErrRadius},
		"a city and no distance": {app.Search{City: &riyadh}, nil},
	} {
		if _, err := h.Search(ctx, tt.search, nil); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", name, err, tt.want)
		}
	}
}

func TestSearchByName(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	riyadh, _ := shared.ParseCity("riyadh")
	store := &listings{all: branches(t, domain.PageSize+3)}
	h := app.NewHandlers(store, clock.NewFake(now))

	// A name alone searches everywhere, best match first, normalised.
	page, err := h.Search(ctx, app.Search{Text: "صالون الأناقة"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	last := page.Found[len(page.Found)-1]
	if store.filter.Text != "صالون الاناقه" || store.filter.City != nil || store.near != nil ||
		page.Next == nil || *page.Next != (domain.Position{Score: last.Score, Branch: last.Branch}) || last.Score != 0.81 {
		t.Errorf("filter %+v, next %+v, last %+v", store.filter, page.Next, last)
	}
	rest, err := h.Search(ctx, app.Search{Text: "صالون الأناقة"}, page.Next)
	if err != nil || len(rest.Found) != 3 || rest.Next != nil {
		t.Errorf("rest: %d, next %v, %v", len(rest.Found), rest.Next, err)
	}
	// With a city: that city's.
	if _, err := h.Search(ctx, app.Search{City: &riyadh, Text: "elegance"}, nil); err != nil || store.filter.City == nil || *store.filter.City != riyadh {
		t.Errorf("in a city: %+v, %v", store.filter, err)
	}
	// Too short once normalised.
	for _, q := range []string{"a", "أ!", "ـ"} {
		if _, err := h.Search(ctx, app.Search{City: &riyadh, Text: q}, nil); !errors.Is(err, domain.ErrQueryTooShort) {
			t.Errorf("%q: %v", q, err)
		}
	}
	for name, tt := range map[string]struct {
		search app.Search
		want   app.Order
	}{
		"a city":              {app.Search{City: &riyadh}, app.ByName},
		"a name":              {app.Search{Text: "x"}, app.ByMatch},
		"a name in a city":    {app.Search{City: &riyadh, Text: "x"}, app.ByMatch},
		"a place":             {app.Search{Near: &domain.Near{}}, app.ByDistance},
		"a name near a place": {app.Search{Near: &domain.Near{}, Text: "x"}, app.ByDistance},
	} {
		if got := tt.search.Order(); got != tt.want {
			t.Errorf("%s: order %d, want %d", name, got, tt.want)
		}
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	h := app.NewHandlers(&listings{err: boom}, clock.NewFake(now))
	riyadh, _ := shared.ParseCity("riyadh")
	if _, err := h.Search(t.Context(), app.Search{City: &riyadh}, nil); !errors.Is(err, boom) {
		t.Errorf("browse: %v", err)
	}
	olaya, _ := shared.NewGeoPoint(24.6911, 46.6851)
	if _, err := h.Search(t.Context(), app.Search{Near: &domain.Near{Point: olaya, RadiusM: 1000}}, nil); !errors.Is(err, boom) {
		t.Errorf("near: %v", err)
	}
	if _, err := h.Search(t.Context(), app.Search{Text: "صالون"}, nil); !errors.Is(err, boom) {
		t.Errorf("by name: %v", err)
	}
	if err := h.Keep(t.Context(), domain.Listing{}); !errors.Is(err, boom) {
		t.Errorf("keep: %v", err)
	}
	if err := h.KeepService(t.Context(), domain.Service{}); !errors.Is(err, boom) {
		t.Errorf("keep a service: %v", err)
	}
	if err := h.KeepHours(t.Context(), domain.OpeningHours{}); !errors.Is(err, boom) {
		t.Errorf("keep opening hours: %v", err)
	}
}

// Every search asks about the clock's now, and only for open branches
// when told to.
func TestSearchOpenNow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	riyadh, _ := shared.ParseCity("riyadh")
	olaya, _ := shared.NewGeoPoint(24.6911, 46.6851)
	clk := clock.NewFake(now)
	store := &listings{all: branches(t, 2)}
	h := app.NewHandlers(store, clk)
	for name, s := range map[string]app.Search{
		"in a city":     {City: &riyadh},
		"near a place":  {Near: &domain.Near{Point: olaya, RadiusM: 1000}},
		"by name":       {Text: "صالون"},
		"a name nearby": {Near: &domain.Near{Point: olaya, RadiusM: 1000}, Text: "صالون"},
	} {
		for _, open := range []bool{false, true} {
			s.OpenNow = open
			later := now.Add(time.Duration(len(name)) * time.Minute)
			clk.Set(later)
			if _, err := h.Search(ctx, s, nil); err != nil || store.filter.OpenNow != open || !store.filter.At.Equal(later) {
				t.Errorf("%s, open %v: %+v, %v", name, open, store.filter, err)
			}
		}
	}
}

func TestKeepHours(t *testing.T) {
	t.Parallel()
	store := &listings{}
	o := domain.OpeningHours{Branch: shared.NewID[shared.BranchTag](), Version: 3, Open: [][2]int{{540, 1260}}}
	if err := app.NewHandlers(store, clock.NewFake(now)).KeepHours(t.Context(), o); err != nil || store.hours == nil ||
		store.hours.Version != 3 || len(store.hours.Open) != 1 || store.hours.Open[0] != [2]int{540, 1260} {
		t.Errorf("kept %+v, %v", store.hours, err)
	}
}

// A category reaches the store whatever the search's order, and each
// branch comes with its price from.
func TestSearchByCategory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	riyadh, _ := shared.ParseCity("riyadh")
	beard, _ := shared.ParseCategory("beard")
	olaya, _ := shared.NewGeoPoint(24.6911, 46.6851)
	store := &listings{all: branches(t, 3)}
	h := app.NewHandlers(store, clock.NewFake(now))

	page, err := h.Search(ctx, app.Search{City: &riyadh, Category: &beard}, nil)
	if err != nil || store.filter.Category == nil || *store.filter.Category != beard || store.filter.City == nil || *store.filter.City != riyadh {
		t.Fatalf("in a city: %+v, %v", store.filter, err)
	}
	if len(page.Found) != 3 || page.Found[2].PriceFrom != shared.Halalas(2000) {
		t.Errorf("found %+v", page.Found)
	}
	if _, err := h.Search(ctx, app.Search{City: &riyadh}, nil); err != nil || store.filter.Category != nil {
		t.Errorf("any category: %+v, %v", store.filter, err)
	}
	if _, err := h.Search(ctx, app.Search{Near: &domain.Near{Point: olaya, RadiusM: 1000}, Category: &beard}, nil); err != nil ||
		store.filter.Category == nil || *store.filter.Category != beard {
		t.Errorf("near a place: %+v, %v", store.filter, err)
	}
	if _, err := h.Search(ctx, app.Search{Text: "صالون", Category: &beard}, nil); err != nil ||
		store.filter.Category == nil || *store.filter.Category != beard {
		t.Errorf("by name: %+v, %v", store.filter, err)
	}
	// A category alone is not a place to search.
	if _, err := h.Search(ctx, app.Search{Category: &beard}, nil); !errors.Is(err, domain.ErrNoPlace) {
		t.Errorf("a category alone: %v", err)
	}
}

func TestKeepService(t *testing.T) {
	t.Parallel()
	store := &listings{}
	beard, _ := shared.ParseCategory("beard")
	s := domain.Service{
		Service: shared.NewID[shared.ServiceTag](), Branch: shared.NewID[shared.BranchTag](), Version: 2,
		Offered: true, Category: beard, PriceFrom: shared.Halalas(3500),
	}
	if err := app.NewHandlers(store, clock.NewFake(now)).KeepService(t.Context(), s); err != nil || store.kept == nil || *store.kept != s {
		t.Errorf("kept %+v, %v", store.kept, err)
	}
}
