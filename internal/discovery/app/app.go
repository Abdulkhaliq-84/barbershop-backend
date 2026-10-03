// Package app holds discovery's use cases: keeping the listings from
// business's and catalog's events, and answering customers' searches.
package app

import (
	"context"
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers are discovery's use cases.
type Handlers struct {
	listings domain.Listings
	clock    clock.Clock // "open now" is open at this clock's now
}

// NewHandlers wires the use cases.
func NewHandlers(listings domain.Listings, clk clock.Clock) *Handlers {
	return &Handlers{listings: listings, clock: clk}
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

// KeepService applies a service event to discovery's copy, the same way:
// an older version than the copy's changes nothing.
func (h *Handlers) KeepService(ctx context.Context, s domain.Service) error {
	if _, err := h.listings.KeepService(ctx, s); err != nil {
		return fmt.Errorf("keep service %s: %w", s.Service, err)
	}
	return nil
}

// KeepHours applies an opening hours event to discovery's copy, the same
// way: an older version than the copy's changes nothing.
func (h *Handlers) KeepHours(ctx context.Context, o domain.OpeningHours) error {
	if _, err := h.listings.KeepHours(ctx, o); err != nil {
		return fmt.Errorf("keep opening hours of %s: %w", o.Branch, err)
	}
	return nil
}

// Branch returns the branch's public page, open now or not by the clock;
// domain.ErrNotFound unless search would show the branch.
func (h *Handlers) Branch(ctx context.Context, id shared.BranchID) (domain.Page, error) {
	p, err := h.listings.Page(ctx, id, h.clock.Now())
	if err != nil {
		return domain.Page{}, fmt.Errorf("branch page: %w", err)
	}
	return p, nil
}

// Search is what a customer looks for: the branches of a city, near a
// point, or with a name, or several of these at once; optionally only
// those offering a kind of service, or open now.
type Search struct {
	City     *shared.City     // nil: any city
	Near     *domain.Near     // nil: no place
	Text     string           // what the customer typed to find a name; "": any name
	Category *shared.Category // nil: any service
	OpenNow  bool             // only branches open now
}

// Order is how a search's results are sorted, and so what a page's
// position holds.
type Order int

// The orders: near a point, nearest first; by name searched for, best
// match first; else browsing a city, by Arabic name.
const (
	ByName Order = iota
	ByDistance
	ByMatch
)

// Order says how the search's results are sorted.
func (s Search) Order() Order {
	switch {
	case s.Near != nil:
		return ByDistance
	case s.Text != "":
		return ByMatch
	default:
		return ByName
	}
}

// Page is one page of search results. Next is where the next page starts;
// nil on the last page.
type Page struct {
	Found []domain.Found
	Next  *domain.Position
}

// Search returns a page of results, after the given position (nil for the
// first page), in the search's Order.
func (h *Handlers) Search(ctx context.Context, s Search, after *domain.Position) (Page, error) {
	f := domain.Filter{City: s.City, Category: s.Category, OpenNow: s.OpenNow, At: h.clock.Now()}
	if s.Text != "" {
		text, err := domain.ParseQuery(s.Text)
		if err != nil {
			return Page{}, err
		}
		f.Text = text
	}
	// One more than a page says whether another page follows.
	var found []domain.Found
	var err error
	switch s.Order() {
	case ByDistance:
		if s.Near.RadiusM <= 0 || s.Near.RadiusM > domain.MaxRadiusKm*1000 {
			return Page{}, domain.ErrRadius
		}
		found, err = h.listings.Near(ctx, *s.Near, f, after, domain.PageSize+1)
	case ByMatch:
		found, err = h.listings.Matching(ctx, f, after, domain.PageSize+1)
	case ByName:
		if s.City == nil {
			return Page{}, domain.ErrNoPlace
		}
		found, err = h.listings.InCity(ctx, f, after, domain.PageSize+1)
	}
	if err != nil {
		return Page{}, fmt.Errorf("search: %w", err)
	}
	if len(found) <= domain.PageSize {
		return Page{Found: found}, nil
	}
	found = found[:domain.PageSize]
	last := found[len(found)-1]
	next := &domain.Position{Branch: last.Branch}
	switch s.Order() {
	case ByDistance:
		next.DistanceM = last.DistanceM
	case ByMatch:
		next.Score = last.Score
	case ByName:
		next.NameAr = last.Name.Ar()
	}
	return Page{Found: found, Next: next}, nil
}
