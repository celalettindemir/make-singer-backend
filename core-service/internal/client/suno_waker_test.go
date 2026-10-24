package client

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type chanWaker struct{ ch chan struct{} }

func (w *chanWaker) Wait(ctx context.Context, taskID string) (<-chan struct{}, func()) {
	return w.ch, func() {}
}

func TestSunoClient_WaitWakesBeforeInterval(t *testing.T) {
	var polls atomic.Int32
	times := make(chan time.Time, 8)
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		n := polls.Add(1)
		times <- time.Now()
		status := "PENDING"
		if n >= 2 {
			status = "SUCCESS"
		}
		writeEnvelope(w, 200, map[string]any{"status": status, "response": map[string]any{
			"sunoData": []map[string]any{{"id": "a1", "audio_url": "https://x/a.mp3", "duration": 10}}}})
	})
	waker := &chanWaker{ch: make(chan struct{}, 1)}
	c.WithWaker(waker)
	go func() { time.Sleep(50 * time.Millisecond); waker.ch <- struct{}{} }()

	start := time.Now()
	if _, err := c.WaitMusic(context.Background(), "t1", time.Minute, 5*time.Second); err != nil {
		t.Fatalf("WaitMusic: %v", err)
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("took %v, want the wake to cut the 1 min interval", d)
	}
	if polls.Load() != 2 {
		t.Fatalf("polls = %d, want 2", polls.Load())
	}
}

func TestSunoClient_NoWakerPollsAsBefore(t *testing.T) {
	var polls atomic.Int32
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		n := polls.Add(1)
		status := "PENDING"
		if n >= 2 {
			status = "SUCCESS"
		}
		writeEnvelope(w, 200, map[string]any{"status": status, "response": map[string]any{
			"sunoData": []map[string]any{{"id": "a1", "audio_url": "https://x/a.mp3", "duration": 10}}}})
	})
	start := time.Now()
	if _, err := c.WaitMusic(context.Background(), "t1", 30*time.Millisecond, 5*time.Second); err != nil {
		t.Fatalf("WaitMusic: %v", err)
	}
	if d := time.Since(start); d < 25*time.Millisecond {
		t.Fatalf("took %v, want the interval honored", d)
	}
	if polls.Load() != 2 {
		t.Fatalf("polls = %d, want 2", polls.Load())
	}
}

func TestRedisWaker_WakesOnPublish(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 11})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	defer rdb.Close()
	ch, cancel := NewRedisWaker(rdb).Wait(context.Background(), "w1")
	defer cancel()
	rdb.Publish(context.Background(), "suno:uyan:w1", "1")
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no wake")
	}
}
