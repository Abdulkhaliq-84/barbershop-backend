package app_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// docStore is an in-memory domain.VerificationDocuments over the fixture's
// businesses.
type docStore struct {
	mu        sync.Mutex
	store     *store
	docs      []*domain.VerificationDocument
	failFirst bool // the next Attach fails, as if the database went away
}

func (d *docStore) Attach(_ context.Context, doc *domain.VerificationDocument, check func(*domain.Business, int) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failFirst {
		d.failFirst = false
		return errors.New("database down")
	}
	b, ok := d.store.businesses[doc.BusinessID()]
	if !ok {
		return domain.ErrNotFound
	}
	if err := check(b, d.count(doc.BusinessID())); err != nil {
		return err
	}
	d.docs = append(d.docs, doc)
	return nil
}

func (d *docStore) count(business shared.BusinessID) int {
	n := 0
	for _, doc := range d.docs {
		if doc.BusinessID() == business {
			n++
		}
	}
	return n
}

func (d *docStore) List(_ context.Context, business shared.BusinessID) ([]*domain.VerificationDocument, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*domain.VerificationDocument
	for _, doc := range d.docs {
		if doc.BusinessID() == business {
			out = append(out, doc)
		}
	}
	return out, nil
}

// fakeFiles accepts anything starting with %PDF and remembers what it holds.
type fakeFiles struct {
	mu    sync.Mutex
	files map[shared.MediaID][]byte
	saves int
}

func (f *fakeFiles) Save(_ context.Context, _ shared.UserID, body io.Reader) (app.StoredFile, error) {
	b, _ := io.ReadAll(body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		return app.StoredFile{}, domain.ErrUnsupportedFile
	}
	id := shared.NewID[shared.MediaTag]()
	f.files[id] = b
	return app.StoredFile{ID: id, ContentType: "application/pdf", Size: int64(len(b))}, nil
}

func (f *fakeFiles) Discard(_ context.Context, id shared.MediaID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, id)
	return nil
}

func (f *fakeFiles) DownloadURL(id shared.MediaID) (string, time.Time) {
	return "/v1/media/" + id.String() + "?signed", t0.Add(5 * time.Minute)
}

type docFixture struct {
	*fixture
	docs     *docStore
	files    *fakeFiles
	handlers *app.DocumentHandlers
	owner    shared.UserID
	business shared.BusinessID
}

func newDocFixture(t *testing.T) *docFixture {
	t.Helper()
	f := newFixture()
	owner := shared.NewID[shared.UserTag]()
	b, err := f.register.Handle(t.Context(), registerCmd(owner))
	if err != nil {
		t.Fatal(err)
	}
	docs := &docStore{store: f.store}
	files := &fakeFiles{files: map[shared.MediaID][]byte{}}
	return &docFixture{
		fixture: f, docs: docs, files: files, owner: owner, business: b.ID(),
		handlers: app.NewDocumentHandlers(f.store, docs, f.store, files, f.clock),
	}
}

func (f *docFixture) upload(actor shared.UserID, body string) (app.DocumentView, error) {
	return f.handlers.Upload(context.Background(), app.UploadDocument{
		Actor: actor, BusinessID: f.business, Kind: "cr_certificate", Body: strings.NewReader(body),
	})
}

func TestUploadDocument(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)
	v, err := f.upload(f.owner, "%PDF-1.7 certificate")
	if err != nil {
		t.Fatal(err)
	}
	d := v.Document
	if d.BusinessID() != f.business || d.Kind() != domain.DocumentCRCertificate || d.ContentType() != "application/pdf" ||
		d.Size() != 20 || d.UploadedBy() != f.owner || !d.UploadedAt().Equal(t0) ||
		v.DownloadURL != "/v1/media/"+d.FileID().String()+"?signed" {
		t.Errorf("view = %+v, doc = %+v", v, d)
	}
	list, err := f.handlers.List(t.Context(), f.owner, f.business)
	if err != nil || len(list) != 1 || list[0].Document.FileID() != d.FileID() {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestUploadDocumentRefuses(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)
	manager := shared.NewID[shared.UserTag]()
	f.store.addStaff(f.business, manager, domain.RoleManager, true)

	for name, tt := range map[string]struct {
		actor shared.UserID
		kind  string
		body  string
		want  error
	}{
		"stranger":     {shared.NewID[shared.UserTag](), "cr_certificate", "%PDF-", domain.ErrNotFound},
		"manager":      {manager, "cr_certificate", "%PDF-", domain.ErrForbidden},
		"unknown kind": {f.owner, "selfie", "%PDF-", domain.ErrUnknownDocumentKind},
		"not a pdf":    {f.owner, "cr_certificate", "<html>", domain.ErrUnsupportedFile},
	} {
		_, err := f.handlers.Upload(t.Context(), app.UploadDocument{Actor: tt.actor, BusinessID: f.business, Kind: tt.kind, Body: strings.NewReader(tt.body)})
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.want)
		}
	}
	// Refused before the upload (who, what kind) never stored anything.
	if f.files.saves != 1 { // only "not a pdf" reached the file store
		t.Errorf("file store called %d times, want 1", f.files.saves)
	}
	if _, err := f.handlers.List(t.Context(), manager, f.business); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("manager list: %v", err)
	}
}

func TestUploadDocumentLimitAndStatus(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)
	for i := range domain.MaxVerificationDocuments {
		if _, err := f.upload(f.owner, "%PDF-"); err != nil {
			t.Fatalf("upload %d: %v", i+1, err)
		}
	}
	if _, err := f.upload(f.owner, "%PDF-"); !errors.Is(err, domain.ErrDocumentLimitReached) {
		t.Fatalf("sixth upload: error = %v", err)
	}
	if f.files.saves != domain.MaxVerificationDocuments {
		t.Errorf("the refused sixth file was stored anyway (%d saves)", f.files.saves)
	}

	// Once submitted, documents are frozen for the reviewer.
	g := newDocFixture(t)
	b := g.store.businesses[g.business]
	g.store.businesses[g.business] = domain.RehydrateBusiness(b.ID(), b.OwnerID(), b.DisplayName(), b.LegalName(), b.CRNumber(), domain.StatusPendingReview, b.Version(), b.CreatedAt(), b.UpdatedAt(), domain.Review{})
	if _, err := g.upload(g.owner, "%PDF-"); !errors.Is(err, domain.ErrInvalidStateTransition) {
		t.Fatalf("submitted business: error = %v", err)
	}
}

// When attaching fails after the file was stored, the file is discarded.
func TestUploadDocumentDiscardsTheFileWhenAttachFails(t *testing.T) {
	t.Parallel()
	f := newDocFixture(t)
	f.docs.failFirst = true
	if _, err := f.upload(f.owner, "%PDF-"); err == nil {
		t.Fatal("expected an error")
	}
	if f.files.saves != 1 || len(f.files.files) != 0 {
		t.Fatalf("saves=%d, files left=%d; want the stored file discarded", f.files.saves, len(f.files.files))
	}
}
