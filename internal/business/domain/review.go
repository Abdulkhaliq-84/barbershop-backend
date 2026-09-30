package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// MaxRejectionReasonLen caps the note a reviewer sends back, in characters.
const MaxRejectionReasonLen = 500

// Review is where a business is in the platform's verification: when it was
// last submitted and what the reviewer decided.
type Review struct {
	SubmittedAt     *time.Time
	ReviewedAt      *time.Time
	ReviewedBy      shared.UserID // zero until reviewed
	RejectionReason string        // why it was last rejected; "" otherwise
}

// Readiness is what submission depends on, counted by the repository under
// the same lock as the business row.
type Readiness struct {
	Documents int // verification documents (CR certificate)
	Branches  int // branches; each has a map location by construction
}

// Submit sends a draft (or a rejected business, after fixes) for review. The
// reviewer needs the CR document and at least one branch on the map.
//
//	draft ──submit──▶ pending_review ◀──submit── rejected
func (b *Business) Submit(r Readiness, now time.Time) error {
	if b.status != StatusDraft && b.status != StatusRejected {
		return ErrInvalidStateTransition
	}
	if r.Documents < 1 {
		return ErrCRDocumentRequired
	}
	if r.Branches < 1 {
		return ErrBranchRequired
	}
	at := dbTime(now)
	b.status = StatusPendingReview
	b.review.SubmittedAt = &at
	b.touch(at)
	return nil
}

// Approve activates a business under review. Only an active business can
// publish branches and take bookings.
func (b *Business) Approve(reviewer shared.UserID, now time.Time) error {
	if err := b.canReview(reviewer); err != nil {
		return err
	}
	at := dbTime(now)
	b.status = StatusActive
	b.review.ReviewedAt, b.review.ReviewedBy, b.review.RejectionReason = &at, reviewer, ""
	b.touch(at)
	b.events = append(b.events, BusinessApproved{Business: b.id, Owner: b.owner, At: at})
	return nil
}

// Reject sends a business back to its owner with a reason they can act on.
func (b *Business) Reject(reviewer shared.UserID, reason string, now time.Time) error {
	if err := b.canReview(reviewer); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrRejectionReasonRequired
	}
	if utf8.RuneCountInString(reason) > MaxRejectionReasonLen {
		return ErrTextTooLong
	}
	at := dbTime(now)
	b.status = StatusRejected
	b.review.ReviewedAt, b.review.ReviewedBy, b.review.RejectionReason = &at, reviewer, reason
	b.touch(at)
	return nil
}

// canReview says whether reviewer may decide now. Nobody reviews their own
// business, platform admin or not: the check exists to be independent.
func (b *Business) canReview(reviewer shared.UserID) error {
	if reviewer == b.owner {
		return ErrSelfReview
	}
	if b.status != StatusPendingReview {
		return ErrInvalidStateTransition
	}
	return nil
}

// Review returns the verification record.
func (b *Business) Review() Review { return b.review }

// touch records a saved change: a new version and time.
func (b *Business) touch(at time.Time) {
	b.version++
	b.updatedAt = at
}
