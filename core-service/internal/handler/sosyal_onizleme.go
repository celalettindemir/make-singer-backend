package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// PublicRotalar, kimliksiz Kesfet uclarini baglar (spec §4.8). main
// bunlari /api auth grubundan ONCE kaydeder; ipLimiti istemci IP'si
// basina (ya da web anahtariyla ortak) siniri uygular.
func (h *SosyalHandler) PublicRotalar(r fiber.Router, ipLimiti fiber.Handler) {
	r.Get("/s/:slug", ipLimiti, h.Onizleme)
}

// Onizleme handles GET /api/public/s/:slug (oturumsuz). Yalnizca herkes ve
// link yayinlari; digerleri 404 bulunamadi. Yanit imzali bir adres
// tasidigi icin ara bellege alinmaz.
func (h *SosyalHandler) Onizleme(c *fiber.Ctx) error {
	o, err := h.servis.Onizleme(c.Context(), c.Params("slug"))
	if err != nil {
		return sosyalHata(c, err)
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return response.OK(c, o)
}
