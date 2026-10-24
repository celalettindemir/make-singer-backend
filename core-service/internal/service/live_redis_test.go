package service

import (
	"context"
	"github.com/makeasinger/api/internal/jobs"
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

// Job ids are uuids in the store.
const (
	testJobID       = "6f1c2a10-0000-4000-8000-000000000001"
	testJobID2      = "6f1c2a10-0000-4000-8000-000000000002"
	testLegacyJobID = "6f1c2a10-0000-4000-8000-0000000000ee"
	testMasterJobID = "6f1c2a10-0000-4000-8000-0000000000a1"
)

// jobCount is the number of jobs stored for the usual test user.
func jobCount(t *testing.T, repo jobs.Repo) int {
	t.Helper()
	list, err := repo.List(context.Background(), "kullanici-a", time.Time{}, 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return len(list)
}
