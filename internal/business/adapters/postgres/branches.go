package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Branches = (*BranchStore)(nil)

// BranchStore implements domain.Branches. Every query is scoped by the
// business ID as well as the branch ID.
type BranchStore struct {
	store *Store
}

// Branches returns the branch repository sharing this store's pool.
func (s *Store) Branches() *BranchStore { return &BranchStore{store: s} }

// Add inserts a new branch.
func (r *BranchStore) Add(ctx context.Context, b *domain.Branch, allow func(existing int) error) error {
	row, err := toBranchRow(b)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, r.store.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		if err := lockBusiness(ctx, q, b.BusinessID()); err != nil {
			return err
		}
		n, err := q.CountBranches(ctx, b.BusinessID().UUID())
		if err != nil {
			return fmt.Errorf("count branches: %w", err)
		}
		if err := allow(int(n)); err != nil {
			return err
		}
		// InsertBranchParams has exactly the table's columns, in order, so the
		// row converts directly (a Go struct conversion, checked at compile time).
		if err := q.InsertBranch(ctx, sqlcgen.InsertBranchParams(row)); err != nil {
			return fmt.Errorf("insert branch: %w", err)
		}
		return nil
	})
}

// lockBusiness takes the business's "adding rows" lock (see LockBusiness).
func lockBusiness(ctx context.Context, q *sqlcgen.Queries, business shared.BusinessID) error {
	if _, err := q.LockBusiness(ctx, business.UUID()); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("lock business: %w", err)
	}
	return nil
}

// ByID returns the branch with id inside business.
func (r *BranchStore) ByID(ctx context.Context, business shared.BusinessID, id shared.BranchID) (*domain.Branch, error) {
	row, err := sqlcgen.New(r.store.pool).BranchByID(ctx, sqlcgen.BranchByIDParams{BusinessID: business.UUID(), ID: id.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load branch: %w", err)
	}
	return toBranch(row)
}

// List returns the business's branches, oldest first.
func (r *BranchStore) List(ctx context.Context, business shared.BusinessID) ([]*domain.Branch, error) {
	rows, err := sqlcgen.New(r.store.pool).BranchesByBusiness(ctx, business.UUID())
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	branches := make([]*domain.Branch, 0, len(rows))
	for _, row := range rows {
		b, err := toBranch(row)
		if err != nil {
			return nil, err
		}
		branches = append(branches, b)
	}
	return branches, nil
}

// Update applies fn to the branch under a row lock, if its version is still
// expectedVersion.
func (r *BranchStore) Update(ctx context.Context, business shared.BusinessID, id shared.BranchID, expectedVersion int, fn func(*domain.Branch) error) error {
	expected, err := toInt32(expectedVersion)
	if err != nil {
		return domain.ErrVersionConflict
	}
	return pgx.BeginFunc(ctx, r.store.pool, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.BranchByIDForUpdate(ctx, sqlcgen.BranchByIDForUpdateParams{BusinessID: business.UUID(), ID: id.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock branch: %w", err)
		}
		if row.Version != expected {
			return domain.ErrVersionConflict
		}
		b, err := toBranch(row)
		if err != nil {
			return err
		}
		if err := fn(b); err != nil {
			return err
		}
		next, err := toBranchRow(b)
		if err != nil {
			return err
		}
		n, err := q.UpdateBranch(ctx, sqlcgen.UpdateBranchParams{
			NameAr: next.NameAr, NameEn: next.NameEn, CityCode: next.CityCode, District: next.District,
			Address: next.Address, Latitude: next.Latitude, Longitude: next.Longitude, Phone: next.Phone,
			Timezone: next.Timezone, Status: next.Status,
			MinLeadMinutes: next.MinLeadMinutes, HorizonDays: next.HorizonDays, SlotIntervalMinutes: next.SlotIntervalMinutes,
			BufferMinutes: next.BufferMinutes, CancellationMinutes: next.CancellationMinutes, AutoConfirm: next.AutoConfirm,
			PendingExpiryMinutes: next.PendingExpiryMinutes, MaxActiveBookings: next.MaxActiveBookings,
			Version: next.Version, UpdatedAt: next.UpdatedAt,
			BusinessID: business.UUID(), ID: id.UUID(), ExpectedVersion: expected,
		})
		if err != nil {
			return fmt.Errorf("update branch: %w", err)
		}
		if n == 0 {
			return domain.ErrVersionConflict
		}
		return nil
	})
}

func toBranchRow(b *domain.Branch) (sqlcgen.BusinessBranch, error) {
	p, rules := b.Profile(), b.Policy().Rules()
	version, err := toInt32(b.Version())
	if err != nil {
		return sqlcgen.BusinessBranch{}, err
	}
	var phone *string
	if !p.Phone.IsZero() {
		phone = new(p.Phone.String())
	}
	// The domain already limits every value far below these types' ranges;
	// the checked conversions only guard against a future change there.
	minLead, err1 := toInt32(int(rules.MinLead / time.Minute))
	cancellation, err2 := toInt32(int(rules.CancellationWindow / time.Minute))
	horizon, err3 := toInt16(rules.HorizonDays)
	slot, err4 := toInt16(int(rules.SlotInterval / time.Minute))
	buffer, err5 := toInt16(int(rules.Buffer / time.Minute))
	expiry, err6 := toInt16(int(rules.PendingExpiry / time.Minute))
	maxActive, err7 := toInt16(rules.MaxActiveBookings)
	if err := errors.Join(err1, err2, err3, err4, err5, err6, err7); err != nil {
		return sqlcgen.BusinessBranch{}, err
	}
	return sqlcgen.BusinessBranch{
		ID: b.ID().UUID(), BusinessID: b.BusinessID().UUID(),
		NameAr: p.Name.Ar(), NameEn: p.Name.En(), CityCode: string(p.City), District: p.District, Address: p.Address,
		Latitude: p.Location.Lat(), Longitude: p.Location.Lng(), Phone: phone, Timezone: p.Timezone, Status: string(b.Status()),
		MinLeadMinutes: minLead, HorizonDays: horizon, SlotIntervalMinutes: slot, BufferMinutes: buffer,
		CancellationMinutes: cancellation, AutoConfirm: rules.AutoConfirm, PendingExpiryMinutes: expiry, MaxActiveBookings: maxActive,
		Version: version, CreatedAt: b.CreatedAt(), UpdatedAt: b.UpdatedAt(),
	}, nil
}

func toBranch(row sqlcgen.BusinessBranch) (*domain.Branch, error) {
	name, err := shared.NewLocalizedText(row.NameAr, row.NameEn)
	if err != nil {
		return nil, fmt.Errorf("stored branch name: %w", err)
	}
	city, err := domain.ParseCityCode(row.CityCode)
	if err != nil {
		return nil, err
	}
	loc, err := shared.NewGeoPoint(row.Latitude, row.Longitude)
	if err != nil {
		return nil, fmt.Errorf("stored location: %w", err)
	}
	var phone shared.PhoneNumber
	if row.Phone != nil {
		if phone, err = shared.NewPhoneNumber(*row.Phone); err != nil {
			return nil, fmt.Errorf("stored phone: %w", err)
		}
	}
	policy, err := domain.NewBookingPolicy(domain.PolicyRules{
		MinLead:            time.Duration(row.MinLeadMinutes) * time.Minute,
		HorizonDays:        int(row.HorizonDays),
		SlotInterval:       time.Duration(row.SlotIntervalMinutes) * time.Minute,
		Buffer:             time.Duration(row.BufferMinutes) * time.Minute,
		CancellationWindow: time.Duration(row.CancellationMinutes) * time.Minute,
		AutoConfirm:        row.AutoConfirm,
		PendingExpiry:      time.Duration(row.PendingExpiryMinutes) * time.Minute,
		MaxActiveBookings:  int(row.MaxActiveBookings),
	})
	if err != nil {
		return nil, fmt.Errorf("stored booking policy: %w", err)
	}
	status, err := domain.ParseBranchStatus(row.Status)
	if err != nil {
		return nil, err
	}
	profile := domain.BranchProfile{
		Name: name, City: city, District: row.District, Address: row.Address,
		Location: loc, Phone: phone, Timezone: row.Timezone,
	}
	return domain.RehydrateBranch(
		shared.IDFromUUID[shared.BranchTag](row.ID), shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		profile, policy, status, int(row.Version), row.CreatedAt, row.UpdatedAt,
	), nil
}

// toInt16 converts with an overflow check.
func toInt16(n int) (int16, error) {
	if n < math.MinInt16 || n > math.MaxInt16 {
		return 0, fmt.Errorf("value %d out of int16 range", n)
	}
	return int16(n), nil
}
