package rs

import (
	"context"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// Handler processes a single message; return error to leave it pending for retry/claim.
type Handler func(ctx context.Context, msg redis.XMessage) error

// Consumer reads from a concrete stream (already sharded), within a consumer group.
// It handles group creation, reading, acking, and periodic claim of stale messages.
type Consumer struct {
	rdb            *redis.Client
	stream         string
	group          string
	consumer       string
	batch          int64
	block          time.Duration
	claimEvery     time.Duration
	claimMinIdle   time.Duration
	claimScanCount int64
}

type ConsumerOption func(*Consumer)

func WithBatch(n int64) ConsumerOption              { return func(c *Consumer) { c.batch = n } }
func WithBlock(d time.Duration) ConsumerOption      { return func(c *Consumer) { c.block = d } }
func WithClaimEvery(d time.Duration) ConsumerOption { return func(c *Consumer) { c.claimEvery = d } }
func WithClaimMinIdle(d time.Duration) ConsumerOption {
	return func(c *Consumer) { c.claimMinIdle = d }
}
func WithClaimScanCount(n int64) ConsumerOption { return func(c *Consumer) { c.claimScanCount = n } }

// NewShardedConsumer builds a consumer for streamBase:shard.
func NewShardedConsumer(rdb *redis.Client, streamBase string, shard int, group, consumer string, opts ...ConsumerOption) *Consumer {
	stream := fmt.Sprintf("%s:%d", streamBase, shard)
	c := &Consumer{
		rdb:            rdb,
		stream:         stream,
		group:          group,
		consumer:       consumer,
		batch:          100,
		block:          5 * time.Second,
		claimEvery:     30 * time.Second,
		claimMinIdle:   60 * time.Second,
		claimScanCount: 50,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// EnsureGroup creates the consumer group and stream if missing (MKSTREAM).
func (c *Consumer) EnsureGroup(ctx context.Context) error {
	return c.rdb.XGroupCreateMkStream(ctx, c.stream, c.group, "$").Err()
}

// Run starts the read/ack/claim loop until ctx is done.
func (c *Consumer) Run(ctx context.Context, handler Handler) error {
	// try to create group (ignore already-exists)
	if err := c.EnsureGroup(ctx); err != nil {
		if err.Error() != "BUSYGROUP Consumer Group name already exists" {
			// log but continue; some environments pre-create group
		}
	}

	lastClaim := time.Now()
	for ctx.Err() == nil {
		streams, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.group,
			Consumer: c.consumer,
			Streams:  []string{c.stream, ">"},
			Count:    c.batch,
			Block:    c.block,
		}).Result()
		if err != nil && err != redis.Nil {
			// log and continue
			continue
		}
		if len(streams) > 0 {
			for _, s := range streams {
				for _, msg := range s.Messages {
					if err := handler(ctx, msg); err != nil {
						// leave pending for retry/claim
						continue
					}
					_ = c.rdb.XAck(ctx, c.stream, c.group, msg.ID).Err()
				}
			}
		}

		// periodic claim of stale messages
		if time.Since(lastClaim) >= c.claimEvery {
			lastClaim = time.Now()
			pending, err := c.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
				Stream: c.stream,
				Group:  c.group,
				Start:  "-",
				End:    "+",
				Count:  c.claimScanCount,
			}).Result()
			if err == nil {
				var ids []string
				for _, p := range pending {
					if p.Idle >= c.claimMinIdle {
						ids = append(ids, p.ID)
					}
				}
				if len(ids) > 0 {
					_, _ = c.rdb.XClaim(ctx, &redis.XClaimArgs{
						Stream:   c.stream,
						Group:    c.group,
						Consumer: c.consumer,
						MinIdle:  c.claimMinIdle,
						Messages: ids,
					}).Result()
				}
			}
		}
	}
	return ctx.Err()
}
