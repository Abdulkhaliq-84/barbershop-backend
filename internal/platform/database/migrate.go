package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/Abdulkhaliq-84/barbershop-backend/migrations"
)

// Migrate applies every pending migration embedded in the binary.
// A Postgres advisory lock makes it safe when several instances start at the
// same time: one migrates, the others wait and then find nothing to do.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	// goose speaks database/sql; OpenDBFromPool adapts our pgx pool to it.
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	provider, err := newProvider(db)
	if err != nil {
		return err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	if len(results) == 0 {
		logger.InfoContext(ctx, "database schema is up to date")
	}
	for _, r := range results {
		logger.InfoContext(ctx, "migration applied",
			slog.Int64("version", r.Source.Version),
			slog.String("file", r.Source.Path),
			slog.Duration("took", r.Duration),
		)
	}
	return nil
}

// newProvider configures goose with the embedded migrations and a session
// lock. It is shared by Migrate and the integration tests.
func newProvider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return provider, nil
}
