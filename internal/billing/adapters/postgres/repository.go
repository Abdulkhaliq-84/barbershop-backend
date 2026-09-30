// Package postgres stores billing's subscriptions.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/billing/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Subscriptions = (*Subscriptions)(nil)

// Subscriptions implements domain.Subscriptions.
type Subscriptions struct {
	pool *pgxpool.Pool
}

// NewSubscriptions returns the repository.
func NewSubscriptions(pool *pgxpool.Pool) *Subscriptions { return &Subscriptions{pool: pool} }

// AddIfAbsent inserts s unless the business already has a subscription.
func (r *Subscriptions) AddIfAbsent(ctx context.Context, s *domain.Subscription) (bool, error) {
	n, err := sqlcgen.New(r.pool).InsertSubscriptionIfAbsent(ctx, sqlcgen.InsertSubscriptionIfAbsentParams{
		BusinessID:       s.BusinessID().UUID(),
		PlanCode:         string(s.Plan()),
		CurrentPeriodEnd: s.PeriodEnd(),
		CreatedAt:        s.CreatedAt(),
	})
	if err != nil {
		return false, fmt.Errorf("insert subscription: %w", err)
	}
	return n == 1, nil
}

// ForBusiness loads the business's subscription.
func (r *Subscriptions) ForBusiness(ctx context.Context, business shared.BusinessID) (*domain.Subscription, error) {
	row, err := sqlcgen.New(r.pool).SubscriptionByBusiness(ctx, business.UUID())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load subscription: %w", err)
	}
	return domain.RehydrateSubscription(shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		domain.PlanCode(row.PlanCode), row.CurrentPeriodEnd, row.CreatedAt), nil
}
