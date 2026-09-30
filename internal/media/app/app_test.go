package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

type memStorage struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (m *memStorage) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[key] = b
	return nil
}

func (m *memStorage) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memStorage) Remove(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}

type memObjects struct {
	mu      sync.Mutex
	objects map[shared.MediaID]*domain.Object
	failAdd bool
}

func (m *memObjects) Add(_ context.Context, o *domain.Object) error {
	if m.failAdd {
		return errors.New("database down")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[o.ID()] = o
	return nil
}

func (m *memObjects) ByID(_ context.Context, id shared.MediaID) (*domain.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.objects[id]; ok {
		return o, nil
	}
	return nil, domain.ErrNotFound
}

func (m *memObjects) Delete(_ context.Context, id shared.MediaID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, id)
	return nil
}

// plainSigner "signs" readably; the HMAC itself is tested in its adapter.
type plainSigner struct{}

func (plainSigner) Sign(id shared.MediaID, expires int64) string {
	return fmt.Sprintf("sig-%s-%d", id, expires)
}

func (s plainSigner) Verify(id shared.MediaID, expires int64, sig string) bool {
	return sig == s.Sign(id, expires)
}

type fixture struct {
	svc     *app.Service
	storage *memStorage
	objects *memObjects
	clock   *clock.Fake
}

func newFixture() *fixture {
	f := &fixture{
		storage: &memStorage{files: map[string][]byte{}},
		objects: &memObjects{objects: map[shared.MediaID]*domain.Object{}},
		clock:   clock.NewFake(t0),
	}
	f.svc = app.NewService(f.objects, f.storage, plainSigner{}, f.clock)
	return f
}

func pdf(size int) []byte {
	b := bytes.Repeat([]byte{'x'}, size)
	copy(b, "%PDF-1.7\n")
	return b
}

func (f *fixture) store(t *testing.T, body []byte) (*domain.Object, error) {
	t.Helper()
	return f.svc.Store(t.Context(), app.Upload{Purpose: "cr_document", UploadedBy: shared.NewID[shared.UserTag](), Body: bytes.NewReader(body)})
}

func TestStore(t *testing.T) {
	t.Parallel()
	f := newFixture()
	body := pdf(5000)
	o, err := f.store(t, body)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if o.ContentType() != domain.PDF || o.Size() != 5000 || !bytes.Equal(o.SHA256(), sum[:]) || !o.CreatedAt().Equal(t0) {
		t.Errorf("object = %+v", o)
	}
	if !bytes.Equal(f.storage.files[o.ID().String()], body) {
		t.Error("stored bytes differ")
	}
	if _, err := f.objects.ByID(t.Context(), o.ID()); err != nil {
		t.Error("no row saved")
	}
}

func TestStoreRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body []byte
		want error
	}{
		{"empty", nil, domain.ErrEmpty},
		{"html", []byte("<html><script>alert(1)</script>"), domain.ErrUnsupportedType},
		{"one byte too big", pdf(domain.MaxSize + 1), domain.ErrTooLarge},
		{"much too big", pdf(domain.MaxSize + 5<<20), domain.ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture()
			if _, err := f.store(t, tt.body); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if len(f.storage.files) != 0 || len(f.objects.objects) != 0 {
				t.Fatalf("a refused file left %d files and %d rows", len(f.storage.files), len(f.objects.objects))
			}
		})
	}

	// The limit is inclusive, and a tiny valid file is fine.
	f := newFixture()
	for _, size := range []int{domain.MaxSize, 5} {
		if _, err := f.store(t, pdf(size)); err != nil {
			t.Errorf("%d bytes: %v", size, err)
		}
	}
}

// If the row can't be saved the file is removed: no orphans from failures.
func TestStoreCleansUpWhenTheRowFails(t *testing.T) {
	t.Parallel()
	f := newFixture()
	f.objects.failAdd = true
	if _, err := f.store(t, pdf(100)); err == nil {
		t.Fatal("expected an error")
	}
	if len(f.storage.files) != 0 {
		t.Fatal("file left behind after the row failed")
	}
}

func TestDownloadLinks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture()
	o, err := f.store(t, pdf(100))
	if err != nil {
		t.Fatal(err)
	}

	path, expires := f.svc.DownloadPath(o.ID())
	if !expires.Equal(t0.Add(app.LinkTTL)) {
		t.Errorf("expires %v, want %v", expires, t0.Add(app.LinkTTL))
	}
	sig := path[strings.Index(path, "signature=")+len("signature="):]
	wantPrefix := "/v1/media/" + o.ID().String() + "?expires=" + strconv.FormatInt(expires.Unix(), 10) + "&signature="
	if !strings.HasPrefix(path, wantPrefix) {
		t.Fatalf("path = %s", path)
	}

	got, rc, err := f.svc.Open(ctx, o.ID(), expires.Unix(), sig)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if got.ID() != o.ID() || len(data) != 100 {
		t.Errorf("opened %v, %d bytes", got.ID(), len(data))
	}

	for name, tt := range map[string]struct {
		id      shared.MediaID
		expires int64
		sig     string
	}{
		"wrong signature": {o.ID(), expires.Unix(), "sig-forged"},
		"extended expiry": {o.ID(), expires.Unix() + 3600, sig},
		"another file":    {shared.NewID[shared.MediaTag](), expires.Unix(), sig},
	} {
		if _, _, err := f.svc.Open(ctx, tt.id, tt.expires, tt.sig); !errors.Is(err, domain.ErrLinkInvalid) {
			t.Errorf("%s: error = %v, want ErrLinkInvalid", name, err)
		}
	}

	// Still valid at the last second, not after.
	f.clock.Advance(app.LinkTTL)
	if _, rc, err := f.svc.Open(ctx, o.ID(), expires.Unix(), sig); err != nil {
		t.Errorf("at expiry: %v", err)
	} else {
		_ = rc.Close()
	}
	f.clock.Advance(time.Second)
	if _, _, err := f.svc.Open(ctx, o.ID(), expires.Unix(), sig); !errors.Is(err, domain.ErrLinkInvalid) {
		t.Errorf("after expiry: error = %v", err)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	f := newFixture()
	o, err := f.store(t, pdf(100))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(t.Context(), o.ID()); err != nil {
		t.Fatal(err)
	}
	if len(f.storage.files) != 0 || len(f.objects.objects) != 0 {
		t.Fatal("delete left something behind")
	}
}
