package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.VerificationDocuments = (*DocumentStore)(nil)

// DocumentStore implements domain.VerificationDocuments.
type DocumentStore struct {
	store *Store
}

// Documents returns the verification-document repository.
func (s *Store) Documents() *DocumentStore { return &DocumentStore{store: s} }

// Attach locks the business, counts its documents, runs check and inserts
// doc — one transaction. The lock makes concurrent uploads take turns, so
// the count check can't be raced past.
func (r *DocumentStore) Attach(ctx context.Context, doc *domain.VerificationDocument, check func(*domain.Business, int) error) error {
	return pgx.BeginFunc(ctx, r.store.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.BusinessByIDForUpdate(ctx, doc.BusinessID().UUID())
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock business: %w", err)
		}
		b, err := toBusiness(row)
		if err != nil {
			return err
		}
		n, err := q.CountVerificationDocuments(ctx, doc.BusinessID().UUID())
		if err != nil {
			return fmt.Errorf("count documents: %w", err)
		}
		if err := check(b, int(n)); err != nil {
			return err
		}
		if err := q.InsertVerificationDocument(ctx, sqlcgen.InsertVerificationDocumentParams{
			ObjectID:    doc.FileID().UUID(),
			BusinessID:  doc.BusinessID().UUID(),
			Kind:        string(doc.Kind()),
			ContentType: doc.ContentType(),
			SizeBytes:   doc.Size(),
			UploadedBy:  doc.UploadedBy().UUID(),
			UploadedAt:  doc.UploadedAt(),
		}); err != nil {
			return fmt.Errorf("insert document: %w", err)
		}
		return nil
	})
}

// List returns the business's documents, oldest first.
func (r *DocumentStore) List(ctx context.Context, business shared.BusinessID) ([]*domain.VerificationDocument, error) {
	rows, err := sqlcgen.New(r.store.pool).VerificationDocumentsByBusiness(ctx, business.UUID())
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	docs := make([]*domain.VerificationDocument, 0, len(rows))
	for _, row := range rows {
		kind, err := domain.ParseDocumentKind(row.Kind)
		if err != nil {
			return nil, err
		}
		docs = append(docs, domain.NewVerificationDocument(
			shared.IDFromUUID[shared.MediaTag](row.ObjectID), business, kind,
			row.ContentType, row.SizeBytes, shared.IDFromUUID[shared.UserTag](row.UploadedBy), row.UploadedAt,
		))
	}
	return docs, nil
}
