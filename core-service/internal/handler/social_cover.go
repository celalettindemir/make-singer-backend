package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// Covers handles GET /api/social/yayinlar/:id/coverlar (paginated;
// an empty page when the backing tracks are disabled).
func (h *SocialHandler) Covers(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	page, err := h.service.Covers(c.Context(), userID, c.Params("id"), c.Query("imlec"), social.PageSize(c.Query("limit")))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, page)
}

// StartCover handles POST /api/social/yayinlar/:id/cover-baslat. The body
// is ignored; the per-user hourly limit is applied on the route.
func (h *SocialHandler) StartCover(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	start, err := h.service.StartCover(c.Context(), userID, c.Params("id"))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, start)
}
