package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/redis/go-redis/v9"
)

func gatedApp(rdb *redis.Client, userID string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("userId", userID)
		}
		return c.Next()
	})
	app.Use(DeletedUserGate(deletionmark.NewRedisChecker(rdb)))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("gecti") })
	return app
}

func gateStatus(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp.StatusCode
}

func TestDeletedUserGate_503WithoutRedis(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond})
	if got := gateStatus(t, gatedApp(rdb, "u1")); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
}

func TestDeletedUserGate_401WithoutUserID(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if got := gateStatus(t, gatedApp(rdb, "")); got != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
}

func TestDeletedUserGate_401ForMarkedUser(t *testing.T) {
	rdb := liveRedis(t)
	if err := rdb.Set(context.Background(), deletionmark.Key("u1"), "1", time.Minute).Err(); err != nil {
		t.Fatalf("could not write mark: %v", err)
	}
	if got := gateStatus(t, gatedApp(rdb, "u1")); got != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
	if got := gateStatus(t, gatedApp(rdb, "u2")); got != http.StatusOK {
		t.Fatalf("unmarked user status = %d, want 200", got)
	}
}
