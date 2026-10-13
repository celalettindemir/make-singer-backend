package deletionmark

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// liveRedis needs a local Redis and skips the test without one. It uses DB 11
// (handler 12, service 13, middleware 14, identity 15).
func liveRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 11})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

func TestKey_FormatUnchanged(t *testing.T) {
	if got := Key("u1"); got != "kimlik:silindi:u1" {
		t.Fatalf("key = %q", got)
	}
}

func TestTTL_EqualsJobRecordTTL(t *testing.T) {
	if TTL != 24*time.Hour {
		t.Fatalf("mark lifetime = %v, want 24 hours", TTL)
	}
}

func TestRedisChecker_ReadsMark(t *testing.T) {
	rdb := liveRedis(t)
	d := NewRedisChecker(rdb)
	if err := rdb.Set(context.Background(), Key("u1"), "1", time.Minute).Err(); err != nil {
		t.Fatalf("could not write mark: %v", err)
	}
	if s, err := d.IsDeleted(context.Background(), "u1"); err != nil || !s {
		t.Fatalf("u1 should appear deleted: %v, %v", s, err)
	}
	if s, err := d.IsDeleted(context.Background(), "u2"); err != nil || s {
		t.Fatalf("u2 should not appear deleted: %v, %v", s, err)
	}
}

func TestRedisChecker_ErrorsWithoutRedis(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	if _, err := NewRedisChecker(rdb).IsDeleted(context.Background(), "u1"); err == nil {
		t.Fatal("expected an error when Redis is down")
	}
}
