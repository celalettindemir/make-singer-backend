package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type HesapHandler struct {
	service *service.HesapService
}

func NewHesapHandler(svc *service.HesapService) *HesapHandler {
	return &HesapHandler{service: svc}
}

// Sil handles DELETE /api/account
// @Summary      Delete account
// @Description  Permanently delete the caller's account, linked sign-ins, refresh tokens and every stored file. The same token is rejected afterwards.
// @Tags         Account
// @Success      204
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/account [delete]
func (h *HesapHandler) Sil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	if err := h.service.Sil(c.Context(), userID); err != nil {
		if errors.Is(err, service.ErrHesapYok) {
			return response.NotFound(c, "Hesap bulunamadi")
		}
		return response.ServiceError(c, "Hesap silinemedi")
	}
	return c.SendStatus(fiber.StatusNoContent)
}
