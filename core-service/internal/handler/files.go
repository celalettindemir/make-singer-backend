package handler

import (
	"errors"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type FileHandler struct {
	service   *service.FileService
	validator *validator.Validate
}

func NewFileHandler(svc *service.FileService, v *validator.Validate) *FileHandler {
	return &FileHandler{service: svc, validator: v}
}

// Sign handles POST /api/files/sign
// @Summary      Refresh file URLs
// @Description  Re-sign stored file URLs (1-50) owned by the caller. Response keeps the request order. Public export URLs have no expiresAt.
// @Tags         Files
// @Accept       json
// @Produce      json
// @Param        request body model.FileSignRequest true "URLs to refresh"
// @Success      200 {object} model.FileSignResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/files/sign [post]
func (h *FileHandler) Sign(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var req model.FileSignRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}
	result, err := h.service.Sign(c.Context(), userID, req.URLs)
	if err != nil {
		if errors.Is(err, client.ErrKeyNotOwned) {
			return response.NotFound(c, "File not found")
		}
		if errors.Is(err, client.ErrInvalidURL) {
			return response.ValidationError(c, "Gecersiz dosya URL'si", nil)
		}
		return response.ServiceError(c, "Adres uretilemedi")
	}
	return response.OK(c, result)
}
