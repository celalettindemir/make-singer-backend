package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/middleware"
)

// istekSahibi, auth middleware'in yazdigi userId'yi okur. Bos gelmesi bir
// yapilandirma hatasidir; cagiran fail-closed 401 doner (sahipsiz is
// yaratmak ya da "bos sahip" ile is okumak yerine).
func istekSahibi(c *fiber.Ctx) (string, bool) {
	userID := middleware.GetUserID(c)
	return userID, userID != ""
}
