package rs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// Producer publishes messages to sharded Redis Streams: streamBase:{shard}.
// Shard = hasher.PartitionID(id, parts).
// Values map will be extended to include the "id" when absent.
type Producer struct {
	rdb        *redis.Client
	streamBase string
	parts      int
	hasher     Hasher
}

type ProducerOption func(*Producer)

func WithHasher(h Hasher) ProducerOption {
	return func(p *Producer) { p.hasher = h }
}

// NewProducer constructs a Producer. parts must be > 0.
func NewProducer(rdb *redis.Client, streamBase string, parts int, opts ...ProducerOption) *Producer {
	p := &Producer{
		rdb:        rdb,
		streamBase: streamBase,
		parts:      parts,
		hasher:     ModHasher{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// StreamName returns the concrete stream name for the given id.
func (p *Producer) StreamName(id int64) string {
	shard := p.hasher.PartitionID(id, p.parts)
	return fmt.Sprintf("%s:%d", p.streamBase, shard)
}

// StreamNameByShard returns streamBase:{shard} directly.
func (p *Producer) StreamNameByShard(shard int) string {
	return fmt.Sprintf("%s:%d", p.streamBase, shard)
}

// Publish adds a message with partitioned stream based on id.
func (p *Producer) Publish(ctx context.Context, id int64, values map[string]any) (string, error) {
	if values == nil {
		values = map[string]any{}
	}
	if _, ok := values["id"]; !ok {
		values["id"] = id
	}
	stream := p.StreamName(id)
	return p.rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
}

// PublishToShard writes values to the concrete shard stream.
func (p *Producer) PublishToShard(ctx context.Context, shard int, values map[string]any) (string, error) {
	stream := p.StreamNameByShard(shard)
	return p.rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
}

// PublishBatchMemberReadTasks groups tasks by shard (hash of MessageID) and publishes
// one BatchMemberReadTask per shard. Returns a map of shard->streamEntryID.
func (p *Producer) PublishBatchMemberReadTasks(ctx context.Context, batchID string, tasks []*MemberReadTask) (map[int]string, error) {
	byShard := make(map[int][]*MemberReadTask)
	for _, t := range tasks {
		shard := p.hasher.PartitionID(t.MessageID, p.parts)
		byShard[shard] = append(byShard[shard], t)
	}
	res := make(map[int]string)
	now := time.Now().UnixMilli()
	for shard, tsks := range byShard {
		b := &BatchMemberReadTask{ID: batchID, Tasks: tsks}
		payload, err := json.Marshal(b)
		if err != nil {
			return res, err
		}
		values := map[string]any{
			"kind":     "BatchMemberReadTask",
			"batch_id": batchID,
			"payload":  string(payload),
			"ts":       now,
		}
		id, err := p.PublishToShard(ctx, shard, values)
		if err != nil {
			return res, err
		}
		res[shard] = id
	}
	return res, nil
}
