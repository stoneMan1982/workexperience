package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/stoneMan1982/workexperience/practice/golang/db"
	"github.com/stoneMan1982/workexperience/practice/golang/pkg/dbx"
	"github.com/uptrace/bun"
)

// Demo models used only for compilation; adjust to your schema when running.
type Inventory struct {
	ID      int64 `bun:",pk,autoincrement"`
	Stock   int64
	Version int64
}

type Job struct {
	ID     int64 `bun:",pk,autoincrement"`
	Status string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbconn, err := db.OpenFromEnv()
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer dbconn.Close()

	fmt.Println("db connected")

	// 1) Serializable transaction with retry
	err = dbx.WithSerializableRetry(ctx, dbconn, func(ctx context.Context, tx bun.Tx) error {
		// placeholder write
		_, err := tx.NewRaw("SELECT 1").Exec(ctx)
		return err
	}, &dbx.RetryOption{MaxAttempts: 3, InitialBackoff: 50 * time.Millisecond})
	if err != nil {
		log.Printf("serializable tx failed: %v", err)
	} else {
		fmt.Println("serializable tx ok")
	}

	// 2) Pessimistic lock with timeouts
	err = dbx.WithTx(ctx, dbconn, &dbx.TxOption{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		_ = dbx.SetLocalLockTimeout(ctx, tx, 3*time.Second)
		_ = dbx.SetLocalStatementTimeout(ctx, tx, 5*time.Second)
		// This select is just for demonstration; replace with your table.
		var inv Inventory
		// Note: if table doesn't exist, running this demo will error; it's fine for compile demo.
		_ = tx.NewSelect().Model(&inv).Where("id = ?", 1).For("UPDATE").Scan(ctx)
		return nil
	})
	if err != nil {
		log.Printf("pessimistic section: %v", err)
	} else {
		fmt.Println("pessimistic section ok")
	}

	// 3) Optimistic CAS example (compile-only)
	{
		var cur Inventory
		// Normally you'd read cur first; we skip errors in demo.
		_ = dbconn.NewSelect().Model(&cur).Where("id = ?", 1).Scan(ctx)
		_, _ = dbx.UpdateWithVersion(ctx, dbconn, &Inventory{ID: 1}, cur.Version, "stock = stock - 1", "id = ?", 1)
	}

	// 4) SKIP LOCKED queue example (compile-only)
	err = dbx.WithTx(ctx, dbconn, nil, func(ctx context.Context, tx bun.Tx) error {
		var jobs []Job
		return dbx.SelectForUpdateSkipLocked(ctx, tx, &jobs, "jobs", "id, status", "status = 'pending'", "id", 10)
	})
	if err != nil {
		log.Printf("queue demo: %v", err)
	} else {
		fmt.Println("queue demo ok")
	}
}
