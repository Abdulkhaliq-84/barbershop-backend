package outbox

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// migrationLock is the advisory lock key that makes River migrations take
// turns when several instances start at once ("rivermig" in ASCII).
const migrationLock int64 = 0x7269766572_6d6967

// Migrate creates or upgrades River's tables in the river schema (created by
// the goose migrations). It is idempotent.
//
// River applies each of its migrations in its own transaction (some can't
// share one: a new enum value can't be used in the transaction that adds
// it), so the lock is a session lock on a connection held for the run.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (err error) {
	m, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: Schema, Logger: logger})
	if err != nil {
		return fmt.Errorf("outbox: migrator: %w", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("outbox: migration lock: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return fmt.Errorf("outbox: migration lock: %w", err)
	}
	defer func() {
		// Unlock even if ctx is cancelled, or the pooled connection keeps the lock.
		if _, uerr := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock); uerr != nil && err == nil {
			err = fmt.Errorf("outbox: migration unlock: %w", uerr)
		}
	}()

	res, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("outbox: migrate: %w", err)
	}
	for _, v := range res.Versions {
		logger.InfoContext(ctx, "river migration applied", slog.Int("version", v.Version), slog.String("name", v.Name))
	}
	return nil
}
