package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// StoredFile is a file the file store accepted.
type StoredFile struct {
	ID          shared.MediaID
	ContentType string
	Size        int64
}

// DocumentFiles is what this module needs from file storage. It is declared
// here, in business's own words, and implemented by an adapter over the media
// module (adapters/acl): business never sees media's types, and media can
// change without touching business's use cases.
type DocumentFiles interface {
	// Save stores a private document. It returns ErrEmptyFile,
	// ErrFileTooLarge or ErrUnsupportedFile for files it refuses.
	Save(ctx context.Context, by shared.UserID, body io.Reader) (StoredFile, error)
	// Discard deletes a stored file that ended up unused.
	Discard(ctx context.Context, id shared.MediaID) error
	// DownloadURL returns a short-lived signed link to the file.
	DownloadURL(id shared.MediaID) (url string, expires time.Time)
}

// UploadDocument is the command to add a verification document.
type UploadDocument struct {
	Actor      shared.UserID
	BusinessID shared.BusinessID
	Kind       string
	Body       io.Reader
}

// DocumentView is a document with a fresh download link.
type DocumentView struct {
	Document    *domain.VerificationDocument
	DownloadURL string
	Expires     time.Time
}

// DocumentHandlers are the verification-document use cases (owner only).
type DocumentHandlers struct {
	businesses domain.Businesses
	documents  domain.VerificationDocuments
	staff      domain.Staff
	files      DocumentFiles
	clock      clock.Clock
	logger     *slog.Logger
}

// NewDocumentHandlers wires the use cases.
func NewDocumentHandlers(businesses domain.Businesses, documents domain.VerificationDocuments, staff domain.Staff, files DocumentFiles, clk clock.Clock, logger *slog.Logger) *DocumentHandlers {
	return &DocumentHandlers{businesses: businesses, documents: documents, staff: staff, files: files, clock: clk, logger: logger}
}

// Upload stores the file, then attaches it to the business.
//
// The rules are checked twice: once before the upload, so a refused request
// doesn't store 10 MiB for nothing, and again under a lock when attaching,
// because the business may have changed while the file was uploading. If
// attaching fails the stored file is discarded.
func (h *DocumentHandlers) Upload(ctx context.Context, cmd UploadDocument) (DocumentView, error) {
	if _, err := authorize(ctx, h.staff, cmd.Actor, cmd.BusinessID, domain.RoleOwner); err != nil {
		return DocumentView{}, err
	}
	kind, err := domain.ParseDocumentKind(cmd.Kind)
	if err != nil {
		return DocumentView{}, err
	}
	b, err := h.businesses.ByID(ctx, cmd.BusinessID)
	if err != nil {
		return DocumentView{}, fmt.Errorf("upload document: %w", err)
	}
	existing, err := h.documents.List(ctx, cmd.BusinessID)
	if err != nil {
		return DocumentView{}, fmt.Errorf("upload document: %w", err)
	}
	if err := b.CanAttachDocument(len(existing)); err != nil {
		return DocumentView{}, err
	}

	file, err := h.files.Save(ctx, cmd.Actor, cmd.Body)
	if err != nil {
		return DocumentView{}, fmt.Errorf("upload document: %w", err)
	}
	doc := domain.NewVerificationDocument(file.ID, cmd.BusinessID, kind, file.ContentType, file.Size, cmd.Actor, h.clock.Now())
	err = h.documents.Attach(ctx, doc, func(b *domain.Business, existing int) error {
		return b.CanAttachDocument(existing)
	})
	if err != nil {
		h.discard(ctx, file.ID)
		return DocumentView{}, fmt.Errorf("upload document: %w", err)
	}
	return h.view(doc), nil
}

// List returns the business's documents with fresh download links. Only the
// owner gets them: the links open the files for anyone who holds them.
func (h *DocumentHandlers) List(ctx context.Context, actor shared.UserID, business shared.BusinessID) ([]DocumentView, error) {
	if _, err := authorize(ctx, h.staff, actor, business, domain.RoleOwner); err != nil {
		return nil, err
	}
	docs, err := h.documents.List(ctx, business)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	views := make([]DocumentView, 0, len(docs))
	for _, d := range docs {
		views = append(views, h.view(d))
	}
	return views, nil
}

func (h *DocumentHandlers) view(d *domain.VerificationDocument) DocumentView {
	url, expires := h.files.DownloadURL(d.FileID())
	return DocumentView{Document: d, DownloadURL: url, Expires: expires}
}

// discard removes an unused file, even if the request was cancelled (it
// keeps the request's values, for the log line).
func (h *DocumentHandlers) discard(ctx context.Context, id shared.MediaID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := h.files.Discard(ctx, id); err != nil {
		// Harmless — nothing links to it — but worth a sweep.
		h.logger.WarnContext(ctx, "business: unattached document left in media", slog.String("media_id", id.String()), slog.String("error_type", fmt.Sprintf("%T", err)))
	}
}
