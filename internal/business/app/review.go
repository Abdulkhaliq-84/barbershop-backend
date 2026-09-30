package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// QueuePosition is where a page of the review queue ends: the next page
// starts after it. (Keyset pagination: "after this row", not "skip N rows",
// so pages stay stable while businesses are submitted and reviewed.)
type QueuePosition struct {
	SubmittedAt time.Time
	ID          shared.BusinessID
}

// ReviewQueue reads the admin review queue.
type ReviewQueue interface {
	// ReviewPage returns up to limit businesses in status, oldest submission
	// first, after the given position (nil for the first page).
	ReviewPage(ctx context.Context, status domain.Status, after *QueuePosition, limit int) ([]*domain.Business, error)
}

// Admin is the caller of a platform-admin operation. The role comes from the
// access token (ADR-0014): revoking it takes effect within 15 minutes.
type Admin struct {
	ID              shared.UserID
	IsPlatformAdmin bool
}

func (a Admin) require() error {
	if !a.IsPlatformAdmin || a.ID.IsZero() {
		return domain.ErrNotPlatformAdmin
	}
	return nil
}

// SubmitHandler lets the owner send a business for review.
type SubmitHandler struct {
	businesses domain.Businesses
	staff      domain.Staff
	clock      clock.Clock
}

// NewSubmitHandler wires the handler.
func NewSubmitHandler(businesses domain.Businesses, staff domain.Staff, clk clock.Clock) *SubmitHandler {
	return &SubmitHandler{businesses: businesses, staff: staff, clock: clk}
}

// Handle submits the business. Readiness (a CR document, a branch) is counted
// under the business row lock, and the CR number is claimed platform-wide in
// the same transaction.
func (h *SubmitHandler) Handle(ctx context.Context, actor shared.UserID, business shared.BusinessID, expectedVersion int) (*domain.Business, error) {
	if _, err := authorize(ctx, h.staff, actor, business, domain.RoleOwner); err != nil {
		return nil, err
	}
	var submitted *domain.Business
	err := h.businesses.UpdateWithReadiness(ctx, business, expectedVersion, func(b *domain.Business, r domain.Readiness) error {
		if err := b.Submit(r, h.clock.Now()); err != nil {
			return err
		}
		submitted = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("submit business: %w", err)
	}
	return submitted, nil
}

// AdminView is everything a reviewer needs to decide.
type AdminView struct {
	Business  *domain.Business
	Documents []DocumentView // with fresh signed links
	Branches  []*domain.Branch
}

// ReviewHandlers are the platform admin's review use cases.
type ReviewHandlers struct {
	businesses domain.Businesses
	documents  domain.VerificationDocuments
	branches   domain.Branches
	queue      ReviewQueue
	files      DocumentFiles
	clock      clock.Clock
}

// NewReviewHandlers wires the use cases.
func NewReviewHandlers(businesses domain.Businesses, documents domain.VerificationDocuments, branches domain.Branches, queue ReviewQueue, files DocumentFiles, clk clock.Clock) *ReviewHandlers {
	return &ReviewHandlers{businesses: businesses, documents: documents, branches: branches, queue: queue, files: files, clock: clk}
}

// Queue returns one page of businesses in status (never drafts: those are
// the owner's business until submitted) and the position to continue from,
// or nil after the last page.
func (h *ReviewHandlers) Queue(ctx context.Context, admin Admin, status domain.Status, after *QueuePosition, limit int) ([]*domain.Business, *QueuePosition, error) {
	if err := admin.require(); err != nil {
		return nil, nil, err
	}
	if status == domain.StatusDraft {
		return nil, nil, domain.ErrUnknownStatus
	}
	// Ask for one extra row: if it comes back, there is a next page.
	page, err := h.queue.ReviewPage(ctx, status, after, limit+1)
	if err != nil {
		return nil, nil, fmt.Errorf("review queue: %w", err)
	}
	if len(page) <= limit {
		return page, nil, nil
	}
	page = page[:limit]
	last := page[len(page)-1]
	return page, &QueuePosition{SubmittedAt: *last.Review().SubmittedAt, ID: last.ID()}, nil
}

// Detail returns a submitted business with its documents and branches.
// Drafts stay private to their owner: an admin gets ErrNotFound for them.
func (h *ReviewHandlers) Detail(ctx context.Context, admin Admin, id shared.BusinessID) (AdminView, error) {
	if err := admin.require(); err != nil {
		return AdminView{}, err
	}
	b, err := h.businesses.ByID(ctx, id)
	if err != nil {
		return AdminView{}, fmt.Errorf("admin detail: %w", err)
	}
	if b.Status() == domain.StatusDraft {
		return AdminView{}, domain.ErrNotFound
	}
	docs, err := h.documents.List(ctx, id)
	if err != nil {
		return AdminView{}, fmt.Errorf("admin detail: %w", err)
	}
	branches, err := h.branches.List(ctx, id)
	if err != nil {
		return AdminView{}, fmt.Errorf("admin detail: %w", err)
	}
	view := AdminView{Business: b, Branches: branches, Documents: make([]DocumentView, 0, len(docs))}
	for _, d := range docs {
		url, expires := h.files.DownloadURL(d.FileID())
		view.Documents = append(view.Documents, DocumentView{Document: d, DownloadURL: url, Expires: expires})
	}
	return view, nil
}

// Approve activates a business under review.
func (h *ReviewHandlers) Approve(ctx context.Context, admin Admin, id shared.BusinessID, expectedVersion int) (*domain.Business, error) {
	return h.decide(ctx, admin, id, expectedVersion, func(b *domain.Business) error {
		return b.Approve(admin.ID, h.clock.Now())
	})
}

// Reject sends a business under review back to its owner with a reason.
func (h *ReviewHandlers) Reject(ctx context.Context, admin Admin, id shared.BusinessID, expectedVersion int, reason string) (*domain.Business, error) {
	return h.decide(ctx, admin, id, expectedVersion, func(b *domain.Business) error {
		return b.Reject(admin.ID, reason, h.clock.Now())
	})
}

func (h *ReviewHandlers) decide(ctx context.Context, admin Admin, id shared.BusinessID, expectedVersion int, fn func(*domain.Business) error) (*domain.Business, error) {
	if err := admin.require(); err != nil {
		return nil, err
	}
	var decided *domain.Business
	err := h.businesses.Update(ctx, id, expectedVersion, func(b *domain.Business) error {
		if err := fn(b); err != nil {
			return err
		}
		decided = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("review decision: %w", err)
	}
	return decided, nil
}
