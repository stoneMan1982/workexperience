package dbx

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// TxOption configures transaction behavior.
// Currently a simple wrapper to pass isolation levels or timeouts from caller if needed.
// Extend as necessary.
type TxOption struct {
	Isolation sql.IsolationLevel
}

// WithTx begins a transaction, runs fn, and commits/rolls back accordingly.
func WithTx(ctx context.Context, db *bun.DB, opt *TxOption, fn func(ctx context.Context, tx bun.Tx) error) error {
	txOpt := &sql.TxOptions{}
	if opt != nil {
		txOpt.Isolation = opt.Isolation
	}
	tx, err := db.BeginTx(ctx, txOpt)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// RetryOption controls retry behavior for serializable transactions.
type RetryOption struct {
	MaxAttempts    int
	InitialBackoff time.Duration
}

func (o *RetryOption) normalize() {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = 50 * time.Millisecond
	}
}

// WithSerializableRetry runs fn inside a serializable transaction and retries on
// PostgreSQL serialization failures (SQLSTATE 40001). Useful with PG/Cockroach.
func WithSerializableRetry(ctx context.Context, db *bun.DB, fn func(ctx context.Context, tx bun.Tx) error, ropt *RetryOption) error {
	opt := &RetryOption{}
	if ropt != nil {
		*opt = *ropt
	}
	opt.normalize()

	backoff := opt.InitialBackoff
	for attempt := 0; attempt < opt.MaxAttempts; attempt++ {
		err := WithTx(ctx, db, &TxOption{Isolation: sql.LevelSerializable}, fn)
		if err == nil {
			return nil
		}
		// Detect PG serialization error: SQLSTATE 40001
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.SQLState() == "40001" {
			// retry with backoff
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
			backoff *= 2
			continue
		}
		return err
	}
	return errors.New("too many serialization retries")
}
