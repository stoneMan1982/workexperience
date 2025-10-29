package dbx

import (
	"context"

	"github.com/uptrace/bun"
)

// UpdateWithVersion performs an optimistic-lock update using a version column.
// It executes:
//
//	UPDATE <table>
//	SET <setClauses>, version = version + 1
//	WHERE <wherePK> AND version = <oldVersion>
//
// Caller is responsible for passing correct table/model and where condition.
// Returns ok=true if exactly one row was updated, ok=false if version conflict.
func UpdateWithVersion(ctx context.Context, db bun.IDB, model any, oldVersion any, setClause string, wherePK string, args ...any) (bool, error) {
	q := db.NewUpdate().Model(model).
		Set(setClause).
		Set("version = version + 1").
		Where(wherePK).
		Where("version = ?", oldVersion)
	res, err := q.Exec(ctx, args...)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	return false, nil
}
