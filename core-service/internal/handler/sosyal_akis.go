package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// Akis handles GET /api/social/akis?sekme=senin-icin|takip|yeni&imlec=&limit=
func (h *SosyalHandler) Akis(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.Akis(c.Context(), userID, c.Query("sekme"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// ProfilYayinlari handles GET /api/social/profiller/:ad/yayinlar?tur=sarkilar|coverlar
func (h *SosyalHandler) ProfilYayinlari(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.ProfilYayinlari(c.Context(), userID, c.Params("ad"), c.Query("tur"),
		c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// Begen handles PUT /api/social/yayinlar/:id/begeni
func (h *SosyalHandler) Begen(c *fiber.Ctx) error {
	return h.begeni(c, true)
}

// BegeniyiKaldir handles DELETE /api/social/yayinlar/:id/begeni
func (h *SosyalHandler) BegeniyiKaldir(c *fiber.Ctx) error {
	return h.begeni(c, false)
}

func (h *SosyalHandler) begeni(c *fiber.Ctx, begen bool) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	b, err := h.servis.Begeni(c.Context(), userID, c.Params("id"), begen)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, b)
}
