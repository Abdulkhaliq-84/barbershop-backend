// Package postgres stores catalog's services.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Services = (*Services)(nil)

// Services implements domain.Services.
type Services struct {
	pool *pgxpool.Pool
}

// NewServices returns the repository.
func NewServices(pool *pgxpool.Pool) *Services { return &Services{pool: pool} }

// Add inserts a new service.
func (r *Services) Add(ctx context.Context, s *domain.Service) error {
	row, err := toRow(s)
	if err != nil {
		return err
	}
	if err := sqlcgen.New(r.pool).InsertService(ctx, sqlcgen.InsertServiceParams(row)); err != nil {
		return fmt.Errorf("insert service: %w", err)
	}
	return nil
}

// List returns the branch's services in display order.
func (r *Services) List(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]*domain.Service, error) {
	rows, err := sqlcgen.New(r.pool).ServicesByBranch(ctx, sqlcgen.ServicesByBranchParams{BusinessID: business.UUID(), BranchID: branch.UUID()})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	out := make([]*domain.Service, 0, len(rows))
	for _, row := range rows {
		s, err := toService(row)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// Update locks the service, checks the version, calls fn and saves.
func (r *Services) Update(ctx context.Context, business shared.BusinessID, branch shared.BranchID, id domain.ServiceID, expectedVersion int, fn func(*domain.Service) error) error {
	if expectedVersion < 1 || expectedVersion > math.MaxInt32 {
		return domain.ErrVersionConflict // no service ever has that version
	}
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.ServiceForUpdate(ctx, sqlcgen.ServiceForUpdateParams{BusinessID: business.UUID(), BranchID: branch.UUID(), ID: id.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock service: %w", err)
		}
		if int(row.Version) != expectedVersion {
			return domain.ErrVersionConflict
		}
		s, err := toService(row)
		if err != nil {
			return err
		}
		if err := fn(s); err != nil {
			return err
		}
		next, err := toRow(s)
		if err != nil {
			return err
		}
		n, err := q.UpdateService(ctx, sqlcgen.UpdateServiceParams{
			CategoryCode: next.CategoryCode, NameAr: next.NameAr, NameEn: next.NameEn,
			DescriptionAr: next.DescriptionAr, DescriptionEn: next.DescriptionEn,
			DurationMinutes: next.DurationMinutes, PriceAmount: next.PriceAmount, PriceCurrency: next.PriceCurrency,
			Active: next.Active, SortOrder: next.SortOrder, Version: next.Version, UpdatedAt: next.UpdatedAt,
			ID: next.ID, ExpectedVersion: row.Version,
		})
		if err != nil {
			return fmt.Errorf("update service: %w", err)
		}
		if n == 0 {
			return domain.ErrVersionConflict
		}
		return nil
	})
}

// toRow flattens a service into the table's columns, in order.
func toRow(s *domain.Service) (sqlcgen.CatalogService, error) {
	d := s.Details()
	minutes, err := toInt16(int(d.Duration / time.Minute))
	if err != nil {
		return sqlcgen.CatalogService{}, err
	}
	sort, err := toInt16(d.SortOrder)
	if err != nil {
		return sqlcgen.CatalogService{}, err
	}
	version, err := toInt32(s.Version())
	if err != nil {
		return sqlcgen.CatalogService{}, err
	}
	return sqlcgen.CatalogService{
		ID: s.ID().UUID(), BusinessID: s.BusinessID().UUID(), BranchID: s.BranchID().UUID(),
		CategoryCode: string(d.Category), NameAr: d.Name.Ar(), NameEn: d.Name.En(),
		DescriptionAr: d.Description.Ar, DescriptionEn: d.Description.En,
		DurationMinutes: minutes, PriceAmount: d.Price.Amount(), PriceCurrency: string(d.Price.Currency()),
		Active: s.IsActive(), SortOrder: sort,
		Version: version, CreatedAt: s.CreatedAt(), UpdatedAt: s.UpdatedAt(),
	}, nil
}

// toInt32 converts with a range check.
func toInt32(n int) (int32, error) {
	if n < 0 || n > math.MaxInt32 {
		return 0, fmt.Errorf("value %d out of integer range", n)
	}
	return int32(n), nil
}

// toInt16 converts with a range check (the domain keeps these values small).
func toInt16(n int) (int16, error) {
	if n < 0 || n > math.MaxInt16 {
		return 0, fmt.Errorf("value %d out of smallint range", n)
	}
	return int16(n), nil
}

func toService(row sqlcgen.CatalogService) (*domain.Service, error) {
	name, err := shared.NewLocalizedText(row.NameAr, row.NameEn)
	if err != nil {
		return nil, fmt.Errorf("stored service name: %w", err)
	}
	price, err := shared.NewMoney(row.PriceAmount, shared.Currency(row.PriceCurrency))
	if err != nil {
		return nil, fmt.Errorf("stored price: %w", err)
	}
	return domain.RehydrateService(
		shared.IDFromUUID[domain.ServiceTag](row.ID), shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		shared.IDFromUUID[shared.BranchTag](row.BranchID),
		domain.ServiceDetails{
			Category: domain.CategoryCode(row.CategoryCode), Name: name,
			Description: domain.Description{Ar: row.DescriptionAr, En: row.DescriptionEn},
			Duration:    time.Duration(row.DurationMinutes) * time.Minute, Price: price, SortOrder: int(row.SortOrder),
		},
		row.Active, int(row.Version), row.CreatedAt, row.UpdatedAt,
	), nil
}
