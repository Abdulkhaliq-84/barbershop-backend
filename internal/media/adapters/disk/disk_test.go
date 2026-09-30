package disk_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/disk"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
)

func newStorage(t *testing.T) (*disk.Storage, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "media")
	s, err := disk.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestPutOpenRemove(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, dir := newStorage(t)
	key := uuid.Must(uuid.NewV7()).String()
	data := bytes.Repeat([]byte("%PDF-"), 1000)

	if err := s.Put(ctx, key, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, key[:2], key))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, want 0600 (only the server reads uploads)", info.Mode().Perm())
	}
	rc, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("read back different bytes")
	}

	if err := s.Remove(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("open after remove: %v", err)
	}
	if err := s.Remove(ctx, key); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

// Keys are object IDs. Anything else — above all a path — is refused before
// the file system is touched.
func TestRejectsKeysThatAreNotIDs(t *testing.T) {
	t.Parallel()
	s, _ := newStorage(t)
	for _, key := range []string{"", "../../etc/passwd", "a/b", "01A0E410-90F4-75DA-8156-6B695634873A", strings.Repeat("a", 36)} {
		if err := s.Put(t.Context(), key, strings.NewReader("x")); err == nil {
			t.Errorf("Put(%q) accepted", key)
		}
		if _, err := s.Open(t.Context(), key); err == nil || errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Open(%q) = %v, want a key error", key, err)
		}
	}
}

// A failed or cancelled upload leaves nothing behind, not even a temp file.
func TestFailedPutLeavesNothing(t *testing.T) {
	t.Parallel()
	s, dir := newStorage(t)
	key := uuid.Must(uuid.NewV7()).String()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Put(ctx, key, strings.NewReader("%PDF-1.7")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled put: %v", err)
	}
	if err := s.Put(t.Context(), key, io.MultiReader(strings.NewReader("%PDF-"), errReader{})); err == nil {
		t.Fatal("a failing reader was stored")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, key[:2]))
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
