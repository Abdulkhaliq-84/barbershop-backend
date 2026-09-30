package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// makeReady gives a business the CR document and branch that submission needs.
func makeReady(t *testing.T, store *postgres.Store, business shared.BusinessID) {
	t.Helper()
	if err := store.Documents().Attach(t.Context(), newDoc(business, t0), allow); err != nil {
		t.Fatal(err)
	}
	if err := store.Branches().Add(t.Context(), newBranch(t, business, false), allowAll); err != nil {
		t.Fatal(err)
	}
}

func submit(t *testing.T, store *postgres.Store, id shared.BusinessID, version int, at time.Time) error {
	t.Helper()
	return store.UpdateWithReadiness(t.Context(), id, version, func(b *domain.Business, r domain.Readiness) error {
		return b.Submit(r, at)
	})
}

func TestStoreUpdateWithReadiness(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	id := registered(t, store, "1010123456")

	var seen domain.Readiness
	peek := func(_ *domain.Business, r domain.Readiness) error { seen = r; return errors.New("just looking") }
	_ = store.UpdateWithReadiness(ctx, id, 1, peek)
	if seen != (domain.Readiness{}) {
		t.Fatalf("empty business readiness = %+v", seen)
	}
	if err := submit(t, store, id, 1, t0); !errors.Is(err, domain.ErrCRDocumentRequired) {
		t.Fatalf("submit without documents: %v", err)
	}
	makeReady(t, store, id)
	_ = store.UpdateWithReadiness(ctx, id, 1, peek)
	if seen != (domain.Readiness{Documents: 1, Branches: 1}) {
		t.Fatalf("readiness = %+v, want 1 and 1", seen)
	}
}

func TestStoreReviewRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	id := registered(t, store, "1010123456")
	makeReady(t, store, id)
	admin := shared.NewID[shared.UserTag]()

	if err := submit(t, store, id, 1, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, id, 2, func(b *domain.Business) error {
		return b.Reject(admin, "الصورة غير واضحة", t0.Add(2*time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.ByID(ctx, id)
	r := got.Review()
	if got.Status() != domain.StatusRejected || !r.SubmittedAt.Equal(t0.Add(time.Hour)) || !r.ReviewedAt.Equal(t0.Add(2*time.Hour)) ||
		r.ReviewedBy != admin || r.RejectionReason != "الصورة غير واضحة" || got.Version() != 3 {
		t.Fatalf("after reject: %+v %+v", got, r)
	}
}

// Submitting claims the CR number platform-wide. A second business with the
// same number waits until the first lets go of it (rejected).
func TestSubmitClaimsTheCRNumber(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	first, second := registered(t, store, "1010123456"), registered(t, store, "1010123456")
	makeReady(t, store, first)
	makeReady(t, store, second)

	if err := submit(t, store, first, 1, t0); err != nil {
		t.Fatal(err)
	}
	if err := submit(t, store, second, 1, t0); !errors.Is(err, domain.ErrCRNumberClaimed) {
		t.Fatalf("second submission: %v, want ErrCRNumberClaimed", err)
	}
	if got, _ := store.ByID(ctx, second); got.Status() != domain.StatusDraft || got.Version() != 1 {
		t.Fatal("the refused submission was saved")
	}
	if err := store.Update(ctx, first, 2, func(b *domain.Business) error {
		return b.Reject(shared.NewID[shared.UserTag](), "not your CR", t0)
	}); err != nil {
		t.Fatal(err)
	}
	if err := submit(t, store, second, 1, t0); err != nil {
		t.Fatalf("after the first was rejected: %v", err)
	}
}

func TestReviewPage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	var want []shared.BusinessID
	for i, at := range []time.Time{t0, t0.Add(time.Minute), t0.Add(time.Minute), t0.Add(2 * time.Minute)} { // a tie in the middle
		id := registered(t, store, "101000000"+string(rune('1'+i)))
		makeReady(t, store, id)
		if err := submit(t, store, id, 1, at); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	_ = registered(t, store, "1010000009") // a draft: never in the queue
	// The tie is broken by ID, which grows with creation (UUIDv7).

	var got []shared.BusinessID
	var after *app.QueuePosition
	for {
		page, err := store.ReviewPage(ctx, domain.StatusPendingReview, after, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range page {
			got = append(got, b.ID())
		}
		if len(page) < 3 {
			break
		}
		last := page[len(page)-1]
		after = &app.QueuePosition{SubmittedAt: *last.Review().SubmittedAt, ID: last.ID()}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d businesses, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
	if page, _ := store.ReviewPage(ctx, domain.StatusActive, nil, 10); len(page) != 0 {
		t.Fatalf("active queue = %d, want empty", len(page))
	}
}
