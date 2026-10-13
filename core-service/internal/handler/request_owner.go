package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/middleware"
)

// requestOwner reads the userId written by the auth middleware. An empty value
// is a configuration error; the caller fails closed with 401 (instead of
// creating an ownerless job or reading a job with an "empty owner").
func requestOwner(c *fiber.Ctx) (string, bool) {
	userID := middleware.GetUserID(c)
	return userID, userID != ""
}
