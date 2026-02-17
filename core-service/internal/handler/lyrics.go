package handler

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type LyricsHandler struct {
	service   *service.LyricsService
	validator *validator.Validate
}

func NewLyricsHandler(svc *service.LyricsService, v *validator.Validate) *LyricsHandler {
	return &LyricsHandler{
		service:   svc,
		validator: v,
	}
}

// Generate handles POST /api/lyrics/generate
// @Summary      Generate lyrics
// @Description  Generate lyrics for a song section using AI
// @Tags         Lyrics
// @Accept       json
// @Produce      json
// @Param        request body model.LyricsGenerateRequest true "Generate request"
// @Success      200 {object} model.LyricsGenerateResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      502 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/lyrics/generate [post]
func (h *LyricsHandler) Generate(c *fiber.Ctx) error {
	var req model.LyricsGenerateRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.Generate(c.Context(), &req)
	if err != nil {
		return response.AIError(c, err.Error())
	}

	return response.OK(c, result)
}

// Rewrite handles POST /api/lyrics/rewrite
// @Summary      Rewrite lyrics
// @Description  Rewrite existing lyrics with AI-powered suggestions
// @Tags         Lyrics
// @Accept       json
// @Produce      json
// @Param        request body model.LyricsRewriteRequest true "Rewrite request"
// @Success      200 {object} model.LyricsRewriteResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      502 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/lyrics/rewrite [post]
func (h *LyricsHandler) Rewrite(c *fiber.Ctx) error {
	var req model.LyricsRewriteRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.Rewrite(c.Context(), &req)
	if err != nil {
		return response.AIError(c, err.Error())
	}

	return response.OK(c, result)
}

// formatValidationErrors formats validator errors for response
func formatValidationErrors(err error) interface{} {
	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		errors := make(map[string]string)
		for _, e := range validationErrors {
			errors[e.Field()] = e.Tag()
		}
		return errors
	}
	return nil
}
