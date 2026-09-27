// Package postgres implements the business repositories on PostgreSQL. The
// SQL lives in queries.sql; sqlc generates the typed Go in sqlcgen/. This
// file only maps rows ↔ domain objects.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Store implements the business module's repositories and read models.
// They share one pool; each method is one statement or one transaction.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Compile-time checks that the adapter satisfies the ports.
var (
	_ domain.Businesses    = (*Store)(nil)
	_ domain.Staff         = (*Store)(nil)
	_ app.MembershipReader = (*Store)(nil)
)

// uniqueViolation is PostgreSQL's error code for a broken unique constraint.
const uniqueViolation = "23505"

// Register inserts the business and its owner in one transaction: either
// both exist afterwards or neither does.
func (s *Store) Register(ctx context.Context, b *domain.Business, owner *domain.StaffMember) error {
	version, err := toInt32(b.Version())
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		if err := q.InsertBusiness(ctx, sqlcgen.InsertBusinessParams{
			ID:            b.ID().UUID(),
			OwnerUserID:   b.OwnerID().UUID(),
			DisplayNameAr: b.DisplayName().Ar(),
			DisplayNameEn: b.DisplayName().En(),
			LegalName:     b.LegalName(),
			CrNumber:      b.CRNumber().String(),
			Status:        string(b.Status()),
			Version:       version,
			CreatedAt:     b.CreatedAt(),
			UpdatedAt:     b.UpdatedAt(),
		}); err != nil {
			return err
		}
		user := owner.UserID().UUID()
		return q.InsertStaffMember(ctx, sqlcgen.InsertStaffMemberParams{
			ID:         owner.ID().UUID(),
			BusinessID: owner.BusinessID().UUID(),
			UserID:     &user,
			Role:       string(owner.Role()),
			Active:     owner.IsActive(),
			CreatedAt:  owner.CreatedAt(),
		})
	})
	// The database, not a read-then-insert check, decides duplicates: two
	// retries racing each other can't both pass a unique index.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "businesses_owner_cr_number_key" {
		return domain.ErrAlreadyRegistered
	}
	if err != nil {
		return fmt.Errorf("insert business: %w", err)
	}
	return nil
}

// ByID returns the business with id.
func (s *Store) ByID(ctx context.Context, id shared.BusinessID) (*domain.Business, error) {
	row, err := sqlcgen.New(s.pool).BusinessByID(ctx, id.UUID())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load business: %w", err)
	}
	return toBusiness(row)
}

// Membership returns user's staff record in business.
func (s *Store) Membership(ctx context.Context, business shared.BusinessID, user shared.UserID) (*domain.StaffMember, error) {
	u := user.UUID()
	row, err := sqlcgen.New(s.pool).StaffMembership(ctx, sqlcgen.StaffMembershipParams{BusinessID: business.UUID(), UserID: &u})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load membership: %w", err)
	}
	return toStaffMember(row)
}

// ForUser lists user's active memberships, newest business first.
func (s *Store) ForUser(ctx context.Context, user shared.UserID) ([]app.MembershipView, error) {
	u := user.UUID()
	rows, err := sqlcgen.New(s.pool).MembershipsForUser(ctx, &u)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	views := make([]app.MembershipView, 0, len(rows))
	for _, r := range rows {
		role, err := domain.ParseRole(r.Role)
		if err != nil {
			return nil, err
		}
		status, err := domain.ParseStatus(r.Status)
		if err != nil {
			return nil, err
		}
		name, err := shared.NewLocalizedText(r.DisplayNameAr, r.DisplayNameEn)
		if err != nil {
			return nil, fmt.Errorf("stored display name: %w", err)
		}
		views = append(views, app.MembershipView{
			StaffID:     shared.IDFromUUID[shared.StaffTag](r.StaffID),
			Role:        role,
			BusinessID:  shared.IDFromUUID[shared.BusinessTag](r.BusinessID),
			DisplayName: name,
			Status:      status,
		})
	}
	return views, nil
}

func toBusiness(row sqlcgen.BusinessBusiness) (*domain.Business, error) {
	name, err := shared.NewLocalizedText(row.DisplayNameAr, row.DisplayNameEn)
	if err != nil {
		return nil, fmt.Errorf("stored display name: %w", err)
	}
	cr, err := domain.NewCRNumber(row.CrNumber)
	if err != nil {
		return nil, fmt.Errorf("stored cr number: %w", err)
	}
	status, err := domain.ParseStatus(row.Status)
	if err != nil {
		return nil, err
	}
	return domain.RehydrateBusiness(
		shared.IDFromUUID[shared.BusinessTag](row.ID), shared.IDFromUUID[shared.UserTag](row.OwnerUserID),
		name, row.LegalName, cr, status, int(row.Version), row.CreatedAt, row.UpdatedAt,
	), nil
}

func toStaffMember(row sqlcgen.BusinessStaffMember) (*domain.StaffMember, error) {
	role, err := domain.ParseRole(row.Role)
	if err != nil {
		return nil, err
	}
	var user shared.UserID
	if row.UserID != nil {
		user = shared.IDFromUUID[shared.UserTag](*row.UserID)
	}
	return domain.RehydrateStaffMember(
		shared.IDFromUUID[shared.StaffTag](row.ID), shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		user, role, row.Active, row.CreatedAt,
	), nil
}

// toInt32 converts with an overflow check (a version never gets near it).
func toInt32(n int) (int32, error) {
	if n < 0 || n > math.MaxInt32 {
		return 0, fmt.Errorf("value %d out of int32 range", n)
	}
	return int32(n), nil
}
