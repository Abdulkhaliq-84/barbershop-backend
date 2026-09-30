// Package media stores uploaded files — CR documents now; logos, branch
// photos and avatars later — and serves private ones through short-lived
// signed links (docs/architecture/domain-model.md §3.9, ADR-0016).
//
// This root package is the module's public face. Other modules use only what
// is exported here; domain, app and adapters are private (lint rules).
package media

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/disk"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/signer"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Errors other modules may see from Store. They are the domain's own
// sentinels, re-exported so callers never import media/domain.
var (
	ErrEmpty           = domain.ErrEmpty
	ErrTooLarge        = domain.ErrTooLarge
	ErrUnsupportedType = domain.ErrUnsupportedType
)

// MaxSize is the largest file Store accepts.
const MaxSize = domain.MaxSize

// Purposes a caller may store files for.
const (
	PurposeCRDocument = string(domain.PurposeCRDocument)
)

// Deps are what the module needs from the outside world.
type Deps struct {
	Pool   *pgxpool.Pool
	Clock  clock.Clock
	Logger *slog.Logger
	Dir    string // where files are kept (local disk storage)
	Secret []byte // signs download links, ≥ 32 bytes
}

// Module is the wired media module.
type Module struct {
	svc  *app.Service
	http *httpapi.Handlers
	disk *disk.Storage
}

// New wires storage, signing, repositories and HTTP handlers.
func New(d Deps) (*Module, error) {
	sig, err := signer.New(d.Secret)
	if err != nil {
		return nil, err
	}
	store, err := disk.New(d.Dir)
	if err != nil {
		return nil, err
	}
	svc := app.NewService(postgres.NewObjects(d.Pool), store, sig, d.Clock)
	return &Module{svc: svc, http: httpapi.NewHandlers(svc, d.Logger), disk: store}, nil
}

// HTTP returns the handlers for the media API operations.
func (m *Module) HTTP() *httpapi.Handlers { return m.http }

// Close releases the storage directory.
func (m *Module) Close() error { return m.disk.Close() }

// Stored describes a file after Store.
type Stored struct {
	ID          shared.MediaID
	ContentType string // application/pdf, image/jpeg or image/png
	Size        int64
	CreatedAt   time.Time
}

// Store checks and saves a file for purpose, uploaded by user. It returns
// ErrEmpty, ErrTooLarge or ErrUnsupportedType for a file it refuses.
func (m *Module) Store(ctx context.Context, purpose string, by shared.UserID, body io.Reader) (Stored, error) {
	o, err := m.svc.Store(ctx, app.Upload{Purpose: purpose, UploadedBy: by, Body: body})
	if err != nil {
		return Stored{}, err
	}
	return Stored{ID: o.ID(), ContentType: string(o.ContentType()), Size: o.Size(), CreatedAt: o.CreatedAt()}, nil
}

// Delete removes a file (for callers undoing an upload they couldn't use).
func (m *Module) Delete(ctx context.Context, id shared.MediaID) error { return m.svc.Delete(ctx, id) }

// DownloadURL returns a relative, signed download link and when it expires.
// Whoever holds the link can download the file until then, so callers must
// hand it only to people allowed to see the file.
func (m *Module) DownloadURL(id shared.MediaID) (string, time.Time) { return m.svc.DownloadPath(id) }
