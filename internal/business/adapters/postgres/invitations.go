package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Invitations = (*InvitationStore)(nil)

// InvitationStore implements domain.Invitations.
type InvitationStore struct {
	store *Store
}

// Invitations returns the invitation repository.
func (s *Store) Invitations() *InvitationStore { return &InvitationStore{store: s} }

// Invite checks the branches belong to the business, revokes any other
// pending invitation for the phone, and saves inv — one transaction, under
// a lock on the business so parallel invites take turns.
func (r *InvitationStore) Invite(ctx context.Context, inv *domain.Invitation) error {
	ids := make([]uuid.UUID, 0, len(inv.Branches()))
	for _, b := range inv.Branches() {
		ids = append(ids, b.UUID())
	}
	return pgx.BeginFunc(ctx, r.store.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		if _, err := q.LockBusinessForInvite(ctx, inv.BusinessID().UUID()); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("lock business: %w", err)
		}
		n, err := q.CountBranchesIn(ctx, sqlcgen.CountBranchesInParams{BusinessID: inv.BusinessID().UUID(), Ids: ids})
		if err != nil {
			return fmt.Errorf("check branches: %w", err)
		}
		if int(n) != len(ids) {
			return domain.ErrUnknownBranch
		}
		if err := q.RevokePendingInvitations(ctx, sqlcgen.RevokePendingInvitationsParams{BusinessID: inv.BusinessID().UUID(), Phone: inv.Phone().String()}); err != nil {
			return fmt.Errorf("revoke older invitation: %w", err)
		}
		if err := q.InsertInvitation(ctx, sqlcgen.InsertInvitationParams{
			ID:          inv.ID().UUID(),
			BusinessID:  inv.BusinessID().UUID(),
			Phone:       inv.Phone().String(),
			DisplayName: inv.Name(),
			Role:        string(inv.Role()),
			BranchIds:   ids,
			TokenHash:   inv.TokenHash(),
			Status:      string(inv.Status()),
			InvitedBy:   inv.InvitedBy().UUID(),
			CreatedAt:   inv.CreatedAt(),
			ExpiresAt:   inv.ExpiresAt(),
		}); err != nil {
			return fmt.Errorf("insert invitation: %w", err)
		}
		return nil
	})
}

// Pending returns the business's pending invitations, newest first.
func (r *InvitationStore) Pending(ctx context.Context, business shared.BusinessID) ([]*domain.Invitation, error) {
	rows, err := sqlcgen.New(r.store.pool).PendingInvitations(ctx, business.UUID())
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	out := make([]*domain.Invitation, 0, len(rows))
	for _, row := range rows {
		inv, err := toInvitation(row)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, nil
}

// Update locks the invitation inside business, calls fn and saves it.
func (r *InvitationStore) Update(ctx context.Context, business shared.BusinessID, id domain.InvitationID, fn func(*domain.Invitation) error) error {
	return pgx.BeginFunc(ctx, r.store.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.InvitationForUpdate(ctx, sqlcgen.InvitationForUpdateParams{BusinessID: business.UUID(), ID: id.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock invitation: %w", err)
		}
		inv, err := toInvitation(row)
		if err != nil {
			return err
		}
		if err := fn(inv); err != nil {
			return err
		}
		return saveInvitation(ctx, q, inv)
	})
}

// Accept locks the invitation by token hash, lets fn turn it into a staff
// member, and saves both. The lock makes a double-tapped "accept" take turns:
// the second sees the invitation already accepted.
func (r *InvitationStore) Accept(ctx context.Context, tokenHash []byte, fn func(*domain.Invitation) (*domain.StaffMember, error)) error {
	err := pgx.BeginFunc(ctx, r.store.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.InvitationByTokenForUpdate(ctx, tokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrInvitationInvalid
		}
		if err != nil {
			return fmt.Errorf("lock invitation: %w", err)
		}
		inv, err := toInvitation(row)
		if err != nil {
			return err
		}
		member, err := fn(inv)
		if err != nil {
			return err
		}
		if err := saveInvitation(ctx, q, inv); err != nil {
			return err
		}
		return insertStaff(ctx, q, member)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "staff_members_user_business_key" {
		return domain.ErrAlreadyStaff
	}
	return err
}

func saveInvitation(ctx context.Context, q *sqlcgen.Queries, inv *domain.Invitation) error {
	var by *uuid.UUID
	if !inv.AcceptedBy().IsZero() {
		u := inv.AcceptedBy().UUID()
		by = &u
	}
	if err := q.SaveInvitation(ctx, sqlcgen.SaveInvitationParams{
		Status: string(inv.Status()), AcceptedAt: inv.AcceptedAt(), AcceptedBy: by, ID: inv.ID().UUID(),
	}); err != nil {
		return fmt.Errorf("save invitation: %w", err)
	}
	return nil
}

func toInvitation(row sqlcgen.BusinessInvitation) (*domain.Invitation, error) {
	phone, err := shared.NewPhoneNumber(row.Phone)
	if err != nil {
		return nil, fmt.Errorf("stored phone: %w", err)
	}
	role, err := domain.ParseRole(row.Role)
	if err != nil {
		return nil, err
	}
	branches := make([]shared.BranchID, 0, len(row.BranchIds))
	for _, b := range row.BranchIds {
		branches = append(branches, shared.IDFromUUID[shared.BranchTag](b))
	}
	var acceptedBy shared.UserID
	if row.AcceptedBy != nil {
		acceptedBy = shared.IDFromUUID[shared.UserTag](*row.AcceptedBy)
	}
	return domain.RehydrateInvitation(
		shared.IDFromUUID[domain.InvitationTag](row.ID), shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		domain.StaffInvite{Phone: phone, Name: row.DisplayName, Role: role, Branches: branches},
		row.TokenHash, domain.InvitationStatus(row.Status), shared.IDFromUUID[shared.UserTag](row.InvitedBy),
		row.CreatedAt, row.ExpiresAt, row.AcceptedAt, acceptedBy,
	), nil
}
