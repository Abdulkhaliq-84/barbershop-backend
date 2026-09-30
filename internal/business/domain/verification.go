package domain

import (
	"context"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// DocumentKind says what a verification document is.
type DocumentKind string

// Document kinds.
const (
	DocumentCRCertificate DocumentKind = "cr_certificate"
)

// ParseDocumentKind reads a document kind.
func ParseDocumentKind(s string) (DocumentKind, error) {
	if DocumentKind(s) != DocumentCRCertificate {
		return "", ErrUnknownDocumentKind
	}
	return DocumentKind(s), nil
}

// MaxVerificationDocuments caps the documents one business keeps: enough for
// a CR certificate and a few re-scans, not a free file host.
const MaxVerificationDocuments = 5

// VerificationDocument is a file an owner submitted so the platform can
// check the business, e.g. the CR certificate. The file itself belongs to
// the media module; this records whose it is and what it is.
type VerificationDocument struct {
	file        shared.MediaID
	business    shared.BusinessID
	kind        DocumentKind
	contentType string
	size        int64
	uploadedBy  shared.UserID
	uploadedAt  time.Time
}

// NewVerificationDocument describes a stored file as a business's document.
func NewVerificationDocument(file shared.MediaID, business shared.BusinessID, kind DocumentKind, contentType string, size int64, by shared.UserID, at time.Time) *VerificationDocument {
	return &VerificationDocument{file: file, business: business, kind: kind, contentType: contentType, size: size, uploadedBy: by, uploadedAt: dbTime(at)}
}

// FileID returns the media file's ID.
func (d *VerificationDocument) FileID() shared.MediaID { return d.file }

// BusinessID returns the business it belongs to.
func (d *VerificationDocument) BusinessID() shared.BusinessID { return d.business }

// Kind returns what the document is.
func (d *VerificationDocument) Kind() DocumentKind { return d.kind }

// ContentType returns the file type (application/pdf, image/jpeg, image/png).
func (d *VerificationDocument) ContentType() string { return d.contentType }

// Size returns the file size in bytes.
func (d *VerificationDocument) Size() int64 { return d.size }

// UploadedBy returns who uploaded it.
func (d *VerificationDocument) UploadedBy() shared.UserID { return d.uploadedBy }

// UploadedAt returns when.
func (d *VerificationDocument) UploadedAt() time.Time { return d.uploadedAt }

// CanAttachDocument says whether the business may take another document,
// given how many it already has. Documents change only while the owner is
// preparing the business (draft) or fixing it after a rejection: once it is
// submitted, the reviewer must see exactly what was submitted.
func (b *Business) CanAttachDocument(existing int) error {
	if b.status != StatusDraft && b.status != StatusRejected {
		return ErrInvalidStateTransition
	}
	if existing >= MaxVerificationDocuments {
		return ErrDocumentLimitReached
	}
	return nil
}

// VerificationDocuments stores verification documents.
type VerificationDocuments interface {
	// Attach saves doc if check allows it. The business row is locked and the
	// documents counted in the same transaction, so two uploads at once can't
	// both squeeze past the limit or a status change. ErrNotFound when the
	// business doesn't exist.
	Attach(ctx context.Context, doc *VerificationDocument, check func(b *Business, existing int) error) error
	// List returns the business's documents, oldest first.
	List(ctx context.Context, business shared.BusinessID) ([]*VerificationDocument, error)
}
