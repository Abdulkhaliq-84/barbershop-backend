package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// listings is an in-memory domain.Listings holding a city's branches in
// search order (by name, or by distance: the order is the fake's data).
type listings struct {
	all    []domain.Listing
	asked  int // the limit of the last call
	near   *domain.Near
	filter *domain.Filter // the last Near or Matching call's
	err    error
}

func (l *listings) Keep(context.Context, domain.Listing) (bool, error) { return true, l.err }

func (l *listings) InCity(_ context.Context, _ shared.City, after *domain.Position, limit int) ([]domain.Listing, error) {
	l.asked = limit
	start := 0
	if after != nil {
		for i, x := range l.all {
			if x.Branch == after.Branch {
				start = i + 1
			}
		}
	}
	return l.all[start:min(start+limit, len(l.all))], l.err
}

// Near returns the same branches, the i-th 100·i metres away.
func (l *listings) Near(ctx context.Context, near domain.Near, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	l.near, l.filter = &near, &f
	got, err := l.InCity(ctx, shared.City{}, after, limit)
	found := make([]domain.Found, len(got))
	for i, x := range got {
		found[i] = domain.Found{Listing: x, DistanceM: float64(100 * slices.Index(l.all, x))}
	}
	return found, err
}

// Matching returns the same branches, the i-th scoring 1 - i/100.
func (l *listings) Matching(ctx context.Context, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	l.filter = &f
	got, err := l.InCity(ctx, shared.City{}, after, limit)
	found := make([]domain.Found, len(got))
	for i, x := range got {
		found[i] = domain.Found{Listing: x, Score: 1 - float64(slices.Index(l.all, x))/100}
	}
	return found, err
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
		page, err := app.NewHandlers(store).Search(ctx, inRiyadh, nil)
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
	h := app.NewHandlers(store)
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
	h := app.NewHandlers(store)
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
	h := app.NewHandlers(store)

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
	h := app.NewHandlers(&listings{err: boom})
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
}
