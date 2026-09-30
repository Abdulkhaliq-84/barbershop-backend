package postgres_test

import (
	"crypto/sha256"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// staffSetup is a registered business with two branches.
type staffSetup struct {
	pool     *pgxpool.Pool
	store    *postgres.Store
	business shared.BusinessID
	owner    shared.UserID
	a, b     shared.BranchID
}

func newStaffSetup(t *testing.T) staffSetup {
	t.Helper()
	pool := migratedDB(t)
	s := staffSetup{pool: pool, store: postgres.NewStore(pool)}
	s.business, s.owner, s.a = s.addBusiness(t, "1010123456")
	br := newBranch(t, s.business, false)
	if err := s.store.Branches().Add(t.Context(), br); err != nil {
		t.Fatal(err)
	}
	s.b = br.ID()
	return s
}

// addBusiness registers a business with one branch in the same database.
func (s staffSetup) addBusiness(t *testing.T, cr string) (shared.BusinessID, shared.UserID, shared.BranchID) {
	t.Helper()
	biz, owner := newBusiness(t, shared.NewID[shared.UserTag](), cr)
	if err := s.store.Register(t.Context(), biz, owner); err != nil {
		t.Fatal(err)
	}
	br := newBranch(t, biz.ID(), false)
	if err := s.store.Branches().Add(t.Context(), br); err != nil {
		t.Fatal(err)
	}
	return biz.ID(), biz.OwnerID(), br.ID()
}

func hashOf(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s staffSetup) invitation(t *testing.T, phone, token string, branches ...shared.BranchID) *domain.Invitation {
	t.Helper()
	p, err := shared.NewPhoneNumber(phone)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := domain.NewInvitation(shared.NewID[domain.InvitationTag](), s.business,
		domain.StaffInvite{Phone: p, Name: "أحمد", Role: domain.RoleBarber, Branches: branches}, hashOf(token), s.owner, t0)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

// accept accepts token as user, at t0 + 1h.
func accept(t *testing.T, store *postgres.Store, token string, user shared.UserID) error {
	t.Helper()
	return store.Invitations().Accept(t.Context(), hashOf(token), func(inv *domain.Invitation) (*domain.StaffMember, error) {
		return inv.Accept(shared.NewID[shared.StaffTag](), user, t0.Add(time.Hour))
	})
}

func TestInvitationStoreRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newStaffSetup(t)
	invs := s.store.Invitations()
	inv := s.invitation(t, "0551234567", "inv_one", s.a, s.b)
	if err := invs.Invite(ctx, inv); err != nil {
		t.Fatal(err)
	}
	pending, err := invs.Pending(ctx, s.business)
	if err != nil || len(pending) != 1 {
		t.Fatalf("Pending = %d, %v", len(pending), err)
	}
	got := pending[0]
	if got.ID() != inv.ID() || got.Phone() != inv.Phone() || got.Name() != "أحمد" || got.Role() != domain.RoleBarber ||
		!slices.Equal(got.Branches(), inv.Branches()) || !slices.Equal(got.TokenHash(), hashOf("inv_one")) ||
		got.Status() != domain.InvitationPending || got.InvitedBy() != s.owner ||
		!got.CreatedAt().Equal(t0) || !got.ExpiresAt().Equal(inv.ExpiresAt()) || got.AcceptedAt() != nil {
		t.Errorf("Pending[0] = %+v, want %+v", got, inv)
	}

	user := shared.NewID[shared.UserTag]()
	if err := accept(t, s.store, "inv_one", user); err != nil {
		t.Fatal(err)
	}
	m, err := s.store.Membership(ctx, s.business, user)
	if err != nil || m.Role() != domain.RoleBarber || m.DisplayName() != "أحمد" || !m.WorksAt(s.a) || !m.WorksAt(s.b) || len(m.Branches()) != 2 {
		t.Fatalf("Membership = %+v, %v", m, err)
	}
	staff, err := s.store.List(ctx, s.business)
	if err != nil || len(staff) != 2 || staff[0].Role() != domain.RoleOwner || len(staff[0].Branches()) != 0 || staff[1].ID() != m.ID() {
		t.Fatalf("List = %+v, %v", staff, err)
	}
	if pending, _ := invs.Pending(ctx, s.business); len(pending) != 0 {
		t.Errorf("accepted invitation still pending")
	}
	// Used up.
	if err := accept(t, s.store, "inv_one", shared.NewID[shared.UserTag]()); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("second accept: error = %v", err)
	}
	if err := accept(t, s.store, "inv_unknown", user); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("unknown token: error = %v", err)
	}
	// Revoking an accepted invitation is refused, and nothing changes.
	err = invs.Update(ctx, s.business, inv.ID(), (*domain.Invitation).Revoke)
	if !errors.Is(err, domain.ErrInvitationClosed) {
		t.Errorf("revoke accepted: error = %v", err)
	}
}

func TestInvitationStoreScopingAndResend(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newStaffSetup(t)
	otherBusiness, _, otherBranch := s.addBusiness(t, "2020123456")
	invs := s.store.Invitations()

	// A branch of another business is refused.
	if err := invs.Invite(ctx, s.invitation(t, "0551234567", "inv_x", s.a, otherBranch)); !errors.Is(err, domain.ErrUnknownBranch) {
		t.Fatalf("foreign branch: error = %v", err)
	}
	first := s.invitation(t, "0551234567", "inv_first", s.a)
	if err := invs.Invite(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Invite the same phone again: the first is revoked.
	second := s.invitation(t, "+966551234567", "inv_second", s.b)
	if err := invs.Invite(ctx, second); err != nil {
		t.Fatal(err)
	}
	pending, err := invs.Pending(ctx, s.business)
	if err != nil || len(pending) != 1 || pending[0].ID() != second.ID() {
		t.Fatalf("Pending = %+v, %v", pending, err)
	}
	if err := accept(t, s.store, "inv_first", shared.NewID[shared.UserTag]()); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("replaced invitation: error = %v", err)
	}
	// Invitations are only found inside their business.
	if err := invs.Update(ctx, otherBusiness, second.ID(), (*domain.Invitation).Revoke); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoke from another business: error = %v", err)
	}
	if err := invs.Update(ctx, s.business, second.ID(), (*domain.Invitation).Revoke); err != nil {
		t.Fatal(err)
	}
	if pending, _ := invs.Pending(ctx, s.business); len(pending) != 0 {
		t.Errorf("revoked invitation still pending")
	}
}

func TestInvitationStoreAlreadyStaff(t *testing.T) {
	t.Parallel()
	s := newStaffSetup(t)
	if err := s.store.Invitations().Invite(t.Context(), s.invitation(t, "0551234567", "inv_owner", s.a)); err != nil {
		t.Fatal(err)
	}
	if err := accept(t, s.store, "inv_owner", s.owner); !errors.Is(err, domain.ErrAlreadyStaff) {
		t.Fatalf("owner accepts: error = %v, want ErrAlreadyStaff", err)
	}
	// Nothing was saved: the invitation is still open.
	if pending, _ := s.store.Invitations().Pending(t.Context(), s.business); len(pending) != 1 {
		t.Errorf("failed accept changed the invitation")
	}
}

func TestInvitationStoreParallelInvites(t *testing.T) {
	t.Parallel()
	s := newStaffSetup(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 4 {
		wg.Go(func() {
			<-start
			inv := s.invitation(t, "0551234567", "inv_"+strconv.Itoa(i), s.a)
			if err := s.store.Invitations().Invite(t.Context(), inv); err != nil {
				t.Errorf("Invite: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if pending, err := s.store.Invitations().Pending(t.Context(), s.business); err != nil || len(pending) != 1 {
		t.Fatalf("pending = %d, %v; want exactly 1", len(pending), err)
	}
}

func TestInvitationStoreParallelAccepts(t *testing.T) {
	t.Parallel()
	s := newStaffSetup(t)
	if err := s.store.Invitations().Invite(t.Context(), s.invitation(t, "0551234567", "inv_tap", s.a)); err != nil {
		t.Fatal(err)
	}
	user := shared.NewID[shared.UserTag]()
	var (
		wg              sync.WaitGroup
		start           = make(chan struct{})
		joined, refused atomic.Int32
	)
	for range 2 {
		wg.Go(func() {
			<-start
			err := s.store.Invitations().Accept(t.Context(), hashOf("inv_tap"), func(inv *domain.Invitation) (*domain.StaffMember, error) {
				time.Sleep(20 * time.Millisecond) // overlap the two transactions
				return inv.Accept(shared.NewID[shared.StaffTag](), user, t0.Add(time.Hour))
			})
			switch {
			case err == nil:
				joined.Add(1)
			case errors.Is(err, domain.ErrInvitationInvalid):
				refused.Add(1)
			default:
				t.Errorf("Accept: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if joined.Load() != 1 || refused.Load() != 1 {
		t.Fatalf("joined=%d refused=%d, want 1 and 1", joined.Load(), refused.Load())
	}
}

func TestStaffBranchesStayInTheirBusiness(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newStaffSetup(t)
	pool := s.pool
	otherBusiness, _, foreign := s.addBusiness(t, "2020123456")
	ownerStaff, err := s.store.Membership(ctx, s.business, s.owner)
	if err != nil {
		t.Fatal(err)
	}
	// Whichever business the row claims, staff and branch don't match.
	for _, businessID := range []shared.BusinessID{s.business, otherBusiness} {
		_, err = pool.Exec(ctx, `INSERT INTO business.staff_branches (staff_id, branch_id, business_id) VALUES ($1, $2, $3)`,
			ownerStaff.ID().UUID(), foreign.UUID(), businessID.UUID())
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Errorf("staff of %s in a foreign branch: error = %v, want a foreign key violation", businessID, err)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO business.invitations (id, business_id, phone, display_name, role, branch_ids, token_hash, status, invited_by, created_at, expires_at)
		VALUES ($1, $2, '0551234567', 'x', 'barber', ARRAY[$3::uuid], $4, 'pending', $5, $6, $7)`,
		shared.NewID[domain.InvitationTag]().UUID(), s.business.UUID(), s.a.UUID(), hashOf("t"), s.owner.UUID(), t0, t0.Add(time.Hour))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "invitations_phone_check" {
		t.Errorf("non-E.164 phone: error = %v", err)
	}
}
