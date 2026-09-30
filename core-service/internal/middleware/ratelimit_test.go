package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// sinirliUygulama, verilen locals kurulumuyla rate limit'li bir uc kurar.
func sinirliUygulama(redisAdres string, userID string) *fiber.App {
	rl := NewRateLimiter(redis.NewClient(&redis.Options{
		Addr:        redisAdres,
		DialTimeout: 200 * time.Millisecond,
	}))
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

func sinirDurum(t *testing.T, app *fiber.App) int {
	t.Helper()
	y, err := app.Test(httptest.NewRequest("GET", "/uc", nil), -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	return y.StatusCode
}

// userId bos iken eskiden rate limit ATLANIYORDU (fail-open). Artik 401.
func TestUserIDBossaRateLimitFailClosed(t *testing.T) {
	// 127.0.0.1:1 kasten kapali: Redis'e hic gidilmemesi gerekir.
	if durum := sinirDurum(t, sinirliUygulama("127.0.0.1:1", "")); durum != fiber.StatusUnauthorized {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Redis erisilemezse sayac artirilamaz, yani kota bilinemez: istek
// serbest gecirilmez, 503 donulur.
func TestRedisHatasindaFailClosed(t *testing.T) {
	// Ag gerekmez: 127.0.0.1:1 baglanti reddi verir.
	if durum := sinirDurum(t, sinirliUygulama("127.0.0.1:1", "kullanici-1")); durum != fiber.StatusServiceUnavailable {
		t.Errorf("durum = %d, beklenen 503", durum)
	}
}
