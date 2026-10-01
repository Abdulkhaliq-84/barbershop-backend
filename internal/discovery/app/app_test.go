package app_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// listings is an in-memory domain.Listings holding a city's branches in
// search order.
type listings struct {
	all   []domain.Listing
	asked int // the limit of the last InCity call
	err   error
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

func TestBrowseCity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	riyadh, _ := shared.ParseCity("riyadh")

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
		page, err := app.NewHandlers(store).BrowseCity(ctx, riyadh, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Listings) != tt.wantPage || (page.Next != nil) != tt.wantNext || store.asked != domain.PageSize+1 {
			t.Errorf("%s: %d listings, next %v (asked for %d)", name, len(page.Listings), page.Next, store.asked)
		}
		// The next page starts after the last listing shown.
		if tt.wantNext {
			last := page.Listings[len(page.Listings)-1]
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
		page, err := h.BrowseCity(ctx, riyadh, after)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page.Listings...)
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

func TestErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	h := app.NewHandlers(&listings{err: boom})
	riyadh, _ := shared.ParseCity("riyadh")
	if _, err := h.BrowseCity(t.Context(), riyadh, nil); !errors.Is(err, boom) {
		t.Errorf("browse: %v", err)
	}
	if err := h.Keep(t.Context(), domain.Listing{}); !errors.Is(err, boom) {
		t.Errorf("keep: %v", err)
	}
}
