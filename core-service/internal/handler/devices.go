package handler

import (
	"context"
	"net/url"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/pkg/response"
)

// DeviceStore keeps the push tokens of the users.
type DeviceStore interface {
	UpsertDevice(ctx context.Context, userID string, d model.Device) error
	DeleteDevice(ctx context.Context, userID, token string) error
}

// DevicesHandler registers and removes push tokens.
type DevicesHandler struct {
	store     DeviceStore
	validator *validator.Validate
}

func NewDevicesHandler(store DeviceStore, v *validator.Validate) *DevicesHandler {
	return &DevicesHandler{store: store, validator: v}
}

// deviceRegistration is the body of PUT /api/devices.
type deviceRegistration struct {
	Token      string  `json:"token" validate:"required,min=1,max=4096"`
	Platform   string  `json:"platform" validate:"required,oneof=ios android"`
	Locale     string  `json:"locale" validate:"required,oneof=tr en fr"`
	AppVersion *string `json:"appVersion" validate:"required,max=32"`
}

// Put handles PUT /api/devices: stores or refreshes the caller's push token.
func (h *DevicesHandler) Put(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req deviceRegistration
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}
	err := h.store.UpsertDevice(c.Context(), userID, model.Device{
		Token: req.Token, Platform: req.Platform, Language: req.Locale, AppVersion: *req.AppVersion,
	})
	if err != nil {
		return internalError(c, err)
	}
	return response.NoContent(c)
}

// Delete handles DELETE /api/devices/:token. Idempotent: an unknown token, or
// one that belongs to someone else, is also 204.
func (h *DevicesHandler) Delete(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	token := c.Params("token")
	if dec, err := url.PathUnescape(token); err == nil {
		token = dec
	}
	if token == "" || len(token) > 4096 {
		return response.ValidationError(c, i18n.KeyValidationFailed, nil)
	}
	if err := h.store.DeleteDevice(c.Context(), userID, token); err != nil {
		return internalError(c, err)
	}
	return response.NoContent(c)
}
