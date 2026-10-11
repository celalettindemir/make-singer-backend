package service

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// canliRedisAdresi: CI redis:7-alpine servisini localhost:6379'da kosar.
const canliRedisAdresi = "localhost:6379"

// canliRedis, yerel Redis ister; yoksa test atlanir. DB 13 kullanilir
// (middleware 14, kimlik 15, handler 12).
func canliRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: canliRedisAdresi, DB: 13})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}
