package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// CreatePublication handles POST /api/social/yayinlar (202). Without a profile
// it returns 409 profil_yok.
func (h *SocialHandler) CreatePublication(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req social.PublicationRequest
	if err := c.BodyParser(&req); err != nil {
		return badBodyError(c)
	}
	publication, err := h.service.CreatePublication(c.Context(), userID, req)
	if err != nil {
		return socialError(c, err)
	}
	return response.Accepted(c, publication)
}

// Publication handles GET /api/social/yayinlar/:id
func (h *SocialHandler) Publication(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	publication, err := h.service.Publication(c.Context(), userID, c.Params("id"))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, publication)
}

// PublicationBySlug handles GET /api/social/s/:slug
func (h *SocialHandler) PublicationBySlug(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	publication, err := h.service.PublicationBySlug(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, publication)
}

// UpdatePublication handles PATCH /api/social/yayinlar/:id
func (h *SocialHandler) UpdatePublication(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var patch social.PublicationPatch
	if err := c.BodyParser(&patch); err != nil {
		return badBodyError(c)
	}
	publication, err := h.service.UpdatePublication(c.Context(), userID, c.Params("id"), patch)
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, publication)
}

// DeletePublication handles DELETE /api/social/yayinlar/:id (204). The body is ignored.
func (h *SocialHandler) DeletePublication(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	if err := h.service.DeletePublication(c.Context(), userID, c.Params("id")); err != nil {
		return socialError(c, err)
	}
	return response.NoContent(c)
}

// Retry handles POST /api/social/yayinlar/:id/tekrar-dene (202).
func (h *SocialHandler) Retry(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	publication, err := h.service.Retry(c.Context(), userID, c.Params("id"))
	if err != nil {
		return socialError(c, err)
	}
	return response.Accepted(c, publication)
}

// Files handles POST /api/social/yayinlar/:id/dosyalar
func (h *SocialHandler) Files(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	files, err := h.service.Files(c.Context(), userID, c.Params("id"))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, files)
}
