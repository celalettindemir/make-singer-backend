package handler

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type ExportHandler struct {
	service   *service.ExportService
	validator *validator.Validate
}

func NewExportHandler(svc *service.ExportService, v *validator.Validate) *ExportHandler {
	return &ExportHandler{
		service:   svc,
		validator: v,
	}
}

// MP3 handles POST /api/export/mp3
func (h *ExportHandler) MP3(c *fiber.Ctx) error {
	var req model.ExportMP3Request
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.ExportMP3(c.Context(), &req)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.OK(c, result)
}

// WAV handles POST /api/export/wav
func (h *ExportHandler) WAV(c *fiber.Ctx) error {
	var req model.ExportWAVRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.ExportWAV(c.Context(), &req)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.OK(c, result)
}

// Stems handles POST /api/export/stems
func (h *ExportHandler) Stems(c *fiber.Ctx) error {
	var req model.ExportStemsRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.ExportStems(c.Context(), &req)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.OK(c, result)
}
