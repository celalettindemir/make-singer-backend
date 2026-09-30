package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
	"github.com/redis/go-redis/v9"
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
			// Auth middleware userId'yi garanti eder; bos gelmesi bir
			// hatadir. Eskiden burada rate limit ATLANIYORDU (return
			// c.Next()), bu da kota bypass'i demekti.
			return response.Unauthorized(c, "Kimlik dogrulanamadi")
		}

		key := fmt.Sprintf("ratelimit:%s:%s", keyPrefix, userID)
		ctx := context.Background()

		// Increment counter
		count, err := rl.redis.Incr(ctx, key).Result()
		if err != nil {
			// Fail-closed: sayaci artiramadiysak kotanin asilip
			// asilmadigini BILEMEYIZ. Eskiden istek serbest gecirilirdi;
			// bu, Redis'i dusurebilen birine sinirsiz kota veriyordu.
			// Hata metni loglanmaz (baglanti dizgesi sir tasiyabilir).
			c.Set("Retry-After", "5")
			return response.Error(c, fiber.StatusServiceUnavailable,
				response.CodeServiceError, "Rate limit denetlenemedi", nil)
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
