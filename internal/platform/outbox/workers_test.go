package outbox

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handlers get what River leaves of the pool: never zero, never more than 10.
func TestMaxWorkers(t *testing.T) {
	t.Parallel()
	for conns, want := range map[int32]int{2: 1, 5: 1, 8: 4, 10: 6, 14: 10, 50: 10} {
		cfg, err := pgxpool.ParseConfig("postgres://nobody@127.0.0.1:1/none")
		if err != nil {
			t.Fatal(err)
		}
		cfg.MaxConns = conns
		pool, err := pgxpool.NewWithConfig(t.Context(), cfg) // connects lazily: nothing listens there
		if err != nil {
			t.Fatal(err)
		}
		if got := maxWorkers(pool); got != want {
			t.Errorf("a pool of %d: %d workers, want %d", conns, got, want)
		}
		pool.Close()
	}
}
