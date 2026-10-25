package handler

import (
	"errors"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

const maxUploadSize = 50 * 1024 * 1024 // 50MB

// vocalContentTypes are the accepted part Content-Types of a vocal upload.
// React Native on iOS replaces the type the app sets with the system MIME type
// of the file extension, which is audio/vnd.wave for .wav.
var vocalContentTypes = map[string]bool{
	"audio/wav":      true,
	"audio/x-wav":    true,
	"audio/wave":     true,
	"audio/vnd.wave": true,
	"audio/mpeg":     true,
	"audio/mp3":      true,
	"audio/mp4":      true,
	"audio/x-m4a":    true,
	"audio/aac":      true,
	"audio/x-aac":    true,
}

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
// @Param        micKind   formData string false "Input that recorded the take (built_in, headset, bluetooth, usb, other); unknown values are stored as other" Enums(built_in, headset, bluetooth, usb, other)
// @Param        micName   formData string false "System name of that input; trimmed, control characters removed, cut to 64 characters"
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
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}

	// Get form values
	projectID := c.FormValue("projectId")
	if projectID == "" {
		return response.ValidationError(c, i18n.KeyProjectIDRequired, nil)
	}

	sectionID := c.FormValue("sectionId")
	if sectionID == "" {
		return response.ValidationError(c, i18n.KeySectionIDRequired, nil)
	}

	takeName := c.FormValue("takeName")

	// Optional mic fields (R-VOC-17): never a reason to reject the request.
	mic := model.MicInfo{
		Kind: normalizeMicKind(c.FormValue("micKind")),
		Name: cleanMicName(c.FormValue("micName")),
	}

	// Get file
	file, err := c.FormFile("file")
	if err != nil {
		return response.ValidationError(c, i18n.KeyFileRequired, nil)
	}

	// Validate file size
	if file.Size > maxUploadSize {
		return response.ValidationError(c, i18n.KeyFileTooLarge, map[string]interface{}{
			"maxSize":  maxUploadSize,
			"fileSize": file.Size,
		})
	}

	// Validate file type
	contentType := file.Header.Get("Content-Type")
	if !vocalContentTypes[contentType] {
		return response.ValidationError(c, i18n.KeyFileTypeInvalid, map[string]interface{}{
			"contentType": contentType,
		})
	}

	// Open file
	f, err := file.Open()
	if err != nil {
		return response.ServiceError(c, i18n.KeyFileOpenFailed)
	}
	defer f.Close()

	// Upload
	result, err := h.service.UploadVocal(c.Context(), userID, projectID, sectionID, takeName, f, file.Size, mic)
	if err != nil {
		if errors.Is(err, client.ErrInvalidSegment) {
			return response.ValidationError(c, i18n.KeyUploadIDsInvalid, nil)
		}
		return internalError(c, err)
	}

	return response.Created(c, result)
}

var micKinds = map[string]bool{"built_in": true, "headset": true, "bluetooth": true, "usb": true, "other": true}

// maxMicNameRunes is the cap on micName, counted in characters, not bytes.
const maxMicNameRunes = 64

// normalizeMicKind: empty stays empty (not stored), unknown becomes "other".
func normalizeMicKind(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if micKinds[v] {
		return v
	}
	return "other"
}

// cleanMicName trims spaces, drops control characters and cuts to 64 runes.
func cleanMicName(v string) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if r := []rune(v); len(r) > maxMicNameRunes {
		v = strings.TrimSpace(string(r[:maxMicNameRunes]))
	}
	return v
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
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	takeID := c.Params("takeId")
	projectID := c.Query("projectId")
	sectionID := c.Query("sectionId")
	if takeID == "" || projectID == "" || sectionID == "" {
		return response.ValidationError(c, i18n.KeyVocalIDsRequired, nil)
	}

	if err := h.service.DeleteVocal(c.Context(), userID, projectID, sectionID, takeID); err != nil {
		if errors.Is(err, client.ErrInvalidSegment) {
			return response.ValidationError(c, i18n.KeyVocalIDsInvalid, nil)
		}
		return internalError(c, err)
	}

	return response.NoContent(c)
}
