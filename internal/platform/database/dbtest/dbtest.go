// Package dbtest gives each test its own throwaway PostgreSQL database.
//
// `go test ./...` runs packages in parallel. Sharing one database, one test's
// migration round-trip would drop tables under another test's feet. Instead
// every call creates a fresh database on the server named by
// TEST_DATABASE_URL and drops it when the test ends.
package dbtest

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewDatabase returns a pool connected to a new, empty database, or skips
// the test when TEST_DATABASE_URL is not set (so plain `go test ./...` works
// without Postgres). In CI a missing URL fails instead, so a misconfigured
// pipeline can't silently skip every database test. The database is dropped
// on cleanup. Decision: docs/adr/0012-test-database-per-test.md.
func NewDatabase(t testing.TB) *pgxpool.Pool {
	t.Helper()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		if os.Getenv("CI") != "" { // GitHub Actions sets CI=true
			t.Fatal("TEST_DATABASE_URL must be set in CI")
		}
		t.Skip("TEST_DATABASE_URL not set: run `make db-up` then `make test-all`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("dbtest: connect: %v", err)
	}
	name := pgx.Identifier{"test_" + strings.ToLower(rand.Text())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("dbtest: create database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(baseURL)
	if err != nil {
		t.Fatalf("dbtest: parse url: %v", err)
	}
	cfg.ConnConfig.Database = strings.Trim(name, `"`)
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("dbtest: connect to test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// WITH (FORCE) ends any connection the test left open.
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dbtest: drop database: %v", err)
		}
		_ = admin.Close(ctx)
	})
	return pool
}
