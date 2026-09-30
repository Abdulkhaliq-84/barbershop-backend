package dbtest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OthersQueued is for race tests: call the function it returns inside the
// section that runs under the lock being tested. The first call waits until
// others more transactions are queued on a lock — proof the calls really
// overlap — and every later call returns at once. If they never queue, the
// code under test is missing its lock: the test fails, rather than passing
// because the calls happened to take turns.
//
// It watches pg_stat_activity on a connection of its own: the transactions
// under test may be holding every connection of the pool.
func OthersQueued(t *testing.T, pool *pgxpool.Pool, others int) func() {
	t.Helper()
	conn, err := pgx.ConnectConfig(t.Context(), pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("dbtest: watcher connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	var once sync.Once
	return func() {
		once.Do(func() {
			deadline := time.Now().Add(10 * time.Second)
			for {
				var queued int
				err := conn.QueryRow(t.Context(),
					`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&queued)
				switch {
				case err != nil:
					t.Errorf("dbtest: watch locks: %v", err)
					return
				case queued >= others:
					return
				case time.Now().After(deadline):
					t.Errorf("dbtest: %d of %d other transactions queued on the lock after 10s: is the lock missing?", queued, others)
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}
