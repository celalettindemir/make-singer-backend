package handler

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// liveRedis needs a local Redis; the test is skipped without one. It uses DB 12
// (service 13, middleware 14, identity 15).
func liveRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 12})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}
