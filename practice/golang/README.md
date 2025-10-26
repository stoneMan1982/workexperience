# Golang practice: DB connection with Uptrace Bun

This module shows how to connect to PostgreSQL using Bun (Uptrace) + pgx.

## Configure environment

Set either a full DSN via `DATABASE_DSN` or individual parts:

- DB_HOST
- DB_PORT
- DB_USER
- DB_PASSWORD (optional)
- DB_NAME

Examples:

```sh
# Full DSN (recommended for SSL/TLS and options)
export DATABASE_DSN="postgresql://user:pass@localhost:5432/app?sslmode=disable"

# Or individual variables (non-SSL by default)
export DB_HOST=localhost
export DB_PORT=5432
export DB_USER=postgres
export DB_PASSWORD=postgres
export DB_NAME=app
```

## Run

```sh
cd practice/golang
go run .
```

Expected output on success:

```text
Hello, World!
DB connected (ping succeeded)
```

## Notes

- Connection pool defaults: max open 10, max idle 2, conn lifetime 30m. Adjust by calling `OpenPostgres` directly.
- To enable SQL logging, add a query hook:

```go
import (
    "github.com/uptrace/bun/extra/bunlog"
)
// after creating db (*bun.DB)
db.AddQueryHook(bunlog.NewQueryHook(bunlog.WithVerbose(true)))
```

- Models can use Bun tags for table/column mapping, for example:

```go
type User struct {
    bun.BaseModel `bun:"table:users,alias:u"`
    ID            int64  `bun:",pk,autoincrement"`
    Name          string `bun:",notnull"`
}
```

- For migrations, consider `github.com/uptrace/bun/migrate`. Bun docs: [https://bun.uptrace.dev/guide/](https://bun.uptrace.dev/guide/)
