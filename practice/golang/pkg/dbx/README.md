# dbx: Bun + PostgreSQL helpers

Small utilities for:

- Transactions and retries (serializable retry on PG 40001)
- Transaction-local timeouts (lock_timeout, statement_timeout)
- Optimistic concurrency (version CAS)
- Work queue pattern (SELECT ... FOR UPDATE SKIP LOCKED)

These helpers are designed for `github.com/uptrace/bun` with PostgreSQL/pgx.

## Install

This package is part of this repo/module; import paths use your module path.

```go
import (
  "github.com/stoneMan1982/workexperience/practice/golang/pkg/dbx"
)
```

## Usage

### Serializable transaction with automatic retry

```go
err := dbx.WithSerializableRetry(ctx, db, func(ctx context.Context, tx bun.Tx) error {
    // do multi-table writes here
    // _, err := tx.NewInsert().Model(&obj).Exec(ctx)
    return nil
}, &dbx.RetryOption{MaxAttempts: 5, InitialBackoff: 50 * time.Millisecond})
```

### Pessimistic locking with timeouts

```go
err := dbx.WithTx(ctx, db, &dbx.TxOption{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
    // optional: set transaction-local timeouts
    if err := dbx.SetLocalLockTimeout(ctx, tx, 3*time.Second); err != nil { return err }
    if err := dbx.SetLocalStatementTimeout(ctx, tx, 5*time.Second); err != nil { return err }

    // lock a row
    var row struct{ ID int64 }
    if err := tx.NewSelect().TableExpr("inventory").ColumnExpr("id").Where("id = ?", 42).For("UPDATE").Scan(ctx, &row); err != nil {
        return err
    }
    // update quickly and commit
    _, err := tx.NewUpdate().TableExpr("inventory").Set("stock = stock - 1").Where("id = ?", 42).Exec(ctx)
    return err
})
```

### Optimistic locking (version CAS)

```go
// read current version first
var cur struct{ ID, Version, Stock int64 }
_ = db.NewSelect().TableExpr("inventory").ColumnExpr("id, version, stock").Where("id = ?", 42).Scan(ctx, &cur)

ok, err := dbx.UpdateWithVersion(
    ctx, db,
    &struct{}{},                // model placeholder when using TableExpr in Set/Where
    cur.Version,                // old version
    "stock = stock - 1",       // set clause(s)
    "id = ?",                  // where primary key
    42,
)
if err != nil { /* handle */ }
if !ok { /* conflict: retry or return 409 */ }
```

### Work queue (SKIP LOCKED)

```go
var jobs []struct{ ID int64; Status string }
err := dbx.WithTx(ctx, db, nil, func(ctx context.Context, tx bun.Tx) error {
    return dbx.SelectForUpdateSkipLocked(
        ctx, tx, &jobs,
        "jobs",                         // table
        "id, status",                   // columns
        "status = 'pending'",           // where
        "id",                           // order
        50,                               // limit
    )
})
```

## Notes

- SKIP LOCKED is PostgreSQL-specific.
- Prefer short transactions; avoid doing external calls while holding locks.
- For strong isolation, combine `sql.LevelSerializable` with retry on PG SQLSTATE 40001.
- For high-contention hot rows, consider queueing/SKIP LOCKED or a pessimistic lock, not pure CAS.
