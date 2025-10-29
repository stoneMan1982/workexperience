package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stoneMan1982/workexperience/practice/golang/pkg/config"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/schema"
	_ "modernc.org/sqlite"
)

// OpenFromConfig constructs a DB connection from DatabaseConfig.
// Supports dialects: postgres, mysql, sqlite3.
func OpenFromConfig(c *config.DatabaseConfig) (*bun.DB, error) {
	if c == nil {
		return nil, fmt.Errorf("database config is nil")
	}
	dialect := strings.ToLower(strings.TrimSpace(c.Dialect))

	var (
		driver string
		dsn    string
		dial   schema.Dialect
	)

	switch dialect {
	case "postgres", "postgresql", "pg":
		driver = "pgx"
		// Default sslmode=disable unless provided via environment overrides.
		dsn = fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
			urlUser(c.User), urlPassword(c.Password), c.Host, c.Port, c.DBName)
		dial = pgdialect.New()
	case "mysql":
		driver = "mysql"
		// Use utf8mb4 and parseTime for time scan.
		// Example: user:pass@tcp(host:port)/dbname?parseTime=true&charset=utf8mb4&loc=Local
		addr := fmt.Sprintf("tcp(%s:%d)", c.Host, c.Port)
		dsn = fmt.Sprintf("%s:%s@%s/%s?parseTime=true&charset=utf8mb4&loc=Local",
			c.User, c.Password, addr, c.DBName)
		dial = mysqldialect.New()
	case "sqlite", "sqlite3":
		driver = "sqlite"
		// Use DBName as file path (":memory:" allowed)
		if strings.TrimSpace(c.DBName) == "" {
			return nil, fmt.Errorf("sqlite requires dbname as file path or :memory:")
		}
		dsn = c.DBName
		dial = sqlitedialect.New()
	default:
		return nil, fmt.Errorf("unsupported dialect: %s", c.Dialect)
	}

	sqldb, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}

	// Pool settings with defaults
	maxOpen := c.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	maxIdle := c.MaxIdleConns
	if maxIdle < 0 {
		maxIdle = 0
	}
	sqldb.SetMaxOpenConns(maxOpen)
	sqldb.SetMaxIdleConns(maxIdle)
	if c.ConnMaxLifetime > 0 {
		sqldb.SetConnMaxLifetime(time.Duration(c.ConnMaxLifetime))
	} else {
		sqldb.SetConnMaxLifetime(30 * time.Minute)
	}
	if c.ConnMaxIdleTime > 0 {
		sqldb.SetConnMaxIdleTime(time.Duration(c.ConnMaxIdleTime))
	}

	b := bun.NewDB(sqldb, dial)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.PingContext(ctx); err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return b, nil
}

// OpenFromConfigFile loads config from file and opens DB using the database section.
func OpenFromConfigFile(path string) (*bun.DB, error) {
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	return OpenFromConfig(&cfg.Database)
}

// OpenFromEnv is kept for compatibility; it builds a PostgreSQL DSN from env vars
// or uses DATABASE_DSN. Prefer using OpenFromConfig/OpenFromConfigFile.
func OpenFromEnv() (*bun.DB, error) {
	if dsn := os.Getenv("DATABASE_DSN"); dsn != "" {
		// Default pool values
		return openPostgresWithDSN(dsn, 10, 2, 30*time.Minute)
	}
	host := os.Getenv("DB_HOST")
	portStr := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	name := os.Getenv("DB_NAME")
	if host == "" || portStr == "" || user == "" || name == "" {
		return nil, fmt.Errorf("DATABASE_DSN or DB_HOST/DB_PORT/DB_USER/DB_NAME must be set")
	}
	port, _ := strconv.Atoi(portStr)
	dbc := &config.DatabaseConfig{
		Dialect:         "postgres",
		Host:            host,
		Port:            port,
		User:            user,
		Password:        password,
		DBName:          name,
		MaxOpenConns:    10,
		MaxIdleConns:    2,
		ConnMaxLifetime: config.Duration(30 * time.Minute),
	}
	return OpenFromConfig(dbc)
}

// Close closes the underlying bun DB and its driver.
func Close(db *bun.DB) error {
	if db == nil {
		return nil
	}
	return db.Close()
}

// openPostgresWithDSN is a small helper for env compatibility path.
func openPostgresWithDSN(dsn string, maxOpenConns, maxIdleConns int, connMaxLifetime time.Duration) (*bun.DB, error) {
	sqldb, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}
	sqldb.SetMaxOpenConns(maxOpenConns)
	sqldb.SetMaxIdleConns(maxIdleConns)
	sqldb.SetConnMaxLifetime(connMaxLifetime)
	b := bun.NewDB(sqldb, pgdialect.New())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.PingContext(ctx); err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return b, nil
}

// urlUser and urlPassword provide minimal escaping for DSN construction.
func urlUser(u string) string     { return strings.ReplaceAll(u, "@", "%40") }
func urlPassword(p string) string { return strings.ReplaceAll(p, "@", "%40") }
