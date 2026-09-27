package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Sessions implements domain.Sessions.
type Sessions struct {
	pool *pgxpool.Pool
}

// NewSessions returns a repository backed by pool.
func NewSessions(pool *pgxpool.Pool) *Sessions {
	return &Sessions{pool: pool}
}

// Start saves the session and its first refresh token in one transaction.
func (r *Sessions) Start(ctx context.Context, s *domain.Session, first *domain.RefreshToken) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		reason := revokeReason(s)
		if err := q.InsertSession(ctx, sqlcgen.InsertSessionParams{
			ID:              s.ID().UUID(),
			UserID:          s.UserID().UUID(),
			CreatedAt:       s.CreatedAt(),
			LastRefreshedAt: s.LastRefreshedAt(),
			RevokedAt:       s.RevokedAt(),
			RevokeReason:    reason,
		}); err != nil {
			return fmt.Errorf("insert session: %w", err)
		}
		return insertRefreshToken(ctx, q, first)
	})
}

// Rotate locks the presented token and its session (SELECT … FOR UPDATE OF
// both rows), so parallel refreshes of one session take turns: the second
// one sees the token already used.
func (r *Sessions) Rotate(ctx context.Context, tokenHash []byte, fn func(*domain.Session, *domain.RefreshToken) (*domain.RefreshToken, error)) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.RefreshTokenForUpdate(ctx, tokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock refresh token: %w", err)
		}
		sessionID := shared.IDFromUUID[domain.SessionTag](row.SessionID)
		session := domain.RehydrateSession(sessionID, shared.IDFromUUID[shared.UserTag](row.UserID),
			row.SessionCreatedAt, row.LastRefreshedAt, row.RevokedAt, toRevokeReason(row.RevokeReason))
		token := domain.RehydrateRefreshToken(row.TokenHash, sessionID, row.CreatedAt, row.ExpiresAt, row.UsedAt)

		next, err := fn(session, token)
		if err != nil {
			return err
		}
		if err := saveSession(ctx, q, session); err != nil {
			return err
		}
		if used := token.UsedAt(); used != nil && row.UsedAt == nil {
			if err := q.MarkRefreshTokenUsed(ctx, sqlcgen.MarkRefreshTokenUsedParams{TokenHash: token.Hash(), UsedAt: used}); err != nil {
				return fmt.Errorf("mark refresh token used: %w", err)
			}
		}
		if next != nil {
			return insertRefreshToken(ctx, q, next)
		}
		return nil
	})
}

// Update locks the session, calls fn and saves the session.
func (r *Sessions) Update(ctx context.Context, id domain.SessionID, fn func(*domain.Session) error) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.SessionForUpdate(ctx, id.UUID())
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session: %w", err)
		}
		session := domain.RehydrateSession(id, shared.IDFromUUID[shared.UserTag](row.UserID),
			row.CreatedAt, row.LastRefreshedAt, row.RevokedAt, toRevokeReason(row.RevokeReason))
		if err := fn(session); err != nil {
			return err
		}
		return saveSession(ctx, q, session)
	})
}

func saveSession(ctx context.Context, q *sqlcgen.Queries, s *domain.Session) error {
	err := q.UpdateSession(ctx, sqlcgen.UpdateSessionParams{
		ID:              s.ID().UUID(),
		LastRefreshedAt: s.LastRefreshedAt(),
		RevokedAt:       s.RevokedAt(),
		RevokeReason:    revokeReason(s),
	})
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

func insertRefreshToken(ctx context.Context, q *sqlcgen.Queries, t *domain.RefreshToken) error {
	err := q.InsertRefreshToken(ctx, sqlcgen.InsertRefreshTokenParams{
		TokenHash: t.Hash(),
		SessionID: t.SessionID().UUID(),
		CreatedAt: t.CreatedAt(),
		ExpiresAt: t.ExpiresAt(),
		UsedAt:    t.UsedAt(),
	})
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func revokeReason(s *domain.Session) *string {
	if !s.IsRevoked() {
		return nil
	}
	reason := string(s.RevokeReason())
	return &reason
}

func toRevokeReason(stored *string) domain.RevokeReason {
	if stored == nil {
		return ""
	}
	return domain.RevokeReason(*stored)
}
