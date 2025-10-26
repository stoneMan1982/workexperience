package dbx

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// SetLocalLockTimeout sets a transaction-local lock timeout in PostgreSQL.
// Example: 3s  => SET LOCAL lock_timeout = '3s'
func SetLocalLockTimeout(ctx context.Context, tx bun.Tx, d time.Duration) error {
	// Postgres interval string accepts e.g. '3000ms' or '3s'
	_, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = $1", d.String())
	return err
}

// SetLocalStatementTimeout sets a statement timeout for the current transaction.
func SetLocalStatementTimeout(ctx context.Context, tx bun.Tx, d time.Duration) error {
	_, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = $1", d.String())
	return err
}
