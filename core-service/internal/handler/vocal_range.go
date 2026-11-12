package handler

import (
	"context"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/pkg/response"
)

// VocalRangeStore keeps the vocal range of each user.
type VocalRangeStore interface {
	GetVocalRange(ctx context.Context, userID string) (*model.VocalRangeRecord, error)
	SaveVocalRange(ctx context.Context, userID string, r model.VocalRangeRecord) error
}

// VocalRangeHandler reads and saves the caller's vocal range.
type VocalRangeHandler struct {
	store     VocalRangeStore
	validator *validator.Validate
}

func NewVocalRangeHandler(store VocalRangeStore, v *validator.Validate) *VocalRangeHandler {
	return &VocalRangeHandler{store: store, validator: v}
}

// vocalRangeBody is the response of both vocal-range endpoints.
type vocalRangeBody struct {
	Range *model.VocalRangeRecord `json:"range"`
}

// Get handles GET /api/profile/vocal-range.
// @Summary      Get the stored vocal range
// @Description  Returns the caller's stored vocal range, or null when none was saved.
// @Tags         account
// @Produce      json
// @Success      200 {object} vocalRangeBody
// @Failure      401 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/profile/vocal-range [get]
func (h *VocalRangeHandler) Get(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	rec, err := h.store.GetVocalRange(c.Context(), userID)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(vocalRangeBody{Range: rec})
}

// Put handles PUT /api/profile/vocal-range: saves (replaces) the caller's range.
// @Summary      Save the vocal range
// @Description  Stores the caller's vocal range, replacing any earlier one.
// @Tags         account
// @Accept       json
// @Produce      json
// @Param        request body model.VocalRangeRecord true "Vocal range"
// @Success      200 {object} vocalRangeBody
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/profile/vocal-range [put]
func (h *VocalRangeHandler) Put(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req model.VocalRangeRecord
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}
	if details := req.CenterErrors(); details != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, details)
	}
	req.MeasuredAt = req.MeasuredAt.UTC()
	if err := h.store.SaveVocalRange(c.Context(), userID, req); err != nil {
		return internalError(c, err)
	}
	return c.JSON(vocalRangeBody{Range: &req})
}
