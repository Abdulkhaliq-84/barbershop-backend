package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestApproveRecordsAnEvent(t *testing.T) {
	t.Parallel()
	b := businessIn(t, domain.StatusPendingReview)
	if len(b.Events()) != 0 {
		t.Fatalf("events before approval = %v", b.Events())
	}
	admin := shared.NewID[shared.UserTag]()
	at := t0.Add(2 * time.Hour)
	if err := b.Approve(admin, at); err != nil {
		t.Fatal(err)
	}
	got := b.Events()
	want := domain.BusinessApproved{Business: b.ID(), Owner: b.OwnerID(), At: at}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("events = %+v, want [%+v]", got, want)
	}

	// Rejections and refused approvals record nothing.
	r := businessIn(t, domain.StatusPendingReview)
	if err := r.Reject(admin, "صورة غير واضحة", at); err != nil || len(r.Events()) != 0 {
		t.Errorf("reject: %v, events %v", err, r.Events())
	}
	if err := r.Approve(admin, at); err == nil || len(r.Events()) != 0 {
		t.Errorf("approving a rejected business: %v, events %v", err, r.Events())
	}
}

func TestLimits(t *testing.T) {
	t.Parallel()
	l := domain.Limits{MaxBranches: 2, MaxStaff: 3}
	if l.AllowBranch(1) != nil || !errors.Is(l.AllowBranch(2), domain.ErrBranchLimitReached) {
		t.Error("branch limit boundary")
	}
	if l.AllowStaff(2) != nil || !errors.Is(l.AllowStaff(3), domain.ErrStaffLimitReached) {
		t.Error("staff limit boundary")
	}
}
