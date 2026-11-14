package handler

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

const maxImportProjectName = 120

var importProjectIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ImportHandler serves the bring-your-own-track routes.
type ImportHandler struct {
	service  *service.ImportService
	maxBytes int64
	minMs    int
	maxMs    int
}

func NewImportHandler(svc *service.ImportService, maxBytes int64, minMs, maxMs int) *ImportHandler {
	return &ImportHandler{service: svc, maxBytes: maxBytes, minMs: minMs, maxMs: maxMs}
}

// Upload handles POST /api/imports
// @Summary      Bring your own track
// @Description  Store a track, measure it, charge the import and queue an import job
// @Tags         Upload
// @Accept       multipart/form-data
// @Produce      json
// @Param        projectId   formData string true  "Project ID"
// @Param        projectName formData string false "Project name for the push text"
// @Param        file        formData file   true  "Audio file (WAV, MP3, M4A, AAC)"
// @Success      202 {object} model.ImportStartResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      402 {object} response.ErrorResponse
// @Failure      409 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      503 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/imports [post]
func (h *ImportHandler) Upload(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	projectID := c.FormValue("projectId")
	if projectID == "" {
		return response.ValidationError(c, i18n.KeyProjectIDRequired, nil)
	}
	if !importProjectIDPattern.MatchString(projectID) {
		return response.ValidationError(c, i18n.KeyHumProjectInvalid, nil)
	}
	projectName := c.FormValue("projectName")
	if utf8.RuneCountInString(projectName) > maxImportProjectName {
		return response.ValidationError(c, i18n.KeyValidationFailed, map[string]string{"projectName": "max"})
	}

	file, err := c.FormFile("file")
	if err != nil {
		return response.ValidationError(c, i18n.KeyFileRequired, nil)
	}
	tooLarge := func(size int64) error {
		return response.ValidationError(c, i18n.KeyFileTooLarge, map[string]interface{}{
			"maxSize":  h.maxBytes,
			"fileSize": size,
		})
	}
	if file.Size > h.maxBytes {
		return tooLarge(file.Size)
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
	data, err := io.ReadAll(io.LimitReader(f, h.maxBytes+1))
	if err != nil {
		return response.ServiceError(c, i18n.KeyFileOpenFailed)
	}
	if int64(len(data)) > h.maxBytes {
		return tooLarge(int64(len(data)))
	}

	result, err := h.service.Start(c.Context(), userID, service.ImportUpload{
		ProjectID: projectID, ProjectName: projectName, ContentType: contentType, Data: data,
	})
	if err != nil {
		return h.startError(c, err)
	}
	return response.Accepted(c, result)
}

// startError maps a start error to its answer.
func (h *ImportHandler) startError(c *fiber.Ctx, err error) error {
	var de *service.ImportDurationError
	var insufficient *credit.InsufficientError
	switch {
	case errors.As(err, &de):
		return response.ValidationError(c, i18n.KeyImportDuration, map[string]interface{}{
			"minMs":      h.minMs,
			"maxMs":      h.maxMs,
			"durationMs": de.DurationMs,
		})
	case errors.Is(err, service.ErrImportUnreadable):
		return response.ValidationError(c, i18n.KeyImportUnreadable, nil)
	case errors.Is(err, service.ErrImportDisabled):
		return response.Error(c, fiber.StatusServiceUnavailable, response.CodeServiceError, i18n.KeyInternal, nil)
	case errors.As(err, &insufficient):
		return response.InsufficientCredits(c, insufficient.Required, insufficient.Balance, insufficient.Debt)
	case errors.Is(err, jobs.ErrActiveLyricsJob):
		return response.JobActive(c, jobs.ActiveJobID(err))
	case errors.Is(err, client.ErrInvalidSegment):
		return response.ValidationError(c, i18n.KeyHumProjectInvalid, nil)
	}
	return internalError(c, err)
}

// Retry handles POST /api/imports/:jobId/retry
// @Summary      Run a failed import again
// @Description  Start a new import job from the stored source of a failed or canceled import
// @Tags         Upload
// @Produce      json
// @Param        jobId path string true "Import job ID"
// @Success      202 {object} model.ImportStartResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      402 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      409 {object} response.ErrorResponse
// @Failure      410 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      503 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/imports/{jobId}/retry [post]
func (h *ImportHandler) Retry(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	// Params aliases fasthttp's buffer, so the id is copied.
	id := strings.Clone(c.Params("jobId"))
	if _, err := uuid.Parse(id); err != nil {
		return response.NotFound(c, i18n.KeyJobNotFound)
	}
	result, err := h.service.Retry(c.Context(), userID, id)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrJobNotFound):
			return response.NotFound(c, i18n.KeyJobNotFound)
		case errors.Is(err, service.ErrImportNotRetryable):
			return response.Error(c, fiber.StatusConflict, response.CodeJobNotRetryable, i18n.KeyImportNotRetryable, map[string]string{})
		case errors.Is(err, service.ErrImportSourceGone):
			return response.Error(c, fiber.StatusGone, response.CodeImportSourceGone, i18n.KeyImportSourceGone, map[string]string{})
		}
		return h.startError(c, err)
	}
	return response.Accepted(c, result)
}
