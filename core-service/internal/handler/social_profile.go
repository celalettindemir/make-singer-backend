package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// MyProfile handles GET /api/social/profil/ben. When there is no
// profile it returns 404 profil_yok (mobile reads that as "no profile yet").
func (h *SocialHandler) MyProfile(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	profile, err := h.service.MyProfile(c.Context(), userID)
	if errors.Is(err, social.ErrNoProfile) {
		return response.Error(c, fiber.StatusNotFound, "profil_yok", i18n.KeySocialProfileMissing, nil)
	}
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, profile)
}

// SaveProfile handles PUT /api/social/profil/ben.
func (h *SocialHandler) SaveProfile(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var input social.ProfileInput
	if err := c.BodyParser(&input); err != nil {
		return badBodyError(c)
	}
	profile, err := h.service.SaveProfile(c.Context(), userID, input)
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, profile)
}

// CheckUsername handles GET /api/social/profil/kullanici-adi/uygun?ad=
func (h *SocialHandler) CheckUsername(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	availability, err := h.service.CheckUsername(c.Context(), userID, c.Query("ad"))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, availability)
}

// Profile handles GET /api/social/profiller/:ad
func (h *SocialHandler) Profile(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	profile, err := h.service.Profile(c.Context(), userID, c.Params("ad"))
	if err != nil {
		return socialError(c, err)
	}
	return response.OK(c, profile)
}
