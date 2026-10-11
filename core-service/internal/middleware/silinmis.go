package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
	"github.com/redis/go-redis/v9"
)

// silinmisOnek altindaki anahtar, hesabi silinmis bir kullaniciyi
// isaretler. Omru access token omrune esittir: o sure dolunca elde
// kalan hicbir token zaten gecerli degildir.
const silinmisOnek = "kimlik:silindi:"

// SilinmisAnahtar, kullanicinin silinmis isaretinin Redis anahtaridir.
func SilinmisAnahtar(userID string) string { return silinmisOnek + userID }

// SilinmisKullaniciEngeli, hesabi silinmis kullanicinin elinde kalan
// (imzasi hala gecerli) access token'i reddeder. API token'i yalnizca
// imzayla dogruladigi icin bu engel olmadan silinen hesap 15 dakika
// boyunca render baslatip R2'ye dosya yazabilirdi. Redis'e
// ulasilamazsa rate limiter gibi fail-closed davranir.
func SilinmisKullaniciEngeli(rdb *redis.Client) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			return response.Unauthorized(c, "Kimlik dogrulanamadi")
		}
		n, err := rdb.Exists(c.Context(), SilinmisAnahtar(userID)).Result()
		if err != nil {
			c.Set("Retry-After", "5")
			return response.Error(c, fiber.StatusServiceUnavailable,
				response.CodeServiceError, "Hesap durumu denetlenemedi", nil)
		}
		if n > 0 {
			return response.Unauthorized(c, "Hesap silindi")
		}
		return c.Next()
	}
}
