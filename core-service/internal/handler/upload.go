package handler

import (
	"errors"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

const maxUploadSize = 50 * 1024 * 1024 // 50MB

type UploadHandler struct {
	service   *service.UploadService
	validator *validator.Validate
}

func NewUploadHandler(svc *service.UploadService, v *validator.Validate) *UploadHandler {
	return &UploadHandler{
		service:   svc,
		validator: v,
	}
}

// Vocal handles POST /api/upload/vocal
// @Summary      Upload vocal take
// @Description  Upload an audio file as a vocal take for a song section
// @Tags         Upload
// @Accept       multipart/form-data
// @Produce      json
// @Param        projectId formData string true "Project ID"
// @Param        sectionId formData string true "Section ID"
// @Param        takeName  formData string false "Take name"
// @Param        file      formData file   true "Audio file (WAV, MP3, M4A, AAC; max 50MB)"
// @Success      201 {object} model.UploadVocalResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/upload/vocal [post]
func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}

	// Get form values
	projectID := c.FormValue("projectId")
	if projectID == "" {
		return response.ValidationError(c, "projectId is required", nil)
	}

	sectionID := c.FormValue("sectionId")
	if sectionID == "" {
		return response.ValidationError(c, "sectionId is required", nil)
	}

	takeName := c.FormValue("takeName")

	// Get file
	file, err := c.FormFile("file")
	if err != nil {
		return response.ValidationError(c, "File is required", nil)
	}

	// Validate file size
	if file.Size > maxUploadSize {
		return response.ValidationError(c, "File size exceeds 50MB limit", map[string]interface{}{
			"maxSize":  maxUploadSize,
			"fileSize": file.Size,
		})
	}

	// Validate file type
	contentType := file.Header.Get("Content-Type")
	validTypes := map[string]bool{
		"audio/wav":   true,
		"audio/x-wav": true,
		"audio/wave":  true,
		"audio/mpeg":  true,
		"audio/mp3":   true,
		"audio/mp4":   true,
		"audio/x-m4a": true,
		"audio/aac":   true,
		"audio/x-aac": true,
	}

	if !validTypes[contentType] {
		return response.ValidationError(c, "Invalid file type. Supported: WAV, M4A, MP3, AAC", map[string]interface{}{
			"contentType": contentType,
		})
	}

	// Open file
	f, err := file.Open()
	if err != nil {
		return response.ServiceError(c, "Failed to open file")
	}
	defer f.Close()

	// Upload
	result, err := h.service.UploadVocal(c.Context(), userID, projectID, sectionID, takeName, f, file.Size)
	if err != nil {
		if errors.Is(err, client.ErrInvalidSegment) {
			return response.ValidationError(c, "Invalid projectId or sectionId", nil)
		}
		return response.ServiceError(c, err.Error())
	}

	return response.Created(c, result)
}

// DeleteVocal handles DELETE /api/upload/vocal/:takeId
// @Summary      Delete vocal take
// @Description  Delete a previously uploaded vocal take
// @Tags         Upload
// @Produce      json
// @Param        takeId path string true "Take ID"
// @Param        projectId query string true "Project ID"
// @Param        sectionId query string true "Section ID"
// @Success      204 "No Content"
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/upload/vocal/{takeId} [delete]
func (h *UploadHandler) DeleteVocal(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	takeID := c.Params("takeId")
	projectID := c.Query("projectId")
	sectionID := c.Query("sectionId")
	if takeID == "" || projectID == "" || sectionID == "" {
		return response.ValidationError(c, "takeId, projectId and sectionId are required", nil)
	}

	if err := h.service.DeleteVocal(c.Context(), userID, projectID, sectionID, takeID); err != nil {
		if errors.Is(err, client.ErrInvalidSegment) {
			return response.ValidationError(c, "Invalid takeId, projectId or sectionId", nil)
		}
		return response.ServiceError(c, err.Error())
	}

	return response.NoContent(c)
}
