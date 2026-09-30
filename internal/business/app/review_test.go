package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

type reviewFixture struct {
	*docFixture
	submit *app.SubmitHandler
	review *app.ReviewHandlers
	admin  app.Admin
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	f := newDocFixture(t)
	f.store.readiness = domain.Readiness{Documents: 1, Branches: 1}
	return &reviewFixture{
		docFixture: f,
		submit:     app.NewSubmitHandler(f.store, f.store, f.clock),
		review:     app.NewReviewHandlers(f.store, f.docs, &branchStore{}, f.store, f.files, f.clock),
		admin:      app.Admin{ID: shared.NewID[shared.UserTag](), IsPlatformAdmin: true},
	}
}

func TestSubmitBusiness(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newReviewFixture(t)
	manager := shared.NewID[shared.UserTag]()
	f.store.addStaff(f.business, manager, domain.RoleManager, true)

	if _, err := f.submit.Handle(ctx, manager, f.business, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("manager: %v", err)
	}
	if _, err := f.submit.Handle(ctx, shared.NewID[shared.UserTag](), f.business, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
	f.store.readiness = domain.Readiness{Branches: 1}
	if _, err := f.submit.Handle(ctx, f.owner, f.business, 1); !errors.Is(err, domain.ErrCRDocumentRequired) {
		t.Errorf("no document: %v", err)
	}
	f.store.readiness = domain.Readiness{Documents: 1, Branches: 1}
	b, err := f.submit.Handle(ctx, f.owner, f.business, 1)
	if err != nil || b.Status() != domain.StatusPendingReview || b.Version() != 2 {
		t.Fatalf("submit = %+v, %v", b, err)
	}
	if _, err := f.submit.Handle(ctx, f.owner, f.business, 2); !errors.Is(err, domain.ErrInvalidStateTransition) {
		t.Errorf("submit twice: %v", err)
	}
}

// Every admin operation checks the platform role first: an owner, even of
// the very business, can't review it.
func TestReviewNeedsAPlatformAdmin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newReviewFixture(t)
	if _, err := f.submit.Handle(ctx, f.owner, f.business, 1); err != nil {
		t.Fatal(err)
	}
	f.store.loads = 0
	for _, caller := range []app.Admin{
		{ID: f.owner},
		{ID: shared.NewID[shared.UserTag]()},
		{IsPlatformAdmin: true}, // a role without a user is not an admin either
	} {
		if _, _, err := f.review.Queue(ctx, caller, domain.StatusPendingReview, nil, 20); !errors.Is(err, domain.ErrNotPlatformAdmin) {
			t.Errorf("queue: %v", err)
		}
		if _, err := f.review.Detail(ctx, caller, f.business); !errors.Is(err, domain.ErrNotPlatformAdmin) {
			t.Errorf("detail: %v", err)
		}
		if _, err := f.review.Approve(ctx, caller, f.business, 2); !errors.Is(err, domain.ErrNotPlatformAdmin) {
			t.Errorf("approve: %v", err)
		}
		if _, err := f.review.Reject(ctx, caller, f.business, 2, "no"); !errors.Is(err, domain.ErrNotPlatformAdmin) {
			t.Errorf("reject: %v", err)
		}
	}
	if f.store.loads != 0 || f.store.businesses[f.business].Status() != domain.StatusPendingReview {
		t.Fatal("a non-admin reached the store or changed the business")
	}
}

func TestReviewDecisions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newReviewFixture(t)
	if _, err := f.upload(f.owner, "%PDF-1.7"); err != nil {
		t.Fatal(err)
	}

	// Drafts are the owner's own until submitted.
	if _, err := f.review.Detail(ctx, f.admin, f.business); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("draft detail: %v", err)
	}
	if _, err := f.submit.Handle(ctx, f.owner, f.business, 1); err != nil {
		t.Fatal(err)
	}
	v, err := f.review.Detail(ctx, f.admin, f.business)
	if err != nil || len(v.Documents) != 1 || v.Documents[0].DownloadURL == "" {
		t.Fatalf("detail = %+v, %v", v, err)
	}

	b, err := f.review.Reject(ctx, f.admin, f.business, 2, "الصورة غير واضحة")
	if err != nil || b.Status() != domain.StatusRejected || b.Review().ReviewedBy != f.admin.ID {
		t.Fatalf("reject = %+v, %v", b, err)
	}
	if _, err := f.submit.Handle(ctx, f.owner, f.business, 3); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if _, err := f.review.Approve(ctx, f.admin, f.business, 3); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale approve: %v", err)
	}
	b, err = f.review.Approve(ctx, f.admin, f.business, 4)
	if err != nil || b.Status() != domain.StatusActive {
		t.Fatalf("approve = %+v, %v", b, err)
	}
}

func TestReviewQueuePages(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newReviewFixture(t)
	f.store.readiness = domain.Readiness{Documents: 1, Branches: 1}
	var submitted []shared.BusinessID
	for i := range 5 {
		owner := shared.NewID[shared.UserTag]()
		cmd := registerCmd(owner)
		cmd.CRNumber = "101000000" + string(rune('1'+i))
		b, err := f.register.Handle(ctx, cmd)
		if err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(time.Minute) // distinct submission times, oldest first
		if _, err := f.submit.Handle(ctx, owner, b.ID(), 1); err != nil {
			t.Fatal(err)
		}
		submitted = append(submitted, b.ID())
	}

	var got []shared.BusinessID
	var after *app.QueuePosition
	for pages := 0; ; pages++ {
		page, next, err := f.review.Queue(ctx, f.admin, domain.StatusPendingReview, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range page {
			got = append(got, b.ID())
		}
		if next == nil {
			if pages != 2 || len(page) != 1 {
				t.Fatalf("last page after %d pages had %d rows, want 3 pages of 2+2+1", pages+1, len(page))
			}
			break
		}
		after = next
	}
	if len(got) != 5 {
		t.Fatalf("paged through %d businesses, want 5", len(got))
	}
	for i := range got {
		if got[i] != submitted[i] {
			t.Fatalf("order %v, want oldest submission first %v", got, submitted)
		}
	}
	// The fixture's own draft business is never listed; drafts aren't a queue.
	if _, _, err := f.review.Queue(ctx, f.admin, domain.StatusDraft, nil, 20); !errors.Is(err, domain.ErrUnknownStatus) {
		t.Errorf("draft queue: %v", err)
	}
}
