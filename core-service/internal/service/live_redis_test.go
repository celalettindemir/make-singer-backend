package service

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// liveRedisAddr: CI runs the redis:7-alpine service on localhost:6379.
const liveRedisAddr = "localhost:6379"

// liveRedis needs a local Redis; the test is skipped if there is none. DB 13
// is used (middleware 14, identity 15, handler 12).
func liveRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: liveRedisAddr, DB: 13})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}
