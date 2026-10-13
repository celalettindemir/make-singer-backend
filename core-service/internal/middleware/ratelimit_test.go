package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// limitedApp builds a rate-limited endpoint with the given locals setup.
func limitedApp(redisAddr string, userID string) *fiber.App {
	rl := NewRateLimiter(redis.NewClient(&redis.Options{
		Addr:        redisAddr,
		DialTimeout: 200 * time.Millisecond,
	}), nil)
	app := fiber.New()
	app.Get("/uc", func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("userId", userID)
		}
		return c.Next()
	}, rl.Limit("deneme", 5, time.Minute), func(c *fiber.Ctx) error {
		return c.SendString("tamam")
	})
	return app
}

func limitStatus(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", "/uc", nil), -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	return resp.StatusCode
}

// While userId was empty the rate limit used to be SKIPPED (fail-open). Now it is 401.
func TestLimit_FailsClosedWithoutUserID(t *testing.T) {
	// 127.0.0.1:1 is deliberately closed: Redis must never be contacted.
	if status := limitStatus(t, limitedApp("127.0.0.1:1", "")); status != fiber.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

// If Redis is unreachable the counter cannot be incremented, so the quota
// is unknown: the request is not let through, 503 is returned.
func TestLimit_FailsClosedOnRedisError(t *testing.T) {
	// No network needed: 127.0.0.1:1 gives a connection refusal.
	if status := limitStatus(t, limitedApp("127.0.0.1:1", "kullanici-1")); status != fiber.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
}
