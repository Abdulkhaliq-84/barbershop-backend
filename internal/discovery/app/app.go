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

// Search is what a customer looks for: the branches of a city, the
// branches near a point, or both (near a point, in that city).
type Search struct {
	City *shared.City // nil: any city
	Near *domain.Near // nil: by Arabic name, which needs a city
}

// Page is one page of search results. Next is where the next page starts;
// nil on the last page.
type Page struct {
	Found []domain.Found
	Next  *domain.Position
}

// Search returns a page of results, after the given position (nil for the
// first page): nearest first when searching near a point, else by name.
func (h *Handlers) Search(ctx context.Context, s Search, after *domain.Position) (Page, error) {
	// One more than a page says whether another page follows.
	var found []domain.Found
	switch {
	case s.Near != nil:
		if s.Near.RadiusM <= 0 || s.Near.RadiusM > domain.MaxRadiusKm*1000 {
			return Page{}, domain.ErrRadius
		}
		got, err := h.listings.Near(ctx, *s.Near, s.City, after, domain.PageSize+1)
		if err != nil {
			return Page{}, fmt.Errorf("search near: %w", err)
		}
		found = got
	case s.City != nil:
		got, err := h.listings.InCity(ctx, *s.City, after, domain.PageSize+1)
		if err != nil {
			return Page{}, fmt.Errorf("browse %s: %w", s.City.Code(), err)
		}
		for _, l := range got {
			found = append(found, domain.Found{Listing: l})
		}
	default:
		return Page{}, domain.ErrNoPlace
	}
	if len(found) <= domain.PageSize {
		return Page{Found: found}, nil
	}
	found = found[:domain.PageSize]
	last := found[len(found)-1]
	next := &domain.Position{Branch: last.Branch}
	if s.Near != nil {
		next.DistanceM = last.DistanceM
	} else {
		next.NameAr = last.Name.Ar()
	}
	return Page{Found: found, Next: next}, nil
}
