package dbx

import (
	"context"

	"github.com/uptrace/bun"
)

// SelectForUpdateSkipLocked is a helper to select rows for processing with SKIP LOCKED pattern.
// It builds a query like:
//
//	SELECT <columns>
//	FROM <tableExpr>
//	WHERE <whereExpr>
//	ORDER BY <orderExpr>
//	LIMIT <limit>
//	FOR UPDATE SKIP LOCKED
//
// Note: This is Postgres-specific due to SKIP LOCKED.
func SelectForUpdateSkipLocked(ctx context.Context, tx bun.Tx, dest any, tableExpr, columns, whereExpr, orderExpr string, limit int, args ...any) error {
	q := tx.NewSelect().Model(dest).
		TableExpr(tableExpr)
	if columns != "" {
		q = q.ColumnExpr(columns)
	}
	if whereExpr != "" {
		q = q.Where(whereExpr, args...)
	}
	if orderExpr != "" {
		q = q.OrderExpr(orderExpr)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	q = q.For("UPDATE SKIP LOCKED")
	return q.Scan(ctx)
}
