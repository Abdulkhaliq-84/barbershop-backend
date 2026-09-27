package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// store is an in-memory stand-in for the postgres adapter: it implements
// domain.Businesses, domain.Staff and app.MembershipReader.
type store struct {
	mu         sync.Mutex
	businesses map[shared.BusinessID]*domain.Business
	staff      []*domain.StaffMember
	loads      int // ByID calls, to prove authorization runs first
}

func newStore() *store {
	return &store{businesses: map[shared.BusinessID]*domain.Business{}}
}

func (s *store) Register(_ context.Context, b *domain.Business, owner *domain.StaffMember) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, other := range s.businesses {
		if other.OwnerID() == b.OwnerID() && other.CRNumber() == b.CRNumber() {
			return domain.ErrAlreadyRegistered
		}
	}
	s.businesses[b.ID()] = b
	s.staff = append(s.staff, owner)
	return nil
}

func (s *store) ByID(_ context.Context, id shared.BusinessID) (*domain.Business, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if b, ok := s.businesses[id]; ok {
		return b, nil
	}
	return nil, domain.ErrNotFound
}

func (s *store) Membership(_ context.Context, business shared.BusinessID, user shared.UserID) (*domain.StaffMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.staff {
		if m.BusinessID() == business && m.UserID() == user {
			return m, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *store) ForUser(_ context.Context, user shared.UserID) ([]app.MembershipView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var views []app.MembershipView
	for _, m := range s.staff {
		if m.UserID() != user || !m.IsActive() {
			continue
		}
		b := s.businesses[m.BusinessID()]
		views = append(views, app.MembershipView{StaffID: m.ID(), Role: m.Role(), BusinessID: b.ID(), DisplayName: b.DisplayName(), Status: b.Status()})
	}
	return views, nil
}

// addStaff puts user into business with role.
func (s *store) addStaff(business shared.BusinessID, user shared.UserID, role domain.Role, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.staff = append(s.staff, domain.RehydrateStaffMember(shared.NewID[shared.StaffTag](), business, user, role, active, t0))
}

type fixture struct {
	store    *store
	register *app.RegisterBusinessHandler
	get      *app.GetBusinessHandler
	list     *app.ListMyMembershipsHandler
}

func newFixture() *fixture {
	s := newStore()
	return &fixture{
		store:    s,
		register: app.NewRegisterBusinessHandler(s, clock.NewFake(t0)),
		get:      app.NewGetBusinessHandler(s, s),
		list:     app.NewListMyMembershipsHandler(s),
	}
}

func registerCmd(actor shared.UserID) app.RegisterBusiness {
	return app.RegisterBusiness{Actor: actor, DisplayNameAr: "صالون الأناقة", DisplayNameEn: "Elegance", LegalName: "مؤسسة الأناقة", CRNumber: "١٠١٠١٢٣٤٥٦"}
}

func TestRegisterBusiness(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()
	owner := shared.NewID[shared.UserTag]()

	b, err := f.register.Handle(ctx, registerCmd(owner))
	if err != nil {
		t.Fatal(err)
	}
	if b.Status() != domain.StatusDraft || b.OwnerID() != owner || b.CRNumber().String() != "1010123456" || !b.CreatedAt().Equal(t0) {
		t.Errorf("business = %+v", b)
	}
	views, err := f.list.Handle(ctx, owner)
	if err != nil || len(views) != 1 || views[0].BusinessID != b.ID() || views[0].Role != domain.RoleOwner || views[0].Status != domain.StatusDraft {
		t.Fatalf("memberships = %+v, %v", views, err)
	}

	// Same owner, same CR number: a retry, refused.
	if _, err := f.register.Handle(ctx, registerCmd(owner)); !errors.Is(err, domain.ErrAlreadyRegistered) {
		t.Fatalf("second registration: error = %v, want ErrAlreadyRegistered", err)
	}
	// Another user may register the same number as a draft (ADR-0015).
	if _, err := f.register.Handle(ctx, registerCmd(shared.NewID[shared.UserTag]())); err != nil {
		t.Fatalf("another owner's draft: %v", err)
	}
}

func TestRegisterBusinessValidates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*app.RegisterBusiness)
		want   error
	}{
		{"cr number", func(c *app.RegisterBusiness) { c.CRNumber = "123" }, domain.ErrInvalidCRNumber},
		{"arabic name", func(c *app.RegisterBusiness) { c.DisplayNameAr = " " }, shared.ErrArabicRequired},
		{"legal name", func(c *app.RegisterBusiness) { c.LegalName = "" }, domain.ErrLegalNameRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture()
			cmd := registerCmd(shared.NewID[shared.UserTag]())
			tt.change(&cmd)
			if _, err := f.register.Handle(t.Context(), cmd); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if len(f.store.businesses) != 0 {
				t.Fatal("an invalid business was saved")
			}
		})
	}
}

func TestGetBusinessAuthorizesFirst(t *testing.T) {
	t.Parallel()
	f := newFixture()
	owner := shared.NewID[shared.UserTag]()
	b, err := f.register.Handle(t.Context(), registerCmd(owner))
	if err != nil {
		t.Fatal(err)
	}
	manager, barber, former := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	f.store.addStaff(b.ID(), manager, domain.RoleManager, true)
	f.store.addStaff(b.ID(), barber, domain.RoleBarber, true)
	f.store.addStaff(b.ID(), former, domain.RoleOwner, false)

	tests := []struct {
		name     string
		actor    shared.UserID
		business shared.BusinessID
		want     error
	}{
		{"owner", owner, b.ID(), nil},
		{"stranger", shared.NewID[shared.UserTag](), b.ID(), domain.ErrNotFound},
		{"unknown business", owner, shared.NewID[shared.BusinessTag](), domain.ErrNotFound},
		{"manager", manager, b.ID(), domain.ErrForbidden},
		{"barber", barber, b.ID(), domain.ErrForbidden},
		{"former staff", former, b.ID(), domain.ErrNotFound},
	}
	for _, tt := range tests {
		f.store.loads = 0
		got, err := f.get.Handle(t.Context(), app.GetBusiness{Actor: tt.actor, BusinessID: tt.business})
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
			continue
		}
		if tt.want == nil && got.ID() != b.ID() {
			t.Errorf("%s: got business %s", tt.name, got.ID())
		}
		// Refused callers never cause a load: the check comes first.
		if tt.want != nil && f.store.loads != 0 {
			t.Errorf("%s: business loaded before authorization", tt.name)
		}
	}
}

func TestListMyMembershipsForCustomer(t *testing.T) {
	t.Parallel()
	f := newFixture()
	if _, err := f.register.Handle(t.Context(), registerCmd(shared.NewID[shared.UserTag]())); err != nil {
		t.Fatal(err)
	}
	views, err := f.list.Handle(t.Context(), shared.NewID[shared.UserTag]())
	if err != nil || len(views) != 0 {
		t.Fatalf("a customer's memberships = %+v, %v; want none", views, err)
	}
}
