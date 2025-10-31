package main
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
)

func main() {
	var (
		redisAddr   string
		redisPass   string
		redisDB     int
		streamBase  string
		partitions  int
		count       int
		startID     int64
		intervalMs  int
		payload     string
	)

	flag.StringVar(&redisAddr, "redis-addr", "127.0.0.1:6379", "redis address host:port")
	flag.StringVar(&redisPass, "redis-password", "", "redis password")
	flag.IntVar(&redisDB, "redis-db", 0, "redis db index")
	flag.StringVar(&streamBase, "stream", "mystream", "stream base name (messages go to stream:{shard})")
	flag.IntVar(&partitions, "partitions", 3, "number of shards/partitions")
	flag.IntVar(&count, "count", 100, "number of messages to produce (<=0 for infinite)")
	flag.Int64Var(&startID, "start-id", 1, "starting numeric message id")
	flag.IntVar(&intervalMs, "interval-ms", 100, "interval between messages in milliseconds")
	flag.StringVar(&payload, "payload", "", "optional payload template; supports {id} placeholder")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPass, DB: redisDB})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("redis ping failed: %v", err)
		os.Exit(1)
	}

	log.Printf("producer started: streamBase=%s partitions=%d count=%d", streamBase, partitions, count)
	id := startID
	sent := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("producer stopping; sent=%d", sent)
			return
		default:
		}

		shard := int(id % int64(partitions))
		stream := fmt.Sprintf("%s:%d", streamBase, shard)
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
		res := rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values})
		if _, err := res.Result(); err != nil {
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
		time.Sleep(time.Duration(intervalMs) * time.Millisecond)
	}
}
