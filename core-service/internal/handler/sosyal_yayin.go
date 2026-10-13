package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// YayinOlustur handles POST /api/social/yayinlar (202). Profil yoksa 409
// profil_yok.
func (h *SosyalHandler) YayinOlustur(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var ist social.PublicationRequest
	if err := c.BodyParser(&ist); err != nil {
		return govdeHatasi(c)
	}
	y, err := h.servis.CreatePublication(c.Context(), userID, ist)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.Accepted(c, y)
}

// Yayin handles GET /api/social/yayinlar/:id
func (h *SosyalHandler) Yayin(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	y, err := h.servis.Publication(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, y)
}

// Slug handles GET /api/social/s/:slug
func (h *SosyalHandler) Slug(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	y, err := h.servis.PublicationBySlug(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, y)
}

// YayinGuncelle handles PATCH /api/social/yayinlar/:id
func (h *SosyalHandler) YayinGuncelle(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var yama social.PublicationPatch
	if err := c.BodyParser(&yama); err != nil {
		return govdeHatasi(c)
	}
	y, err := h.servis.UpdatePublication(c.Context(), userID, c.Params("id"), yama)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, y)
}

// YayinSil handles DELETE /api/social/yayinlar/:id (204). Govde yok sayilir.
func (h *SosyalHandler) YayinSil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	if err := h.servis.DeletePublication(c.Context(), userID, c.Params("id")); err != nil {
		return sosyalHata(c, err)
	}
	return response.NoContent(c)
}

// TekrarDene handles POST /api/social/yayinlar/:id/tekrar-dene (202).
func (h *SosyalHandler) TekrarDene(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	y, err := h.servis.Retry(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.Accepted(c, y)
}

// Dosyalar handles POST /api/social/yayinlar/:id/dosyalar
func (h *SosyalHandler) Dosyalar(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	d, err := h.servis.Files(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, d)
}
