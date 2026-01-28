package handler

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
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
func (h *UploadHandler) Vocal(c *fiber.Ctx) error {
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
		"audio/wav":      true,
		"audio/x-wav":    true,
		"audio/wave":     true,
		"audio/mpeg":     true,
		"audio/mp3":      true,
		"audio/mp4":      true,
		"audio/x-m4a":    true,
		"audio/aac":      true,
		"audio/x-aac":    true,
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
	result, err := h.service.UploadVocal(c.Context(), projectID, sectionID, takeName, f, file.Size)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.Created(c, result)
}

// DeleteVocal handles DELETE /api/upload/vocal/:takeId
func (h *UploadHandler) DeleteVocal(c *fiber.Ctx) error {
	takeID := c.Params("takeId")
	if takeID == "" {
		return response.ValidationError(c, "Take ID is required", nil)
	}

	err := h.service.DeleteVocal(c.Context(), takeID)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.NoContent(c)
}
