// Package app holds catalog's use cases. Each starts by asking business
// whether the caller may work on the branch (Access), then loads, changes
// and saves through the domain's repository port. It knows nothing about
// HTTP or SQL.
package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Role is how much a caller must be allowed to do at a branch.
type Role string

// Roles, as business defines them.
const (
	RoleManager Role = "manager" // the owner, or a manager of the branch
	RoleBarber  Role = "barber"  // anyone working at the branch
)

// Access asks business whether actor may work on branch (through
// adapters/acl). It returns domain.ErrNotFound for strangers and for a
// branch that isn't the business's, domain.ErrForbidden for staff without
// the role or not working at the branch.
type Access interface {
	Branch(ctx context.Context, actor shared.UserID, business shared.BusinessID, branch shared.BranchID, need Role) error
	// StaffAtBranch checks that each staff member works at the branch;
	// domain.ErrUnknownStaff if not.
	StaffAtBranch(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) error
}

// BranchRef names a branch, on behalf of Actor.
type BranchRef struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
}

// Money is a price as the client sent it: an amount in halalas.
type Money struct {
	Amount   int64
	Currency string
}

// Name is text in Arabic (required) and English.
type Name struct {
	Ar, En string
}

// CreateService is the command to add a service to a branch.
type CreateService struct {
	BranchRef
	Category    string
	Name        Name
	Description domain.Description
	Duration    time.Duration
	Price       Money
	SortOrder   int
}

// UpdateService changes a service. Nil fields stay as they are.
type UpdateService struct {
	BranchRef
	ServiceID       domain.ServiceID
	ExpectedVersion int
	Category        *string
	Name            *Name
	Description     *domain.Description
	Duration        *time.Duration
	Price           *Money
	SortOrder       *int
	Active          *bool
}

// OfferingInput is one person performing a service, as the client sent it.
// Nil Price or Duration means the service's own.
type OfferingInput struct {
	Staff    shared.StaffID
	Price    *Money
	Duration *time.Duration
}

// SetOfferings replaces who performs a service.
type SetOfferings struct {
	BranchRef
	ServiceID       domain.ServiceID
	ExpectedVersion int
	Offerings       []OfferingInput
}

// ServiceHandlers are the service use cases. Who may do what:
//
//	list                          anyone working at the branch (and the owner)
//	create, edit, set offerings   the owner, or a manager of the branch
type ServiceHandlers struct {
	services domain.Services
	access   Access
	clock    clock.Clock
}

// NewServiceHandlers wires the use cases.
func NewServiceHandlers(services domain.Services, access Access, clk clock.Clock) *ServiceHandlers {
	return &ServiceHandlers{services: services, access: access, clock: clk}
}

// Categories returns the service categories (public reference data).
func (h *ServiceHandlers) Categories() []domain.Category { return domain.Categories() }

// Create adds an active service to the branch.
func (h *ServiceHandlers) Create(ctx context.Context, cmd CreateService) (*domain.Service, error) {
	if err := h.access.Branch(ctx, cmd.Actor, cmd.BusinessID, cmd.BranchID, RoleManager); err != nil {
		return nil, err
	}
	d, err := details(cmd.Category, cmd.Name, cmd.Description, cmd.Duration, cmd.Price, cmd.SortOrder)
	if err != nil {
		return nil, err
	}
	s, err := domain.NewService(shared.NewID[domain.ServiceTag](), cmd.BusinessID, cmd.BranchID, d, h.clock.Now())
	if err != nil {
		return nil, err
	}
	if err := h.services.Add(ctx, s); err != nil {
		return nil, fmt.Errorf("create service: %w", err)
	}
	return s, nil
}

// List returns the branch's services, inactive ones included (staff see
// what they turned off).
func (h *ServiceHandlers) List(ctx context.Context, ref BranchRef) ([]*domain.Service, error) {
	if err := h.access.Branch(ctx, ref.Actor, ref.BusinessID, ref.BranchID, RoleBarber); err != nil {
		return nil, err
	}
	services, err := h.services.List(ctx, ref.BusinessID, ref.BranchID)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	return services, nil
}

// Update edits a service under the version check.
func (h *ServiceHandlers) Update(ctx context.Context, cmd UpdateService) (*domain.Service, error) {
	if err := h.access.Branch(ctx, cmd.Actor, cmd.BusinessID, cmd.BranchID, RoleManager); err != nil {
		return nil, err
	}
	var updated *domain.Service
	err := h.services.Update(ctx, cmd.BusinessID, cmd.BranchID, cmd.ServiceID, cmd.ExpectedVersion, func(s *domain.Service) error {
		d, active, err := cmd.apply(s)
		if err != nil {
			return err
		}
		if err := s.Edit(d, active, h.clock.Now()); err != nil {
			return err
		}
		updated = s
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}
	return updated, nil
}

// SetOfferings replaces who performs the service. Everyone listed must work
// at the branch; the version check is the service's, as offerings are part
// of it.
func (h *ServiceHandlers) SetOfferings(ctx context.Context, cmd SetOfferings) (*domain.Service, error) {
	if err := h.access.Branch(ctx, cmd.Actor, cmd.BusinessID, cmd.BranchID, RoleManager); err != nil {
		return nil, err
	}
	offerings := make([]domain.Offering, 0, len(cmd.Offerings))
	staff := make([]shared.StaffID, 0, len(cmd.Offerings))
	for _, in := range cmd.Offerings {
		o := domain.Offering{Staff: in.Staff, Duration: in.Duration}
		if in.Price != nil {
			p, err := money(*in.Price)
			if err != nil {
				return nil, err
			}
			o.Price = &p
		}
		offerings = append(offerings, o)
		staff = append(staff, in.Staff)
	}
	if len(offerings) > domain.MaxOfferings {
		return nil, domain.ErrTooManyOfferings // before asking business about each of them
	}
	if err := h.access.StaffAtBranch(ctx, cmd.BusinessID, cmd.BranchID, staff); err != nil {
		return nil, err
	}
	var updated *domain.Service
	err := h.services.Update(ctx, cmd.BusinessID, cmd.BranchID, cmd.ServiceID, cmd.ExpectedVersion, func(s *domain.Service) error {
		if err := s.SetOfferings(offerings, h.clock.Now()); err != nil {
			return err
		}
		updated = s
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("set offerings: %w", err)
	}
	return updated, nil
}

// apply lays the command's fields over the service's current ones.
func (cmd UpdateService) apply(s *domain.Service) (domain.ServiceDetails, bool, error) {
	d, active := s.Details(), s.IsActive()
	var err error
	if cmd.Category != nil {
		d.Category = domain.CategoryCode(*cmd.Category)
	}
	if cmd.Name != nil {
		if d.Name, err = shared.NewLocalizedText(cmd.Name.Ar, cmd.Name.En); err != nil {
			return d, active, err
		}
	}
	if cmd.Description != nil {
		d.Description = *cmd.Description
	}
	if cmd.Duration != nil {
		d.Duration = *cmd.Duration
	}
	if cmd.Price != nil {
		if d.Price, err = money(*cmd.Price); err != nil {
			return d, active, err
		}
	}
	if cmd.SortOrder != nil {
		d.SortOrder = *cmd.SortOrder
	}
	if cmd.Active != nil {
		active = *cmd.Active
	}
	return d, active, nil
}

func details(category string, name Name, desc domain.Description, duration time.Duration, price Money, sort int) (domain.ServiceDetails, error) {
	text, err := shared.NewLocalizedText(name.Ar, name.En)
	if err != nil {
		return domain.ServiceDetails{}, err
	}
	p, err := money(price)
	if err != nil {
		return domain.ServiceDetails{}, err
	}
	return domain.ServiceDetails{
		Category: domain.CategoryCode(category), Name: text, Description: desc,
		Duration: duration, Price: p, SortOrder: sort,
	}, nil
}

func money(m Money) (shared.Money, error) {
	p, err := shared.NewMoney(m.Amount, shared.Currency(m.Currency))
	if err != nil {
		return shared.Money{}, domain.ErrInvalidPrice
	}
	return p, nil
}

// PerformingStaff returns who performs at least one active service at the
// branch, in ID order. It authorizes nobody: business's publish rule asks
// it (through main) after checking the caller is the owner.
func (h *ServiceHandlers) PerformingStaff(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]shared.StaffID, error) {
	services, err := h.services.List(ctx, business, branch)
	if err != nil {
		return nil, fmt.Errorf("performing staff: %w", err)
	}
	var staff []shared.StaffID
	for _, s := range services {
		if !s.IsActive() {
			continue
		}
		for _, o := range s.Offerings() {
			staff = append(staff, o.Staff)
		}
	}
	slices.SortFunc(staff, func(a, b shared.StaffID) int { return cmp.Compare(a.String(), b.String()) })
	return slices.Compact(staff), nil
}

// MenuItem is an active service as booking needs it: what it's called and,
// for each barber who performs it, how long it takes and what it costs
// (their own duration and price, or the service's).
type MenuItem struct {
	ID         domain.ServiceID
	Name       shared.LocalizedText
	Performers []Performer
}

// Performer is one barber's version of a service.
type Performer struct {
	Staff    shared.StaffID
	Duration time.Duration
	Price    shared.Money
}

// Menu returns the branch's active services with who performs them. It
// authorizes nobody: a published branch's menu is public, and booking asks
// only after finding the branch bookable.
func (h *ServiceHandlers) Menu(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]MenuItem, error) {
	services, err := h.services.List(ctx, business, branch)
	if err != nil {
		return nil, fmt.Errorf("menu: %w", err)
	}
	var menu []MenuItem
	for _, s := range services {
		if !s.IsActive() {
			continue
		}
		d := s.Details()
		item := MenuItem{ID: s.ID(), Name: d.Name}
		for _, o := range s.Offerings() {
			item.Performers = append(item.Performers, Performer{Staff: o.Staff, Duration: o.DurationOr(d.Duration), Price: o.PriceOr(d.Price)})
		}
		menu = append(menu, item)
	}
	return menu, nil
}
