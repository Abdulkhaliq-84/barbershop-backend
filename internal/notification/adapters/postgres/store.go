// Package postgres stores notification's devices, branch copies and
// delivery log.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/adapters/postgres/sqlcgen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/notification/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var _ domain.Store = (*Store)(nil)

// Store implements domain.Store.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns the store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// SaveDevice registers d by its token and keeps only the user's newest
// devices, in one transaction.
func (s *Store) SaveDevice(ctx context.Context, d domain.Device) (domain.Device, error) {
	var row sqlcgen.SaveDeviceRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		q := sqlcgen.New(tx)
		row, err = q.SaveDevice(ctx, sqlcgen.SaveDeviceParams{
			ID: d.ID.UUID(), UserID: d.User.UUID(), Token: d.Token.Reveal(),
			Platform: string(d.Platform), Locale: string(d.Locale), Now: d.UpdatedAt,
		})
		if err != nil {
			return err
		}
		return q.TrimDevices(ctx, sqlcgen.TrimDevicesParams{UserID: d.User.UUID(), Keep: domain.MaxDevicesPerUser})
	})
	if err != nil {
		return domain.Device{}, fmt.Errorf("save device: %w", err)
	}
	return toDevice(row.ID, row.UserID, d.Token, row.Platform, row.Locale, row.CreatedAt, row.UpdatedAt)
}

// RemoveDevice unregisters the user's device.
func (s *Store) RemoveDevice(ctx context.Context, user shared.UserID, id domain.DeviceID) error {
	n, err := sqlcgen.New(s.pool).RemoveDevice(ctx, sqlcgen.RemoveDeviceParams{ID: id.UUID(), UserID: user.UUID()})
	if err != nil {
		return fmt.Errorf("remove device: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ForgetDevice drops a device the push service no longer knows.
func (s *Store) ForgetDevice(ctx context.Context, id domain.DeviceID) error {
	if err := sqlcgen.New(s.pool).ForgetDevice(ctx, id.UUID()); err != nil {
		return fmt.Errorf("forget device: %w", err)
	}
	return nil
}

// Devices returns the user's devices, newest first.
func (s *Store) Devices(ctx context.Context, user shared.UserID) ([]domain.Device, error) {
	rows, err := sqlcgen.New(s.pool).DevicesOfUser(ctx, user.UUID())
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	out := make([]domain.Device, 0, len(rows))
	for _, r := range rows {
		token, err := domain.ParseToken(r.Token)
		if err != nil {
			return nil, fmt.Errorf("device %s: stored token: %w", r.ID, err)
		}
		d, err := toDevice(r.ID, r.UserID, token, r.Platform, r.Locale, r.CreatedAt, r.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func toDevice(id, user uuid.UUID, token domain.Token, platform, locale string, created, updated time.Time) (domain.Device, error) {
	p, err := domain.ParsePlatform(platform)
	if err != nil {
		return domain.Device{}, fmt.Errorf("device %s: %w", id, err)
	}
	return domain.Device{
		ID: shared.IDFromUUID[domain.DeviceTag](id), User: shared.IDFromUUID[shared.UserTag](user), Token: token,
		Platform: p, Locale: shared.ParseLanguage(locale), CreatedAt: created.UTC(), UpdatedAt: updated.UTC(),
	}, nil
}

// KeepBranch saves b unless the copy is of the same or a newer version.
func (s *Store) KeepBranch(ctx context.Context, b domain.Branch) (bool, error) {
	if b.Version < 1 || b.Version > math.MaxInt32 {
		return false, fmt.Errorf("keep branch %s: version %d", b.ID, b.Version)
	}
	n, err := sqlcgen.New(s.pool).KeepBranch(ctx, sqlcgen.KeepBranchParams{
		BranchID: b.ID.UUID(), Version: int32(b.Version), NameAr: b.Name.Ar(), NameEn: b.Name.En(), Timezone: b.Timezone,
	})
	if err != nil {
		return false, fmt.Errorf("keep branch: %w", err)
	}
	return n == 1, nil
}

// Branch returns the copy of a branch.
func (s *Store) Branch(ctx context.Context, id shared.BranchID) (domain.Branch, error) {
	row, err := sqlcgen.New(s.pool).BranchByID(ctx, id.UUID())
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Branch{}, domain.ErrUnknownBranch
	}
	if err != nil {
		return domain.Branch{}, fmt.Errorf("branch: %w", err)
	}
	name, err := shared.NewLocalizedText(row.NameAr, row.NameEn)
	if err != nil {
		return domain.Branch{}, fmt.Errorf("branch %s: name: %w", row.BranchID, err)
	}
	return domain.Branch{ID: id, Version: int(row.Version), Name: name, Timezone: row.Timezone}, nil
}

// Delivered reports whether event was already pushed to device.
func (s *Store) Delivered(ctx context.Context, event uuid.UUID, device domain.DeviceID) (bool, error) {
	done, err := sqlcgen.New(s.pool).Delivered(ctx, sqlcgen.DeliveredParams{EventID: event, DeviceID: device.UUID()})
	if err != nil {
		return false, fmt.Errorf("delivered: %w", err)
	}
	return done, nil
}

// RecordDelivery logs a push's outcome.
func (s *Store) RecordDelivery(ctx context.Context, d domain.Delivery) error {
	p := sqlcgen.RecordDeliveryParams{
		EventID: d.Event, DeviceID: d.Device.UUID(), UserID: d.User.UUID(), Kind: string(d.Kind),
		Outcome: string(d.Outcome), SentAt: d.SentAt,
	}
	if !d.Appointment.IsZero() {
		p.AppointmentID = new(d.Appointment.UUID())
	}
	if err := sqlcgen.New(s.pool).RecordDelivery(ctx, p); err != nil {
		return fmt.Errorf("record delivery: %w", err)
	}
	return nil
}
