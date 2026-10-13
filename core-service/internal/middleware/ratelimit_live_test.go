package middleware

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// liveRedis wants a local Redis. If there is none the test is skipped: unit
// tests must pass without a network (see the same pattern in
// internal/identity/request_test.go). DB 14 is used; the identity tests use DB 15.
func liveRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 14})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

// liveApp builds a rate-limited endpoint connected to the real Redis.
func liveApp(rdb *redis.Client, userID, prefix string, maxRequests int, window time.Duration) *fiber.App {
	rl := NewRateLimiter(rdb)
	app := fiber.New()
	app.Get("/uc", func(c *fiber.Ctx) error {
		c.Locals("userId", userID)
		return c.Next()
	}, rl.Limit(prefix, maxRequests, window), func(c *fiber.Ctx) error {
		return c.SendString("tamam")
	})
	return app
}

type liveResponse struct {
	status     int
	remaining  string
	retryAfter string
}

func liveRequest(t *testing.T, app *fiber.App) liveResponse {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", "/uc", nil), -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	return liveResponse{
		status:     resp.StatusCode,
		remaining:  resp.Header.Get("X-RateLimit-Remaining"),
		retryAfter: resp.Header.Get("Retry-After"),
	}
}

// Happy path: the counter starts at 1, X-RateLimit-Remaining decreases, and
// once the quota is exceeded 429 + a positive Retry-After arrives. This
// behavior comes from Redis's SET NX EX + INCR + MULTI/EXEC semantics, not
// from Go; a Go-side test cannot catch it.
func TestLiveLimit_HappyPathAndCounter(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	const user = "kullanici-mutlu"
	app := liveApp(rdb, user, "mutlu", 3, time.Minute)
	key := fmt.Sprintf("ratelimit:mutlu:%s", user)

	for i, wantRemaining := range []string{"2", "1", "0"} {
		resp := liveRequest(t, app)
		if resp.status != fiber.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, resp.status)
		}
		if resp.remaining != wantRemaining {
			t.Errorf("request %d: X-RateLimit-Remaining = %q, want %q", i+1, resp.remaining, wantRemaining)
		}
		if i == 0 {
			// The counter must start at 1: SET NX 0 + INCR.
			if value, err := rdb.Get(ctx, key).Result(); err != nil || value != "1" {
				t.Errorf("counter after the first request = %q (err %v), want \"1\"", value, err)
			}
			// The TTL must be pinned at creation time.
			if ttl, err := rdb.TTL(ctx, key).Result(); err != nil || ttl <= 0 {
				t.Errorf("TTL after the first request = %v (err %v), want > 0", ttl, err)
			}
		}
	}

	resp := liveRequest(t, app)
	if resp.status != fiber.StatusTooManyRequests {
		t.Fatalf("4th request: status = %d, want 429", resp.status)
	}
	seconds, err := strconv.Atoi(resp.retryAfter)
	if err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q (err %v), want >= 1", resp.retryAfter, err)
	}
}

// The window is FIXED, not sliding: the TTL is NOT renewed on later
// requests. CLAUDE.md used to say "sliding window"; this is the measured
// behavior.
func TestLiveLimit_FixedWindowTTLNotRenewed(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	const user = "kullanici-pencere"
	app := liveApp(rdb, user, "pencere", 100, 10*time.Second)
	key := fmt.Sprintf("ratelimit:pencere:%s", user)

	if resp := liveRequest(t, app); resp.status != fiber.StatusOK {
		t.Fatalf("first request: status = %d, want 200", resp.status)
	}
	firstTTL, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("could not read TTL: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)

	if resp := liveRequest(t, app); resp.status != fiber.StatusOK {
		t.Fatalf("second request: status = %d, want 200", resp.status)
	}
	secondTTL, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("could not read TTL: %v", err)
	}
	if secondTTL >= firstTTL {
		t.Errorf("TTL was refreshed (%v -> %v); the window must be fixed", firstTTL, secondTTL)
	}
}

// When the window ends the counter resets and the user can get through again.
func TestLiveLimit_CounterResetsAfterWindow(t *testing.T) {
	rdb := liveRedis(t)
	const user = "kullanici-sifir"
	app := liveApp(rdb, user, "sifir", 1, time.Second)

	if resp := liveRequest(t, app); resp.status != fiber.StatusOK {
		t.Fatalf("first request: status = %d, want 200", resp.status)
	}
	if resp := liveRequest(t, app); resp.status != fiber.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429", resp.status)
	}

	time.Sleep(1200 * time.Millisecond)

	if resp := liveRequest(t, app); resp.status != fiber.StatusOK {
		t.Errorf("after the window expired: status = %d, want 200", resp.status)
	}
}

// LEGACY KEY: the old code (Incr succeeded + Expire blew up) could leave a
// key without a TTL. If such a key has exceeded the counter, the user would
// stay PERMANENTLY locked out. When the new code sees TTL -1 in the 429
// branch it gives the window back.
func TestLiveLimit_HealsLegacyKeyWithoutTTL(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	const user = "kullanici-miras"
	app := liveApp(rdb, user, "miras", 3, 30*time.Second)
	key := fmt.Sprintf("ratelimit:miras:%s", user)

	// Set up a key by hand with no TTL and over quota (the legacy of the old code).
	if err := rdb.Set(ctx, key, 99, 0).Err(); err != nil {
		t.Fatalf("could not set up the legacy key: %v", err)
	}
	if ttl, err := rdb.TTL(ctx, key).Result(); err != nil || ttl >= 0 {
		t.Fatalf("setup: TTL = %v (err %v), must have no TTL (-1)", ttl, err)
	}

	resp := liveRequest(t, app)
	if resp.status != fiber.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.status)
	}
	seconds, err := strconv.Atoi(resp.retryAfter)
	if err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q (err %v), want >= 1", resp.retryAfter, err)
	}
	// Healing: the key must now carry a TTL, so the lock will open on its own.
	ttl, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("could not read TTL: %v", err)
	}
	if ttl <= 0 {
		t.Errorf("TTL after the request = %v, want > 0 (the legacy key was not repaired)", ttl)
	}
}

// The ws ticket and signing endpoints must not stay unlimited on the public
// API: 429 once the limit is exceeded, the counter under its own prefix and
// in a per-minute window.
func TestLiveLimit_WSTicketAndSign(t *testing.T) {
	for _, d := range []struct {
		name   string
		prefix string
		yap    func(rl *RateLimiter, n int) fiber.Handler
	}{
		{"wsticket", "wsticket", func(rl *RateLimiter, n int) fiber.Handler { return rl.WSTicketLimit(n) }},
		{"sign", "sign", func(rl *RateLimiter, n int) fiber.Handler { return rl.SignLimit(n) }},
	} {
		t.Run(d.name, func(t *testing.T) {
			rdb := liveRedis(t)
			ctx := context.Background()
			user := "kullanici-" + d.name
			rl := NewRateLimiter(rdb)
			app := fiber.New()
			app.Get("/uc", func(c *fiber.Ctx) error {
				c.Locals("userId", user)
				return c.Next()
			}, d.yap(rl, 2), func(c *fiber.Ctx) error {
				return c.SendString("tamam")
			})

			for i := 1; i <= 2; i++ {
				if resp := liveRequest(t, app); resp.status != fiber.StatusOK {
					t.Fatalf("request %d: status = %d, want 200", i, resp.status)
				}
			}
			if resp := liveRequest(t, app); resp.status != fiber.StatusTooManyRequests {
				t.Fatalf("3rd request: status = %d, want 429", resp.status)
			}

			key := fmt.Sprintf("ratelimit:%s:%s", d.prefix, user)
			ttl, err := rdb.TTL(ctx, key).Result()
			if err != nil || ttl <= 0 || ttl > time.Minute {
				t.Errorf("TTL = %v (err %v), want 0 < ttl <= 60s", ttl, err)
			}
		})
	}
}
