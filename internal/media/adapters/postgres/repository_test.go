package postgres_test

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/postgres"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database/dbtest"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestObjects(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.NewDatabase(t)
	if err := database.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewObjects(pool)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	sum := bytes.Repeat([]byte{7}, 32)
	o, err := domain.NewObject(shared.NewID[shared.MediaTag](), domain.PurposeCRDocument, domain.PNG, 4096, sum, shared.NewID[shared.UserTag](), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(ctx, o); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ByID(ctx, o.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Purpose() != o.Purpose() || got.ContentType() != domain.PNG || got.Size() != 4096 ||
		!bytes.Equal(got.SHA256(), sum) || got.CreatedBy() != o.CreatedBy() || !got.CreatedAt().Equal(now) {
		t.Errorf("round trip = %+v", got)
	}

	if err := repo.Delete(ctx, o.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ByID(ctx, o.ID()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := repo.Delete(ctx, o.ID()); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}

	// The database refuses what the domain refuses.
	_, err = pool.Exec(ctx, `INSERT INTO media.objects VALUES ($1, 'cr_document', 'text/html', 10, $2, $3, now())`,
		shared.NewID[shared.MediaTag]().UUID(), sum, shared.NewID[shared.UserTag]().UUID())
	if err == nil {
		t.Error("an html object was stored")
	}
}
