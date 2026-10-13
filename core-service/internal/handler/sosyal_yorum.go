package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// Yorumlar handles GET /api/social/yayinlar/:id/yorumlar (sayfali).
func (h *SosyalHandler) Yorumlar(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.Yorumlar(c.Context(), userID, c.Params("id"), c.Query("imlec"), social.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// YorumYaz handles POST /api/social/yayinlar/:id/yorumlar (201 Yorum).
// Kullanici basina dakikalik limit (429 yavasla) rotada uygulanir.
func (h *SosyalHandler) YorumYaz(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var g social.YorumGirdisi
	if err := c.BodyParser(&g); err != nil {
		return govdeHatasi(c)
	}
	y, err := h.servis.YorumYaz(c.Context(), userID, c.Params("id"), g)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.Created(c, y)
}

// YorumSil handles DELETE /api/social/yorumlar/:id (204).
func (h *SosyalHandler) YorumSil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	if err := h.servis.YorumSil(c.Context(), userID, c.Params("id")); err != nil {
		return sosyalHata(c, err)
	}
	return response.NoContent(c)
}
