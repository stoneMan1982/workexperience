package models

import "github.com/uptrace/bun"

type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	ID            int64
	Name          string
	// bun:rel:has-many 表示一对多
	Orders []*Order `bun:"rel:has-many,join:id=user_id"`
}
