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

func engelliUygulama(rdb *redis.Client, userID string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("userId", userID)
		}
		return c.Next()
	})
	app.Use(SilinmisKullaniciEngeli(deletionmark.NewRedisChecker(rdb)))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("gecti") })
	return app
}

func engelDurum(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	return resp.StatusCode
}

func TestSilinmisEngeli_RedisYoksa503(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond})
	if got := engelDurum(t, engelliUygulama(rdb, "u1")); got != http.StatusServiceUnavailable {
		t.Fatalf("durum = %d, beklenen 503", got)
	}
}

func TestSilinmisEngeli_UserIDBossa401(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if got := engelDurum(t, engelliUygulama(rdb, "")); got != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", got)
	}
}

func TestSilinmisEngeli_IsaretliKullanici401(t *testing.T) {
	rdb := canliRedis(t)
	if err := rdb.Set(context.Background(), deletionmark.Key("u1"), "1", time.Minute).Err(); err != nil {
		t.Fatalf("isaret yazilamadi: %v", err)
	}
	if got := engelDurum(t, engelliUygulama(rdb, "u1")); got != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", got)
	}
	if got := engelDurum(t, engelliUygulama(rdb, "u2")); got != http.StatusOK {
		t.Fatalf("isaretsiz kullanici durumu = %d, beklenen 200", got)
	}
}
