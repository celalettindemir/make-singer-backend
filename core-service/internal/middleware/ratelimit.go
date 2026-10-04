package middleware

import (
	"context"
	"errors"
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

		// Sayaci artirma ve TTL atama TEK islemde (MULTI/EXEC) yapilir.
		// Eskiden Incr basarili olup Expire hata verdiginde hata
		// yutuluyordu: anahtar TTL'siz kaliyor, sonraki her istekte
		// count > maxRequests oluyor ve TTL -1 dondugu icin
		// Retry-After: -1 ile kullanici KALICI kilitleniyordu.
		// SET key 0 EX window NX + INCR sirasi, anahtari yalnizca yokken
		// olusturur ve TTL'i olusturma aninda kenetler.
		boru := rl.redis.TxPipeline()
		boru.SetArgs(ctx, key, 0, redis.SetArgs{Mode: "NX", TTL: window})
		artir := boru.Incr(ctx, key)
		// redis.Nil = SET NX uygulanmadi (anahtar zaten vardi); bu
		// normal akistir, hata degil.
		if _, err := boru.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			// Fail-closed: sayaci artiramadiysak kotanin asilip
			// asilmadigini BILEMEYIZ. Eskiden istek serbest gecirilirdi;
			// bu, Redis'i dusurebilen birine sinirsiz kota veriyordu.
			// Hata metni loglanmaz (baglanti dizgesi sir tasiyabilir).
			c.Set("Retry-After", "5")
			return response.Error(c, fiber.StatusServiceUnavailable,
				response.CodeServiceError, "Rate limit denetlenemedi", nil)
		}
		count, err := artir.Result()
		if err != nil {
			c.Set("Retry-After", "5")
			return response.Error(c, fiber.StatusServiceUnavailable,
				response.CodeServiceError, "Rate limit denetlenemedi", nil)
		}

		if count > int64(maxRequests) {
			// Retry-After en az 1 saniye: TTL okunamazsa veya anahtar
			// bir sekilde TTL'siz kaldiysa (-1) negatif/sifir deger
			// donmemeli.
			bekle := window
			ttl, ttlErr := rl.redis.TTL(ctx, key).Result()
			switch {
			case ttlErr == nil && ttl > 0:
				bekle = ttl
			case ttlErr == nil && ttl < 0:
				// MIRAS ANAHTAR IYILESTIRMESI: TTL -1 demek anahtarin
				// suresi yok. Yeni kod boyle bir anahtar URETEMEZ
				// (MULTI/EXEC atomik), ama eski kodun uretimde biraktigi
				// anahtarlar olabilir: SET NX uygulanmadigi icin TTL -1
				// kalir, sayac sonsuza buyur ve kullanici KALICI kilitli
				// kalir — tek cikis manuel DEL. Burada pencereyi geri
				// veriyoruz ki anahtar kendiliginden sifirlansin.
				rl.redis.Expire(ctx, key, window)
			}
			saniye := int(bekle.Seconds())
			if saniye < 1 {
				saniye = 1
			}
			c.Set("Retry-After", fmt.Sprintf("%d", saniye))
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
