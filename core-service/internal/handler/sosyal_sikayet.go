package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// Sikayet handles POST /api/social/sikayetler. 201, govdesiz; tekrar da
// 201'dir (yeni kayit acilmaz).
func (h *SosyalHandler) Sikayet(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var g social.ReportInput
	if err := c.BodyParser(&g); err != nil {
		return govdeHatasi(c)
	}
	if err := h.servis.Report(c.Context(), userID, g); err != nil {
		return sosyalHata(c, err)
	}
	// SendStatus bos govdeye "Created" yazardi; govde bilerek bos kalir.
	c.Status(fiber.StatusCreated)
	return nil
}
