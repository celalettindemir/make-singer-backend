package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/pkg/response"
)

// DeletedUserGate rejects the access token (whose signature is still
// valid) left in the hands of a user whose account was deleted. The API
// verifies the token by signature only, so without this gate a deleted
// account could start renders and write files to R2 for 15 minutes. If
// the mark cannot be reached it fails closed like the rate limiter (503).
func DeletedUserGate(checker deletionmark.Checker) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			return response.Unauthorized(c, "Kimlik dogrulanamadi")
		}
		deleted, err := checker.IsDeleted(c.Context(), userID)
		if err != nil {
			c.Set("Retry-After", "5")
			return response.Error(c, fiber.StatusServiceUnavailable,
				response.CodeServiceError, "Hesap durumu denetlenemedi", nil)
		}
		if deleted {
			return response.Unauthorized(c, "Hesap silindi")
		}
		return c.Next()
	}
}
