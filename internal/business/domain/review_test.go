package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func businessIn(t *testing.T, status domain.Status) *domain.Business {
	t.Helper()
	r := registration(t)
	return domain.RehydrateBusiness(shared.NewID[shared.BusinessTag](), shared.NewID[shared.UserTag](), r.DisplayName, "مؤسسة", r.CRNumber, status, 3, t0, t0, domain.Review{})
}

var ready = domain.Readiness{Documents: 1, Branches: 1}

func TestSubmit(t *testing.T) {
	t.Parallel()
	later := t0.Add(time.Hour)
	tests := []struct {
		name   string
		status domain.Status
		r      domain.Readiness
		want   error
	}{
		{"draft, ready", domain.StatusDraft, ready, nil},
		{"rejected, fixed", domain.StatusRejected, ready, nil},
		{"no CR document", domain.StatusDraft, domain.Readiness{Branches: 2}, domain.ErrCRDocumentRequired},
		{"no branch", domain.StatusDraft, domain.Readiness{Documents: 1}, domain.ErrBranchRequired},
		{"already under review", domain.StatusPendingReview, ready, domain.ErrInvalidStateTransition},
		{"active", domain.StatusActive, ready, domain.ErrInvalidStateTransition},
		{"suspended", domain.StatusSuspended, ready, domain.ErrInvalidStateTransition},
	}
	for _, tt := range tests {
		b := businessIn(t, tt.status)
		err := b.Submit(tt.r, later)
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
			continue
		}
		if tt.want != nil {
			if b.Status() != tt.status || b.Version() != 3 || b.Review().SubmittedAt != nil {
				t.Errorf("%s: a refused submit changed the business", tt.name)
			}
			continue
		}
		if b.Status() != domain.StatusPendingReview || b.Version() != 4 || !b.UpdatedAt().Equal(later) || !b.Review().SubmittedAt.Equal(later) {
			t.Errorf("%s: after submit = %+v", tt.name, b)
		}
	}
}

func TestApproveAndReject(t *testing.T) {
	t.Parallel()
	admin := shared.NewID[shared.UserTag]()
	later := t0.Add(2 * time.Hour)

	// Reject: a reason is required, trimmed, at most 500 characters.
	b := businessIn(t, domain.StatusPendingReview)
	for reason, want := range map[string]error{
		"   ":                    domain.ErrRejectionReasonRequired,
		strings.Repeat("ب", 501): domain.ErrTextTooLong,
	} {
		if err := b.Reject(admin, reason, later); !errors.Is(err, want) {
			t.Errorf("Reject(%d chars): error = %v, want %v", len(reason), err, want)
		}
	}
	if b.Status() != domain.StatusPendingReview || b.Version() != 3 {
		t.Fatal("a refused rejection changed the business")
	}
	if err := b.Reject(admin, "  الصورة غير واضحة  ", later); err != nil {
		t.Fatal(err)
	}
	r := b.Review()
	if b.Status() != domain.StatusRejected || r.RejectionReason != "الصورة غير واضحة" || r.ReviewedBy != admin || !r.ReviewedAt.Equal(later) || b.Version() != 4 {
		t.Fatalf("after reject: %+v %+v", b, r)
	}

	// The owner fixes it and resubmits; the reviewer approves.
	if err := b.Submit(ready, later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := b.Approve(admin, later.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	r = b.Review()
	if b.Status() != domain.StatusActive || r.RejectionReason != "" || !r.ReviewedAt.Equal(later.Add(2*time.Hour)) || b.Version() != 6 {
		t.Fatalf("after approve: %+v %+v", b, r)
	}

	// Nobody reviews their own business.
	own := businessIn(t, domain.StatusPendingReview)
	if err := own.Approve(own.OwnerID(), later); !errors.Is(err, domain.ErrSelfReview) {
		t.Errorf("self-approve: %v", err)
	}
	if err := own.Reject(own.OwnerID(), "x", later); !errors.Is(err, domain.ErrSelfReview) {
		t.Errorf("self-reject: %v", err)
	}

	// Decisions need a business under review.
	for _, status := range []domain.Status{domain.StatusDraft, domain.StatusActive, domain.StatusRejected, domain.StatusSuspended} {
		if err := businessIn(t, status).Approve(admin, later); !errors.Is(err, domain.ErrInvalidStateTransition) {
			t.Errorf("approve %s: %v", status, err)
		}
		if err := businessIn(t, status).Reject(admin, "x", later); !errors.Is(err, domain.ErrInvalidStateTransition) {
			t.Errorf("reject %s: %v", status, err)
		}
	}
}
