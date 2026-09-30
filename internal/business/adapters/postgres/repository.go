// Package postgres implements the business repositories on PostgreSQL. The
// SQL lives in queries.sql; sqlc generates the typed Go in sqlcgen/. This
// file only maps rows ↔ domain objects.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Store implements the business module's repositories and read models.
// They share one pool; each method is one statement or one transaction.
type Store struct {
	pool   *pgxpool.Pool
	events EventPublisher
}

// EventPublisher saves events inside the caller's transaction: the outbox
// (ADR-0009). An event is published if and only if its change commits.
type EventPublisher interface {
	PublishTx(ctx context.Context, tx pgx.Tx, events ...outbox.Event) error
}

// NewStore returns a store backed by pool that publishes the aggregates'
// events through events.
func NewStore(pool *pgxpool.Pool, events EventPublisher) *Store {
	return &Store{pool: pool, events: events}
}

// Compile-time checks that the adapter satisfies the ports.
var (
	_ domain.Businesses    = (*Store)(nil)
	_ domain.Staff         = (*Store)(nil)
	_ app.MembershipReader = (*Store)(nil)
	_ app.ReviewQueue      = (*Store)(nil)
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
		// Count, then insert, one registration of this user at a time.
		if err := q.AdvisoryLock(ctx, "business.register:"+b.OwnerID().String()); err != nil {
			return fmt.Errorf("lock owner: %w", err)
		}
		open, err := q.CountOpenRegistrations(ctx, b.OwnerID().UUID())
		if err != nil {
			return fmt.Errorf("count registrations: %w", err)
		}
		if open >= domain.MaxOpenRegistrations {
			return domain.ErrTooManyRegistrations
		}
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
		return insertStaff(ctx, q, owner)
	})
	// The database, not a read-then-insert check, decides duplicates: two
	// retries racing each other can't both pass a unique index.
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "businesses_owner_cr_number_key" {
		return domain.ErrAlreadyRegistered
	}
	if err != nil && !errors.Is(err, domain.ErrTooManyRegistrations) {
		return fmt.Errorf("insert business: %w", err)
	}
	return err
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

// Update applies fn to the business under a row lock, if its version is
// still expectedVersion (optimistic concurrency: two owners editing from two
// phones can't silently overwrite each other).
func (s *Store) Update(ctx context.Context, id shared.BusinessID, expectedVersion int, fn func(*domain.Business) error) error {
	return s.update(ctx, id, expectedVersion, func(_ *sqlcgen.Queries, b *domain.Business) error { return fn(b) })
}

// UpdateWithReadiness is Update for submission: fn also gets the document
// and branch counts, read after the business row is locked. Attaching a
// document takes the same lock, so the count can't change underneath.
func (s *Store) UpdateWithReadiness(ctx context.Context, id shared.BusinessID, expectedVersion int, fn func(*domain.Business, domain.Readiness) error) error {
	return s.update(ctx, id, expectedVersion, func(q *sqlcgen.Queries, b *domain.Business) error {
		docs, err := q.CountVerificationDocuments(ctx, id.UUID())
		if err != nil {
			return fmt.Errorf("count documents: %w", err)
		}
		branches, err := q.CountBranches(ctx, id.UUID())
		if err != nil {
			return fmt.Errorf("count branches: %w", err)
		}
		return fn(b, domain.Readiness{Documents: int(docs), Branches: int(branches)})
	})
}

// update is the one locked read-change-write path every business change
// goes through.
func (s *Store) update(ctx context.Context, id shared.BusinessID, expectedVersion int, fn func(*sqlcgen.Queries, *domain.Business) error) error {
	expected, err := toInt32(expectedVersion)
	if err != nil {
		return domain.ErrVersionConflict // no business ever has that version
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.BusinessByIDForUpdate(ctx, id.UUID())
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock business: %w", err)
		}
		if row.Version != expected {
			return domain.ErrVersionConflict
		}
		b, err := toBusiness(row)
		if err != nil {
			return err
		}
		if err := fn(q, b); err != nil {
			return err
		}
		version, err := toInt32(b.Version())
		if err != nil {
			return err
		}
		r := b.Review()
		var reviewer *uuid.UUID
		if !r.ReviewedBy.IsZero() {
			reviewer = new(r.ReviewedBy.UUID())
		}
		n, err := q.UpdateBusiness(ctx, sqlcgen.UpdateBusinessParams{
			DisplayNameAr:   b.DisplayName().Ar(),
			DisplayNameEn:   b.DisplayName().En(),
			LegalName:       b.LegalName(),
			Status:          string(b.Status()),
			Version:         version,
			UpdatedAt:       b.UpdatedAt(),
			SubmittedAt:     r.SubmittedAt,
			ReviewedAt:      r.ReviewedAt,
			ReviewedBy:      reviewer,
			RejectionReason: r.RejectionReason,
			ID:              b.ID().UUID(),
			ExpectedVersion: expected,
		})
		// Submitting claims the CR number platform-wide (a partial unique
		// index); the database is the one place two submissions can't race.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "businesses_claimed_cr_number_key" {
			return domain.ErrCRNumberClaimed
		}
		if err != nil {
			return fmt.Errorf("update business: %w", err)
		}
		if n == 0 {
			return domain.ErrVersionConflict
		}
		return s.publish(ctx, tx, b.Events())
	})
}

// publish hands the aggregate's events to the outbox inside tx, in the
// contract's JSON shape (package events).
func (s *Store) publish(ctx context.Context, tx pgx.Tx, recorded []domain.Event) error {
	out := make([]outbox.Event, 0, len(recorded))
	for _, e := range recorded {
		var (
			ev  outbox.Event
			err error
		)
		switch e := e.(type) {
		case domain.BusinessApproved:
			ev, err = outbox.NewEvent(events.TypeBusinessApproved, e.At, events.BusinessApproved{
				BusinessID: e.Business.UUID(), OwnerID: e.Owner.UUID(), ApprovedAt: e.At,
			})
		case domain.BranchPublishedEvent:
			ev, err = outbox.NewEvent(events.TypeBranchPublished, e.At, events.BranchPublished{
				BusinessID: e.Business.UUID(), BranchID: e.Branch.UUID(), PublishedAt: e.At,
			})
		case domain.BranchUnpublishedEvent:
			ev, err = outbox.NewEvent(events.TypeBranchUnpublished, e.At, events.BranchUnpublished{
				BusinessID: e.Business.UUID(), BranchID: e.Branch.UUID(), UnpublishedAt: e.At,
			})
		default:
			err = fmt.Errorf("no contract for event %T", e)
		}
		if err != nil {
			return err
		}
		out = append(out, ev)
	}
	if err := s.events.PublishTx(ctx, tx, out...); err != nil {
		return fmt.Errorf("publish events: %w", err)
	}
	return nil
}

// ReviewPage returns up to limit businesses in status, oldest submission
// first, after the given position (nil for the first page).
func (s *Store) ReviewPage(ctx context.Context, status domain.Status, after *app.QueuePosition, limit int) ([]*domain.Business, error) {
	size, err := toInt32(limit)
	if err != nil {
		return nil, err
	}
	params := sqlcgen.BusinessesForReviewParams{Status: string(status), PageSize: size}
	if after != nil {
		at, id := after.SubmittedAt, after.ID.UUID()
		params.AfterSubmittedAt, params.AfterID = &at, &id
	}
	rows, err := sqlcgen.New(s.pool).BusinessesForReview(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("review queue: %w", err)
	}
	out := make([]*domain.Business, 0, len(rows))
	for _, row := range rows {
		b, err := toBusiness(row)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
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
	return toStaffMember(sqlcgen.StaffByBusinessRow(row))
}

// List returns the business's staff, oldest first.
func (s *Store) List(ctx context.Context, business shared.BusinessID) ([]*domain.StaffMember, error) {
	rows, err := sqlcgen.New(s.pool).StaffByBusiness(ctx, business.UUID())
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	out := make([]*domain.StaffMember, 0, len(rows))
	for _, row := range rows {
		m, err := toStaffMember(row)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// insertStaff saves a staff member and the branches they work at.
func insertStaff(ctx context.Context, q *sqlcgen.Queries, m *domain.StaffMember) error {
	user := m.UserID().UUID()
	if err := q.InsertStaffMember(ctx, sqlcgen.InsertStaffMemberParams{
		ID:          m.ID().UUID(),
		BusinessID:  m.BusinessID().UUID(),
		UserID:      &user,
		Role:        string(m.Role()),
		Active:      m.IsActive(),
		CreatedAt:   m.CreatedAt(),
		DisplayName: m.DisplayName(),
	}); err != nil {
		return err
	}
	for _, b := range m.Branches() {
		if err := q.InsertStaffBranch(ctx, sqlcgen.InsertStaffBranchParams{StaffID: m.ID().UUID(), BranchID: b.UUID(), BusinessID: m.BusinessID().UUID()}); err != nil {
			return err
		}
	}
	return nil
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
	review := domain.Review{SubmittedAt: row.SubmittedAt, ReviewedAt: row.ReviewedAt, RejectionReason: row.RejectionReason}
	if row.ReviewedBy != nil {
		review.ReviewedBy = shared.IDFromUUID[shared.UserTag](*row.ReviewedBy)
	}
	return domain.RehydrateBusiness(
		shared.IDFromUUID[shared.BusinessTag](row.ID), shared.IDFromUUID[shared.UserTag](row.OwnerUserID),
		name, row.LegalName, cr, status, int(row.Version), row.CreatedAt, row.UpdatedAt, review,
	), nil
}

func toStaffMember(row sqlcgen.StaffByBusinessRow) (*domain.StaffMember, error) {
	role, err := domain.ParseRole(row.Role)
	if err != nil {
		return nil, err
	}
	var user shared.UserID
	if row.UserID != nil {
		user = shared.IDFromUUID[shared.UserTag](*row.UserID)
	}
	branches := make([]shared.BranchID, 0, len(row.BranchIds))
	for _, b := range row.BranchIds {
		branches = append(branches, shared.IDFromUUID[shared.BranchTag](b))
	}
	return domain.RehydrateStaffMember(
		shared.IDFromUUID[shared.StaffTag](row.ID), shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		user, role, row.Active, row.CreatedAt, row.DisplayName, branches,
	), nil
}

// toInt32 converts with an overflow check (a version never gets near it).
func toInt32(n int) (int32, error) {
	if n < 0 || n > math.MaxInt32 {
		return 0, fmt.Errorf("value %d out of int32 range", n)
	}
	return int32(n), nil
}
