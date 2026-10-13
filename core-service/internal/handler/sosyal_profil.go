package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// ProfilBen handles GET /api/social/profil/ben. Profil yoksa 404
// profil_yok (mobil bunu "henuz profil yok" olarak okur).
func (h *SosyalHandler) ProfilBen(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	p, err := h.servis.MyProfile(c.Context(), userID)
	if errors.Is(err, social.ErrNoProfile) {
		return response.Error(c, fiber.StatusNotFound, "profil_yok", err.Error(), nil)
	}
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, p)
}

// ProfilKaydet handles PUT /api/social/profil/ben.
func (h *SosyalHandler) ProfilKaydet(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var g social.ProfileInput
	if err := c.BodyParser(&g); err != nil {
		return govdeHatasi(c)
	}
	p, err := h.servis.SaveProfile(c.Context(), userID, g)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, p)
}

// AdUygun handles GET /api/social/profil/kullanici-adi/uygun?ad=
func (h *SosyalHandler) AdUygun(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	u, err := h.servis.CheckUsername(c.Context(), userID, c.Query("ad"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, u)
}

// Profil handles GET /api/social/profiller/:ad
func (h *SosyalHandler) Profil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	p, err := h.servis.Profile(c.Context(), userID, c.Params("ad"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, p)
}
