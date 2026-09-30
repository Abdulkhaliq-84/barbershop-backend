// Package acl adapts other modules' public APIs to this module's ports — an
// anti-corruption layer: other modules' types and errors stop here, so the
// business use cases speak only their own language.
package acl

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ app.DocumentFiles = (*MediaFiles)(nil)

// MediaFiles stores verification documents in the media module.
type MediaFiles struct {
	media *media.Module
}

// NewMediaFiles wraps the media module.
func NewMediaFiles(m *media.Module) *MediaFiles { return &MediaFiles{media: m} }

// Save stores a private CR document, translating media's refusals into
// business's own errors.
func (f *MediaFiles) Save(ctx context.Context, by shared.UserID, body io.Reader) (app.StoredFile, error) {
	s, err := f.media.Store(ctx, media.PurposeCRDocument, by, body)
	switch {
	case errors.Is(err, media.ErrEmpty):
		return app.StoredFile{}, domain.ErrEmptyFile
	case errors.Is(err, media.ErrTooLarge):
		return app.StoredFile{}, domain.ErrFileTooLarge
	case errors.Is(err, media.ErrUnsupportedType):
		return app.StoredFile{}, domain.ErrUnsupportedFile
	case err != nil:
		return app.StoredFile{}, err
	}
	return app.StoredFile{ID: s.ID, ContentType: s.ContentType, Size: s.Size}, nil
}

// Discard deletes a file that was never attached.
func (f *MediaFiles) Discard(ctx context.Context, id shared.MediaID) error {
	return f.media.Delete(ctx, id)
}

// DownloadURL returns a signed link.
func (f *MediaFiles) DownloadURL(id shared.MediaID) (string, time.Time) {
	return f.media.DownloadURL(id)
}
