// Package deletionmark holds the deleted-account marker. The HTTP layer
// (middleware), the account service and the queue workers all read the same
// marker, so it lives in a separate package that belongs to none of them.
package deletionmark

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// A key under prefix marks a user whose account has been deleted.
const prefix = "kimlik:silindi:"

// TTL is the marker's lifetime, equal to the job record (job:{id}) lifetime.
// Any job that is queued or running when the deletion happens sees that its
// owner was deleted for this long. User IDs are UUIDs and are never reused,
// so the long lifetime cannot affect another user.
const TTL = 24 * time.Hour

// Key is the Redis key of the user's deleted marker.
func Key(userID string) string { return prefix + userID }

// Checker reports whether a user's account has been deleted.
type Checker interface {
	IsDeleted(ctx context.Context, userID string) (bool, error)
}

// RedisChecker reads the marker from Redis with EXISTS.
type RedisChecker struct{ rdb *redis.Client }

// NewRedisChecker returns a Checker backed by rdb.
func NewRedisChecker(rdb *redis.Client) *RedisChecker { return &RedisChecker{rdb: rdb} }

// IsDeleted reports whether the marker for userID exists.
func (c *RedisChecker) IsDeleted(ctx context.Context, userID string) (bool, error) {
	n, err := c.rdb.Exists(ctx, Key(userID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
