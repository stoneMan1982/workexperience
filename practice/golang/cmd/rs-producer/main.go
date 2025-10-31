package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"strings"
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
		partitions int
		totalNodes int
		hashMode   string
		count      int
		startID    int64
		intervalMs int
		payload    string
		batchSize  int
	)

	flag.StringVar(&redisAddr, "redis-addr", "127.0.0.1:6379", "redis address host:port")
	flag.StringVar(&redisPass, "redis-password", "", "redis password")
	flag.IntVar(&redisDB, "redis-db", 0, "redis db index")
	flag.StringVar(&streamBase, "stream", "mystream", "stream base name (messages go to stream:{shard})")
	flag.IntVar(&partitions, "partitions", 3, "number of shards/partitions (alias of total-nodes)")
	flag.IntVar(&totalNodes, "total-nodes", 0, "total nodes for partitioning; if >0 overrides partitions")
	flag.StringVar(&hashMode, "hash-mode", "mod", "partition strategy: mod|xxhash64 (default: mod)")
	flag.IntVar(&count, "count", 100, "number of messages to produce (<=0 for infinite)")
	flag.Int64Var(&startID, "start-id", 1, "starting numeric message id")
	flag.IntVar(&intervalMs, "interval-ms", 100, "interval between messages in milliseconds")
	flag.StringVar(&payload, "payload", "", "optional payload template; supports {id} placeholder")
	flag.IntVar(&batchSize, "batch-size", 0, "when >0, produce BatchMemberReadTask by grouping tasks per shard")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPass, DB: redisDB})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("redis ping failed: %v", err)
		os.Exit(1)
	}

	parts := partitions
	if totalNodes > 0 {
		parts = totalNodes
	}
	if parts <= 0 {
		log.Printf("invalid partitions/total-nodes: %d", parts)
		os.Exit(1)
	}

	log.Printf("producer started: streamBase=%s partitions=%d hashMode=%s count=%d", streamBase, parts, hashMode, count)

	// build producer
	h := rs.NewHasherFromString(strings.ToLower(hashMode))
	p := rs.NewProducer(rdb, streamBase, parts, rs.WithHasher(h))
	id := startID
	sent := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("producer stopping; sent=%d", sent)
			return
		default:
		}

		if batchSize > 0 {
			// build synthetic tasks [id, id+batchSize)
			var tasks []*rs.MemberReadTask
			for k := 0; k < batchSize; k++ {
				mid := id + int64(k)
				msgPayload := payload
				if msgPayload == "" {
					msgPayload = fmt.Sprintf("hello-%d", mid)
				} else {
					msgPayload = strings.ReplaceAll(msgPayload, "{id}", fmt.Sprintf("%d", mid))
				}
				tasks = append(tasks, &rs.MemberReadTask{
					ID:             fmt.Sprintf("t-%d", mid),
					MessageID:      mid,
					MessageIDStr:   fmt.Sprintf("%d", mid),
					MessageSeq:     uint32(mid),
					UID:            fmt.Sprintf("u_%d", mid%10),
					FromUID:        fmt.Sprintf("fu_%d", mid%7),
					LoginUID:       fmt.Sprintf("lu_%d", mid%5),
					ChannelID:      fmt.Sprintf("c_%d", mid%3),
					ChannelType:    1,
					ReqChannelID:   fmt.Sprintf("rc_%d", mid%4),
					ReqChannelType: 1,
				})
			}
			batchID := fmt.Sprintf("b-%d", id)
			if _, err := p.PublishBatchMemberReadTasks(ctx, batchID, tasks); err != nil {
				log.Printf("PublishBatch failed: %v", err)
				// backoff and continue
				time.Sleep(500 * time.Millisecond)
				continue
			}
			sent += batchSize
			if count > 0 && sent >= count {
				log.Printf("done; sent=%d (batched)", sent)
				return
			}
			id += int64(batchSize)
		} else {
			msgPayload := payload
			if msgPayload == "" {
				msgPayload = fmt.Sprintf("hello-%d", id)
			} else {
				msgPayload = strings.ReplaceAll(msgPayload, "{id}", fmt.Sprintf("%d", id))
			}

			values := map[string]any{
				"id":      id,
				"payload": msgPayload,
				"rand":    rand.Int64(),
				"ts":      time.Now().UnixMilli(),
			}
			if _, err := p.Publish(ctx, id, values); err != nil {
				log.Printf("XADD failed: %v", err)
				// backoff a bit but continue
				time.Sleep(500 * time.Millisecond)
				continue
			}
			sent++
			if count > 0 && sent >= count {
				log.Printf("done; sent=%d", sent)
				return
			}
			id++
		}

		time.Sleep(time.Duration(intervalMs) * time.Millisecond)
	}
}
