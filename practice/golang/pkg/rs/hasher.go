package rs

import (
	"fmt"

	"github.com/cespare/xxhash/v2"
)

// Hasher maps a numeric id into a partition index [0, parts).
// Implementations must be deterministic and fast.
type Hasher interface {
	PartitionID(id int64, parts int) int
	Name() string
}

// ModHasher uses a simple modulo of the numeric id.
type ModHasher struct{}

func (ModHasher) PartitionID(id int64, parts int) int {
	if parts <= 0 {
		return 0
	}
	return int(id % int64(parts))
}

func (ModHasher) Name() string { return "mod" }

// XXHash64Hasher hashes the decimal string form of id with xxhash64 then mods by parts.
type XXHash64Hasher struct{}

func (XXHash64Hasher) PartitionID(id int64, parts int) int {
	if parts <= 0 {
		return 0
	}
	key := fmt.Sprintf("%d", id)
	h := xxhash.Sum64String(key)
	return int(h % uint64(parts))
}

func (XXHash64Hasher) Name() string { return "xxhash64" }

// NewHasherFromString returns a hasher by name: "mod" or "xxhash64".
func NewHasherFromString(name string) Hasher {
	switch name {
	case "xxhash64":
		return XXHash64Hasher{}
	default:
		return ModHasher{}
	}
}
