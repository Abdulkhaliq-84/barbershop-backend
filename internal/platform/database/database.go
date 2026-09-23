// Package database owns the PostgreSQL connection pool and schema migrations.
// It is platform plumbing: it knows nothing about barbershops.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
)

// pingTimeout bounds how long startup waits for the database to answer.
const pingTimeout = 5 * time.Second

// Open creates a pgx connection pool and checks the database is reachable,
// so a wrong DATABASE_URL fails at startup instead of on the first request.
// The caller owns the pool and must Close it.
func Open(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// Don't wrap err: pgx may echo the URL, and the URL holds a password.
		return nil, errors.New("parse DATABASE_URL: invalid connection string")
	}
	poolCfg.MaxConns = cfg.MaxConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
