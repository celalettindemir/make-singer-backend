package handler

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// canliRedis, yerel Redis ister; yoksa test atlanir. DB 12 kullanilir
// (service 13, middleware 14, kimlik 15).
func canliRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 12})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}
