package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	dbx "github.com/stoneMan1982/workexperience/practice/golang/pkg/dbx"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

type kv struct {
	Key   string `bun:",pk"`
	Value string
}

func main() {
	ctx := context.Background()

	// In-memory sqlite for demo
	sqldb, err := sql.Open("sqlite", "file:memdb1?mode=memory&cache=shared")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer sqldb.Close()

	db := bun.NewDB(sqldb, sqlitedialect.New())
	defer db.Close()

	// Schema
	_, _ = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT)`)

	tm := dbx.NewTxManager(db)
	tm.MaxAttempts = 1 // no retries needed for this demo

	// Register an after-commit hook (runs when outermost tx commits)
	ctx = context.WithValue(ctx, struct{}{}, nil) // no-op; show ctx usage

	err = tm.Run(ctx, dbx.Options{Isolation: sql.LevelDefault}, func(ctx context.Context, tx bun.Tx) error {
		dbx.AfterCommit(ctx, func() { log.Printf("afterCommit: outer committed") })

		// Upsert-like behavior for demo
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv(key, value) VALUES ('hello', 'world')
            ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
			return err
		}

		// Nested scope with savepoint. We'll simulate an error and roll back only the inner work.
		_ = tm.Run(ctx, dbx.Options{RequiresNew: true, SavepointNameHint: "inner"}, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO kv(key, value) VALUES ('temp', '42')`); err != nil {
				return err
			}
			// simulate failure
			return fmt.Errorf("simulate inner failure")
		})

		// Outer continues despite inner failure (because we ignored the error)
		// Update another key and set a short per-tx timeout on PG (no-op on sqlite)
		_ = dbx.SetLocalStatementTimeout(ctx, tx, 2*time.Second)

		if _, err := tx.ExecContext(ctx, `INSERT INTO kv(key, value) VALUES ('foo', 'bar')
            ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Fatalf("tx failed: %v", err)
	}

	// Verify results
	var out []kv
	if err := db.NewSelect().Model(&out).Order("key").Scan(ctx); err != nil {
		log.Fatalf("query: %v", err)
	}
	for _, r := range out {
		log.Printf("row: key=%s value=%s", r.Key, r.Value)
	}
}
