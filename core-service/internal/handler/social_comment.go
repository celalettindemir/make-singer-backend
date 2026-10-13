package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// Comments handles GET /api/social/yayinlar/:id/yorumlar (paginated).
func (h *SocialHandler) Comments(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	page, err := h.service.Comments(c.Context(), userID, c.Params("id"), c.Query("imlec"), social.PageSize(c.Query("limit")))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, page)
}

// PostComment handles POST /api/social/yayinlar/:id/yorumlar (201 Comment).
// The per-user per-minute limit (429 yavasla) is applied on the route.
func (h *SocialHandler) PostComment(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var input social.CommentInput
	if err := c.BodyParser(&input); err != nil {
		return badBodyError(c)
	}
	comment, err := h.service.PostComment(c.Context(), userID, c.Params("id"), input)
	if err != nil {
		return socialError(c, err)
	}
	return response.Created(c, comment)
}

// DeleteComment handles DELETE /api/social/yorumlar/:id (204).
func (h *SocialHandler) DeleteComment(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	if err := h.service.DeleteComment(c.Context(), userID, c.Params("id")); err != nil {
		return socialError(c, err)
	}
	return response.NoContent(c)
}
