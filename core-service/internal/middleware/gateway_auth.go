package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// GatewayAuthMiddleware reads user identity from X-User-* headers
// set by Traefik ForwardAuth and populates Fiber context locals.
func GatewayAuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := c.Get("X-User-Id")
		if userID == "" {
			return response.Unauthorized(c, "Missing user identity headers")
		}

		c.Locals("userId", userID)
		c.Locals("email", c.Get("X-User-Email"))
		c.Locals("name", c.Get("X-User-Name"))

		return c.Next()
	}
}
