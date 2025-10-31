package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	redis "github.com/redis/go-redis/v9"
	rs "github.com/stoneMan1982/workexperience/practice/golang/pkg/rs"
)

func main() {
	var (
		redisAddr  string
		redisPass  string
		redisDB    int
		streamBase string
		shard      int
		nodeID     int
		totalNodes int
		group      string
		consumer   string
		blockMs    int
		batch      int64
	)

	flag.StringVar(&redisAddr, "redis-addr", "127.0.0.1:6379", "redis address host:port")
	flag.StringVar(&redisPass, "redis-password", "", "redis password")
	flag.IntVar(&redisDB, "redis-db", 0, "redis db index")
	flag.StringVar(&streamBase, "stream", "mystream", "stream base name")
	flag.IntVar(&shard, "shard", -1, "shard index to consume (e.g., 0..2); if <0, derive from node-id")
	flag.IntVar(&nodeID, "node-id", 1, "this node id (1..N)")
	flag.IntVar(&totalNodes, "total-nodes", 3, "cluster total nodes; used for validation")
	flag.StringVar(&group, "group", "g", "consumer group name")
	flag.StringVar(&consumer, "consumer", "", "consumer name (default: hostname-pid)")
	flag.IntVar(&blockMs, "block-ms", 5000, "XREADGROUP block timeout in ms")
	flag.Int64Var(&batch, "batch", 100, "max messages per read")
	flag.Parse()

	if consumer == "" {
		h, _ := os.Hostname()
		consumer = fmt.Sprintf("%s-%d", h, os.Getpid())
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPass, DB: redisDB})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis ping failed: %v", err)
	}

	// determine shard from node-id (node-id starts from 1)
	if shard < 0 {
		if nodeID < 1 {
			log.Fatalf("invalid node-id: %d (must be >= 1)", nodeID)
		}
		shard = nodeID - 1
	}
	if totalNodes > 0 {
		if nodeID < 1 || nodeID > totalNodes {
			log.Fatalf("invalid node-id: %d (must be in 1..%d)", nodeID, totalNodes)
		}
		if shard < 0 || shard >= totalNodes {
			log.Fatalf("invalid derived shard: %d (total-nodes=%d)", shard, totalNodes)
		}
	}

	c := rs.NewShardedConsumer(rdb, streamBase, shard, group, consumer,
		rs.WithBatch(batch),
		rs.WithBlock(time.Duration(blockMs)*time.Millisecond),
	)

	log.Printf("consumer started: stream=%s:%d group=%s consumer=%s", streamBase, shard, group, consumer)
	_ = c.Run(ctx, func(ctx context.Context, msg redis.XMessage) error {
		return handleMessage(ctx, rdb, fmt.Sprintf("%s:%d", streamBase, shard), group, msg)
	})
}

func handleMessage(ctx context.Context, rdb *redis.Client, stream, group string, msg redis.XMessage) error {
	// Determine message kind
	var kind string
	if v, ok := msg.Values["kind"]; ok {
		switch t := v.(type) {
		case string:
			kind = t
		case []byte:
			kind = string(t)
		default:
			kind = fmt.Sprintf("%v", v)
		}
	}

	if kind != "BatchMemberReadTask" {
		// Fallback: log and ack other kinds
		log.Printf("processing stream=%s id=%s kind=%s values=%v", stream, msg.ID, kind, msg.Values)
		return nil
	}

	// Extract payload
	var raw []byte
	if v, ok := msg.Values["payload"]; ok {
		switch t := v.(type) {
		case string:
			raw = []byte(t)
		case []byte:
			raw = t
		default:
			// Unknown type; keep pending for inspection
			return fmt.Errorf("unexpected payload type %T", v)
		}
	} else {
		return fmt.Errorf("missing payload field")
	}

	// Decode batch payload
	var batchMsg rs.BatchMemberReadTask
	if err := json.Unmarshal(raw, &batchMsg); err != nil {
		return fmt.Errorf("unmarshal BatchMemberReadTask failed: %w", err)
	}

	// Process each task in the batch. Replace with real logic.
	log.Printf("batch received: stream=%s id=%s batch_id=%s tasks=%d", stream, msg.ID, batchMsg.ID, len(batchMsg.Tasks))
	for i, t := range batchMsg.Tasks {
		log.Printf("  task[%d]: id=%s message_id=%d channel_id=%s channel_type=%d uid=%s from_uid=%s login_uid=%s req_channel_id=%s req_channel_type=%d message_seq=%d message_id_str=%s",
			i, t.ID, t.MessageID, t.ChannelID, t.ChannelType, t.UID, t.FromUID, t.LoginUID, t.ReqChannelID, t.ReqChannelType, t.MessageSeq, t.MessageIDStr)
	}

	return nil
}
