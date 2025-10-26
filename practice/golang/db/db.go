package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// OpenPostgres opens a bun DB backed by pgx. It configures connection pool
// and verifies connectivity by pinging the database.
func OpenPostgres(dsn string, maxOpenConns, maxIdleConns int, connMaxLifetime time.Duration) (*bun.DB, error) {
	sqldb, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}

	sqldb.SetMaxOpenConns(maxOpenConns)
	sqldb.SetMaxIdleConns(maxIdleConns)
	sqldb.SetConnMaxLifetime(connMaxLifetime)

	db := bun.NewDB(sqldb, pgdialect.New())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return db, nil
}

// OpenFromEnv reads environment variables and opens a DB using reasonable
// defaults. Priority: DATABASE_DSN env var; otherwise build DSN from
// DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME.
// Returns an error if required environment variables are missing or ping fails.
func OpenFromEnv() (*bun.DB, error) {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		host := os.Getenv("DB_HOST")
		port := os.Getenv("DB_PORT")
		user := os.Getenv("DB_USER")
		password := os.Getenv("DB_PASSWORD")
		name := os.Getenv("DB_NAME")

		if host == "" || port == "" || user == "" || name == "" {
			return nil, fmt.Errorf("DATABASE_DSN or DB_HOST/DB_PORT/DB_USER/DB_NAME must be set")
		}

		// Build a minimal TLS-disabled DSN. If you need TLS, set DATABASE_DSN explicitly.
		dsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/%s", user, password, host, port, name)
	}

	// default pool settings
	return OpenPostgres(dsn, 10, 2, 30*time.Minute)
}

// Close closes the underlying bun DB and its driver.
func Close(db *bun.DB) error {
	if db == nil {
		return nil
	}
	return db.Close()
}
