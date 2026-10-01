package database

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LockStaff takes a transaction lock on one person's working time. Every
// change that makes them less available — scheduling changing their
// schedule or adding time off — and every booking of them takes it, so a
// booking can't slip into hours that are being taken away at the same
// moment: whoever locks second sees the other's change.
//
// It is the one lock two modules share, so its key is defined here, once.
// It is released when the transaction ends, or when a savepoint taken
// before it is rolled back.
func LockStaff(ctx context.Context, tx pgx.Tx, staff uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('staff:' || $1::text, 0))`, staff); err != nil {
		return fmt.Errorf("lock staff: %w", err)
	}
	return nil
}
