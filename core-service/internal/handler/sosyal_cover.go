package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// Coverlar handles GET /api/social/yayinlar/:id/coverlar (sayfali;
// altyapi kapaliysa bos sayfa).
func (h *SosyalHandler) Coverlar(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.Coverlar(c.Context(), userID, c.Params("id"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// CoverBaslat handles POST /api/social/yayinlar/:id/cover-baslat. Govde
// yok sayilir; kullanici basina saatlik limit rotada uygulanir.
func (h *SosyalHandler) CoverBaslat(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	b, err := h.servis.CoverBaslat(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, b)
}
