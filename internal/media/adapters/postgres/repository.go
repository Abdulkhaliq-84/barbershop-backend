// Package postgres stores media object rows. The bytes are in storage; this
// is only what we know about them.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Objects = (*Objects)(nil)

// Objects implements domain.Objects.
type Objects struct {
	pool *pgxpool.Pool
}

// NewObjects returns a repository backed by pool.
func NewObjects(pool *pgxpool.Pool) *Objects { return &Objects{pool: pool} }

// Add inserts an object row.
func (r *Objects) Add(ctx context.Context, o *domain.Object) error {
	err := sqlcgen.New(r.pool).InsertObject(ctx, sqlcgen.InsertObjectParams{
		ID:          o.ID().UUID(),
		Purpose:     string(o.Purpose()),
		ContentType: string(o.ContentType()),
		SizeBytes:   o.Size(),
		Sha256:      o.SHA256(),
		CreatedBy:   o.CreatedBy().UUID(),
		CreatedAt:   o.CreatedAt(),
	})
	if err != nil {
		return fmt.Errorf("insert media object: %w", err)
	}
	return nil
}

// ByID returns the object with id.
func (r *Objects) ByID(ctx context.Context, id shared.MediaID) (*domain.Object, error) {
	row, err := sqlcgen.New(r.pool).ObjectByID(ctx, id.UUID())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load media object: %w", err)
	}
	purpose, err := domain.ParsePurpose(row.Purpose)
	if err != nil {
		return nil, err
	}
	ct, err := domain.ParseContentType(row.ContentType)
	if err != nil {
		return nil, err
	}
	return domain.RehydrateObject(id, purpose, ct, row.SizeBytes, row.Sha256, shared.IDFromUUID[shared.UserTag](row.CreatedBy), row.CreatedAt), nil
}

// Delete removes the row.
func (r *Objects) Delete(ctx context.Context, id shared.MediaID) error {
	if err := sqlcgen.New(r.pool).DeleteObject(ctx, id.UUID()); err != nil {
		return fmt.Errorf("delete media object: %w", err)
	}
	return nil
}
