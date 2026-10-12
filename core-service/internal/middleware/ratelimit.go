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
	return rl.kullaniciBasina(keyPrefix, maxRequests, window, response.RateLimited)
}

// LimitKod, Limit'in asimda genel RATE_LIMITED yerine verilen kodla 429
// donen bicimidir. Sozlesmesi kendi kodunu isteyen uclar icindir (orn.
// Kesfet yorumlari: 429 yavasla).
func (rl *RateLimiter) LimitKod(keyPrefix string, maxRequests int, window time.Duration, kod, mesaj string) fiber.Handler {
	return rl.kullaniciBasina(keyPrefix, maxRequests, window, func(c *fiber.Ctx) error {
		return response.Error(c, fiber.StatusTooManyRequests, kod, mesaj, nil)
	})
}

func (rl *RateLimiter) kullaniciBasina(keyPrefix string, maxRequests int, window time.Duration, asim fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			// Auth middleware userId'yi garanti eder; bos gelmesi bir
			// hatadir. Eskiden burada rate limit ATLANIYORDU (return
			// c.Next()), bu da kota bypass'i demekti.
			return response.Unauthorized(c, "Kimlik dogrulanamadi")
		}
		return rl.say(c, fmt.Sprintf("ratelimit:%s:%s", keyPrefix, userID), maxRequests, window, asim)
	}
}

// WebAnahtariBasligi, guvenilen web sunucusunun (make-singer-frontend)
// IP basina siniri atlamak icin gonderdigi basliktir. Web sunucusu tum
// ziyaretcilerin isteklerini TEK IP'den yapar; bu baslik olmadan o IP
// dakikada birkac ziyaretciden sonra 429 alirdi.
const WebAnahtariBasligi = "X-Web-Anahtari"

// LimitIP, kimliksiz uclar icin istemci IP'si basina sabit pencereli
// sinirdir; anahtar ratelimit:<onek>:ip:<ip>. webAnahtari doluysa ve
// istek X-Web-Anahtari basligiyla ayni degeri tasiyorsa istek IP yerine
// ortak ratelimit:<onek>:web anahtarinda webMax ile sayilir. Yanlis ya da
// bos anahtar 401 degil, olagan IP siniridir. Karsilastirma sabit
// zamanlidir (once SHA-256, boy da sizmaz). webAnahtari bossa web yolu
// tamamen kapalidir.
func (rl *RateLimiter) LimitIP(keyPrefix string, maxRequests int, window time.Duration, webAnahtari string, webMax int) fiber.Handler {
	webOzeti := sha256.Sum256([]byte(webAnahtari))
	return func(c *fiber.Ctx) error {
		if webAnahtari != "" {
			if gelen := c.Get(WebAnahtariBasligi); gelen != "" {
				gelenOzet := sha256.Sum256([]byte(gelen))
				if subtle.ConstantTimeCompare(gelenOzet[:], webOzeti[:]) == 1 {
					return rl.say(c, fmt.Sprintf("ratelimit:%s:web", keyPrefix), webMax, window, response.RateLimited)
				}
			}
		}
		return rl.say(c, fmt.Sprintf("ratelimit:%s:ip:%s", keyPrefix, istemciIP(c)), maxRequests, window, response.RateLimited)
	}
}

// istemciIP, Traefik'in yazdigi X-Real-Ip basligini kullanir; Traefik
// istemcinin gonderdigi X-Real-Ip'yi (guvenilmeyen kaynaktan) siler ve
// kendi gordugu adresi yazar. Baslik yoksa ya da gecerli bir IP degilse
// baglantinin adresi (c.IP()) kullanilir. Fiber'in genel ProxyHeader
// ayari BILEREK degistirilmez: diger uclar kullanici basina sayar.
// Not: API Traefik'i atlayarak dogrudan erisilebilir olursa bu baslik
// uydurulabilir; bu yuzden API portu disari acilmamalidir.
func istemciIP(c *fiber.Ctx) string {
	if ip := net.ParseIP(strings.TrimSpace(c.Get("X-Real-Ip"))); ip != nil {
		return ip.String()
	}
	return c.IP()
}

// say, key sayacini pencere icinde artirir; maxRequests asilinca asim
// yanitini doner, aksi halde zinciri surdurur.
func (rl *RateLimiter) say(c *fiber.Ctx, key string, maxRequests int, window time.Duration, asim fiber.Handler) error {
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
		return asim(c)
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

// WSTicketLimit, WebSocket bileti ucu icin dakikalik limit.
func (rl *RateLimiter) WSTicketLimit(maxPerMin int) fiber.Handler {
	return rl.Limit("wsticket", maxPerMin, time.Minute)
}

// SignLimit, URL imzalama ucu icin dakikalik limit. Tek istek en fazla
// 50 anahtar imzalar; sayac istek basinadir.
func (rl *RateLimiter) SignLimit(maxPerMin int) fiber.Handler {
	return rl.Limit("sign", maxPerMin, time.Minute)
}
