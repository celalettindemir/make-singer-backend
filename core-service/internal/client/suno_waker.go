package client

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// SunoWakeChannel is the Redis channel the Suno callback publishes to when a
// task changed. The message is only a nudge; the worker re-checks the status.
func SunoWakeChannel(taskID string) string { return "suno:uyan:" + taskID }

// Waker tells a waiting poll that Suno called back. Wait returns a channel
// that receives on a wake and a func that unsubscribes.
type Waker interface {
	Wait(ctx context.Context, taskID string) (<-chan struct{}, func())
}

type redisWaker struct{ rdb *redis.Client }

// NewRedisWaker wakes on a PUBLISH to suno:uyan:<taskId>.
func NewRedisWaker(rdb *redis.Client) Waker { return &redisWaker{rdb: rdb} }

func (w *redisWaker) Wait(ctx context.Context, taskID string) (<-chan struct{}, func()) {
	out := make(chan struct{}, 1)
	sub := w.rdb.Subscribe(ctx, SunoWakeChannel(taskID))
	// Confirm the subscription before the caller polls, so a callback fired
	// right after the poll cannot be missed. On failure the plain interval
	// still applies.
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return out, func() {}
	}
	done := make(chan struct{})
	go func() {
		in := sub.Channel()
		for {
			select {
			case <-done:
				return
			case _, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out, func() {
		close(done)
		_ = sub.Close()
	}
}
