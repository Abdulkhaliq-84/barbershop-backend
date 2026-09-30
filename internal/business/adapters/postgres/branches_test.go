package postgres_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// registered saves a new business and returns its ID.
func registered(t *testing.T, store *postgres.Store, cr string) shared.BusinessID {
	t.Helper()
	b, m := newBusiness(t, shared.NewID[shared.UserTag](), cr)
	if err := store.Register(t.Context(), b, m); err != nil {
		t.Fatal(err)
	}
	return b.ID()
}

func newBranch(t *testing.T, business shared.BusinessID, withPhone bool) *domain.Branch {
	t.Helper()
	name, _ := shared.NewLocalizedText("فرع العليا", "Olaya")
	loc, _ := shared.NewGeoPoint(24.6911, 46.6851)
	p := domain.BranchProfile{Name: name, City: "riyadh", District: "العليا", Address: "شارع العليا العام", Location: loc, Timezone: "Asia/Riyadh"}
	if withPhone {
		p.Phone, _ = shared.NewPhoneNumber("0551234567")
	}
	rules := domain.DefaultBookingPolicy().Rules()
	rules.AutoConfirm, rules.Buffer, rules.CancellationWindow = false, 10*time.Minute, 48*time.Hour
	policy, err := domain.NewBookingPolicy(rules)
	if err != nil {
		t.Fatal(err)
	}
	b, err := domain.NewBranch(shared.NewID[shared.BranchTag](), business, p, policy, t0)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBranchStoreRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	branches := store.Branches()
	business := registered(t, store, "1010123456")
	first, second := newBranch(t, business, true), newBranch(t, business, false)
	for _, b := range []*domain.Branch{first, second} {
		if err := branches.Add(ctx, b, allowAll); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	got, err := branches.ByID(ctx, business, first.ID())
	if err != nil {
		t.Fatal(err)
	}
	// Every field survives the trip, including the policy's durations.
	if got.Profile() != first.Profile() || got.Policy() != first.Policy() || got.Status() != domain.BranchDraft ||
		got.Version() != 1 || !got.CreatedAt().Equal(t0) || got.BusinessID() != business {
		t.Errorf("ByID = %+v\nwant %+v", got, first)
	}
	if got, _ := branches.ByID(ctx, business, second.ID()); !got.Profile().Phone.IsZero() {
		t.Errorf("no phone came back as %q", got.Profile().Phone)
	}

	list, err := branches.List(ctx, business)
	if err != nil || len(list) != 2 || list[0].ID() != first.ID() || list[1].ID() != second.ID() {
		t.Fatalf("List = %v, %v; want oldest first", list, err)
	}
}

// A branch is found only inside its own business: another business's ID
// in the path gets "not found", for reads and for writes.
func TestBranchStoreIsScopedByBusiness(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	branches := store.Branches()
	mine, theirs := registered(t, store, "1010000001"), registered(t, store, "1010000002")
	b := newBranch(t, theirs, true)
	if err := branches.Add(ctx, b, allowAll); err != nil {
		t.Fatal(err)
	}

	if _, err := branches.ByID(ctx, mine, b.ID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByID across businesses: %v", err)
	}
	if list, err := branches.List(ctx, mine); err != nil || len(list) != 0 {
		t.Errorf("List across businesses = %v, %v", list, err)
	}
	err := branches.Update(ctx, mine, b.ID(), 1, func(*domain.Branch) error {
		t.Fatal("fn called for another business's branch")
		return nil
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update across businesses: %v", err)
	}
}

func TestBranchStoreUpdate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	branches := store.Branches()
	business := registered(t, store, "1010123456")
	b := newBranch(t, business, true)
	if err := branches.Add(ctx, b, allowAll); err != nil {
		t.Fatal(err)
	}

	err := branches.Update(ctx, business, b.ID(), 1, func(br *domain.Branch) error {
		p := br.Profile()
		p.Address, p.Phone = "طريق الملك فهد", shared.PhoneNumber{}
		return br.Edit(p, domain.DefaultBookingPolicy(), t0.Add(time.Hour))
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := branches.ByID(ctx, business, b.ID())
	if got.Profile().Address != "طريق الملك فهد" || !got.Profile().Phone.IsZero() || got.Policy() != domain.DefaultBookingPolicy() ||
		got.Version() != 2 || !got.UpdatedAt().Equal(t0.Add(time.Hour)) {
		t.Fatalf("after update: %+v", got)
	}

	// A refused edit (fn error) and a stale version save nothing.
	if err := branches.Update(ctx, business, b.ID(), 2, func(br *domain.Branch) error {
		p := br.Profile()
		p.City = "Not A City"
		return br.Edit(p, br.Policy(), t0)
	}); !errors.Is(err, domain.ErrInvalidCityCode) {
		t.Errorf("invalid edit: %v", err)
	}
	if err := branches.Update(ctx, business, b.ID(), 1, func(*domain.Branch) error { return nil }); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale version: %v", err)
	}
	if got, _ := branches.ByID(ctx, business, b.ID()); got.Version() != 2 || got.Profile().City != "riyadh" {
		t.Fatalf("a refused update was saved: %+v", got)
	}
}

// Two managers' phones save the same version at once: one wins, the other
// is told to reload instead of overwriting.
func TestBranchStoreParallelUpdatesOfTheSameVersion(t *testing.T) {
	t.Parallel()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	branches := store.Branches()
	business := registered(t, store, "1010123456")
	b := newBranch(t, business, true)
	if err := branches.Add(t.Context(), b, allowAll); err != nil {
		t.Fatal(err)
	}

	var (
		wg               sync.WaitGroup
		start            = make(chan struct{})
		saved, conflicts atomic.Int32
	)
	for i := range 2 {
		wg.Go(func() {
			<-start
			err := branches.Update(t.Context(), business, b.ID(), 1, func(br *domain.Branch) error {
				time.Sleep(20 * time.Millisecond) // overlap the two transactions
				p := br.Profile()
				p.Address = []string{"العنوان أ", "العنوان ب"}[i]
				return br.Edit(p, br.Policy(), t0)
			})
			switch {
			case err == nil:
				saved.Add(1)
			case errors.Is(err, domain.ErrVersionConflict):
				conflicts.Add(1)
			default:
				t.Errorf("Update: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if saved.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf("saved=%d conflicts=%d, want 1 and 1", saved.Load(), conflicts.Load())
	}
}
