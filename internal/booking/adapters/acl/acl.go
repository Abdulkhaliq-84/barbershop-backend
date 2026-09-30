// Package acl is booking's anti-corruption layer: it asks business, catalog
// and scheduling through their root packages and answers in booking's own
// terms (app ports, domain errors), so their types never reach booking's
// model.
package acl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Branches answers app.Branches from business.
type Branches struct{ business *business.Module }

// NewBranches wraps the business module.
func NewBranches(b *business.Module) *Branches { return &Branches{business: b} }

// Bookable returns a published branch of an active business.
func (a *Branches) Bookable(ctx context.Context, id shared.BranchID) (app.Branch, error) {
	b, err := a.business.BookableBranch(ctx, id)
	switch {
	case errors.Is(err, business.ErrNotFound):
		return app.Branch{}, domain.ErrNotFound
	case err != nil:
		return app.Branch{}, fmt.Errorf("bookable branch: %w", err)
	}
	return app.Branch{
		BusinessID: b.BusinessID, ID: b.BranchID, Location: b.Location,
		Policy: app.Policy{
			MinLead: b.Policy.MinLead, HorizonDays: b.Policy.HorizonDays,
			SlotInterval: b.Policy.SlotInterval, Buffer: b.Policy.Buffer,
		},
	}, nil
}

// Barbers returns which of staff work at the branch, with their names.
func (a *Branches) Barbers(ctx context.Context, biz shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) ([]app.Barber, error) {
	barbers, err := a.business.BranchBarbers(ctx, biz, branch, staff)
	if err != nil {
		return nil, fmt.Errorf("branch barbers: %w", err)
	}
	out := make([]app.Barber, 0, len(barbers))
	for _, b := range barbers {
		out = append(out, app.Barber{ID: b.ID, Name: b.Name})
	}
	return out, nil
}

// Menus answers app.Menus from catalog.
type Menus struct{ catalog *catalog.Module }

// NewMenus wraps the catalog module.
func NewMenus(c *catalog.Module) *Menus { return &Menus{catalog: c} }

// Menu returns the branch's active services and who performs them.
func (a *Menus) Menu(ctx context.Context, biz shared.BusinessID, branch shared.BranchID) ([]app.MenuItem, error) {
	items, err := a.catalog.Menu(ctx, biz, branch)
	if err != nil {
		return nil, fmt.Errorf("menu: %w", err)
	}
	out := make([]app.MenuItem, 0, len(items))
	for _, it := range items {
		m := app.MenuItem{ID: it.ID, Name: it.Name}
		for _, p := range it.Performers {
			m.Performers = append(m.Performers, app.Performer{Staff: p.Staff, Duration: p.Duration, Price: p.Price})
		}
		out = append(out, m)
	}
	return out, nil
}

// Schedules answers app.Schedules from scheduling.
type Schedules struct{ scheduling *scheduling.Module }

// NewSchedules wraps the scheduling module.
func NewSchedules(s *scheduling.Module) *Schedules { return &Schedules{scheduling: s} }

// WorkingWindows returns when each barber can work in [from, to).
func (a *Schedules) WorkingWindows(ctx context.Context, biz shared.BusinessID, branch shared.BranchID, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, error) {
	w, err := a.scheduling.WorkingWindows(ctx, biz, branch, staff, from, to)
	if errors.Is(err, scheduling.ErrNotFound) {
		// A barber left the branch between business's answer and this one
		// (the branch itself came from business, so it's theirs): show no
		// free times for now rather than fail the whole request.
		return map[shared.StaffID][]shared.Interval{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("working windows: %w", err)
	}
	return w, nil
}
