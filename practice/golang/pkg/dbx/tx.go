package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	mysql "github.com/go-sql-driver/mysql"
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

// ------------------------------
// TxManager: nested tx (savepoints), retries, after-commit hooks
// ------------------------------

type txCtxKey struct{}

type txContext struct {
	tx          bun.Tx
	depth       int
	afterCommit []func()
}

// Options configures a transaction run.
type Options struct {
	Isolation         sql.IsolationLevel
	ReadOnly          bool
	StatementTimeout  time.Duration // PG: SET LOCAL statement_timeout
	LockTimeout       time.Duration // PG: SET LOCAL lock_timeout
	RequiresNew       bool          // start a new logical tx scope (savepoint if already in tx)
	SavepointNameHint string        // optional prefix when creating savepoints
}

// TxManager coordinates transactional execution on top of bun.DB.
type TxManager struct {
	DB             *bun.DB
	MaxAttempts    int
	InitialBackoff time.Duration
}

func NewTxManager(db *bun.DB) *TxManager {
	return &TxManager{DB: db, MaxAttempts: 1, InitialBackoff: 50 * time.Millisecond}
}

// InTx returns current bun.Tx if present in context.
func InTx(ctx context.Context) (bun.Tx, bool) {
	v := ctx.Value(txCtxKey{})
	if v == nil {
		var zero bun.Tx
		return zero, false
	}
	tctx, _ := v.(*txContext)
	if tctx == nil {
		var zero bun.Tx
		return zero, false
	}
	return tctx.tx, true
}

// AfterCommit registers a callback to be executed after the OUTERMOST transaction commits.
// If not in tx, f runs immediately when this function is called.
func AfterCommit(ctx context.Context, f func()) {
	if f == nil {
		return
	}
	v := ctx.Value(txCtxKey{})
	if v == nil {
		// not in tx, run now
		f()
		return
	}
	if tctx, ok := v.(*txContext); ok && tctx != nil {
		tctx.afterCommit = append(tctx.afterCommit, f)
	}
}

// Run executes fn in a transactional scope. If already in a tx, it uses a SAVEPOINT when
// RequiresNew is true or simply reuses the outer tx and increases depth otherwise.
// For a top-level run, it begins/commits/rollbacks the transaction.
func (m *TxManager) Run(ctx context.Context, opts Options, fn func(context.Context, bun.Tx) error) error {
	if m.MaxAttempts <= 0 {
		m.MaxAttempts = 1
	}
	if m.InitialBackoff <= 0 {
		m.InitialBackoff = 50 * time.Millisecond
	}

	attempt := 0
	backoff := m.InitialBackoff
	for {
		err := m.runOnce(ctx, opts, fn)
		if err == nil {
			return nil
		}
		attempt++
		if attempt >= m.MaxAttempts || !isRetryableTxError(err) || ctx.Err() != nil {
			return err
		}
		// backoff
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		backoff *= 2
	}
}

func (m *TxManager) runOnce(ctx context.Context, opts Options, fn func(context.Context, bun.Tx) error) error {
	if outerTx, ok := InTx(ctx); ok && !opts.RequiresNew {
		// reuse current tx scope (no savepoint). Just run.
		tctx := ctx.Value(txCtxKey{}).(*txContext)
		tctx.depth++
		defer func() { tctx.depth-- }()
		return fn(ctx, outerTx)
	}

	if outerTx, ok := InTx(ctx); ok && opts.RequiresNew {
		// nested with savepoint
		name := savepointName(opts.SavepointNameHint)
		if err := execSavepoint(ctx, outerTx, name); err != nil {
			return err
		}
		// run nested
		err := fn(ctx, outerTx)
		if err != nil {
			_ = rollbackToSavepoint(ctx, outerTx, name)
			return err
		}
		return releaseSavepoint(ctx, outerTx, name)
	}

	// top-level: begin tx
	txOpt := &sql.TxOptions{Isolation: opts.Isolation, ReadOnly: opts.ReadOnly}
	tx, err := m.DB.BeginTx(ctx, txOpt)
	if err != nil {
		return err
	}
	tctx := &txContext{tx: tx, depth: 1}
	ctx = context.WithValue(ctx, txCtxKey{}, tctx)

	// apply per-tx settings (Postgres)
	if opts.LockTimeout > 0 {
		_ = SetLocalLockTimeout(ctx, tx, opts.LockTimeout)
	}
	if opts.StatementTimeout > 0 {
		_ = SetLocalStatementTimeout(ctx, tx, opts.StatementTimeout)
	}

	// run user fn
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// run after-commit hooks
	for _, f := range tctx.afterCommit {
		safeCall(f)
	}
	return nil
}

func safeCall(f func()) {
	defer func() { _ = recover() }()
	f()
}

func savepointName(hint string) string {
	if hint == "" {
		hint = "sp"
	}
	return fmt.Sprintf("%s_%d", hint, time.Now().UnixNano())
}

func execSavepoint(ctx context.Context, tx bun.Tx, name string) error {
	_, err := tx.ExecContext(ctx, "SAVEPOINT "+name)
	return err
}
func releaseSavepoint(ctx context.Context, tx bun.Tx, name string) error {
	_, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name)
	return err
}
func rollbackToSavepoint(ctx context.Context, tx bun.Tx, name string) error {
	_, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+name)
	return err
}

// isRetryableTxError decides if an error is worth retrying.
func isRetryableTxError(err error) bool {
	if err == nil {
		return false
	}
	// PostgreSQL: serialization_failure 40001, deadlock_detected 40P01
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.SQLState()
		if code == "40001" || code == "40P01" {
			return true
		}
	}
	// MySQL: ER_LOCK_DEADLOCK 1213, ER_LOCK_WAIT_TIMEOUT 1205
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		if myErr.Number == 1213 || myErr.Number == 1205 {
			return true
		}
	}
	// Fallback: text contains hints (best-effort)
	msg := err.Error()
	if containsAnyFold(msg, "deadlock", "serialization", "timeout") {
		return true
	}
	return false
}

func containsAnyFold(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		if containsFold(s, sub) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	// cheap case-insensitive contains
	return len(s) >= len(sub) && (indexFold(s, sub) >= 0)
}

func indexFold(s, substr string) int {
	// naive implementation to avoid extra deps
	ls, lsub := len(s), len(substr)
	if lsub == 0 {
		return 0
	}
	for i := 0; i <= ls-lsub; i++ {
		if equalFold(s[i:i+lsub], substr) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca := a[i]
		cb := b[i]
		if ca|32 != cb|32 { // ASCII case fold
			if ca >= 128 || cb >= 128 {
				if fmt.Sprintf("%c", ca) == fmt.Sprintf("%c", cb) { // fallback
					continue
				}
			}
			return false
		}
	}
	return true
}
