package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// access answers from a table: (actor, branch) → the highest role there,
// and which staff work at which branch.
type access struct {
	roles  map[shared.UserID]map[shared.BranchID]app.Role
	staff  map[shared.StaffID]shared.BranchID
	checks *int // StaffAtBranch calls
}

func (a access) StaffAtBranch(_ context.Context, _ shared.BusinessID, branch shared.BranchID, staff []shared.StaffID) error {
	*a.checks++
	for _, id := range staff {
		if a.staff[id] != branch {
			return domain.ErrUnknownStaff
		}
	}
	return nil
}

func (a access) Branch(_ context.Context, actor shared.UserID, _ shared.BusinessID, branch shared.BranchID, need app.Role) error {
	branches, ok := a.roles[actor]
	if !ok {
		return domain.ErrNotFound
	}
	role, ok := branches[branch]
	if !ok || (need == app.RoleManager && role != app.RoleManager) {
		return domain.ErrForbidden
	}
	return nil
}

// store is an in-memory domain.Services that counts calls.
type store struct {
	mu       sync.Mutex
	services []*domain.Service
	calls    int
}

func (s *store) Add(_ context.Context, svc *domain.Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.services = append(s.services, svc)
	return nil
}

func (s *store) List(_ context.Context, business shared.BusinessID, branch shared.BranchID) ([]*domain.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	var out []*domain.Service
	for _, svc := range s.services {
		if svc.BusinessID() == business && svc.BranchID() == branch {
			out = append(out, svc)
		}
	}
	return out, nil
}

func (s *store) Update(_ context.Context, business shared.BusinessID, branch shared.BranchID, id domain.ServiceID, expected int, fn func(*domain.Service) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	for i, svc := range s.services {
		if svc.BusinessID() != business || svc.BranchID() != branch || svc.ID() != id {
			continue
		}
		if svc.Version() != expected {
			return domain.ErrVersionConflict
		}
		c := domain.RehydrateService(svc.ID(), svc.BusinessID(), svc.BranchID(), svc.Details(), svc.Offerings(), svc.IsActive(), svc.Version(), svc.CreatedAt(), svc.UpdatedAt())
		if err := fn(c); err != nil {
			return err
		}
		s.services[i] = c
		return nil
	}
	return domain.ErrNotFound
}

type fixture struct {
	barberStaff, otherStaff  shared.StaffID
	staffChecks              int
	store                    *store
	h                        *app.ServiceHandlers
	business                 shared.BusinessID
	branch, other            shared.BranchID
	manager, barber, visitor shared.UserID
}

func newFixture() *fixture {
	f := &fixture{
		store: &store{}, business: shared.NewID[shared.BusinessTag](),
		branch: shared.NewID[shared.BranchTag](), other: shared.NewID[shared.BranchTag](),
		manager: shared.NewID[shared.UserTag](), barber: shared.NewID[shared.UserTag](), visitor: shared.NewID[shared.UserTag](),
	}
	f.barberStaff, f.otherStaff = shared.NewID[shared.StaffTag](), shared.NewID[shared.StaffTag]()
	a := access{
		roles: map[shared.UserID]map[shared.BranchID]app.Role{
			f.manager: {f.branch: app.RoleManager},
			f.barber:  {f.branch: app.RoleBarber},
		},
		staff:  map[shared.StaffID]shared.BranchID{f.barberStaff: f.branch, f.otherStaff: f.other},
		checks: &f.staffChecks,
	}
	f.h = app.NewServiceHandlers(f.store, a, clock.NewFake(t0))
	return f
}

func (f *fixture) ref(actor shared.UserID, branch shared.BranchID) app.BranchRef {
	return app.BranchRef{Actor: actor, BusinessID: f.business, BranchID: branch}
}

func (f *fixture) createCmd(actor shared.UserID) app.CreateService {
	return app.CreateService{
		BranchRef: f.ref(actor, f.branch), Category: "haircut", Name: app.Name{Ar: "قص شعر", En: "Haircut"},
		Duration: 30 * time.Minute, Price: app.Money{Amount: 6000, Currency: "SAR"},
	}
}

func TestCreateAndList(t *testing.T) {
	t.Parallel()
	f := newFixture()
	ctx := t.Context()
	s, err := f.h.Create(ctx, f.createCmd(f.manager))
	if err != nil {
		t.Fatal(err)
	}
	if s.BranchID() != f.branch || s.Details().Price.Amount() != 6000 || s.Details().Name.En() != "Haircut" || !s.IsActive() {
		t.Errorf("service = %+v", s)
	}
	list, err := f.h.List(ctx, f.ref(f.barber, f.branch))
	if err != nil || len(list) != 1 {
		t.Fatalf("barber lists: %d, %v", len(list), err)
	}

	for name, tt := range map[string]struct {
		change func(*app.CreateService)
		want   error
	}{
		"no Arabic name": {func(c *app.CreateService) { c.Name.Ar = " " }, shared.ErrArabicRequired},
		"other currency": {func(c *app.CreateService) { c.Price.Currency = "USD" }, domain.ErrInvalidPrice},
		"bad category":   {func(c *app.CreateService) { c.Category = "massage" }, domain.ErrUnknownCategory},
	} {
		cmd := f.createCmd(f.manager)
		tt.change(&cmd)
		if _, err := f.h.Create(ctx, cmd); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.want)
		}
	}
}

func TestAuthorizationComesFirst(t *testing.T) {
	t.Parallel()
	f := newFixture()
	ctx := t.Context()
	s, err := f.h.Create(ctx, f.createCmd(f.manager))
	if err != nil {
		t.Fatal(err)
	}
	f.store.calls = 0
	active := false
	update := func(actor shared.UserID) app.UpdateService {
		return app.UpdateService{BranchRef: f.ref(actor, f.branch), ServiceID: s.ID(), ExpectedVersion: 1, Active: &active}
	}
	refused := []struct {
		name string
		call func() error
		want error
	}{
		{"barber creates", func() error { _, err := f.h.Create(ctx, f.createCmd(f.barber)); return err }, domain.ErrForbidden},
		{"barber edits", func() error { _, err := f.h.Update(ctx, update(f.barber)); return err }, domain.ErrForbidden},
		{"manager, other branch", func() error { _, err := f.h.List(ctx, f.ref(f.manager, f.other)); return err }, domain.ErrForbidden},
		{"visitor lists", func() error { _, err := f.h.List(ctx, f.ref(f.visitor, f.branch)); return err }, domain.ErrNotFound},
		{"visitor edits", func() error { _, err := f.h.Update(ctx, update(f.visitor)); return err }, domain.ErrNotFound},
	}
	for _, tt := range refused {
		if err := tt.call(); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	if f.store.calls != 0 {
		t.Errorf("refused callers reached the store %d times", f.store.calls)
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	f := newFixture()
	ctx := t.Context()
	s, err := f.h.Create(ctx, f.createCmd(f.manager))
	if err != nil {
		t.Fatal(err)
	}
	price, duration, active := app.Money{Amount: 7500, Currency: "SAR"}, 45*time.Minute, false
	up, err := f.h.Update(ctx, app.UpdateService{
		BranchRef: f.ref(f.manager, f.branch), ServiceID: s.ID(), ExpectedVersion: 1,
		Price: &price, Duration: &duration, Active: &active,
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Version() != 2 || up.IsActive() || up.Details().Price.Amount() != 7500 || up.Details().Duration != duration ||
		up.Details().Name.Ar() != "قص شعر" {
		t.Errorf("updated = %+v %+v", up, up.Details())
	}
	// Stale version, bad value, another branch: refused, nothing saved.
	if _, err := f.h.Update(ctx, app.UpdateService{BranchRef: f.ref(f.manager, f.branch), ServiceID: s.ID(), ExpectedVersion: 1, Active: &active}); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale: %v", err)
	}
	bad := 7 * time.Minute
	if _, err := f.h.Update(ctx, app.UpdateService{BranchRef: f.ref(f.manager, f.branch), ServiceID: s.ID(), ExpectedVersion: 2, Duration: &bad}); !errors.Is(err, domain.ErrInvalidDuration) {
		t.Errorf("bad duration: %v", err)
	}
	if _, err := f.h.Update(ctx, app.UpdateService{BranchRef: f.ref(f.manager, f.branch), ServiceID: shared.NewID[domain.ServiceTag](), ExpectedVersion: 2, Active: &active}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown service: %v", err)
	}
	if list, _ := f.h.List(ctx, f.ref(f.manager, f.branch)); list[0].Version() != 2 {
		t.Errorf("refused updates changed the service")
	}
}

func TestSetOfferings(t *testing.T) {
	t.Parallel()
	f := newFixture()
	ctx := t.Context()
	s, err := f.h.Create(ctx, f.createCmd(f.manager))
	if err != nil {
		t.Fatal(err)
	}
	senior := app.Money{Amount: 9000, Currency: "SAR"}
	longer := 45 * time.Minute
	cmd := func(version int, offerings ...app.OfferingInput) app.SetOfferings {
		return app.SetOfferings{BranchRef: f.ref(f.manager, f.branch), ServiceID: s.ID(), ExpectedVersion: version, Offerings: offerings}
	}
	up, err := f.h.SetOfferings(ctx, cmd(1, app.OfferingInput{Staff: f.barberStaff, Price: &senior, Duration: &longer}))
	if err != nil {
		t.Fatal(err)
	}
	offs := up.Offerings()
	if up.Version() != 2 || len(offs) != 1 || offs[0].Staff != f.barberStaff ||
		offs[0].PriceOr(up.Details().Price).Amount() != 9000 || offs[0].DurationOr(up.Details().Duration) != longer {
		t.Fatalf("offerings = %+v (version %d)", offs, up.Version())
	}

	// Someone from another branch, or nobody at all: refused before saving.
	f.store.calls = 0
	for name, off := range map[string]app.OfferingInput{
		"other branch's barber": {Staff: f.otherStaff},
		"made-up staff":         {Staff: shared.NewID[shared.StaffTag]()},
	} {
		if _, err := f.h.SetOfferings(ctx, cmd(2, off)); !errors.Is(err, domain.ErrUnknownStaff) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if f.store.calls != 0 {
		t.Errorf("refused offerings reached the store %d times", f.store.calls)
	}
	// Listed twice, or a bad override: the domain refuses.
	bad := 7 * time.Minute
	if _, err := f.h.SetOfferings(ctx, cmd(2, app.OfferingInput{Staff: f.barberStaff}, app.OfferingInput{Staff: f.barberStaff})); !errors.Is(err, domain.ErrDuplicateOffering) {
		t.Errorf("twice: %v", err)
	}
	if _, err := f.h.SetOfferings(ctx, cmd(2, app.OfferingInput{Staff: f.barberStaff, Duration: &bad})); !errors.Is(err, domain.ErrInvalidDuration) {
		t.Errorf("bad duration: %v", err)
	}
	// An empty list clears them.
	if up, err := f.h.SetOfferings(ctx, cmd(2)); err != nil || len(up.Offerings()) != 0 || up.Version() != 3 {
		t.Errorf("clear: %v", err)
	}
	// Barbers don't assign services, and nobody is checked for them.
	f.staffChecks = 0
	if _, err := f.h.SetOfferings(ctx, app.SetOfferings{BranchRef: f.ref(f.barber, f.branch), ServiceID: s.ID(), ExpectedVersion: 3}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("barber: %v", err)
	}
	if f.staffChecks != 0 {
		t.Error("staff were looked up for an unauthorized caller")
	}
}
