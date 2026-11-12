package handler

import (
	"errors"
	"io"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

const maxHumSize = 10 * 1024 * 1024 // 10MB

type HumHandler struct {
	service *service.HumService
}

func NewHumHandler(svc *service.HumService) *HumHandler {
	return &HumHandler{service: svc}
}

// Upload handles POST /api/hum
// @Summary      Upload a hum
// @Description  Store a 5-30 second hum as mp3, measure it and read the words in it
// @Tags         Hum
// @Accept       multipart/form-data
// @Produce      json
// @Param        projectId formData string true "Project ID"
// @Param        language  formData string false "Language of the words (tr, en, fr); other values are ignored" Enums(tr, en, fr)
// @Param        file      formData file   true "Audio file (WAV, MP3, M4A, AAC; max 10MB)"
// @Success      201 {object} model.HumUploadResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Failure      503 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/hum [post]
func (h *HumHandler) Upload(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	projectID := c.FormValue("projectId")
	if projectID == "" {
		return response.ValidationError(c, i18n.KeyProjectIDRequired, nil)
	}
	var lang model.Language
	switch l := model.Language(c.FormValue("language")); l {
	case model.LanguageTR, model.LanguageEN, model.LanguageFR:
		lang = l
	}

	file, err := c.FormFile("file")
	if err != nil {
		return response.ValidationError(c, i18n.KeyFileRequired, nil)
	}
	if file.Size > maxHumSize {
		return response.ValidationError(c, i18n.KeyHumFileTooLarge, map[string]interface{}{
			"maxSize":  maxHumSize,
			"fileSize": file.Size,
		})
	}
	contentType := file.Header.Get("Content-Type")
	if !vocalContentTypes[contentType] {
		return response.ValidationError(c, i18n.KeyFileTypeInvalid, map[string]interface{}{
			"contentType": contentType,
		})
	}
	f, err := file.Open()
	if err != nil {
		return response.ServiceError(c, i18n.KeyFileOpenFailed)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxHumSize+1))
	if err != nil {
		return response.ServiceError(c, i18n.KeyFileOpenFailed)
	}
	if len(data) > maxHumSize {
		return response.ValidationError(c, i18n.KeyHumFileTooLarge, map[string]interface{}{
			"maxSize":  maxHumSize,
			"fileSize": len(data),
		})
	}

	result, err := h.service.Upload(c.Context(), userID, projectID, lang, contentType, data)
	if err != nil {
		var de *service.HumDurationError
		switch {
		case errors.Is(err, client.ErrInvalidSegment):
			return response.ValidationError(c, i18n.KeyUploadIDsInvalid, nil)
		case errors.As(err, &de):
			return response.ValidationError(c, i18n.KeyHumDuration, map[string]interface{}{
				"minMs":      model.HumMinMs,
				"maxMs":      model.HumMaxMs,
				"durationMs": de.DurationMs,
			})
		case errors.Is(err, service.ErrHumUnreadable):
			return response.ValidationError(c, i18n.KeyHumUnreadable, nil)
		case errors.Is(err, service.ErrHumUnavailable):
			return response.Error(c, fiber.StatusServiceUnavailable, response.CodeServiceError, i18n.KeyInternal, nil)
		}
		return internalError(c, err)
	}
	return response.Created(c, result)
}

// Delete handles DELETE /api/hum/:humId
// @Summary      Delete a hum
// @Description  Delete an uploaded hum; always 204, even when it is already gone
// @Tags         Hum
// @Produce      json
// @Param        humId     path  string true "Hum ID"
// @Param        projectId query string true "Project ID"
// @Success      204 "No Content"
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/hum/{humId} [delete]
func (h *HumHandler) Delete(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	projectID := c.Query("projectId")
	if projectID == "" {
		return response.ValidationError(c, i18n.KeyProjectIDRequired, nil)
	}
	if err := h.service.Delete(c.Context(), userID, projectID, c.Params("humId")); err != nil {
		if errors.Is(err, client.ErrInvalidSegment) {
			return response.ValidationError(c, i18n.KeyUploadIDsInvalid, nil)
		}
		return internalError(c, err)
	}
	return response.NoContent(c)
}
