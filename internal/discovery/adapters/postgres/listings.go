// Package postgres stores discovery's listings.
package postgres

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/discovery/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Listings = (*Listings)(nil)

// Listings implements domain.Listings.
type Listings struct {
	pool *pgxpool.Pool
}

// NewListings returns the repository.
func NewListings(pool *pgxpool.Pool) *Listings { return &Listings{pool: pool} }

// Keep saves l unless the stored copy is of the same or a newer version.
// One statement does the check and the write, so two events for a branch
// handled at once can't leave the older one in place.
func (r *Listings) Keep(ctx context.Context, l domain.Listing) (bool, error) {
	version, err := toInt32(l.Version)
	if err != nil {
		return false, err
	}
	n, err := sqlcgen.New(r.pool).KeepListing(ctx, sqlcgen.KeepListingParams{
		BranchID: l.Branch.UUID(), BusinessID: l.Business.UUID(), Version: version, Listed: l.Listed,
		NameAr: l.Name.Ar(), NameEn: l.Name.En(), CityCode: l.City.Code(), District: l.District, Address: l.Address,
		Latitude: l.Location.Lat(), Longitude: l.Location.Lng(), Phone: l.Phone, Timezone: l.Timezone,
		UpdatedAt: l.UpdatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("keep listing: %w", err)
	}
	return n == 1, nil
}

// InCity returns up to limit listed branches in city, by Arabic name then
// ID, after the given position.
func (r *Listings) InCity(ctx context.Context, city shared.City, after *domain.Position, limit int) ([]domain.Listing, error) {
	size, err := toInt32(limit)
	if err != nil {
		return nil, err
	}
	params := sqlcgen.ListingsInCityParams{CityCode: city.Code(), PageSize: size}
	if after != nil {
		name, id := after.NameAr, after.Branch.UUID()
		params.AfterName, params.AfterID = &name, &id
	}
	rows, err := sqlcgen.New(r.pool).ListingsInCity(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listings in %s: %w", city.Code(), err)
	}
	out := make([]domain.Listing, 0, len(rows))
	for _, row := range rows {
		l, err := toListing(row)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

func toListing(row sqlcgen.DiscoveryBranchListing) (domain.Listing, error) {
	name, err := shared.NewLocalizedText(row.NameAr, row.NameEn)
	if err != nil {
		return domain.Listing{}, fmt.Errorf("listing %s: name: %w", row.BranchID, err)
	}
	city, err := shared.ParseCity(row.CityCode)
	if err != nil {
		return domain.Listing{}, fmt.Errorf("listing %s: city %q: %w", row.BranchID, row.CityCode, err)
	}
	location, err := shared.NewGeoPoint(row.Latitude, row.Longitude)
	if err != nil {
		return domain.Listing{}, fmt.Errorf("listing %s: location: %w", row.BranchID, err)
	}
	return domain.Listing{
		Branch: shared.IDFromUUID[shared.BranchTag](row.BranchID), Business: shared.IDFromUUID[shared.BusinessTag](row.BusinessID),
		Version: int(row.Version), Listed: row.Listed, Name: name, City: city,
		District: row.District, Address: row.Address, Location: location,
		Phone: row.Phone, Timezone: row.Timezone, UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

func toInt32(n int) (int32, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("discovery: %d doesn't fit an integer", n)
	}
	return int32(n), nil
}
