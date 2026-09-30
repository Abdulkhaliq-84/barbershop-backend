package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// branchStore is an in-memory domain.Branches, scoped by business like the
// real one.
type branchStore struct {
	mu       sync.Mutex
	branches []*domain.Branch
	calls    int // any call, to prove authorization runs first
}

func (s *branchStore) Add(_ context.Context, b *domain.Branch, allow func(int) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	existing := 0
	for _, other := range s.branches {
		if other.BusinessID() == b.BusinessID() {
			existing++
		}
	}
	if err := allow(existing); err != nil {
		return err
	}
	s.branches = append(s.branches, b)
	return nil
}

func (s *branchStore) find(business shared.BusinessID, id shared.BranchID) (int, bool) {
	for i, b := range s.branches {
		if b.BusinessID() == business && b.ID() == id {
			return i, true
		}
	}
	return 0, false
}

func (s *branchStore) ByID(_ context.Context, business shared.BusinessID, id shared.BranchID) (*domain.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if i, ok := s.find(business, id); ok {
		return s.branches[i], nil
	}
	return nil, domain.ErrNotFound
}

func (s *branchStore) List(_ context.Context, business shared.BusinessID) ([]*domain.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	var out []*domain.Branch
	for _, b := range s.branches {
		if b.BusinessID() == business {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *branchStore) Update(_ context.Context, business shared.BusinessID, id shared.BranchID, expectedVersion int, fn func(*domain.Branch) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	i, ok := s.find(business, id)
	if !ok {
		return domain.ErrNotFound
	}
	b := s.branches[i]
	if b.Version() != expectedVersion {
		return domain.ErrVersionConflict
	}
	c := domain.RehydrateBranch(b.ID(), b.BusinessID(), b.Profile(), b.Policy(), b.Status(), b.Version(), b.CreatedAt(), b.UpdatedAt())
	if err := fn(c); err != nil {
		return err
	}
	s.branches[i] = c
	return nil
}

type branchFixture struct {
	*fixture
	branchStore *branchStore
	branches    *app.BranchHandlers
	owner       shared.UserID
	business    shared.BusinessID
}

func newBranchFixture(t *testing.T) *branchFixture {
	t.Helper()
	f := newFixture()
	bs := &branchStore{}
	owner := shared.NewID[shared.UserTag]()
	b, err := f.register.Handle(t.Context(), registerCmd(owner))
	if err != nil {
		t.Fatal(err)
	}
	return &branchFixture{fixture: f, branchStore: bs, branches: app.NewBranchHandlers(app.BranchDeps{Branches: bs, Businesses: f.store, Staff: f.store, Plans: f.plans, Clock: f.clock}), owner: owner, business: b.ID()}
}

func createCmd(actor shared.UserID, business shared.BusinessID) app.CreateBranch {
	return app.CreateBranch{
		Actor: actor, BusinessID: business,
		Name: app.DisplayName{Ar: "فرع العليا", En: "Olaya"}, CityCode: "riyadh", District: "العليا",
		Address: "شارع العليا العام", Location: app.Location{Lat: 24.6911, Lng: 46.6851}, Phone: "0551234567",
	}
}

func TestCreateBranch(t *testing.T) {
	t.Parallel()
	f := newBranchFixture(t)
	b, err := f.branches.Create(t.Context(), createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	p := b.Profile()
	if b.BusinessID() != f.business || b.Status() != domain.BranchDraft || p.Timezone != "Asia/Riyadh" ||
		p.Phone.String() != "+966551234567" || b.Policy() != domain.DefaultBookingPolicy() {
		t.Errorf("branch = %+v", b)
	}

	// A policy of the owner's own, validated.
	cmd := createCmd(f.owner, f.business)
	rules := domain.DefaultBookingPolicy().Rules()
	rules.SlotInterval = 30 * time.Minute
	cmd.Policy = &rules
	if b, err := f.branches.Create(t.Context(), cmd); err != nil || b.Policy().Rules().SlotInterval != 30*time.Minute {
		t.Fatalf("own policy: %v", err)
	}
	rules.SlotInterval = 7 * time.Minute
	if _, err := f.branches.Create(t.Context(), cmd); !errors.Is(err, domain.ErrInvalidBookingPolicy) {
		t.Fatalf("bad policy: error = %v", err)
	}
	cmd = createCmd(f.owner, f.business)
	cmd.Phone = "123"
	if _, err := f.branches.Create(t.Context(), cmd); !errors.Is(err, shared.ErrInvalidPhoneNumber) {
		t.Fatalf("bad phone: error = %v", err)
	}
}

func TestBranchAuthorization(t *testing.T) {
	t.Parallel()
	f := newBranchFixture(t)
	ctx := t.Context()
	branch, err := f.branches.Create(ctx, createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.branches.Create(ctx, createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	barber, stranger := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	manager, otherManager := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	f.store.addStaff(f.business, barber, domain.RoleBarber, true, branch.ID())
	f.store.addStaff(f.business, manager, domain.RoleManager, true, branch.ID())
	f.store.addStaff(f.business, otherManager, domain.RoleManager, true, other.ID())
	q := func(actor shared.UserID) app.BranchQuery {
		return app.BranchQuery{Actor: actor, BusinessID: f.business, BranchID: branch.ID()}
	}
	address := "طريق الملك فهد"
	update := func(actor shared.UserID) app.UpdateBranch {
		return app.UpdateBranch{Actor: actor, BusinessID: f.business, BranchID: branch.ID(), ExpectedVersion: 1, Address: &address}
	}

	// Staff read; the owner and the branch's managers write; strangers see nothing.
	if _, err := f.branches.Get(ctx, q(barber)); err != nil {
		t.Errorf("barber get: %v", err)
	}
	if list, err := f.branches.List(ctx, barber, f.business); err != nil || len(list) != 2 {
		t.Errorf("barber list: %d, %v", len(list), err)
	}
	// A manager edits the branch they work at.
	if b, err := f.branches.Update(ctx, update(manager)); err != nil || b.Profile().Address != address {
		t.Fatalf("manager update own branch: %v", err)
	}

	f.branchStore.calls = 0
	refused := []struct {
		name string
		call func() error
		want error
	}{
		{"barber create", func() error { _, err := f.branches.Create(ctx, createCmd(barber, f.business)); return err }, domain.ErrForbidden},
		{"barber update", func() error { _, err := f.branches.Update(ctx, update(barber)); return err }, domain.ErrForbidden},
		{"manager create", func() error { _, err := f.branches.Create(ctx, createCmd(manager, f.business)); return err }, domain.ErrForbidden},
		{"manager of another branch", func() error { _, err := f.branches.Update(ctx, update(otherManager)); return err }, domain.ErrForbidden},
		{"stranger get", func() error { _, err := f.branches.Get(ctx, q(stranger)); return err }, domain.ErrNotFound},
		{"stranger list", func() error { _, err := f.branches.List(ctx, stranger, f.business); return err }, domain.ErrNotFound},
		{"stranger create", func() error { _, err := f.branches.Create(ctx, createCmd(stranger, f.business)); return err }, domain.ErrNotFound},
		{"stranger update", func() error { _, err := f.branches.Update(ctx, update(stranger)); return err }, domain.ErrNotFound},
	}
	for _, tt := range refused {
		if err := tt.call(); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	if f.branchStore.calls != 0 {
		t.Errorf("refused callers reached the branch store %d times", f.branchStore.calls)
	}
}

func TestUpdateBranch(t *testing.T) {
	t.Parallel()
	f := newBranchFixture(t)
	ctx := t.Context()
	branch, err := f.branches.Create(ctx, createCmd(f.owner, f.business))
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Hour)

	// Only what's sent changes; empty district and phone clear them.
	empty, city := "", "jeddah"
	got, err := f.branches.Update(ctx, app.UpdateBranch{
		Actor: f.owner, BusinessID: f.business, BranchID: branch.ID(), ExpectedVersion: 1,
		CityCode: &city, District: &empty, Phone: &empty,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := got.Profile()
	if p.City != "jeddah" || p.District != "" || !p.Phone.IsZero() || p.Address != "شارع العليا العام" ||
		p.Name != branch.Profile().Name || got.Version() != 2 || !got.UpdatedAt().Equal(t0.Add(time.Hour)) {
		t.Errorf("after update: %+v", got)
	}

	// Stale version, another business's branch, invalid values: all refused.
	if _, err := f.branches.Update(ctx, app.UpdateBranch{Actor: f.owner, BusinessID: f.business, BranchID: branch.ID(), ExpectedVersion: 1, CityCode: &city}); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale version: %v", err)
	}
	tz := "Mars/Olympus"
	if _, err := f.branches.Update(ctx, app.UpdateBranch{Actor: f.owner, BusinessID: f.business, BranchID: branch.ID(), ExpectedVersion: 2, Timezone: &tz}); !errors.Is(err, domain.ErrInvalidTimezone) {
		t.Errorf("bad timezone: %v", err)
	}
	if again, _ := f.branches.Get(ctx, app.BranchQuery{Actor: f.owner, BusinessID: f.business, BranchID: branch.ID()}); again.Version() != 2 {
		t.Errorf("a refused update was saved: version %d", again.Version())
	}
}
