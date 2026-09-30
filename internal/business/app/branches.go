package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Location is a latitude/longitude as the client sent it.
type Location struct {
	Lat, Lng float64
}

// CreateBranch is the command to add a branch. Empty Timezone means
// Asia/Riyadh; nil Policy means the default booking policy.
type CreateBranch struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	Name       DisplayName
	CityCode   string
	District   string
	Address    string
	Location   Location
	Phone      string // "" = none
	Timezone   string
	Policy     *domain.PolicyRules
}

// UpdateBranch changes a branch. Nil fields stay as they are; an empty
// District or Phone clears it.
type UpdateBranch struct {
	Actor           shared.UserID
	BusinessID      shared.BusinessID
	BranchID        shared.BranchID
	ExpectedVersion int
	Name            *DisplayName
	CityCode        *string
	District        *string
	Address         *string
	Location        *Location
	Phone           *string
	Timezone        *string
	Policy          *domain.PolicyRules
}

// BranchQuery names one branch, on behalf of Actor.
type BranchQuery struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	BranchID   shared.BranchID
}

// BranchHandlers are the branch use cases. Who may do what:
//
//	create         owner, within the plan's branch limit
//	edit           owner, or a manager of that branch
//	list, get      any active staff of the business
type BranchHandlers struct {
	branches domain.Branches
	staff    domain.Staff
	plans    Plans
	clock    clock.Clock
}

// NewBranchHandlers wires the branch use cases.
func NewBranchHandlers(branches domain.Branches, staff domain.Staff, plans Plans, clk clock.Clock) *BranchHandlers {
	return &BranchHandlers{branches: branches, staff: staff, plans: plans, clock: clk}
}

// Create adds a draft branch to the business.
func (h *BranchHandlers) Create(ctx context.Context, cmd CreateBranch) (*domain.Branch, error) {
	if _, err := authorize(ctx, h.staff, cmd.Actor, cmd.BusinessID, domain.RoleOwner); err != nil {
		return nil, err
	}
	name, err := shared.NewLocalizedText(cmd.Name.Ar, cmd.Name.En)
	if err != nil {
		return nil, err
	}
	profile := domain.BranchProfile{Name: name, City: domain.CityCode(cmd.CityCode), District: cmd.District, Address: cmd.Address, Timezone: cmd.Timezone}
	if profile.Timezone == "" {
		profile.Timezone = domain.DefaultTimezone
	}
	if profile.Location, err = shared.NewGeoPoint(cmd.Location.Lat, cmd.Location.Lng); err != nil {
		return nil, err
	}
	if profile.Phone, err = parsePhone(cmd.Phone); err != nil {
		return nil, err
	}
	policy := domain.DefaultBookingPolicy()
	if cmd.Policy != nil {
		if policy, err = domain.NewBookingPolicy(*cmd.Policy); err != nil {
			return nil, err
		}
	}

	branch, err := domain.NewBranch(shared.NewID[shared.BranchTag](), cmd.BusinessID, profile, policy, h.clock.Now())
	if err != nil {
		return nil, err
	}
	plan, err := h.plans.Standing(ctx, cmd.BusinessID)
	if err != nil {
		return nil, fmt.Errorf("create branch: %w", err)
	}
	if err := h.branches.Add(ctx, branch, plan.Limits.AllowBranch); err != nil {
		return nil, fmt.Errorf("create branch: %w", err)
	}
	return branch, nil
}

// List returns the business's branches.
func (h *BranchHandlers) List(ctx context.Context, actor shared.UserID, business shared.BusinessID) ([]*domain.Branch, error) {
	if _, err := authorize(ctx, h.staff, actor, business, domain.RoleBarber); err != nil {
		return nil, err
	}
	branches, err := h.branches.List(ctx, business)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	return branches, nil
}

// Get returns one branch of the business.
func (h *BranchHandlers) Get(ctx context.Context, q BranchQuery) (*domain.Branch, error) {
	if _, err := authorize(ctx, h.staff, q.Actor, q.BusinessID, domain.RoleBarber); err != nil {
		return nil, err
	}
	b, err := h.branches.ByID(ctx, q.BusinessID, q.BranchID)
	if err != nil {
		return nil, fmt.Errorf("get branch: %w", err)
	}
	return b, nil
}

// Update edits a branch under the version check.
func (h *BranchHandlers) Update(ctx context.Context, cmd UpdateBranch) (*domain.Branch, error) {
	member, err := authorize(ctx, h.staff, cmd.Actor, cmd.BusinessID, domain.RoleManager)
	if err != nil {
		return nil, err
	}
	if err := member.AuthorizeBranch(domain.RoleManager, cmd.BranchID); err != nil {
		return nil, err
	}
	var updated *domain.Branch
	err = h.branches.Update(ctx, cmd.BusinessID, cmd.BranchID, cmd.ExpectedVersion, func(b *domain.Branch) error {
		p, policy, err := cmd.apply(b.Profile(), b.Policy())
		if err != nil {
			return err
		}
		// The time zone moves every opening hour and schedule of the branch
		// (they are wall-clock times there): the owner's decision alone.
		if p.Timezone != b.Profile().Timezone && member.Role() != domain.RoleOwner {
			return domain.ErrForbidden
		}
		if err := b.Edit(p, policy, h.clock.Now()); err != nil {
			return err
		}
		updated = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update branch: %w", err)
	}
	return updated, nil
}

// apply lays the command's fields over the current profile and policy.
func (cmd UpdateBranch) apply(p domain.BranchProfile, policy domain.BookingPolicy) (domain.BranchProfile, domain.BookingPolicy, error) {
	var err error
	if cmd.Name != nil {
		if p.Name, err = shared.NewLocalizedText(cmd.Name.Ar, cmd.Name.En); err != nil {
			return p, policy, err
		}
	}
	if cmd.CityCode != nil {
		p.City = domain.CityCode(*cmd.CityCode)
	}
	if cmd.District != nil {
		p.District = *cmd.District
	}
	if cmd.Address != nil {
		p.Address = *cmd.Address
	}
	if cmd.Location != nil {
		if p.Location, err = shared.NewGeoPoint(cmd.Location.Lat, cmd.Location.Lng); err != nil {
			return p, policy, err
		}
	}
	if cmd.Phone != nil {
		if p.Phone, err = parsePhone(*cmd.Phone); err != nil {
			return p, policy, err
		}
	}
	if cmd.Timezone != nil {
		p.Timezone = *cmd.Timezone
	}
	if cmd.Policy != nil {
		if policy, err = domain.NewBookingPolicy(*cmd.Policy); err != nil {
			return p, policy, err
		}
	}
	return p, policy, nil
}

// parsePhone reads an optional phone number: blank means none.
func parsePhone(s string) (shared.PhoneNumber, error) {
	if strings.TrimSpace(s) == "" {
		return shared.PhoneNumber{}, nil
	}
	return shared.NewPhoneNumber(s)
}
