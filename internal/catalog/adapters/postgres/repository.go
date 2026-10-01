// Package postgres stores catalog's services.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/catalog/events"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/outbox"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Services = (*Services)(nil)

// Services implements domain.Services.
type Services struct {
	pool   *pgxpool.Pool
	events EventPublisher
}

// EventPublisher saves events inside the caller's transaction: the outbox
// (ADR-0009). An event is published if and only if its change commits.
type EventPublisher interface {
	PublishTx(ctx context.Context, tx pgx.Tx, events ...outbox.Event) error
}

// NewServices returns the repository. It publishes the services' events
// through events.
func NewServices(pool *pgxpool.Pool, events EventPublisher) *Services {
	return &Services{pool: pool, events: events}
}

// Add inserts a new service and publishes its events.
func (r *Services) Add(ctx context.Context, s *domain.Service) error {
	row, err := toRow(s)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if err := sqlcgen.New(tx).InsertService(ctx, sqlcgen.InsertServiceParams(row)); err != nil {
			return fmt.Errorf("insert service: %w", err)
		}
		return r.publish(ctx, tx, s.Events())
	})
}

// List returns the branch's services in display order, with their
// offerings: two queries, whatever the number of services.
func (r *Services) List(ctx context.Context, business shared.BusinessID, branch shared.BranchID) ([]*domain.Service, error) {
	q := sqlcgen.New(r.pool)
	rows, err := q.ServicesByBranch(ctx, sqlcgen.ServicesByBranchParams{BusinessID: business.UUID(), BranchID: branch.UUID()})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	offRows, err := q.OfferingsByBranch(ctx, sqlcgen.OfferingsByBranchParams{BusinessID: business.UUID(), BranchID: branch.UUID()})
	if err != nil {
		return nil, fmt.Errorf("list offerings: %w", err)
	}
	offerings := map[uuid.UUID][]domain.Offering{}
	for _, o := range offRows {
		off, err := toOffering(o)
		if err != nil {
			return nil, err
		}
		offerings[o.ServiceID] = append(offerings[o.ServiceID], off)
	}
	out := make([]*domain.Service, 0, len(rows))
	for _, row := range rows {
		s, err := toService(row, offerings[row.ID])
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
		offRows, err := q.OfferingsByService(ctx, sqlcgen.OfferingsByServiceParams{BusinessID: business.UUID(), ServiceID: id.UUID()})
		if err != nil {
			return fmt.Errorf("load offerings: %w", err)
		}
		offerings := make([]domain.Offering, 0, len(offRows))
		for _, o := range offRows {
			off, err := toOffering(o)
			if err != nil {
				return err
			}
			offerings = append(offerings, off)
		}
		s, err := toService(row, offerings)
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
			BusinessID: business.UUID(), BranchID: branch.UUID(), ID: next.ID, ExpectedVersion: row.Version,
		})
		if err != nil {
			return fmt.Errorf("update service: %w", err)
		}
		if n == 0 {
			return domain.ErrVersionConflict
		}
		if err := saveOfferings(ctx, q, s); err != nil {
			return err
		}
		return r.publish(ctx, tx, s.Events())
	})
}

// publish hands the service's events to the outbox inside tx, in the
// contract's JSON shape (package events).
func (r *Services) publish(ctx context.Context, tx pgx.Tx, recorded []domain.Event) error {
	out := make([]outbox.Event, 0, len(recorded))
	for _, e := range recorded {
		var (
			ev  outbox.Event
			err error
		)
		switch e := e.(type) {
		case domain.ServiceCreatedEvent:
			ev, err = outbox.NewEvent(events.TypeServiceCreated, e.At, events.ServiceChanged{
				BusinessID: e.Business.UUID(), BranchID: e.Branch.UUID(), ServiceID: e.Service.UUID(), ChangedAt: e.At,
				Service: serviceContract(e.Snapshot),
			})
		case domain.ServiceUpdatedEvent:
			ev, err = outbox.NewEvent(events.TypeServiceUpdated, e.At, events.ServiceChanged{
				BusinessID: e.Business.UUID(), BranchID: e.Branch.UUID(), ServiceID: e.Service.UUID(), ChangedAt: e.At,
				Service: serviceContract(e.Snapshot),
			})
		default:
			err = fmt.Errorf("no contract for event %T", e)
		}
		if err != nil {
			return err
		}
		out = append(out, ev)
	}
	if err := r.events.PublishTx(ctx, tx, out...); err != nil {
		return fmt.Errorf("publish events: %w", err)
	}
	return nil
}

// serviceContract is a service snapshot in the events' JSON shape.
func serviceContract(s domain.ServiceSnapshot) events.Service {
	d := s.Details
	out := events.Service{
		Version: s.Version, Active: s.Active, CategoryCode: string(d.Category),
		Name:            events.LocalizedText{Ar: d.Name.Ar(), En: d.Name.En()},
		DurationMinutes: int(d.Duration / time.Minute), Price: moneyContract(d.Price),
	}
	if from, ok := s.PriceFrom(); ok {
		out.PriceFrom = new(moneyContract(from))
	}
	return out
}

func moneyContract(m shared.Money) events.Money {
	return events.Money{Amount: m.Amount(), Currency: string(m.Currency())}
}

// saveOfferings replaces the service's offerings with its current ones.
func saveOfferings(ctx context.Context, q *sqlcgen.Queries, s *domain.Service) error {
	business, service := s.BusinessID().UUID(), s.ID().UUID()
	if err := q.DeleteOfferings(ctx, sqlcgen.DeleteOfferingsParams{BusinessID: business, ServiceID: service}); err != nil {
		return fmt.Errorf("clear offerings: %w", err)
	}
	for _, o := range s.Offerings() {
		p := sqlcgen.InsertOfferingParams{BusinessID: business, ServiceID: service, StaffID: o.Staff.UUID()}
		if o.Price != nil {
			p.PriceAmount = pgtype.Int8{Int64: o.Price.Amount(), Valid: true}
			p.PriceCurrency = pgtype.Text{String: string(o.Price.Currency()), Valid: true}
		}
		if o.Duration != nil {
			minutes, err := toInt16(int(*o.Duration / time.Minute))
			if err != nil {
				return err
			}
			p.DurationMinutes = pgtype.Int2{Int16: minutes, Valid: true}
		}
		if err := q.InsertOffering(ctx, p); err != nil {
			return fmt.Errorf("insert offering: %w", err)
		}
	}
	return nil
}

func toOffering(row sqlcgen.CatalogServiceOffering) (domain.Offering, error) {
	o := domain.Offering{Staff: shared.IDFromUUID[shared.StaffTag](row.StaffID)}
	if row.PriceAmount.Valid {
		p, err := shared.NewMoney(row.PriceAmount.Int64, shared.Currency(row.PriceCurrency.String))
		if err != nil {
			return o, fmt.Errorf("stored offering price: %w", err)
		}
		o.Price = &p
	}
	if row.DurationMinutes.Valid {
		d := time.Duration(row.DurationMinutes.Int16) * time.Minute
		o.Duration = &d
	}
	return o, nil
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

func toService(row sqlcgen.CatalogService, offerings []domain.Offering) (*domain.Service, error) {
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
		offerings, row.Active, int(row.Version), row.CreatedAt, row.UpdatedAt,
	), nil
}
