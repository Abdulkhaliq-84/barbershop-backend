package postgres_test

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Two owners' taps on "add branch" at once can't both take the last place:
// the count is read under the business lock.
func TestBranchLimitUnderParallelAdds(t *testing.T) {
	t.Parallel()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	business := registered(t, store, "1010123456")
	limits := domain.Limits{MaxBranches: 1, MaxStaff: 1}

	var (
		wg             sync.WaitGroup
		start          = make(chan struct{})
		added, refused atomic.Int32
		seen           sync.Map
	)
	for range 4 {
		wg.Go(func() {
			<-start
			err := store.Branches().Add(t.Context(), newBranch(t, business, false), func(existing int) error {
				seen.Store(existing, true)
				time.Sleep(20 * time.Millisecond) // overlap the transactions
				return limits.AllowBranch(existing)
			})
			switch {
			case err == nil:
				added.Add(1)
			case errors.Is(err, domain.ErrBranchLimitReached):
				refused.Add(1)
			default:
				t.Errorf("Add: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if added.Load() != 1 || refused.Load() != 3 {
		t.Fatalf("added=%d refused=%d, want 1 and 3", added.Load(), refused.Load())
	}
	if list, _ := store.Branches().List(t.Context(), business); len(list) != 1 {
		t.Errorf("branches = %d, want 1", len(list))
	}
}

// Seats are active managers and barbers plus invitations that can still be
// accepted; the owner, revoked, expired and replaced invitations don't count.
func TestInviteCountsSeats(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newStaffSetup(t)
	invs := s.store.Invitations()
	var seats []int
	record := func(n int) error { seats = append(seats, n); return nil }

	if err := invs.Invite(ctx, s.invitation(t, "0551111111", "inv_1", s.a), record); err != nil {
		t.Fatal(err)
	}
	// Re-inviting the same phone replaces it: still one seat before this one.
	if err := invs.Invite(ctx, s.invitation(t, "0551111111", "inv_1b", s.a), record); err != nil {
		t.Fatal(err)
	}
	if err := accept(t, s.store, "inv_1b", shared.NewID[shared.UserTag]()); err != nil {
		t.Fatal(err)
	}
	expired := s.invitation(t, "0552222222", "inv_2", s.a)
	if err := invs.Invite(ctx, expired, record); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE business.invitations SET created_at = created_at - interval '8 days', expires_at = expires_at - interval '8 days' WHERE id = $1`, expired.ID().UUID()); err != nil {
		t.Fatal(err)
	}
	revoked := s.invitation(t, "0553333333", "inv_3", s.a)
	if err := invs.Invite(ctx, revoked, record); err != nil {
		t.Fatal(err)
	}
	if err := invs.Update(ctx, s.business, revoked.ID(), (*domain.Invitation).Revoke); err != nil {
		t.Fatal(err)
	}
	if err := invs.Invite(ctx, s.invitation(t, "0554444444", "inv_4", s.a), record); err != nil {
		t.Fatal(err)
	}
	// Seats before each invite: nothing; nothing (the first is replaced, not
	// added); the barber who joined; the barber (the open invitation expired
	// before this count); the barber (expired and revoked don't count).
	want := []int{0, 0, 1, 1, 1}
	if len(seats) != len(want) {
		t.Fatalf("seats = %v, want %v", seats, want)
	}
	for i := range want {
		if seats[i] != want[i] {
			t.Fatalf("seats = %v, want %v", seats, want)
		}
	}

	// Full: the invitation is refused and nothing is saved.
	full := domain.Limits{MaxBranches: 1, MaxStaff: 2}
	if err := invs.Invite(ctx, s.invitation(t, "0555555555", "inv_5", s.a), full.AllowStaff); !errors.Is(err, domain.ErrStaffLimitReached) {
		t.Fatalf("over the limit: %v", err)
	}
	if err := accept(t, s.store, "inv_5", shared.NewID[shared.UserTag]()); !errors.Is(err, domain.ErrInvitationInvalid) {
		t.Errorf("refused invitation was saved: %v", err)
	}
}

func TestInviteLimitUnderParallelInvites(t *testing.T) {
	t.Parallel()
	s := newStaffSetup(t)
	limits := domain.Limits{MaxBranches: 1, MaxStaff: 1}
	var (
		wg            sync.WaitGroup
		start         = make(chan struct{})
		sent, refused atomic.Int32
	)
	for i := range 4 {
		wg.Go(func() {
			<-start
			inv := s.invitation(t, "055000000"+strconv.Itoa(i), "inv_"+strconv.Itoa(i), s.a)
			err := s.store.Invitations().Invite(t.Context(), inv, limits.AllowStaff)
			switch {
			case err == nil:
				sent.Add(1)
			case errors.Is(err, domain.ErrStaffLimitReached):
				refused.Add(1)
			default:
				t.Errorf("Invite: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if sent.Load() != 1 || refused.Load() != 3 {
		t.Fatalf("sent=%d refused=%d, want 1 and 3", sent.Load(), refused.Load())
	}
}
