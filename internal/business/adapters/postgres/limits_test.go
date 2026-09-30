package postgres_test

import (
	"crypto/rand"
	"errors"
	"fmt"
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
	if err := invs.Invite(ctx, s.invitationAt(t, t0.Add(domain.InviteCooldown), s.business, "0551111111", "inv_1b", s.a), record); err != nil {
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

// Every invitation is an SMS: one phone gets at most one a minute from a
// business and five a day from all of them; a business sends at most fifty
// a day.
func TestInvitationSendingLimits(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newStaffSetup(t)
	invs := s.store.Invitations()
	invite := func(at time.Duration, business shared.BusinessID, phone string, branch shared.BranchID) error {
		return invs.Invite(ctx, s.invitationAt(t, t0.Add(at), business, phone, rand.Text(), branch), allowAll)
	}
	retryAfter := func(err error) time.Duration {
		var rate *domain.InviteRateError
		if !errors.As(err, &rate) {
			t.Fatalf("error = %v, want an InviteRateError", err)
		}
		return rate.RetryAfter
	}

	// Resending to one phone: once a minute.
	if err := invite(0, s.business, "0551111111", s.a); err != nil {
		t.Fatal(err)
	}
	err := invite(20*time.Second, s.business, "0551111111", s.a)
	if !errors.Is(err, domain.ErrTooManyInvitations) || retryAfter(err) != 40*time.Second {
		t.Fatalf("resend after 20s: %v", err)
	}
	if err := invite(time.Minute, s.business, "0551111111", s.a); err != nil {
		t.Fatalf("resend after a minute: %v", err)
	}

	// One phone, many businesses: five a day in all.
	for i := range 3 {
		biz, _, branch := s.addBusiness(t, fmt.Sprintf("30%08d", i))
		if err := invite(time.Duration(i+2)*time.Minute, biz, "0551111111", branch); err != nil {
			t.Fatalf("business %d: %v", i, err)
		}
	}
	biz, _, branch := s.addBusiness(t, "3099999999")
	err = invite(10*time.Minute, biz, "0551111111", branch)
	if !errors.Is(err, domain.ErrTooManyInvitations) || retryAfter(err) != 24*time.Hour-10*time.Minute {
		t.Fatalf("sixth to one phone: %v", err)
	}
	if err := invite(24*time.Hour+time.Second, biz, "0551111111", branch); err != nil {
		t.Fatalf("a day later: %v", err)
	}

	// One business, many phones: fifty a day.
	for i := range domain.MaxInvitesPerBusiness - 2 { // two sent above
		if err := invite(time.Hour, s.business, fmt.Sprintf("05600%05d", i), s.a); err != nil {
			t.Fatalf("invitation %d: %v", i, err)
		}
	}
	if err := invite(2*time.Hour, s.business, "0579999999", s.a); !errors.Is(err, domain.ErrTooManyInvitations) {
		t.Fatalf("fifty-first: %v", err)
	}
}

// Each registration can invite staff before any review, so one user keeps
// at most three unapproved businesses — even registering several at once.
func TestRegistrationLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	user := shared.NewID[shared.UserTag]()
	var wg sync.WaitGroup
	const tries = 10
	results := make(chan error, tries)
	start := make(chan struct{})
	for i := range tries {
		biz, owner := newBusiness(t, user, fmt.Sprintf("40%08d", i)) // t.Fatal only on the test goroutine
		wg.Go(func() {
			<-start
			results <- store.Register(ctx, biz, owner)
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var ok, refused int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrTooManyRegistrations):
			refused++
		default:
			t.Fatal(err)
		}
	}
	if ok != domain.MaxOpenRegistrations || refused != tries-domain.MaxOpenRegistrations {
		t.Fatalf("%d registered, %d refused; want 3 and 7", ok, refused)
	}
	// Someone else is unaffected.
	biz, owner := newBusiness(t, shared.NewID[shared.UserTag](), "4099999999")
	if err := store.Register(ctx, biz, owner); err != nil {
		t.Fatal(err)
	}
}

// Seven businesses inviting one phone at the same moment: five get through.
// The business lock can't see the others; the phone lock does.
func TestInvitationPhoneLimitUnderConcurrency(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newStaffSetup(t)
	type sender struct {
		biz    shared.BusinessID
		branch shared.BranchID
	}
	senders := make([]sender, 7)
	for i := range senders {
		biz, _, branch := s.addBusiness(t, fmt.Sprintf("50%08d", i))
		senders[i] = sender{biz, branch}
	}
	invs := make([]*domain.Invitation, len(senders))
	for i, snd := range senders {
		invs[i] = s.invitationAt(t, t0, snd.biz, "0558888888", rand.Text(), snd.branch)
	}
	var sent, refused atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, inv := range invs {
		wg.Go(func() {
			<-start
			switch err := s.store.Invitations().Invite(ctx, inv, allowAll); {
			case err == nil:
				sent.Add(1)
			case errors.Is(err, domain.ErrTooManyInvitations):
				refused.Add(1)
			default:
				t.Errorf("Invite: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if sent.Load() != domain.MaxInvitesPerPhone || refused.Load() != 2 {
		t.Fatalf("%d sent, %d refused; want 5 and 2", sent.Load(), refused.Load())
	}
}
