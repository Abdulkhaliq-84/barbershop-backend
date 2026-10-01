// Package app holds discovery's use cases: keeping the listings from
// business's events, and answering customers' searches.
package app

import (
	"context"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers are discovery's use cases.
type Handlers struct {
	listings domain.Listings
}

// NewHandlers wires the use cases.
func NewHandlers(listings domain.Listings) *Handlers {
	return &Handlers{listings: listings}
}

// Keep applies a branch event to discovery's copy. An event can arrive more
// than once and out of order, so an older version than the copy's changes
// nothing: handling the same event twice is safe.
func (h *Handlers) Keep(ctx context.Context, l domain.Listing) error {
	if _, err := h.listings.Keep(ctx, l); err != nil {
		return fmt.Errorf("keep listing %s: %w", l.Branch, err)
	}
	return nil
}

// Page is one page of search results. Next is where the next page starts;
// nil on the last page.
type Page struct {
	Listings []domain.Listing
	Next     *domain.Position
}

// BrowseCity returns a page of the city's listed branches, after the given
// position (nil for the first page).
func (h *Handlers) BrowseCity(ctx context.Context, city shared.City, after *domain.Position) (Page, error) {
	// One more than a page says whether another page follows.
	got, err := h.listings.InCity(ctx, city, after, domain.PageSize+1)
	if err != nil {
		return Page{}, fmt.Errorf("browse %s: %w", city.Code(), err)
	}
	if len(got) <= domain.PageSize {
		return Page{Listings: got}, nil
	}
	got = got[:domain.PageSize]
	last := got[len(got)-1]
	return Page{Listings: got, Next: &domain.Position{NameAr: last.Name.Ar(), Branch: last.Branch}}, nil
}
