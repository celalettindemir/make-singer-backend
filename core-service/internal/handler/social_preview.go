package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// PublicRoutes registers the unauthenticated Discover endpoints (spec §4.8).
// main registers them BEFORE the /api auth group; ipLimiter applies the
// per-client-IP limit (or one shared with the web key). noStore runs before
// the limiter, so 404, 429 and 500 responses are not cached either.
// It panics at registration time if ipLimiter is nil.
func (h *SocialHandler) PublicRoutes(r fiber.Router, ipLimiter fiber.Handler) {
	if ipLimiter == nil {
		panic("handler: PublicRotalar icin IP limiteri doldurulmali")
	}
	r.Get("/s/:slug", noStore, ipLimiter, h.Preview)
}

// noStore writes the header that forbids caching the response (whatever its
// status) and continues the chain.
func noStore(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Next()
}

// Preview handles GET /api/public/s/:slug (no session). Only
// publications visible to everyone or by link; the others get 404 bulunamadi.
// The response carries a signed URL, so it is not cached (Cache-Control comes
// from noStore on the route).
func (h *SocialHandler) Preview(c *fiber.Ctx) error {
	preview, err := h.service.Preview(c.Context(), c.Params("slug"))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, preview)
}
