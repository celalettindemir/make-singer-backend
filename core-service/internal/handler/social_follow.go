package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// Follow handles PUT /api/social/profiller/:ad/takip
func (h *SocialHandler) Follow(c *fiber.Ctx) error {
	return h.setFollow(c, true)
}

// Unfollow handles DELETE /api/social/profiller/:ad/takip
func (h *SocialHandler) Unfollow(c *fiber.Ctx) error {
	return h.setFollow(c, false)
}

func (h *SocialHandler) setFollow(c *fiber.Ctx, follow bool) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	followStatus, err := h.service.SetFollow(c.Context(), userID, c.Params("ad"), follow)
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, followStatus)
}

// Suggestions handles GET /api/social/oneriler (at most 10 ProfileSummary, unpaginated).
func (h *SocialHandler) Suggestions(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	suggestions, err := h.service.Suggestions(c.Context(), userID)
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, suggestions)
}
