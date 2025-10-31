package main
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	redis "github.com/redis/go-redis/v9"
)

func main() {
	var (
		redisAddr  string
		redisPass  string
		redisDB    int
		streamBase string
		shard      int
		group      string
		consumer   string
		blockMs    int
		batch      int64
	)

	flag.StringVar(&redisAddr, "redis-addr", "127.0.0.1:6379", "redis address host:port")
	flag.StringVar(&redisPass, "redis-password", "", "redis password")
	flag.IntVar(&redisDB, "redis-db", 0, "redis db index")
	flag.StringVar(&streamBase, "stream", "mystream", "stream base name")
	flag.IntVar(&shard, "shard", 0, "shard index to consume (e.g., 0..2)")
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

	stream := fmt.Sprintf("%s:%d", streamBase, shard)
	// ensure group exists
	if err := rdb.XGroupCreateMkStream(ctx, stream, group, "$").Err(); err != nil {
		if err.Error() != "BUSYGROUP Consumer Group name already exists" {
			log.Printf("XGROUP CREATE error: %v (if already exists, it's fine)", err)
		}
	}

	log.Printf("consumer started: stream=%s group=%s consumer=%s", stream, group, consumer)
	lastClaim := time.Now()
	for ctx.Err() == nil {
		// read new messages
		streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    batch,
			Block:    time.Duration(blockMs) * time.Millisecond,
		}).Result()
		if err != nil && err != redis.Nil {
			log.Printf("XREADGROUP error: %v", err)
			continue
		}
		if len(streams) > 0 {
			for _, s := range streams {
				for _, msg := range s.Messages {
					// process
					if err := handleMessage(ctx, rdb, stream, group, msg); err != nil {
						log.Printf("handle error: %v (id=%s)", err, msg.ID)
						// decide whether to NACK by not acking; it will remain pending for retry/claim
						continue
					}
					// ack
					if err := rdb.XAck(ctx, stream, group, msg.ID).Err(); err != nil {
						log.Printf("XACK error: %v", err)
					}
				}
			}
		}

		// periodically attempt to claim long-pending messages for this consumer
		if time.Since(lastClaim) > 30*time.Second {
			lastClaim = time.Now()
			pending, err := rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
				Stream: stream,
				Group:  group,
				Start:  "-",
				End:    "+",
				Count:  50,
			}).Result()
			if err == nil {
				var ids []string
				for _, p := range pending {
					if p.Idle > 60*time.Second {
						ids = append(ids, p.ID)
					}
				}
				if len(ids) > 0 {
					res, err := rdb.XClaim(ctx, &redis.XClaimArgs{
						Stream:   stream,
						Group:    group,
						Consumer: consumer,
						MinIdle:  60 * time.Second,
						Messages: ids,
					}).Result()
					if err != nil {
						log.Printf("XCLAIM error: %v", err)
					} else {
						log.Printf("claimed %d stale messages", len(res))
					}
				}
			}
		}
	}
}

func handleMessage(ctx context.Context, rdb *redis.Client, stream, group string, msg redis.XMessage) error {
	// Example processing: log and simulate work
	id := msg.Values["id"]
	payload := msg.Values["payload"]
	log.Printf("processing stream=%s id=%v payload=%v", stream, id, payload)
	return nil
}
