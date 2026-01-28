package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"github.com/makeasinger/api/pkg/response"
)

type RateLimiter struct {
	redis *redis.Client
}

func NewRateLimiter(redisClient *redis.Client) *RateLimiter {
	return &RateLimiter{redis: redisClient}
}

// Limit creates a rate limiting middleware
func (rl *RateLimiter) Limit(keyPrefix string, maxRequests int, window time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			return c.Next() // Skip rate limiting if no user (auth middleware should catch this)
		}

		key := fmt.Sprintf("ratelimit:%s:%s", keyPrefix, userID)
		ctx := context.Background()

		// Increment counter
		count, err := rl.redis.Incr(ctx, key).Result()
		if err != nil {
			// If Redis fails, allow the request but log the error
			return c.Next()
		}

		// Set expiration on first request
		if count == 1 {
			rl.redis.Expire(ctx, key, window)
		}

		if count > int64(maxRequests) {
			// Get TTL for retry-after header
			ttl, _ := rl.redis.TTL(ctx, key).Result()
			c.Set("Retry-After", fmt.Sprintf("%d", int(ttl.Seconds())))
			return response.RateLimited(c)
		}

		// Add rate limit headers
		c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", maxRequests))
		c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", maxRequests-int(count)))

		return c.Next()
	}
}

// LyricsLimit returns a rate limiter for lyrics endpoints (30 req/min)
func (rl *RateLimiter) LyricsLimit(maxPerMin int) fiber.Handler {
	return rl.Limit("lyrics", maxPerMin, time.Minute)
}

// RenderLimit returns a rate limiter for render endpoints (5 req/hour)
func (rl *RateLimiter) RenderLimit(maxPerHour int) fiber.Handler {
	return rl.Limit("render", maxPerHour, time.Hour)
}

// MasterLimit returns a rate limiter for master endpoints (10 req/hour)
func (rl *RateLimiter) MasterLimit(maxPerHour int) fiber.Handler {
	return rl.Limit("master", maxPerHour, time.Hour)
}

// ExportLimit returns a rate limiter for export endpoints (20 req/hour)
func (rl *RateLimiter) ExportLimit(maxPerHour int) fiber.Handler {
	return rl.Limit("export", maxPerHour, time.Hour)
}

// UploadLimit returns a rate limiter for upload endpoints (50 req/hour)
func (rl *RateLimiter) UploadLimit(maxPerHour int) fiber.Handler {
	return rl.Limit("upload", maxPerHour, time.Hour)
}
