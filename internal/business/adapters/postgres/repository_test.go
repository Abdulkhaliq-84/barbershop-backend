package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// discardEvents is an outbox that keeps nothing: most store tests don't look
// at events (events_test.go uses the real one).
type discardEvents struct{}

func (discardEvents) PublishTx(context.Context, pgx.Tx, ...outbox.Event) error { return nil }

// allowAll is a plan without limits.
func allowAll(int) error { return nil }

func migratedDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// newBusiness builds a draft business owned by owner, with CR number cr.
func newBusiness(t *testing.T, owner shared.UserID, cr string) (*domain.Business, *domain.StaffMember) {
	t.Helper()
	name, _ := shared.NewLocalizedText("صالون الأناقة", "Elegance")
	number, err := domain.NewCRNumber(cr)
	if err != nil {
		t.Fatal(err)
	}
	b, m, err := domain.RegisterBusiness(shared.NewID[shared.BusinessTag](), shared.NewID[shared.StaffTag](), owner,
		domain.Registration{DisplayName: name, LegalName: "مؤسسة الأناقة", CRNumber: number}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return b, m
}

func TestStoreRegisterAndLoad(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	owner := shared.NewID[shared.UserTag]()
	b, m := newBusiness(t, owner, "1010123456")

	if err := store.Register(ctx, b, m); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := store.ByID(ctx, b.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID() != owner || got.DisplayName() != b.DisplayName() || got.LegalName() != b.LegalName() ||
		got.CRNumber() != b.CRNumber() || got.Status() != domain.StatusDraft || got.Version() != 1 ||
		!got.CreatedAt().Equal(t0) || !got.UpdatedAt().Equal(t0) {
		t.Errorf("ByID = %+v, want %+v", got, b)
	}

	member, err := store.Membership(ctx, b.ID(), owner)
	if err != nil || member.ID() != m.ID() || member.Role() != domain.RoleOwner || !member.IsActive() || member.UserID() != owner {
		t.Fatalf("Membership = %+v, %v", member, err)
	}

	views, err := store.ForUser(ctx, owner)
	if err != nil || len(views) != 1 {
		t.Fatalf("ForUser = %+v, %v", views, err)
	}
	if v := views[0]; v.BusinessID != b.ID() || v.StaffID != m.ID() || v.Role != domain.RoleOwner || v.Status != domain.StatusDraft || v.DisplayName.En() != "Elegance" {
		t.Errorf("view = %+v", v)
	}

	// Not found, and nothing leaks across users or businesses.
	if _, err := store.ByID(ctx, shared.NewID[shared.BusinessTag]()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown business: %v", err)
	}
	if _, err := store.Membership(ctx, b.ID(), shared.NewID[shared.UserTag]()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("stranger's membership: %v", err)
	}
	if views, err := store.ForUser(ctx, shared.NewID[shared.UserTag]()); err != nil || len(views) != 0 {
		t.Errorf("stranger's memberships = %+v, %v", views, err)
	}
}

func TestStoreForUserListsNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	owner := shared.NewID[shared.UserTag]()
	older, m1 := newBusiness(t, owner, "1010000001")
	newer, m2 := newBusiness(t, owner, "1010000002")
	for _, r := range []struct {
		b *domain.Business
		m *domain.StaffMember
	}{{older, m1}, {newer, m2}} {
		if err := store.Register(ctx, r.b, r.m); err != nil {
			t.Fatal(err)
		}
	}
	views, err := store.ForUser(ctx, owner)
	if err != nil || len(views) != 2 || views[0].BusinessID != newer.ID() || views[1].BusinessID != older.ID() {
		t.Fatalf("ForUser = %+v, %v", views, err)
	}
}

// Retries of the same registration race each other (a double tap, a
// timeout followed by a retry). The unique index lets exactly one through;
// a read-then-insert check in Go could let several pass.
func TestStoreParallelRegistrationsOfTheSameNumber(t *testing.T) {
	t.Parallel()
	pool := migratedDB(t)
	store := postgres.NewStore(pool, discardEvents{})
	owner := shared.NewID[shared.UserTag]()

	const callers = 8
	var (
		wg                     sync.WaitGroup
		start                  = make(chan struct{})
		registered, duplicates atomic.Int32
	)
	for range callers {
		b, m := newBusiness(t, owner, "1010123456")
		wg.Go(func() {
			<-start
			switch err := store.Register(t.Context(), b, m); {
			case err == nil:
				registered.Add(1)
			case errors.Is(err, domain.ErrAlreadyRegistered):
				duplicates.Add(1)
			default:
				t.Errorf("Register: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if registered.Load() != 1 || duplicates.Load() != callers-1 {
		t.Fatalf("registered=%d duplicates=%d, want 1 and %d", registered.Load(), duplicates.Load(), callers-1)
	}

	// Another owner may hold a draft with the same number (ADR-0015).
	b, m := newBusiness(t, shared.NewID[shared.UserTag](), "1010123456")
	if err := store.Register(t.Context(), b, m); err != nil {
		t.Fatalf("another owner's draft: %v", err)
	}
}

// If the owner can't be saved, the business isn't either.
func TestStoreRegisterIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.NewStore(migratedDB(t), discardEvents{})
	first, owner := newBusiness(t, shared.NewID[shared.UserTag](), "1010000001")
	if err := store.Register(ctx, first, owner); err != nil {
		t.Fatal(err)
	}
	// A second business reusing the first owner's staff ID: its business row
	// inserts fine, its staff row breaks the primary key.
	second, _ := newBusiness(t, shared.NewID[shared.UserTag](), "1010000002")
	clash := domain.RehydrateStaffMember(owner.ID(), second.ID(), second.OwnerID(), domain.RoleOwner, true, t0, "", nil)
	if err := store.Register(ctx, second, clash); err == nil {
		t.Fatal("a clashing staff ID was accepted")
	}
	if _, err := store.ByID(ctx, second.ID()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("business without an owner was saved: %v", err)
	}
}

// Invariants the database keeps even if application code gets them wrong.
func TestSchemaConstraints(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migratedDB(t)
	store := postgres.NewStore(pool, discardEvents{})
	a, ownerA := newBusiness(t, shared.NewID[shared.UserTag](), "1010123456")
	b, ownerB := newBusiness(t, shared.NewID[shared.UserTag](), "1010123456")
	for _, r := range []struct {
		b *domain.Business
		m *domain.StaffMember
	}{{a, ownerA}, {b, ownerB}} {
		if err := store.Register(ctx, r.b, r.m); err != nil {
			t.Fatal(err)
		}
	}

	wantViolation := func(name, constraint string, err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != constraint {
			t.Errorf("%s: error = %v, want a %s violation", name, err, constraint)
		}
	}

	// Exactly one owner per business.
	_, err := pool.Exec(ctx, `INSERT INTO business.staff_members (id, business_id, user_id, role, created_at) VALUES ($1, $2, $3, 'owner', $4)`,
		shared.NewID[shared.StaffTag]().UUID(), a.ID().UUID(), shared.NewID[shared.UserTag]().UUID(), t0)
	wantViolation("second owner", "staff_members_one_owner_key", err)

	// A user is staff of a business once.
	_, err = pool.Exec(ctx, `INSERT INTO business.staff_members (id, business_id, user_id, role, created_at) VALUES ($1, $2, $3, 'barber', $4)`,
		shared.NewID[shared.StaffTag]().UUID(), a.ID().UUID(), a.OwnerID().UUID(), t0)
	wantViolation("same user twice", "staff_members_user_business_key", err)

	// Two drafts may share a CR number; two submitted businesses may not.
	if _, err := pool.Exec(ctx, `UPDATE business.businesses SET status = 'pending_review', submitted_at = now() WHERE id = $1`, a.ID().UUID()); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE business.businesses SET status = 'active', submitted_at = now() WHERE id = $1`, b.ID().UUID())
	wantViolation("claimed cr number", "businesses_claimed_cr_number_key", err)

	// A business past draft has a submission time, and a decision has a reviewer.
	_, err = pool.Exec(ctx, `UPDATE business.businesses SET status = 'active', submitted_at = NULL WHERE id = $1`, a.ID().UUID())
	wantViolation("under review without submitted_at", "businesses_submitted_check", err)
	_, err = pool.Exec(ctx, `UPDATE business.businesses SET reviewed_at = now() WHERE id = $1`, a.ID().UUID())
	wantViolation("reviewed without a reviewer", "businesses_reviewed_check", err)

	// Only 10-digit CR numbers are stored.
	_, err = pool.Exec(ctx, `UPDATE business.businesses SET cr_number = '12345' WHERE id = $1`, b.ID().UUID())
	wantViolation("short cr number", "businesses_cr_number_check", err)
}

// rename renames b's legal name to legal, expecting version.
func rename(t *testing.T, store *postgres.Store, id shared.BusinessID, version int, legal string) error {
	t.Helper()
	return store.Update(t.Context(), id, version, func(b *domain.Business) error {
		return b.Rename(b.DisplayName(), legal, t0.Add(time.Hour))
	})
}

func TestStoreUpdate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := migratedDB(t)
	store := postgres.NewStore(pool, discardEvents{})
	b, m := newBusiness(t, shared.NewID[shared.UserTag](), "1010123456")
	if err := store.Register(ctx, b, m); err != nil {
		t.Fatal(err)
	}

	if err := rename(t, store, b.ID(), 1, "مؤسسة الفخامة"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := store.ByID(ctx, b.ID())
	if got.LegalName() != "مؤسسة الفخامة" || got.Version() != 2 || !got.UpdatedAt().Equal(t0.Add(time.Hour)) || !got.CreatedAt().Equal(t0) {
		t.Fatalf("after update: %+v", got)
	}

	// A stale version, an unknown business, a refused rename: nothing saved.
	if err := rename(t, store, b.ID(), 1, "x"); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale version: %v", err)
	}
	if err := rename(t, store, shared.NewID[shared.BusinessTag](), 1, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown business: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE business.businesses SET status = 'pending_review', submitted_at = now() WHERE id = $1`, b.ID().UUID()); err != nil {
		t.Fatal(err)
	}
	if err := rename(t, store, b.ID(), 2, "x"); !errors.Is(err, domain.ErrInvalidStateTransition) {
		t.Errorf("submitted business: %v", err)
	}
	if got, _ := store.ByID(ctx, b.ID()); got.Version() != 2 || got.LegalName() != "مؤسسة الفخامة" {
		t.Fatalf("a refused update was saved: %+v", got)
	}
}

// Two owners' phones save edits made on the same version at the same
// moment. Exactly one wins; the other gets ErrVersionConflict instead of
// silently overwriting the first (a "lost update").
func TestStoreParallelUpdatesOfTheSameVersion(t *testing.T) {
	t.Parallel()
	pool := migratedDB(t)
	store := postgres.NewStore(pool, discardEvents{})
	b, m := newBusiness(t, shared.NewID[shared.UserTag](), "1010123456")
	if err := store.Register(t.Context(), b, m); err != nil {
		t.Fatal(err)
	}

	var (
		wg               sync.WaitGroup
		start            = make(chan struct{})
		saved, conflicts atomic.Int32
	)
	for i := range 2 {
		wg.Go(func() {
			<-start
			err := store.Update(t.Context(), b.ID(), 1, func(b *domain.Business) error {
				time.Sleep(20 * time.Millisecond) // hold the row a moment, so the calls overlap
				return b.Rename(b.DisplayName(), []string{"مؤسسة أ", "مؤسسة ب"}[i], t0)
			})
			switch {
			case err == nil:
				saved.Add(1)
			case errors.Is(err, domain.ErrVersionConflict):
				conflicts.Add(1)
			default:
				t.Errorf("Update: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()

	if saved.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf("saved=%d conflicts=%d, want 1 and 1", saved.Load(), conflicts.Load())
	}
	if got, _ := store.ByID(t.Context(), b.ID()); got.Version() != 2 {
		t.Fatalf("version = %d, want 2", got.Version())
	}
}
