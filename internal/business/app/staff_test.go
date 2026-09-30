package app_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// invitationStore is an in-memory domain.Invitations. Accepting adds the
// staff member to the main store, like the real transaction does.
type invitationStore struct {
	mu       sync.Mutex
	store    *store
	branches map[shared.BranchID]shared.BusinessID // which branches exist
	invs     []*domain.Invitation
}

func (s *invitationStore) Invite(_ context.Context, inv *domain.Invitation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range inv.Branches() {
		if s.branches[b] != inv.BusinessID() {
			return domain.ErrUnknownBranch
		}
	}
	for _, old := range s.invs {
		if old.BusinessID() == inv.BusinessID() && old.Phone() == inv.Phone() && old.Status() == domain.InvitationPending {
			_ = old.Revoke()
		}
	}
	s.invs = append(s.invs, inv)
	return nil
}

func (s *invitationStore) Pending(_ context.Context, business shared.BusinessID) ([]*domain.Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.Invitation
	for _, inv := range s.invs {
		if inv.BusinessID() == business && inv.Status() == domain.InvitationPending {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (s *invitationStore) Update(_ context.Context, business shared.BusinessID, id domain.InvitationID, fn func(*domain.Invitation) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inv := range s.invs {
		if inv.BusinessID() == business && inv.ID() == id {
			return fn(inv)
		}
	}
	return domain.ErrNotFound
}

func (s *invitationStore) Accept(ctx context.Context, tokenHash []byte, fn func(*domain.Invitation) (*domain.StaffMember, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inv := range s.invs {
		if !slices.Equal(inv.TokenHash(), tokenHash) {
			continue
		}
		m, err := fn(inv)
		if err != nil {
			return err
		}
		if _, err := s.store.Membership(ctx, m.BusinessID(), m.UserID()); err == nil {
			return domain.ErrAlreadyStaff
		}
		s.store.mu.Lock()
		s.store.staff = append(s.store.staff, m)
		s.store.mu.Unlock()
		return nil
	}
	return domain.ErrInvitationInvalid
}

// users is an in-memory app.UserDirectory.
type users map[shared.UserID]shared.PhoneNumber

func (u users) PhoneOf(_ context.Context, id shared.UserID) (shared.PhoneNumber, error) {
	if p, ok := u[id]; ok {
		return p, nil
	}
	return shared.PhoneNumber{}, domain.ErrNotFound
}

// countingTokens makes predictable, distinct tokens.
type countingTokens struct {
	mu sync.Mutex
	n  int
}

func (c *countingTokens) New() (string, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	token := "inv_test_" + strconv.Itoa(c.n)
	return token, c.Hash(token), nil
}

func (*countingTokens) Hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// outbox records the invitations "sent".
type outbox struct {
	mu   sync.Mutex
	sent map[shared.PhoneNumber]string // phone → last token
	fail error
}

func (o *outbox) SendInvitation(_ context.Context, to shared.PhoneNumber, _ shared.LocalizedText, token string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return o.fail
	}
	o.sent[to] = token
	return nil
}

func (o *outbox) last(to shared.PhoneNumber) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sent[to]
}

type staffFixture struct {
	*fixture
	invs     *invitationStore
	users    users
	outbox   *outbox
	staff    *app.StaffHandlers
	owner    shared.UserID
	business shared.BusinessID
	branch   shared.BranchID
}

func newStaffFixture(t *testing.T) *staffFixture {
	t.Helper()
	f := newFixture()
	owner := shared.NewID[shared.UserTag]()
	b, err := f.register.Handle(t.Context(), registerCmd(owner))
	if err != nil {
		t.Fatal(err)
	}
	branch := shared.NewID[shared.BranchTag]()
	invs := &invitationStore{store: f.store, branches: map[shared.BranchID]shared.BusinessID{branch: b.ID()}}
	u := users{owner: mustPhone(t, "0500000001")}
	o := &outbox{sent: map[shared.PhoneNumber]string{}}
	return &staffFixture{
		fixture: f, invs: invs, users: u, outbox: o,
		staff: app.NewStaffHandlers(f.store, f.store, invs, u, &countingTokens{}, o, f.clock),
		owner: owner, business: b.ID(), branch: branch,
	}
}

func mustPhone(t *testing.T, s string) shared.PhoneNumber {
	t.Helper()
	p, err := shared.NewPhoneNumber(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// invitee adds a user who signs in with phone.
func (f *staffFixture) invitee(t *testing.T, phone string) (shared.UserID, shared.PhoneNumber) {
	t.Helper()
	id := shared.NewID[shared.UserTag]()
	f.users[id] = mustPhone(t, phone)
	return id, f.users[id]
}

func (f *staffFixture) inviteCmd(phone, role string) app.InviteStaff {
	return app.InviteStaff{Actor: f.owner, BusinessID: f.business, Phone: phone, Name: " أحمد ", Role: role, Branches: []shared.BranchID{f.branch, f.branch}}
}

func TestInviteAndAccept(t *testing.T) {
	t.Parallel()
	f := newStaffFixture(t)
	ctx := t.Context()
	user, phone := f.invitee(t, "0551234567")

	inv, err := f.staff.Invite(ctx, f.inviteCmd("+966 55 123 4567", "barber"))
	if err != nil {
		t.Fatal(err)
	}
	if inv.Status() != domain.InvitationPending || inv.Phone() != phone || inv.Name() != "أحمد" || inv.Role() != domain.RoleBarber ||
		!slices.Equal(inv.Branches(), []shared.BranchID{f.branch}) || !inv.ExpiresAt().Equal(t0.Add(domain.InvitationTTL)) {
		t.Errorf("invitation = %+v", inv)
	}
	token := f.outbox.last(phone)
	if token == "" {
		t.Fatal("no invitation sent")
	}
	if pending, err := f.staff.ListInvitations(ctx, f.owner, f.business); err != nil || len(pending) != 1 {
		t.Fatalf("pending = %d, %v", len(pending), err)
	}

	v, err := f.staff.Accept(ctx, user, token)
	if err != nil {
		t.Fatal(err)
	}
	if v.BusinessID != f.business || v.Role != domain.RoleBarber || v.Status != domain.StatusDraft {
		t.Errorf("membership = %+v", v)
	}
	m, err := f.store.Membership(ctx, f.business, user)
	if err != nil || m.Role() != domain.RoleBarber || m.DisplayName() != "أحمد" || !m.WorksAt(f.branch) || !m.IsActive() {
		t.Fatalf("staff member = %+v, %v", m, err)
	}
	if pending, _ := f.staff.ListInvitations(ctx, f.owner, f.business); len(pending) != 0 {
		t.Errorf("accepted invitation still pending")
	}
	// The same link again: used up.
	if _, err := f.staff.Accept(ctx, user, token); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("second accept: error = %v, want ErrInvitationInvalid", err)
	}
	// Owner and managers see the team; the barber doesn't manage it.
	if list, err := f.staff.ListStaff(ctx, f.owner, f.business); err != nil || len(list) != 2 {
		t.Errorf("staff = %d, %v", len(list), err)
	}
	if _, err := f.staff.ListStaff(ctx, user, f.business); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("barber lists staff: error = %v, want ErrForbidden", err)
	}
}

func TestAcceptRefusals(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("signed in with another phone", func(t *testing.T) {
		t.Parallel()
		f := newStaffFixture(t)
		_, phone := f.invitee(t, "0551234567")
		someoneElse, _ := f.invitee(t, "0559999999")
		if _, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "barber")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.staff.Accept(ctx, someoneElse, f.outbox.last(phone)); !errors.Is(err, domain.ErrInvitationInvalid) {
			t.Errorf("error = %v, want ErrInvitationInvalid", err)
		}
		if _, err := f.store.Membership(ctx, f.business, someoneElse); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("someone else joined")
		}
	})
	t.Run("unknown token or user", func(t *testing.T) {
		t.Parallel()
		f := newStaffFixture(t)
		user, _ := f.invitee(t, "0551234567")
		for _, token := range []string{"", "  ", "inv_nope"} {
			if _, err := f.staff.Accept(ctx, user, token); !errors.Is(err, domain.ErrInvitationInvalid) {
				t.Errorf("token %q: error = %v", token, err)
			}
		}
		if _, err := f.staff.Accept(ctx, shared.NewID[shared.UserTag](), "inv_nope"); !errors.Is(err, domain.ErrInvitationInvalid) {
			t.Errorf("unknown user: error = %v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		t.Parallel()
		f := newStaffFixture(t)
		user, phone := f.invitee(t, "0551234567")
		if _, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "barber")); err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(domain.InvitationTTL)
		if _, err := f.staff.Accept(ctx, user, f.outbox.last(phone)); !errors.Is(err, domain.ErrInvitationInvalid) {
			t.Errorf("error = %v, want ErrInvitationInvalid", err)
		}
	})
	t.Run("revoked", func(t *testing.T) {
		t.Parallel()
		f := newStaffFixture(t)
		user, phone := f.invitee(t, "0551234567")
		inv, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "barber"))
		if err != nil {
			t.Fatal(err)
		}
		q := app.InvitationQuery{Actor: f.owner, BusinessID: f.business, InvitationID: inv.ID()}
		if err := f.staff.Revoke(ctx, q); err != nil {
			t.Fatal(err)
		}
		if _, err := f.staff.Accept(ctx, user, f.outbox.last(phone)); !errors.Is(err, domain.ErrInvitationInvalid) {
			t.Errorf("error = %v, want ErrInvitationInvalid", err)
		}
		if err := f.staff.Revoke(ctx, q); !errors.Is(err, domain.ErrInvitationClosed) {
			t.Errorf("revoke twice: error = %v, want ErrInvitationClosed", err)
		}
	})
	t.Run("resent", func(t *testing.T) {
		t.Parallel()
		f := newStaffFixture(t)
		user, phone := f.invitee(t, "0551234567")
		if _, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "barber")); err != nil {
			t.Fatal(err)
		}
		first := f.outbox.last(phone)
		if _, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "manager")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.staff.Accept(ctx, user, first); !errors.Is(err, domain.ErrInvitationInvalid) {
			t.Errorf("replaced link: error = %v, want ErrInvitationInvalid", err)
		}
		if v, err := f.staff.Accept(ctx, user, f.outbox.last(phone)); err != nil || v.Role != domain.RoleManager {
			t.Errorf("new link: %+v, %v", v, err)
		}
	})
	t.Run("already staff", func(t *testing.T) {
		t.Parallel()
		f := newStaffFixture(t)
		// The owner invites their own number.
		if _, err := f.staff.Invite(ctx, f.inviteCmd("0500000001", "barber")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.staff.Accept(ctx, f.owner, f.outbox.last(f.users[f.owner])); !errors.Is(err, domain.ErrAlreadyStaff) {
			t.Errorf("error = %v, want ErrAlreadyStaff", err)
		}
	})
}

func TestInviteRules(t *testing.T) {
	t.Parallel()
	f := newStaffFixture(t)
	ctx := t.Context()
	manager, barber, stranger := shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag](), shared.NewID[shared.UserTag]()
	f.store.addStaff(f.business, manager, domain.RoleManager, true, f.branch)
	f.store.addStaff(f.business, barber, domain.RoleBarber, true, f.branch)

	tests := []struct {
		name   string
		change func(*app.InviteStaff)
		want   error
	}{
		{"manager invites", func(c *app.InviteStaff) { c.Actor = manager }, domain.ErrForbidden},
		{"barber invites", func(c *app.InviteStaff) { c.Actor = barber }, domain.ErrForbidden},
		{"stranger invites", func(c *app.InviteStaff) { c.Actor = stranger }, domain.ErrNotFound},
		{"bad phone", func(c *app.InviteStaff) { c.Phone = "123" }, shared.ErrInvalidPhoneNumber},
		{"owner role", func(c *app.InviteStaff) { c.Role = "owner" }, domain.ErrInvalidInviteRole},
		{"unknown role", func(c *app.InviteStaff) { c.Role = "admin" }, domain.ErrInvalidInviteRole},
		{"no name", func(c *app.InviteStaff) { c.Name = " " }, domain.ErrStaffNameRequired},
		{"no branch", func(c *app.InviteStaff) { c.Branches = nil }, domain.ErrStaffBranchRequired},
		{"another shop's branch", func(c *app.InviteStaff) { c.Branches = []shared.BranchID{shared.NewID[shared.BranchTag]()} }, domain.ErrUnknownBranch},
	}
	for _, tt := range tests {
		cmd := f.inviteCmd("0551234567", "barber")
		tt.change(&cmd)
		if _, err := f.staff.Invite(ctx, cmd); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	if len(f.outbox.sent) != 0 || len(f.invs.invs) != 0 {
		t.Errorf("refused invites were saved or sent")
	}
	for _, who := range []shared.UserID{manager, barber} {
		if _, err := f.staff.ListInvitations(ctx, who, f.business); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("non-owner lists invitations: error = %v", err)
		}
	}
	if list, err := f.staff.ListStaff(ctx, manager, f.business); err != nil || len(list) != 3 {
		t.Errorf("manager lists staff: %d, %v", len(list), err)
	}

	// A failed SMS is reported, so the owner knows to try again.
	f.outbox.fail = errors.New("sms down")
	if _, err := f.staff.Invite(ctx, f.inviteCmd("0551234567", "barber")); err == nil {
		t.Error("send failure was swallowed")
	}
}
