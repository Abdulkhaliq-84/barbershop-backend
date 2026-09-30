// Package disk stores media files on the local file system — the development
// and single-server storage. An S3-compatible adapter will implement the same
// app.Storage port later.
package disk

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
)

// Storage keeps each file at <dir>/<first two characters>/<key>. All access
// goes through os.Root, which refuses any path that would leave dir — even
// via ".." or a symlink — so a bug that passed a bad key could not read or
// overwrite files elsewhere on the server.
type Storage struct {
	root *os.Root
}

// New opens (creating if needed) the storage directory. Only the server's
// user can read it.
func New(dir string) (*Storage, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("media dir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("media dir: %w", err)
	}
	return &Storage{root: root}, nil
}

// Close releases the directory handle.
func (s *Storage) Close() error { return s.root.Close() }

// name validates key (it must be a canonical UUID) and returns its path
// inside the root.
func name(key string) (string, error) {
	u, err := uuid.Parse(key)
	if err != nil || u.String() != key {
		return "", fmt.Errorf("media: invalid storage key")
	}
	return filepath.Join(key[:2], key), nil
}

// Put writes r under key. It writes to a temporary file and renames it into
// place, so a failed or cancelled upload never leaves a half file under the
// real name.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader) (err error) {
	final, err := name(key)
	if err != nil {
		return err
	}
	if err := s.root.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return fmt.Errorf("media: mkdir: %w", err)
	}
	tmp := final + ".tmp-" + rand.Text()
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("media: create: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = s.root.Remove(tmp)
		}
	}()
	if _, err = io.Copy(f, ctxReader{ctx, r}); err != nil {
		return fmt.Errorf("media: write: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("media: sync: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("media: close: %w", err)
	}
	if err = s.root.Rename(tmp, final); err != nil {
		return fmt.Errorf("media: rename: %w", err)
	}
	return nil
}

// Open returns the file stored under key, or domain.ErrNotFound.
func (s *Storage) Open(_ context.Context, key string) (io.ReadCloser, error) {
	n, err := name(key)
	if err != nil {
		return nil, err
	}
	f, err := s.root.Open(n)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("media: open: %w", err)
	}
	return f, nil
}

// Remove deletes the file under key; a missing file is not an error.
func (s *Storage) Remove(_ context.Context, key string) error {
	n, err := name(key)
	if err != nil {
		return err
	}
	if err := s.root.Remove(n); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("media: remove: %w", err)
	}
	return nil
}

// ctxReader stops a long copy when the request is cancelled (the client hung
// up), instead of writing the rest of an upload nobody is waiting for.
type ctxReader struct {
	ctx context.Context // scoped to one Put call, never stored
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
