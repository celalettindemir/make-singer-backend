package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// Feed handles GET /api/social/akis?sekme=senin-icin|takip|yeni&imlec=&limit=
func (h *SocialHandler) Feed(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	page, err := h.service.Feed(c.Context(), userID, c.Query("sekme"), c.Query("imlec"), social.PageSize(c.Query("limit")))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, page)
}

// ProfilePublications handles GET /api/social/profiller/:ad/yayinlar?tur=sarkilar|coverlar
func (h *SocialHandler) ProfilePublications(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	page, err := h.service.ProfilePublications(c.Context(), userID, c.Params("ad"), c.Query("tur"),
		c.Query("imlec"), social.PageSize(c.Query("limit")))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, page)
}

// Like handles PUT /api/social/yayinlar/:id/begeni
func (h *SocialHandler) Like(c *fiber.Ctx) error {
	return h.setLike(c, true)
}

// Unlike handles DELETE /api/social/yayinlar/:id/begeni
func (h *SocialHandler) Unlike(c *fiber.Ctx) error {
	return h.setLike(c, false)
}

func (h *SocialHandler) setLike(c *fiber.Ctx, like bool) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	likeStatus, err := h.service.SetLike(c.Context(), userID, c.Params("id"), like)
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, likeStatus)
}
