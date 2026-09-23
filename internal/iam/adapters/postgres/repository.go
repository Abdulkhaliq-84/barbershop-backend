// Package postgres implements the iam repositories on PostgreSQL. The SQL
// lives in queries.sql; sqlc generates the typed Go in sqlcgen/. This file
// only maps rows ↔ domain objects.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// OTPChallenges implements domain.OTPChallenges.
type OTPChallenges struct {
	pool *pgxpool.Pool
}

// NewOTPChallenges returns a repository backed by pool.
func NewOTPChallenges(pool *pgxpool.Pool) *OTPChallenges {
	return &OTPChallenges{pool: pool}
}

// Compile-time checks that the adapters satisfy the domain ports.
var (
	_ domain.OTPChallenges = (*OTPChallenges)(nil)
	_ domain.Users         = (*Users)(nil)
)

// Add inserts a new challenge.
func (r *OTPChallenges) Add(ctx context.Context, c *domain.OTPChallenge) error {
	attempts, err := toInt16(c.Attempts())
	if err != nil {
		return err
	}
	return sqlcgen.New(r.pool).InsertOTPChallenge(ctx, sqlcgen.InsertOTPChallengeParams{
		ID:         c.ID().UUID(),
		Phone:      c.Phone().String(),
		CodeHash:   c.CodeHash(),
		Attempts:   attempts,
		CreatedAt:  c.CreatedAt(),
		ExpiresAt:  c.ExpiresAt(),
		ConsumedAt: c.ConsumedAt(),
	})
}

// Latest returns the most recent challenge for phone.
func (r *OTPChallenges) Latest(ctx context.Context, phone shared.PhoneNumber) (*domain.OTPChallenge, error) {
	row, err := sqlcgen.New(r.pool).LatestOTPChallenge(ctx, phone.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("latest otp challenge: %w", err)
	}
	return toOTPChallenge(row)
}

// CountSince counts challenges for phone created at or after since.
func (r *OTPChallenges) CountSince(ctx context.Context, phone shared.PhoneNumber, since time.Time) (int, error) {
	n, err := sqlcgen.New(r.pool).CountOTPChallengesSince(ctx, sqlcgen.CountOTPChallengesSinceParams{Phone: phone.String(), CreatedAt: since})
	if err != nil {
		return 0, fmt.Errorf("count otp challenges: %w", err)
	}
	return int(n), nil
}

// UpdateLatest locks the latest challenge (SELECT … FOR UPDATE), applies fn
// and saves it — all in one transaction. pgx.BeginFunc commits when the
// function returns nil and rolls back on an error or panic.
func (r *OTPChallenges) UpdateLatest(ctx context.Context, phone shared.PhoneNumber, fn func(*domain.OTPChallenge) error) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.LatestOTPChallengeForUpdate(ctx, phone.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock otp challenge: %w", err)
		}
		c, err := toOTPChallenge(row)
		if err != nil {
			return err
		}
		if err := fn(c); err != nil {
			return err
		}
		attempts, err := toInt16(c.Attempts())
		if err != nil {
			return err
		}
		return q.UpdateOTPChallenge(ctx, sqlcgen.UpdateOTPChallengeParams{ID: c.ID().UUID(), Attempts: attempts, ConsumedAt: c.ConsumedAt()})
	})
}

func toOTPChallenge(row sqlcgen.IamOtpChallenge) (*domain.OTPChallenge, error) {
	phone, err := shared.NewPhoneNumber(row.Phone)
	if err != nil {
		return nil, fmt.Errorf("otp challenge %s: stored phone: %w", row.ID, err)
	}
	return domain.RehydrateOTPChallenge(
		shared.IDFromUUID[domain.OTPChallengeTag](row.ID), phone, row.CodeHash, int(row.Attempts),
		row.CreatedAt, row.ExpiresAt, row.ConsumedAt,
	), nil
}

// Users implements domain.Users.
type Users struct {
	pool *pgxpool.Pool
}

// NewUsers returns a repository backed by pool.
func NewUsers(pool *pgxpool.Pool) *Users {
	return &Users{pool: pool}
}

// Register inserts u unless its phone is taken (ON CONFLICT DO NOTHING), then
// loads the stored user. created is false when the number already existed.
func (r *Users) Register(ctx context.Context, u *domain.User) (*domain.User, bool, error) {
	q := sqlcgen.New(r.pool)
	_, err := q.InsertUserIfNew(ctx, sqlcgen.InsertUserIfNewParams{
		ID:        u.ID().UUID(),
		Phone:     u.Phone().String(),
		Locale:    string(u.Locale()),
		CreatedAt: u.CreatedAt(),
	})
	created := true
	switch {
	case errors.Is(err, pgx.ErrNoRows): // conflict: the number is already registered
		created = false
	case err != nil:
		return nil, false, fmt.Errorf("insert user: %w", err)
	}

	row, err := q.UserByPhone(ctx, u.Phone().String())
	if err != nil {
		return nil, false, fmt.Errorf("load user: %w", err)
	}
	stored, err := toUser(row)
	return stored, created, err
}

func toUser(row sqlcgen.IamUser) (*domain.User, error) {
	phone, err := shared.NewPhoneNumber(row.Phone)
	if err != nil {
		return nil, fmt.Errorf("user %s: stored phone: %w", row.ID, err)
	}
	var name string
	if row.Name != nil {
		name = *row.Name
	}
	return domain.RehydrateUser(
		shared.IDFromUUID[shared.UserTag](row.ID), phone, name,
		shared.ParseLanguage(row.Locale), domain.UserStatus(row.Status), row.CreatedAt,
	), nil
}

func toInt16(n int) (int16, error) {
	if n < math.MinInt16 || n > math.MaxInt16 {
		return 0, fmt.Errorf("value %d does not fit in smallint", n)
	}
	return int16(n), nil
}
