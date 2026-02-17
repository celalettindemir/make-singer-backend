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
// @Summary      Export as MP3
// @Description  Export the mastered track as an MP3 file with optional metadata
// @Tags         Export
// @Accept       json
// @Produce      json
// @Param        request body model.ExportMP3Request true "MP3 export request"
// @Success      200 {object} model.ExportMP3Response
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/export/mp3 [post]
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
// @Summary      Export as WAV
// @Description  Export the mastered track as a WAV file with configurable bit depth and sample rate
// @Tags         Export
// @Accept       json
// @Produce      json
// @Param        request body model.ExportWAVRequest true "WAV export request"
// @Success      200 {object} model.ExportWAVResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/export/wav [post]
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
// @Summary      Export stems
// @Description  Export individual stems as a bundled archive
// @Tags         Export
// @Accept       json
// @Produce      json
// @Param        request body model.ExportStemsRequest true "Stems export request"
// @Success      200 {object} model.ExportStemsResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/export/stems [post]
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
