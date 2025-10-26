package models

import "github.com/uptrace/bun"

type Order struct {
	bun.BaseModel `bun:"table:orders,alias:o"`
	ID            int64
	UserID        int64
	Amount        int64
}
