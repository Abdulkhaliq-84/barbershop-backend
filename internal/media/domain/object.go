// Package domain holds the media module's rules: what a stored file is,
// which kinds of file are accepted and how big they may be. Pure Go.
package domain

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Domain errors. The HTTP adapter maps each one to a stable API error code.
var (
	ErrNotFound        = errors.New("media: not found")
	ErrEmpty           = errors.New("media: the file is empty")
	ErrTooLarge        = errors.New("media: the file is too large")
	ErrUnsupportedType = errors.New("media: only PDF, JPEG and PNG files are accepted")
	ErrUnknownPurpose  = errors.New("media: unknown purpose")
	ErrLinkInvalid     = errors.New("media: download link invalid or expired")
)

// MaxSize is the largest file accepted: a phone photo or a scanned PDF of a
// CR certificate is well under it.
const MaxSize = 10 << 20 // 10 MiB

// Purpose says what a file is for, and so who may see it.
type Purpose string

// Purposes. CR documents are private: only the owner and platform admins see
// them, through signed links.
const (
	PurposeCRDocument Purpose = "cr_document"
)

// ParsePurpose reads a purpose.
func ParsePurpose(s string) (Purpose, error) {
	if Purpose(s) != PurposeCRDocument {
		return "", ErrUnknownPurpose
	}
	return Purpose(s), nil
}

// ContentType is one of the accepted file types.
type ContentType string

// Accepted types.
const (
	PDF  ContentType = "application/pdf"
	JPEG ContentType = "image/jpeg"
	PNG  ContentType = "image/png"
)

// SniffLen is how many leading bytes Sniff needs.
const SniffLen = 8

// Sniff decides the type from the file's first bytes ("magic numbers"),
// never from the name or the Content-Type the client sent: both are just
// claims. An HTML page renamed to scan.pdf is refused here.
func Sniff(head []byte) (ContentType, error) {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return PDF, nil
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return JPEG, nil
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return PNG, nil
	default:
		return "", ErrUnsupportedType
	}
}

// ParseContentType reads a stored content type.
func ParseContentType(s string) (ContentType, error) {
	switch ct := ContentType(s); ct {
	case PDF, JPEG, PNG:
		return ct, nil
	default:
		return "", ErrUnsupportedType
	}
}

// Object is one stored file. The bytes live in storage under the object's
// ID; this is what we know about them.
type Object struct {
	id          shared.MediaID
	purpose     Purpose
	contentType ContentType
	size        int64
	sha256      []byte
	createdBy   shared.UserID
	createdAt   time.Time
}

// NewObject describes a file that was just stored.
func NewObject(id shared.MediaID, purpose Purpose, ct ContentType, size int64, sha256 []byte, by shared.UserID, now time.Time) (*Object, error) {
	switch {
	case size <= 0:
		return nil, ErrEmpty
	case size > MaxSize:
		return nil, ErrTooLarge
	case len(sha256) != 32:
		return nil, errors.New("media: sha256 must be 32 bytes")
	}
	if _, err := ParseContentType(string(ct)); err != nil {
		return nil, err
	}
	if _, err := ParsePurpose(string(purpose)); err != nil {
		return nil, err
	}
	return &Object{
		id: id, purpose: purpose, contentType: ct, size: size, sha256: bytes.Clone(sha256),
		createdBy: by, createdAt: now.UTC().Truncate(time.Microsecond),
	}, nil
}

// RehydrateObject rebuilds an object loaded from storage.
func RehydrateObject(id shared.MediaID, purpose Purpose, ct ContentType, size int64, sha256 []byte, by shared.UserID, createdAt time.Time) *Object {
	return &Object{id: id, purpose: purpose, contentType: ct, size: size, sha256: sha256, createdBy: by, createdAt: createdAt}
}

// ID returns the object ID (also its storage key).
func (o *Object) ID() shared.MediaID { return o.id }

// Purpose returns what the file is for.
func (o *Object) Purpose() Purpose { return o.purpose }

// ContentType returns the sniffed type.
func (o *Object) ContentType() ContentType { return o.contentType }

// Size returns the size in bytes.
func (o *Object) Size() int64 { return o.size }

// SHA256 returns the file's checksum.
func (o *Object) SHA256() []byte { return bytes.Clone(o.sha256) }

// CreatedBy returns the user who uploaded it.
func (o *Object) CreatedBy() shared.UserID { return o.createdBy }

// CreatedAt returns when it was uploaded.
func (o *Object) CreatedAt() time.Time { return o.createdAt }

// Objects stores what we know about files.
type Objects interface {
	Add(ctx context.Context, o *Object) error
	// ByID returns the object, or ErrNotFound.
	ByID(ctx context.Context, id shared.MediaID) (*Object, error)
	// Delete removes the row; deleting a missing object is not an error.
	Delete(ctx context.Context, id shared.MediaID) error
}
