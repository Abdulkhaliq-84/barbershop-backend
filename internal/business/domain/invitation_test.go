package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func validInvite(t *testing.T) domain.StaffInvite {
	t.Helper()
	phone, err := shared.NewPhoneNumber("0551234567")
	if err != nil {
		t.Fatal(err)
	}
	return domain.StaffInvite{Phone: phone, Name: "أحمد", Role: domain.RoleBarber, Branches: []shared.BranchID{shared.NewID[shared.BranchTag]()}}
}

func newInvitation(t *testing.T, in domain.StaffInvite) (*domain.Invitation, error) {
	t.Helper()
	return domain.NewInvitation(shared.NewID[domain.InvitationTag](), shared.NewID[shared.BusinessTag](), in, []byte("hash"), shared.NewID[shared.UserTag](), t0)
}

func TestNewInvitation(t *testing.T) {
	t.Parallel()
	a, b := shared.NewID[shared.BranchTag](), shared.NewID[shared.BranchTag]()
	in := validInvite(t)
	in.Name = "  أحمد  "
	in.Branches = []shared.BranchID{b, a, b}
	inv, err := newInvitation(t, in)
	if err != nil {
		t.Fatal(err)
	}
	want := []shared.BranchID{a, b}
	slices.SortFunc(want, func(x, y shared.BranchID) int { return strings.Compare(x.String(), y.String()) })
	if inv.Name() != "أحمد" || !slices.Equal(inv.Branches(), want) || inv.Status() != domain.InvitationPending ||
		!inv.ExpiresAt().Equal(t0.Add(7*24*time.Hour)) || !inv.IsOpen(t0) {
		t.Errorf("invitation = %+v", inv)
	}

	tooMany := make([]shared.BranchID, domain.MaxStaffBranches+1)
	for i := range tooMany {
		tooMany[i] = shared.NewID[shared.BranchTag]()
	}
	tests := []struct {
		name   string
		change func(*domain.StaffInvite)
		want   error
	}{
		{"owner", func(in *domain.StaffInvite) { in.Role = domain.RoleOwner }, domain.ErrInvalidInviteRole},
		{"no role", func(in *domain.StaffInvite) { in.Role = "" }, domain.ErrInvalidInviteRole},
		{"no phone", func(in *domain.StaffInvite) { in.Phone = shared.PhoneNumber{} }, shared.ErrInvalidPhoneNumber},
		{"blank name", func(in *domain.StaffInvite) { in.Name = " \t" }, domain.ErrStaffNameRequired},
		{"long name", func(in *domain.StaffInvite) { in.Name = strings.Repeat("ب", domain.MaxStaffNameLen+1) }, domain.ErrTextTooLong},
		{"no branches", func(in *domain.StaffInvite) { in.Branches = nil }, domain.ErrStaffBranchRequired},
		{"too many branches", func(in *domain.StaffInvite) { in.Branches = tooMany }, domain.ErrStaffBranchRequired},
	}
	for _, tt := range tests {
		in := validInvite(t)
		tt.change(&in)
		if _, err := newInvitation(t, in); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	in = validInvite(t)
	in.Name = strings.Repeat("ب", domain.MaxStaffNameLen) // 80 letters, 160 bytes: fine
	if _, err := newInvitation(t, in); err != nil {
		t.Errorf("80-letter name: %v", err)
	}
}

func TestInvitationAccept(t *testing.T) {
	t.Parallel()
	in := validInvite(t)
	in.Role = domain.RoleManager
	inv, err := newInvitation(t, in)
	if err != nil {
		t.Fatal(err)
	}
	user, staffID := shared.NewID[shared.UserTag](), shared.NewID[shared.StaffTag]()

	// Open until the last instant before expiry.
	if !inv.IsOpen(inv.ExpiresAt().Add(-time.Nanosecond)) || inv.IsOpen(inv.ExpiresAt()) {
		t.Error("expiry boundary")
	}
	if _, err := inv.Accept(staffID, user, inv.ExpiresAt()); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Fatalf("expired accept: error = %v", err)
	}

	at := t0.Add(time.Hour)
	m, err := inv.Accept(staffID, user, at)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID() != staffID || m.UserID() != user || m.BusinessID() != inv.BusinessID() || m.Role() != domain.RoleManager ||
		m.DisplayName() != "أحمد" || !slices.Equal(m.Branches(), in.Branches) || !m.IsActive() || !m.CreatedAt().Equal(at) {
		t.Errorf("staff member = %+v", m)
	}
	if inv.Status() != domain.InvitationAccepted || inv.AcceptedBy() != user || inv.AcceptedAt() == nil || !inv.AcceptedAt().Equal(at) {
		t.Errorf("invitation after accept = %+v", inv)
	}
	if _, err := inv.Accept(staffID, user, at); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("accept twice: error = %v", err)
	}
	if err := inv.Revoke(); !errors.Is(err, domain.ErrInvitationClosed) {
		t.Errorf("revoke accepted: error = %v", err)
	}
}

func TestInvitationRevoke(t *testing.T) {
	t.Parallel()
	inv, err := newInvitation(t, validInvite(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.Revoke(); err != nil || inv.Status() != domain.InvitationRevoked || inv.IsOpen(t0) {
		t.Fatalf("revoke: %v, status %s", err, inv.Status())
	}
	if _, err := inv.Accept(shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag](), t0); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("accept revoked: error = %v", err)
	}
}

func TestAuthorizeBranch(t *testing.T) {
	t.Parallel()
	business := shared.NewID[shared.BusinessTag]()
	mine, other := shared.NewID[shared.BranchTag](), shared.NewID[shared.BranchTag]()
	member := func(role domain.Role, active bool, branches ...shared.BranchID) *domain.StaffMember {
		return domain.RehydrateStaffMember(shared.NewID[shared.StaffTag](), business, shared.NewID[shared.UserTag](), role, active, t0, "", branches)
	}
	tests := []struct {
		name   string
		m      *domain.StaffMember
		branch shared.BranchID
		want   error
	}{
		{"owner, any branch", member(domain.RoleOwner, true), other, nil},
		{"manager, own branch", member(domain.RoleManager, true, mine), mine, nil},
		{"manager, other branch", member(domain.RoleManager, true, mine), other, domain.ErrForbidden},
		{"barber, own branch", member(domain.RoleBarber, true, mine), mine, domain.ErrForbidden},
		{"inactive manager", member(domain.RoleManager, false, mine), mine, domain.ErrForbidden},
	}
	for _, tt := range tests {
		if err := tt.m.AuthorizeBranch(domain.RoleManager, tt.branch); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
}
