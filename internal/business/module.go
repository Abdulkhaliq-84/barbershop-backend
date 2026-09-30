// Package business is the tenancy module: businesses, their onboarding and
// their staff (docs/architecture/domain-model.md §3.2).
//
// It publishes events through the outbox; OnApproved is how other modules
// (wired in main) subscribe to them.
//
// This root package is the module's public face. Other modules and main use
// only what is exported here; the domain, app and adapters packages are the
// module's private implementation (enforced by lint rules).
package business

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/acl"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/invites"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Logger  *slog.Logger
	Media   *media.Module   // stores verification documents (through adapters/acl)
	Users   *iam.Module     // who a user is: their sign-in phone (through adapters/acl)
	Billing *billing.Module // what the business's plan allows (through adapters/acl)
	Events  *outbox.Bus     // where the business's events are published
	// InviteSender delivers staff invitations; nil = development console
	// sender (only with SMS_PROVIDER=console, refused in production).
	InviteSender app.InvitationSender
	// Readiness says whether a branch could take a booking, for publishing.
	// Catalog and scheduling know; they depend on this module, so main
	// answers for them (see ReadinessChecker).
	Readiness ReadinessChecker
}

// Readiness is whether a branch could take a booking: opening hours, an
// active service at least one barber performs, and one of those barbers
// with a weekly schedule.
type Readiness = domain.BranchReadiness

// ReadinessChecker answers Readiness for a branch. It authorizes nobody:
// business asks it after checking the caller is the owner.
type ReadinessChecker interface {
	BranchReadiness(ctx context.Context, business shared.BusinessID, branch shared.BranchID) (Readiness, error)
}

// Module is the wired business module.
type Module struct {
	http   *httpapi.Handlers
	access *app.AccessHandler
}

// Roles, from most to least powerful, for AuthorizeBranch.
const (
	RoleOwner   = string(domain.RoleOwner)
	RoleManager = string(domain.RoleManager)
	RoleBarber  = string(domain.RoleBarber)
)

// Errors AuthorizeBranch returns. They are the domain's own sentinels,
// re-exported so callers never import business/domain.
var (
	// ErrNotFound: the caller isn't active staff of the business, or the
	// branch isn't the business's. Answer 404, as for an ID that doesn't exist.
	ErrNotFound = domain.ErrNotFound
	// ErrForbidden: staff whose role is too small, or who don't work at the
	// branch. Answer 403.
	ErrForbidden = domain.ErrForbidden
)

// AuthorizeBranch checks that actor may work on branch of business with at
// least role need (RoleOwner, RoleManager or RoleBarber). Modules that keep
// data per branch (catalog, scheduling) call it first in every use case.
func (m *Module) AuthorizeBranch(ctx context.Context, actor shared.UserID, business shared.BusinessID, branch shared.BranchID, need string) error {
	role, err := domain.ParseRole(need)
	if err != nil {
		return fmt.Errorf("authorize branch: %w", err)
	}
	return m.access.Branch(ctx, actor, business, branch, role)
}

// StaffInfo is a staff member as other modules see them.
type StaffInfo struct {
	ID       shared.StaffID
	Role     string            // RoleOwner, RoleManager or RoleBarber
	Branches []shared.BranchID // empty for the owner, who works at every branch
}

// WorksAt reports whether the member works at branch.
func (s StaffInfo) WorksAt(branch shared.BranchID) bool {
	return s.Role == RoleOwner || slices.Contains(s.Branches, branch)
}

// MemberOf returns actor's own active membership of business: who "me" is
// in that business. ErrNotFound if actor isn't active staff there.
func (m *Module) MemberOf(ctx context.Context, actor shared.UserID, business shared.BusinessID) (StaffInfo, error) {
	member, err := m.access.MemberOf(ctx, actor, business)
	return toStaffInfo(member), err
}

// StaffMember returns an active staff member of business, or ErrNotFound.
// Callers have already authorized the actor.
func (m *Module) StaffMember(ctx context.Context, business shared.BusinessID, staff shared.StaffID) (StaffInfo, error) {
	member, err := m.access.StaffMember(ctx, business, staff)
	return toStaffInfo(member), err
}

func toStaffInfo(m app.Member) StaffInfo {
	return StaffInfo{ID: m.ID, Role: string(m.Role), Branches: m.Branches}
}

// BranchLocation returns the branch's time zone (its opening hours and
// schedules are wall-clock times there). ErrNotFound if the branch isn't
// the business's.
func (m *Module) BranchLocation(ctx context.Context, business shared.BusinessID, branch shared.BranchID) (*time.Location, error) {
	return m.access.BranchLocation(ctx, business, branch)
}

// StaffAtBranch checks that every staff ID is active staff of business
// working at branch (the owner works at all of them); ErrNotFound if not.
// Callers have already authorized the actor with AuthorizeBranch.
func (m *Module) StaffAtBranch(ctx context.Context, business shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) error {
	return m.access.StaffAtBranch(ctx, business, branch, staff)
}

// New wires the repositories, use cases and HTTP handlers.
func New(d Deps) *Module {
	store := postgres.NewStore(d.Pool, d.Events)
	files := acl.NewMediaFiles(d.Media)
	plans := acl.NewBillingPlans(d.Billing)
	sender := d.InviteSender
	if sender == nil {
		sender = invites.NewConsole(d.Logger)
	}
	return &Module{
		access: app.NewAccessHandler(store, store.Branches()),
		http: httpapi.NewHandlers(httpapi.UseCases{
			Register: app.NewRegisterBusinessHandler(store, d.Clock),
			Get:      app.NewGetBusinessHandler(store, store),
			Update:   app.NewUpdateBusinessHandler(store, store, d.Clock),
			Branches: app.NewBranchHandlers(app.BranchDeps{
				Branches: store.Branches(), Businesses: store, Staff: store, Plans: plans, Readiness: d.Readiness, Clock: d.Clock,
			}),
			Documents: app.NewDocumentHandlers(store, store.Documents(), store, files, d.Clock, d.Logger),
			Submit:    app.NewSubmitHandler(store, store, d.Clock),
			Review:    app.NewReviewHandlers(store, store, store.Documents(), store.Branches(), store, files, d.Clock),
			Staff: app.NewStaffHandlers(app.StaffDeps{
				Businesses: store, Staff: store, Invitations: store.Invitations(), Users: acl.NewIAMUsers(d.Users),
				Plans: plans, Tokens: invites.Tokens{}, Sender: sender, Clock: d.Clock,
			}),
			Plan:        app.NewPlanHandler(store, plans),
			Memberships: app.NewListMyMembershipsHandler(store),
		}, d.Logger),
	}
}

// HTTP returns the handlers for the business API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// Approved is the business.approved event: a platform admin approved the
// business, which may now start trading.
type Approved struct {
	BusinessID shared.BusinessID
	OwnerID    shared.UserID
	ApprovedAt time.Time
}

// OnApproved subscribes fn, under a stable name, to business approvals.
// fn may be called more than once for one approval: make it idempotent.
func OnApproved(bus *outbox.Bus, name string, fn func(ctx context.Context, e Approved) error) {
	bus.Subscribe(name, events.TypeBusinessApproved, func(ctx context.Context, e outbox.Event) error {
		var p events.BusinessApproved
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("decode %s %s: %w", e.Type, e.ID, err)
		}
		return fn(ctx, Approved{
			BusinessID: shared.IDFromUUID[shared.BusinessTag](p.BusinessID),
			OwnerID:    shared.IDFromUUID[shared.UserTag](p.OwnerID),
			ApprovedAt: p.ApprovedAt,
		})
	})
}
