package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// TakipEt handles PUT /api/social/profiller/:ad/takip
func (h *SosyalHandler) TakipEt(c *fiber.Ctx) error {
	return h.takip(c, true)
}

// TakibiBirak handles DELETE /api/social/profiller/:ad/takip
func (h *SosyalHandler) TakibiBirak(c *fiber.Ctx) error {
	return h.takip(c, false)
}

func (h *SosyalHandler) takip(c *fiber.Ctx, takip bool) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	d, err := h.servis.SetFollow(c.Context(), userID, c.Params("ad"), takip)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, d)
}

// Oneriler handles GET /api/social/oneriler (en cok 10 ProfilOzeti, sayfasiz).
func (h *SosyalHandler) Oneriler(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	o, err := h.servis.Suggestions(c.Context(), userID)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, o)
}
