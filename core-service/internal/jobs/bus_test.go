package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// busRedis needs a local Redis (DB 11); skipped when none is reachable.
// Pub/sub is not DB-scoped, so tests use unique owner and job ids.
func busRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 11})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func uniqueID(t *testing.T, p string) string {
	return fmt.Sprintf("%s-%s-%d", p, t.Name(), time.Now().UnixNano())
}

func TestRedisBus_PublishReachesRun(t *testing.T) {
	rdb := busRedis(t)
	sub, pub := NewRedisBus(rdb), NewRedisBus(rdb)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Event, 8)
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, func(e Event) { got <- e }) }()

	owner := uniqueID(t, "owner")
	ev := Event{JobID: "j1", OwnerID: owner, Seq: 1, Kind: KindQueued,
		Data: json.RawMessage(`{"a":1}`), At: time.Now().UTC().Truncate(time.Millisecond)}
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		pub.Publish(ctx, []Event{ev})
		select {
		case e := <-got:
			if e.JobID != "j1" || e.OwnerID != owner || e.Seq != 1 || e.Kind != KindQueued || string(e.Data) != `{"a":1}` {
				t.Fatalf("unexpected event %+v", e)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("Run did not return after cancel")
			}
			return
		case <-tick.C:
		case <-deadline:
			t.Fatal("event not delivered")
		}
	}
}

func TestRedisBus_LegacyChannel(t *testing.T) {
	rdb := busRedis(t)
	sub, pub := NewRedisBus(rdb), NewRedisBus(rdb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type msg struct {
		id string
		b  []byte
	}
	got := make(chan msg, 8)
	done := make(chan error, 1)
	go func() { done <- sub.RunLegacy(ctx, func(id string, b []byte) { got <- msg{id, b} }) }()

	jobID := uniqueID(t, "job")
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		pub.PublishLegacy(ctx, jobID, []byte(`{"x":true}`))
		select {
		case m := <-got:
			if m.id != jobID || string(m.b) != `{"x":true}` {
				t.Fatalf("unexpected legacy message %+v", m)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("RunLegacy did not return after cancel")
			}
			return
		case <-tick.C:
		case <-deadline:
			t.Fatal("legacy message not delivered")
		}
	}
}
