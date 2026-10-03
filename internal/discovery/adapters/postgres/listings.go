// Package postgres stores discovery's listings.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
		UpdatedAt: l.UpdatedAt, SearchText: domain.SearchText(l.Name),
	})
	if err != nil {
		return false, fmt.Errorf("keep listing: %w", err)
	}
	return n == 1, nil
}

// KeepService saves s unless the stored copy is of the same or a newer
// version, in one statement like Keep.
func (r *Listings) KeepService(ctx context.Context, s domain.Service) (bool, error) {
	if s.PriceFrom.Currency() != shared.SAR {
		return false, domain.ErrNotSAR
	}
	version, err := toInt32(s.Version)
	if err != nil {
		return false, err
	}
	minutes, err := toInt32(int(s.Duration / time.Minute))
	if err != nil {
		return false, err
	}
	sortOrder, err := toInt32(s.SortOrder)
	if err != nil {
		return false, err
	}
	n, err := sqlcgen.New(r.pool).KeepService(ctx, sqlcgen.KeepServiceParams{
		ServiceID: s.Service.UUID(), BranchID: s.Branch.UUID(), BusinessID: s.Business.UUID(), Version: version,
		Offered: s.Offered, CategoryCode: s.Category.Code(), PriceFrom: s.PriceFrom.Amount(), UpdatedAt: s.UpdatedAt,
		NameAr: s.Name.Ar(), NameEn: s.Name.En(), DurationMinutes: minutes, SortOrder: sortOrder,
	})
	if err != nil {
		return false, fmt.Errorf("keep service: %w", err)
	}
	return n == 1, nil
}

// KeepHours saves h unless the stored copy is of the same or a newer
// version, in one statement like Keep. The week is sent as multirange text:
// {[540,1260),[1980,2700)}.
func (r *Listings) KeepHours(ctx context.Context, h domain.OpeningHours) (bool, error) {
	ranges := make([]string, 0, len(h.Open))
	for _, o := range h.Open {
		if o[0] < 0 || o[1] <= o[0] || o[1] > 2*domain.MinutesPerWeek {
			return false, domain.ErrBadHours
		}
		ranges = append(ranges, fmt.Sprintf("[%d,%d)", o[0], o[1]))
	}
	version, err := toInt32(h.Version)
	if err != nil {
		return false, err
	}
	intervals, err := json.Marshal(append([][2]int{}, h.Open...)) // [] rather than null when closed
	if err != nil {
		return false, err
	}
	n, err := sqlcgen.New(r.pool).KeepHours(ctx, sqlcgen.KeepHoursParams{
		BranchID: h.Branch.UUID(), BusinessID: h.Business.UUID(), Version: version,
		Open: "{" + strings.Join(ranges, ",") + "}", Intervals: intervals, UpdatedAt: h.UpdatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("keep opening hours: %w", err)
	}
	return n == 1, nil
}

// InCity returns up to limit listed branches in f.City that pass f, by
// Arabic name then ID, after the given position.
func (r *Listings) InCity(ctx context.Context, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	if f.City == nil {
		return nil, errors.New("listings in a city: no city")
	}
	city := *f.City
	size, err := toInt32(limit)
	if err != nil {
		return nil, err
	}
	params := sqlcgen.ListingsInCityParams{
		CityCode: city.Code(), Category: categoryCode(f.Category), At: f.At, OpenOnly: f.OpenNow, PageSize: size,
	}
	if after != nil {
		name, id := after.NameAr, after.Branch.UUID()
		params.AfterName, params.AfterID = &name, &id
	}
	rows, err := sqlcgen.New(r.pool).ListingsInCity(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listings in %s: %w", city.Code(), err)
	}
	out := make([]domain.Found, 0, len(rows))
	for _, row := range rows {
		f, err := toFound(row)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// Near returns up to limit listed branches within near's radius that pass
// f, nearest first then by ID, after the given position.
func (r *Listings) Near(ctx context.Context, near domain.Near, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	size, err := toInt32(limit)
	if err != nil {
		return nil, err
	}
	params := sqlcgen.ListingsNearParams{
		Lat: near.Point.Lat(), Lng: near.Point.Lng(), RadiusM: near.RadiusM, Category: categoryCode(f.Category),
		At: f.At, OpenOnly: f.OpenNow, PageSize: size,
	}
	if f.City != nil {
		params.CityCode = new(f.City.Code())
	}
	if f.Text != "" {
		params.Text, params.TextLike = new(f.Text), new(escapeLike(f.Text))
	}
	if after != nil {
		params.AfterDistance, params.AfterID = new(after.DistanceM), new(after.Branch.UUID())
	}
	var rows []sqlcgen.ListingsNearRow
	err = r.matching(ctx, func(q *sqlcgen.Queries) (err error) {
		rows, err = q.ListingsNear(ctx, params)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("listings near: %w", err)
	}
	return found(rows, func(f *domain.Found, key float64) { f.DistanceM = key })
}

// Matching returns up to limit listed branches whose names match f.Text and
// pass f, best match first then by ID, after the given position.
func (r *Listings) Matching(ctx context.Context, f domain.Filter, after *domain.Position, limit int) ([]domain.Found, error) {
	size, err := toInt32(limit)
	if err != nil {
		return nil, err
	}
	params := sqlcgen.ListingsMatchingParams{
		Text: f.Text, TextLike: escapeLike(f.Text), Category: categoryCode(f.Category), At: f.At, OpenOnly: f.OpenNow, PageSize: size,
	}
	if f.City != nil {
		params.CityCode = new(f.City.Code())
	}
	if after != nil {
		params.AfterScore, params.AfterID = new(after.Score), new(after.Branch.UUID())
	}
	var rows []sqlcgen.ListingsMatchingRow
	err = r.matching(ctx, func(q *sqlcgen.Queries) (err error) {
		rows, err = q.ListingsMatching(ctx, params)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("listings matching: %w", err)
	}
	// The two queries select the same columns, so their rows convert.
	near := make([]sqlcgen.ListingsNearRow, len(rows))
	for i, row := range rows {
		near[i] = sqlcgen.ListingsNearRow(row)
	}
	return found(near, func(f *domain.Found, key float64) { f.Score = key })
}

// Page reads the branch's page and its menu from one snapshot (a
// repeatable-read transaction), so the price from and the menu agree even
// while a service event is being applied.
func (r *Listings) Page(ctx context.Context, branch shared.BranchID, at time.Time) (domain.Page, error) {
	var (
		row  sqlcgen.BranchPageRow
		menu []sqlcgen.BranchMenuRow
	)
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, r.pool, opts, func(tx pgx.Tx) (err error) {
		q := sqlcgen.New(tx)
		if row, err = q.BranchPage(ctx, sqlcgen.BranchPageParams{BranchID: branch.UUID(), At: at}); err != nil {
			return err
		}
		menu, err = q.BranchMenu(ctx, branch.UUID())
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Page{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Page{}, fmt.Errorf("branch page: %w", err)
	}
	f, err := toFound(sqlcgen.ListingsInCityRow{
		BranchID: row.BranchID, BusinessID: row.BusinessID, Version: row.Version, Listed: row.Listed,
		NameAr: row.NameAr, NameEn: row.NameEn, CityCode: row.CityCode, District: row.District, Address: row.Address,
		Latitude: row.Latitude, Longitude: row.Longitude, Phone: row.Phone, Timezone: row.Timezone, UpdatedAt: row.UpdatedAt,
		PriceFrom: row.PriceFrom, OpenNow: row.OpenNow,
	})
	if err != nil {
		return domain.Page{}, err
	}
	p := domain.Page{Found: f, Menu: make([]domain.MenuItem, 0, len(menu))}
	if err := json.Unmarshal(row.Intervals, &p.Hours); err != nil {
		return domain.Page{}, fmt.Errorf("branch %s: stored hours: %w", row.BranchID, err)
	}
	for _, m := range menu {
		item, err := toMenuItem(m)
		if err != nil {
			return domain.Page{}, fmt.Errorf("branch %s: %w", row.BranchID, err)
		}
		p.Menu = append(p.Menu, item)
	}
	return p, nil
}

func toMenuItem(row sqlcgen.BranchMenuRow) (domain.MenuItem, error) {
	category, err := shared.ParseCategory(row.CategoryCode)
	if err != nil {
		return domain.MenuItem{}, fmt.Errorf("service %s: category %q: %w", row.ServiceID, row.CategoryCode, err)
	}
	name, err := shared.NewLocalizedText(row.NameAr, row.NameEn)
	if err != nil {
		return domain.MenuItem{}, fmt.Errorf("service %s: name: %w", row.ServiceID, err)
	}
	return domain.MenuItem{
		Service: shared.IDFromUUID[shared.ServiceTag](row.ServiceID), Category: category, Name: name,
		Duration: time.Duration(row.DurationMinutes) * time.Minute, PriceFrom: shared.Halalas(row.PriceFrom),
	}, nil
}

// setMatchThreshold sets how close part of a name must be to a search to
// match it (pg_trgm's word_similarity, from 0 to 1), for one transaction.
// pg_trgm's default, 0.6, misses one wrong letter in a short Arabic word:
// "الانقه" for "الاناقه" scores 0.57.
const setMatchThreshold = "SET LOCAL pg_trgm.word_similarity_threshold = 0.5"

// matching runs fn in a read-only transaction with the match threshold set.
func (r *Listings) matching(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	return pgx.BeginTxFunc(ctx, r.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, setMatchThreshold); err != nil {
			return fmt.Errorf("set the match threshold: %w", err)
		}
		return fn(sqlcgen.New(tx))
	})
}

// categoryCode is the query parameter for a category: NULL for any.
func categoryCode(c *shared.Category) *string {
	if c == nil {
		return nil
	}
	return new(c.Code())
}

// escapeLike makes text match itself in a LIKE pattern. Normalized text
// has no %, _ or \ (they separate words), but a pattern shouldn't rely on it.
func escapeLike(text string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
}

// found reads ranked rows; key sets the sort key (a distance, a score).
func found(rows []sqlcgen.ListingsNearRow, key func(*domain.Found, float64)) ([]domain.Found, error) {
	out := make([]domain.Found, 0, len(rows))
	for _, row := range rows {
		// The columns are ListingsInCity's, plus the sort key.
		f, err := toFound(sqlcgen.ListingsInCityRow{
			BranchID: row.BranchID, BusinessID: row.BusinessID, Version: row.Version, Listed: row.Listed,
			NameAr: row.NameAr, NameEn: row.NameEn, CityCode: row.CityCode, District: row.District, Address: row.Address,
			Latitude: row.Latitude, Longitude: row.Longitude, Phone: row.Phone, Timezone: row.Timezone, UpdatedAt: row.UpdatedAt,
			PriceFrom: row.PriceFrom, OpenNow: row.OpenNow,
		})
		if err != nil {
			return nil, err
		}
		key(&f, row.SortKey)
		out = append(out, f)
	}
	return out, nil
}

// toFound reads a found branch: the listing, the least a service there
// costs (in halalas: discovery keeps SAR only), and whether it is open.
func toFound(row sqlcgen.ListingsInCityRow) (domain.Found, error) {
	l, err := toListing(row)
	return domain.Found{Listing: l, PriceFrom: shared.Halalas(row.PriceFrom), OpenNow: row.OpenNow}, err
}

func toListing(row sqlcgen.ListingsInCityRow) (domain.Listing, error) {
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
