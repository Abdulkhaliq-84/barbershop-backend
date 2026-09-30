// Package app holds the media use cases: store a file, hand out a signed
// download link, and open a file for a valid link. It talks to storage and
// signing through small interfaces (ports).
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Storage keeps file bytes (local disk now, S3-compatible later). Keys are
// object IDs, never names a user chose.
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Remove(ctx context.Context, key string) error
}

// Signer signs and checks download links.
type Signer interface {
	Sign(id shared.MediaID, expires int64) string
	Verify(id shared.MediaID, expires int64, signature string) bool
}

// LinkTTL is how long a download link works: long enough to open a
// document in the app, short enough that a leaked link soon stops working.
const LinkTTL = 5 * time.Minute

// Upload is a file to store.
type Upload struct {
	Purpose    string
	UploadedBy shared.UserID
	Body       io.Reader
}

// Service is the media use cases.
type Service struct {
	objects domain.Objects
	storage Storage
	signer  Signer
	clock   clock.Clock
	logger  *slog.Logger
}

// NewService wires the use cases.
func NewService(objects domain.Objects, storage Storage, signer Signer, clk clock.Clock, logger *slog.Logger) *Service {
	return &Service{objects: objects, storage: storage, signer: signer, clock: clk, logger: logger}
}

// Store checks and saves a file. The bytes are streamed to storage — never
// held in memory whole — while being counted and hashed; the type comes from
// the first bytes, not from anything the client claimed.
//
// Storage and the database can't share a transaction, so the order matters:
// the file is written first and the row second, and a failed row removes the
// file. (A crash in between leaves an unreferenced file, never a row pointing
// at nothing.)
func (s *Service) Store(ctx context.Context, in Upload) (*domain.Object, error) {
	purpose, err := domain.ParsePurpose(in.Purpose)
	if err != nil {
		return nil, err
	}
	head := make([]byte, domain.SniffLen)
	n, err := io.ReadFull(in.Body, head)
	switch {
	case n == 0 && (err == nil || errors.Is(err, io.EOF)):
		return nil, domain.ErrEmpty
	case err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF):
		return nil, fmt.Errorf("read upload: %w", err)
	}
	head = head[:n]
	ct, err := domain.Sniff(head)
	if err != nil {
		return nil, err
	}

	id := shared.NewID[shared.MediaTag]()
	counted := &counter{h: sha256.New()}
	// Read one byte past the limit, so "exactly MaxSize" and "too big" differ.
	body := io.TeeReader(io.LimitReader(io.MultiReader(bytes.NewReader(head), in.Body), domain.MaxSize+1), counted)
	if err := s.storage.Put(ctx, id.String(), body); err != nil {
		return nil, fmt.Errorf("store file: %w", err)
	}
	if counted.n > domain.MaxSize {
		s.discard(ctx, id)
		return nil, domain.ErrTooLarge
	}
	obj, err := domain.NewObject(id, purpose, ct, counted.n, counted.h.Sum(nil), in.UploadedBy, s.clock.Now())
	if err != nil {
		s.discard(ctx, id)
		return nil, err
	}
	if err := s.objects.Add(ctx, obj); err != nil {
		s.discard(ctx, id)
		return nil, fmt.Errorf("save object: %w", err)
	}
	return obj, nil
}

// Delete removes a file and its row. The row goes first: once it's gone no
// link can open the file, even if removing the bytes fails.
func (s *Service) Delete(ctx context.Context, id shared.MediaID) error {
	if err := s.objects.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	if err := s.storage.Remove(ctx, id.String()); err != nil {
		return fmt.Errorf("remove file: %w", err)
	}
	return nil
}

// discard cleans up after a refused or failed upload. It must outlive the
// request's cancellation, which may be the reason we are cleaning up, but
// keeps its values (the request ID in the log line).
func (s *Service) discard(ctx context.Context, id shared.MediaID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := s.storage.Remove(ctx, id.String()); err != nil {
		// Harmless (nothing links to the file) but worth a sweep.
		s.logger.WarnContext(ctx, "media: refused upload left on disk", slog.String("media_id", id.String()), slog.String("error_type", fmt.Sprintf("%T", err)))
	}
}

// cleanupTimeout bounds cleanup after a failed request.
const cleanupTimeout = 5 * time.Second

// DownloadPath returns a signed, relative download link and when it stops
// working. Callers decide who may receive it; the link itself is the key.
func (s *Service) DownloadPath(id shared.MediaID) (string, time.Time) {
	expires := s.clock.Now().Add(LinkTTL).Truncate(time.Second)
	sig := s.signer.Sign(id, expires.Unix())
	return fmt.Sprintf("/v1/media/%s?expires=%d&signature=%s", id, expires.Unix(), sig), expires
}

// Open checks a download link and opens the file. The signature is checked
// before anything is loaded, so guessed links cost no database work.
func (s *Service) Open(ctx context.Context, id shared.MediaID, expires int64, signature string) (*domain.Object, io.ReadCloser, error) {
	if s.clock.Now().Unix() > expires || !s.signer.Verify(id, expires, signature) {
		return nil, nil, domain.ErrLinkInvalid
	}
	obj, err := s.objects.ByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.storage.Open(ctx, id.String())
	if err != nil {
		return nil, nil, fmt.Errorf("open file: %w", err)
	}
	return obj, rc, nil
}

// counter counts and hashes what passes through it.
type counter struct {
	n int64
	h hash.Hash
}

func (c *counter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return c.h.Write(p)
}
