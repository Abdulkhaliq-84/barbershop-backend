// Package app holds booking's use cases. Booking combines three other
// modules — business (the branch, its barbers), catalog (the menu) and
// scheduling (working windows) — through ports that adapters/acl
// implements with their root packages.
package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Policy is how a branch takes bookings, as far as booking needs it.
type Policy struct {
	MinLead      time.Duration // earliest start: now + MinLead
	HorizonDays  int           // latest start: on today + HorizonDays
	SlotInterval time.Duration // starts every SlotInterval on the clock
	Buffer       time.Duration // after each appointment, before the next
}

// Branch is a bookable branch: published, of an active business.
type Branch struct {
	BusinessID shared.BusinessID
	ID         shared.BranchID
	Location   *time.Location
	Policy     Policy
}

// Barber is a staff member as customers see them.
type Barber struct {
	ID   shared.StaffID
	Name string
}

// Branches finds bookable branches and their barbers (business).
type Branches interface {
	// Bookable returns a published branch of an active business, or
	// domain.ErrNotFound.
	Bookable(ctx context.Context, branch shared.BranchID) (Branch, error)
	// Barbers returns which of staff are active staff working at the
	// branch, with their names, in the order asked.
	Barbers(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) ([]Barber, error)
}

// MenuItem is an active service and who performs it.
type MenuItem struct {
	ID         shared.ServiceID
	Name       shared.LocalizedText
	Performers []Performer
}

// Performer is one barber's version of a service: their duration and price.
type Performer struct {
	Staff    shared.StaffID
	Duration time.Duration
	Price    shared.Money
}

// Menus reads a branch's active services (catalog).
type Menus interface {
	Menu(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]MenuItem, error)
}

// Schedules reads working windows (scheduling): opening hours ∩ each
// barber's schedule − time off, sorted and merged.
type Schedules interface {
	WorkingWindows(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, error)
}

// Busy reads barbers' active appointments (booking's own repository).
type Busy interface {
	Busy(ctx context.Context, staff []shared.StaffID, from, to time.Time) (map[shared.StaffID][]shared.Interval, error)
}

// AvailabilityQuery asks when a branch can do some services on a day.
type AvailabilityQuery struct {
	Branch   shared.BranchID
	Day      domain.Day
	Services []shared.ServiceID // 1 to 5, performed back to back by one barber
	Barber   *shared.StaffID    // nil: any barber
}

// Offer is what a barber would charge and take for the chosen services.
type Offer struct {
	Barber
	Duration time.Duration // the services, without the buffer
	Price    shared.Money
}

// Availability is the answer: who could do it, and when.
type Availability struct {
	Branch Branch
	Day    domain.Day
	Offers []Offer       // every barber who performs all the services, by ID
	Slots  []domain.Slot // start times, each with who is free then
}

// AvailabilityHandlers answer availability questions. Anyone may ask,
// signed in or not: a published branch's free times are public.
type AvailabilityHandlers struct {
	branches  Branches
	menus     Menus
	schedules Schedules
	busy      Busy
	clock     clock.Clock
}

// NewAvailabilityHandlers wires the use case.
func NewAvailabilityHandlers(branches Branches, menus Menus, schedules Schedules, busy Busy, clk clock.Clock) *AvailabilityHandlers {
	return &AvailabilityHandlers{branches: branches, menus: menus, schedules: schedules, busy: busy, clock: clk}
}

// Query returns the slots on q.Day. A day before today or past the
// booking horizon has none (not an error: the app shows an empty day).
func (h *AvailabilityHandlers) Query(ctx context.Context, q AvailabilityQuery) (Availability, error) {
	if len(q.Services) == 0 || len(q.Services) > domain.MaxServices || hasDuplicates(q.Services) {
		return Availability{}, domain.ErrNoServices
	}
	branch, err := h.branches.Bookable(ctx, q.Branch)
	if err != nil {
		return Availability{}, err
	}
	menu, err := h.menus.Menu(ctx, branch.BusinessID, branch.ID)
	if err != nil {
		return Availability{}, fmt.Errorf("availability: %w", err)
	}
	offers, err := offersFor(q.Services, menu)
	if err != nil {
		return Availability{}, err
	}
	offers, err = h.stillThere(ctx, branch, offers)
	if err != nil {
		return Availability{}, err
	}
	if q.Barber != nil {
		i := slices.IndexFunc(offers, func(o Offer) bool { return o.ID == *q.Barber })
		if i < 0 {
			return Availability{}, domain.ErrBarberUnavailable
		}
		offers = offers[i : i+1]
	}
	out := Availability{Branch: branch, Day: q.Day, Offers: offers}

	now := h.clock.Now()
	today := domain.DayOf(now.In(branch.Location))
	last := today.AddDays(branch.Policy.HorizonDays)
	if len(offers) == 0 || q.Day.Compare(today) < 0 || q.Day.Compare(last) > 0 {
		return out, nil
	}
	cands, err := h.candidates(ctx, branch, q.Day, offers)
	if err != nil {
		return Availability{}, err
	}
	out.Slots = domain.Slots(q.Day, domain.SlotRules{
		Location: branch.Location,
		Interval: branch.Policy.SlotInterval,
		Earliest: now.Add(branch.Policy.MinLead),
		Latest:   last.AddDays(1).Start(branch.Location),
	}, cands)
	return out, nil
}

// offersFor returns the barbers who perform every chosen service, with
// their total duration and price, by ID.
func offersFor(services []shared.ServiceID, menu []MenuItem) ([]Offer, error) {
	var offers []Offer
	for n, id := range services {
		i := slices.IndexFunc(menu, func(m MenuItem) bool { return m.ID == id })
		if i < 0 {
			return nil, domain.ErrServiceUnavailable
		}
		byStaff := make(map[shared.StaffID]Performer, len(menu[i].Performers))
		for _, p := range menu[i].Performers {
			byStaff[p.Staff] = p
		}
		if n == 0 {
			for _, p := range menu[i].Performers {
				offers = append(offers, Offer{Barber: Barber{ID: p.Staff}, Duration: p.Duration, Price: p.Price})
			}
			continue
		}
		kept := offers[:0]
		for _, o := range offers {
			p, ok := byStaff[o.ID]
			if !ok {
				continue // doesn't perform this one
			}
			price, err := o.Price.Add(p.Price)
			if err != nil {
				return nil, fmt.Errorf("availability: %w", err)
			}
			o.Duration += p.Duration
			o.Price = price
			kept = append(kept, o)
		}
		offers = kept
	}
	slices.SortFunc(offers, func(a, b Offer) int { return cmp.Compare(a.ID.String(), b.ID.String()) })
	return offers, nil
}

// stillThere keeps the offers of barbers who are active staff of the
// branch today (an offering may outlive its barber's place there), with
// their names.
func (h *AvailabilityHandlers) stillThere(ctx context.Context, branch Branch, offers []Offer) ([]Offer, error) {
	if len(offers) == 0 {
		return nil, nil
	}
	ids := make([]shared.StaffID, 0, len(offers))
	for _, o := range offers {
		ids = append(ids, o.ID)
	}
	barbers, err := h.branches.Barbers(ctx, branch.BusinessID, branch.ID, ids)
	if err != nil {
		return nil, fmt.Errorf("availability: %w", err)
	}
	names := make(map[shared.StaffID]string, len(barbers))
	for _, b := range barbers {
		names[b.ID] = b.Name
	}
	kept := offers[:0]
	for _, o := range offers {
		if name, ok := names[o.ID]; ok {
			o.Name = name
			kept = append(kept, o)
		}
	}
	return kept, nil
}

// candidates loads each barber's working windows and appointments around
// the day — at the same time: they come from two modules and don't depend
// on each other.
func (h *AvailabilityHandlers) candidates(ctx context.Context, branch Branch, day domain.Day, offers []Offer) ([]domain.Candidate, error) {
	ids := make([]shared.StaffID, 0, len(offers))
	longest := time.Duration(0)
	for _, o := range offers {
		ids = append(ids, o.ID)
		longest = max(longest, o.Duration+branch.Policy.Buffer)
	}
	// A booking may start late in the day and end after midnight.
	from := day.Start(branch.Location)
	to := day.AddDays(1).Start(branch.Location).Add(longest)

	var windows, busy map[shared.StaffID][]shared.Interval
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		windows, err = h.schedules.WorkingWindows(gctx, branch.BusinessID, branch.ID, ids, from, to)
		return err
	})
	g.Go(func() (err error) {
		busy, err = h.busy.Busy(gctx, ids, from, to)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("availability: %w", err)
	}
	cands := make([]domain.Candidate, 0, len(offers))
	for _, o := range offers {
		cands = append(cands, domain.Candidate{
			Staff:   o.ID,
			Length:  o.Duration + branch.Policy.Buffer,
			Windows: windows[o.ID],
			Busy:    busy[o.ID],
		})
	}
	return cands, nil
}

func hasDuplicates(ids []shared.ServiceID) bool {
	seen := make(map[shared.ServiceID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return false
}
