package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/pkg/response"
)

// SilinmisKullaniciEngeli, hesabi silinmis kullanicinin elinde kalan
// (imzasi hala gecerli) access token'i reddeder. API token'i yalnizca
// imzayla dogruladigi icin bu engel olmadan silinen hesap 15 dakika
// boyunca render baslatip R2'ye dosya yazabilirdi. Isarete
// ulasilamazsa rate limiter gibi fail-closed davranir (503).
func SilinmisKullaniciEngeli(denetci deletionmark.Checker) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			return response.Unauthorized(c, "Kimlik dogrulanamadi")
		}
		silinmis, err := denetci.IsDeleted(c.Context(), userID)
		if err != nil {
			c.Set("Retry-After", "5")
			return response.Error(c, fiber.StatusServiceUnavailable,
				response.CodeServiceError, "Hesap durumu denetlenemedi", nil)
		}
		if silinmis {
			return response.Unauthorized(c, "Hesap silindi")
		}
		return c.Next()
	}
}
