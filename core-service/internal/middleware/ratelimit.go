package middleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"strings"
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
	return rl.perUser(keyPrefix, maxRequests, window, response.RateLimited)
}

// LimitWithCode is the form of Limit that, on overflow, returns 429 with
// the given code instead of the generic RATE_LIMITED. It is for endpoints
// whose contract asks for its own code (e.g. Discover comments: 429
// "yavasla").
func (rl *RateLimiter) LimitWithCode(keyPrefix string, maxRequests int, window time.Duration, code, message string) fiber.Handler {
	return rl.perUser(keyPrefix, maxRequests, window, func(c *fiber.Ctx) error {
		return response.Error(c, fiber.StatusTooManyRequests, code, message, nil)
	})
}

func (rl *RateLimiter) perUser(keyPrefix string, maxRequests int, window time.Duration, overflow fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			// The auth middleware guarantees userId; an empty value is a
			// bug. The rate limit used to be SKIPPED here (return
			// c.Next()), which meant a quota bypass.
			return response.Unauthorized(c, "Kimlik dogrulanamadi")
		}
		return rl.hit(c, fmt.Sprintf("ratelimit:%s:%s", keyPrefix, userID), maxRequests, window, overflow)
	}
}

// WebKeyHeader is the header the trusted web server (make-singer-frontend)
// sends to bypass the per-IP limit. The web server makes all visitors'
// requests from ONE IP; without this header that IP would get 429 after
// a few visitors per minute.
const WebKeyHeader = "X-Web-Anahtari"

// LimitIP is a fixed-window limit per client IP for unauthenticated
// endpoints; the key is ratelimit:<prefix>:ip:<ip>. If webKey is set and
// the request carries the same value in the X-Web-Anahtari header, the
// request is counted against webMax under the shared
// ratelimit:<prefix>:web key instead of the IP. A wrong or empty key is
// not a 401, it just gets the normal IP limit. The comparison is
// constant-time (SHA-256 first, so the length does not leak either). If
// webKey is empty the web path is completely disabled.
func (rl *RateLimiter) LimitIP(keyPrefix string, maxRequests int, window time.Duration, webKey string, webMax int) fiber.Handler {
	webDigest := sha256.Sum256([]byte(webKey))
	return func(c *fiber.Ctx) error {
		if webKey != "" {
			if provided := c.Get(WebKeyHeader); provided != "" {
				providedDigest := sha256.Sum256([]byte(provided))
				if subtle.ConstantTimeCompare(providedDigest[:], webDigest[:]) == 1 {
					return rl.hit(c, fmt.Sprintf("ratelimit:%s:web", keyPrefix), webMax, window, response.RateLimited)
				}
			}
		}
		return rl.hit(c, fmt.Sprintf("ratelimit:%s:ip:%s", keyPrefix, clientIP(c)), maxRequests, window, response.RateLimited)
	}
}

// clientIP uses the X-Real-Ip header written by Traefik (the key format
// is in ipKey: per IPv6 /64); Traefik deletes the X-Real-Ip sent by the
// client (an untrusted source) and writes the address it saw itself. If
// the header is missing or is not a valid IP, the connection address
// (c.IP()) is used. Fiber's global ProxyHeader setting is deliberately
// NOT changed: other endpoints count per user.
// Note: if the API becomes directly reachable, bypassing Traefik, this
// header can be forged; so the API port must not be exposed.
func clientIP(c *fiber.Ctx) string {
	if raw := strings.TrimSpace(c.Get("X-Real-Ip")); net.ParseIP(raw) != nil {
		return ipKey(raw)
	}
	return ipKey(c.IP())
}

// ipKey is the IP form used in the counter key. IPv4 (including
// IPv4-mapped IPv6) is in plain IPv4 form; IPv6 is reduced to its /64
// prefix, because a single client can freely change its source address
// within an ordinary /64. A value that cannot be parsed is returned as is.
func ipKey(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return raw
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}

// hit increments the key's counter within the window; once maxRequests is
// exceeded it returns the overflow response, otherwise it continues the
// chain.
func (rl *RateLimiter) hit(c *fiber.Ctx, key string, maxRequests int, window time.Duration, overflow fiber.Handler) error {
	ctx := context.Background()

	// Incrementing the counter and setting the TTL happen in ONE
	// operation (MULTI/EXEC). Previously, when Incr succeeded but Expire
	// failed, the error was swallowed: the key stayed without a TTL,
	// every later request had count > maxRequests, and since TTL returned
	// -1 the user was locked out PERMANENTLY with Retry-After: -1.
	// The sequence SET key 0 EX window NX + INCR creates the key only when
	// it does not exist and pins the TTL at creation time.
	pipe := rl.redis.TxPipeline()
	pipe.SetArgs(ctx, key, 0, redis.SetArgs{Mode: "NX", TTL: window})
	incr := pipe.Incr(ctx, key)
	// redis.Nil = SET NX was not applied (the key already existed); this
	// is the normal flow, not an error.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		// Fail-closed: if we could not increment the counter we CANNOT
		// KNOW whether the quota was exceeded. Requests used to be let
		// through; that gave unlimited quota to anyone able to take Redis
		// down. The error text is not logged (the connection string may
		// carry a secret).
		c.Set("Retry-After", "5")
		return response.Error(c, fiber.StatusServiceUnavailable,
			response.CodeServiceError, "Rate limit denetlenemedi", nil)
	}
	count, err := incr.Result()
	if err != nil {
		c.Set("Retry-After", "5")
		return response.Error(c, fiber.StatusServiceUnavailable,
			response.CodeServiceError, "Rate limit denetlenemedi", nil)
	}

	if count > int64(maxRequests) {
		// Retry-After is at least 1 second: if the TTL cannot be read or
		// the key somehow stayed without a TTL (-1), a negative or zero
		// value must not be returned.
		wait := window
		ttl, ttlErr := rl.redis.TTL(ctx, key).Result()
		switch {
		case ttlErr == nil && ttl > 0:
			wait = ttl
		case ttlErr == nil && ttl < 0:
			// LEGACY KEY HEALING: TTL -1 means the key has no expiry. The
			// new code CANNOT produce such a key (MULTI/EXEC is atomic), but
			// the old code may have left some in production: since SET NX
			// was not applied the TTL stays -1, the counter grows forever
			// and the user stays PERMANENTLY locked out; the only way out
			// is a manual DEL. Here we give the window back so the key
			// resets on its own.
			rl.redis.Expire(ctx, key, window)
		}
		seconds := int(wait.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		c.Set("Retry-After", fmt.Sprintf("%d", seconds))
		return overflow(c)
	}

	// Add rate limit headers
	c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", maxRequests))
	c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", maxRequests-int(count)))

	return c.Next()
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

// WSTicketLimit is the per-minute limit for the WebSocket ticket endpoint.
func (rl *RateLimiter) WSTicketLimit(maxPerMin int) fiber.Handler {
	return rl.Limit("wsticket", maxPerMin, time.Minute)
}

// SignLimit is the per-minute limit for the URL signing endpoint. A single
// request signs at most 50 keys; the counter is per request.
func (rl *RateLimiter) SignLimit(maxPerMin int) fiber.Handler {
	return rl.Limit("sign", maxPerMin, time.Minute)
}
