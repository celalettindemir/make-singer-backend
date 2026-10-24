package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

func TestHub_FanOutAcrossInstances(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 11})
	pctx, pcancel := context.WithTimeout(context.Background(), time.Second)
	defer pcancel()
	if err := rdb.Ping(pctx).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	defer rdb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hubA, hubB := NewHub(jobs.NewRedisBus(rdb)), NewHub(jobs.NewRedisBus(rdb))
	go hubA.Run(ctx)
	go hubB.Run(ctx)

	jobID := fmt.Sprintf("hub-test-%d", time.Now().UnixNano())
	client := &Client{JobID: jobID, Send: make(chan []byte, 16)}
	hubB.Register(client)

	want, _ := json.Marshal(model.WSCompleteMessage{Type: model.WSMessageTypeComplete, JobID: jobID, Result: map[string]string{"a": "b"}})
	// The subscription starts asynchronously; re-send until it is live.
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		hubA.BroadcastComplete(jobID, map[string]string{"a": "b"})
		select {
		case got := <-client.Send:
			if string(got) != string(want) {
				t.Fatalf("got %s, want %s", got, want)
			}
			return
		case <-tick.C:
		case <-deadline:
			t.Fatal("client on hub B never received the broadcast from hub A")
		}
	}
}
