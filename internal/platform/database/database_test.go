package database

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
)

// testPool connects to the database named by TEST_DATABASE_URL, or skips the
// test when it is not set, so `go test ./...` works without a database and CI
// (which starts PostGIS) runs everything.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set: run `make db-up` then `make test-all`")
	}
	pool, err := Open(t.Context(), config.Database{URL: url, MaxConns: 4})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestOpenDoesNotLeakPassword(t *testing.T) {
	t.Parallel()

	_, err := Open(t.Context(), config.Database{URL: "postgres://app:s3cret@[bad-host", MaxConns: 1})
	if err == nil {
		t.Fatal("Open() error = nil, want error for malformed URL")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaks the password: %q", err)
	}
}

func TestMigrations(t *testing.T) {
	pool := testPool(t)
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	if err := Migrate(ctx, pool, logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	assertExtensions(t, pool)

	// Every migration must be reversible: go all the way down, then up again.
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := newProvider(db)
	if err != nil {
		t.Fatalf("newProvider() error = %v", err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("DownTo(0) error = %v", err)
	}
	// Rolling back must not remove shared extensions (see 00001's Down).
	assertExtensions(t, pool)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up() after DownTo(0) error = %v", err)
	}

	// Running again is a no-op.
	results, err := provider.Up(ctx)
	if err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("second Up() applied %d migrations, want 0", len(results))
	}
}

func assertExtensions(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, ext := range []string{"postgis", "btree_gist", "pg_trgm"} {
		var installed bool
		err := pool.QueryRow(t.Context(),
			"SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)", ext,
		).Scan(&installed)
		if err != nil {
			t.Fatalf("query extension %s: %v", ext, err)
		}
		if !installed {
			t.Errorf("extension %s is not installed", ext)
		}
	}
}
