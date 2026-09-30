package postgres_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func newDoc(business shared.BusinessID, at time.Time) *domain.VerificationDocument {
	return domain.NewVerificationDocument(shared.NewID[shared.MediaTag](), business, domain.DocumentCRCertificate,
		"application/pdf", 1234, shared.NewID[shared.UserTag](), at)
}

func allow(*domain.Business, int) error { return nil }

func TestDocumentStoreAttachAndList(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	docs := store.Documents()
	mine, theirs := registered(t, store, "1010000001"), registered(t, store, "1010000002")

	first, second := newDoc(mine, t0), newDoc(mine, t0.Add(time.Minute))
	for _, d := range []*domain.VerificationDocument{second, first} { // inserted out of order
		if err := docs.Attach(ctx, d, allow); err != nil {
			t.Fatal(err)
		}
	}
	if err := docs.Attach(ctx, newDoc(theirs, t0), allow); err != nil {
		t.Fatal(err)
	}

	got, err := docs.List(ctx, mine)
	if err != nil || len(got) != 2 || got[0].FileID() != first.FileID() || got[1].FileID() != second.FileID() {
		t.Fatalf("List = %v, %v; want mine only, oldest first", got, err)
	}
	g := got[0]
	if g.BusinessID() != mine || g.Kind() != domain.DocumentCRCertificate || g.ContentType() != "application/pdf" ||
		g.Size() != 1234 || g.UploadedBy() != first.UploadedBy() || !g.UploadedAt().Equal(t0) {
		t.Errorf("round trip = %+v, want %+v", g, first)
	}

	// The check sees the business and the current count, and can refuse.
	var seen int
	refuse := errors.New("no")
	err = docs.Attach(ctx, newDoc(mine, t0), func(b *domain.Business, n int) error {
		if b.ID() != mine {
			t.Errorf("check got business %s", b.ID())
		}
		seen = n
		return refuse
	})
	if !errors.Is(err, refuse) || seen != 2 {
		t.Fatalf("refused attach: err=%v seen=%d", err, seen)
	}
	if got, _ := docs.List(ctx, mine); len(got) != 2 {
		t.Fatal("a refused document was saved")
	}
	if err := docs.Attach(ctx, newDoc(shared.NewID[shared.BusinessTag](), t0), allow); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown business: %v", err)
	}
}

// Eight uploads finish at the same moment on a business with room for five.
// The row lock makes them count one at a time: exactly five get in.
func TestDocumentStoreParallelAttachesRespectTheLimit(t *testing.T) {
	t.Parallel()
	pool := migratedDB(t)
	store := postgres.NewStore(pool, discardEvents{})
	docs := store.Documents()
	business := registered(t, store, "1010123456")

	const callers = 8 // dbtest pools hold 8 connections
	var (
		wg              sync.WaitGroup
		start           = make(chan struct{})
		attached, limit atomic.Int32
	)
	for range callers {
		wg.Go(func() {
			<-start
			err := docs.Attach(t.Context(), newDoc(business, t0), func(b *domain.Business, n int) error {
				time.Sleep(20 * time.Millisecond) // widen the window an unlocked count would race through
				return b.CanAttachDocument(n)
			})
			switch {
			case err == nil:
				attached.Add(1)
			case errors.Is(err, domain.ErrDocumentLimitReached):
				limit.Add(1)
			default:
				t.Errorf("Attach: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()

	if attached.Load() != domain.MaxVerificationDocuments || limit.Load() != callers-domain.MaxVerificationDocuments {
		t.Fatalf("attached=%d refused=%d, want %d and %d", attached.Load(), limit.Load(), domain.MaxVerificationDocuments, callers-domain.MaxVerificationDocuments)
	}
	if got, _ := docs.List(t.Context(), business); len(got) != domain.MaxVerificationDocuments {
		t.Fatalf("%d documents stored, want %d", len(got), domain.MaxVerificationDocuments)
	}
}
