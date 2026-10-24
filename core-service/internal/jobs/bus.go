package jobs

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	eventChannelPrefix  = "uretim:olay:"
	legacyChannelPrefix = "uretim:eski:"
)

// RedisBus fans job events out across pods over Redis pub/sub. It implements
// Publisher.
type RedisBus struct {
	rdb *redis.Client
}

func NewRedisBus(rdb *redis.Client) *RedisBus { return &RedisBus{rdb: rdb} }

// Publish sends each event on uretim:olay:<ownerId>. Failures are logged and
// never returned: the event is already durable and clients catch up by replay.
func (b *RedisBus) Publish(ctx context.Context, events []Event) {
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			log.Printf("Warning: could not encode job event (job %s): %v", e.JobID, err)
			continue
		}
		if err := b.rdb.Publish(ctx, eventChannelPrefix+e.OwnerID, raw).Err(); err != nil {
			log.Printf("Warning: could not publish job event (job %s): %v", e.JobID, err)
		}
	}
}

// PublishLegacy sends a legacy hub message on uretim:eski:<jobId>.
func (b *RedisBus) PublishLegacy(ctx context.Context, jobID string, msg []byte) {
	if err := b.rdb.Publish(ctx, legacyChannelPrefix+jobID, msg).Err(); err != nil {
		log.Printf("Warning: could not publish legacy message (job %s): %v", jobID, err)
	}
}

// Run delivers every event published on any pod until ctx is canceled.
func (b *RedisBus) Run(ctx context.Context, deliver func(Event)) error {
	return b.runPattern(ctx, eventChannelPrefix+"*", func(m *redis.Message) {
		var e Event
		if err := json.Unmarshal([]byte(m.Payload), &e); err != nil {
			log.Printf("Warning: bad job event on %s: %v", m.Channel, err)
			return
		}
		deliver(e)
	})
}

// RunLegacy delivers legacy hub messages until ctx is canceled.
func (b *RedisBus) RunLegacy(ctx context.Context, deliver func(jobID string, msg []byte)) error {
	return b.runPattern(ctx, legacyChannelPrefix+"*", func(m *redis.Message) {
		deliver(strings.TrimPrefix(m.Channel, legacyChannelPrefix), []byte(m.Payload))
	})
}

// runPattern holds one PSUBSCRIBE and re-subscribes with 1/2/4/8 s backoff
// (capped at 8 s) when the connection drops. It returns nil on ctx cancel.
func (b *RedisBus) runPattern(ctx context.Context, pattern string, handle func(*redis.Message)) error {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := b.subscribeOnce(ctx, pattern, handle)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		log.Printf("Warning: redis subscription %s lost, retrying in %s: %v", pattern, backoff, err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 8*time.Second {
			backoff *= 2
		}
	}
	return nil
}

func (b *RedisBus) subscribeOnce(ctx context.Context, pattern string, handle func(*redis.Message)) error {
	ps := b.rdb.PSubscribe(ctx, pattern)
	defer ps.Close()
	if _, err := ps.Receive(ctx); err != nil {
		return err
	}
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-ch:
			if !ok {
				return context.Canceled
			}
			handle(m)
		}
	}
}
