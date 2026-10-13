package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// PublicRotalar, kimliksiz Kesfet uclarini baglar (spec §4.8). main
// bunlari /api auth grubundan ONCE kaydeder; ipLimiti istemci IP'si
// basina (ya da web anahtariyla ortak) siniri uygular. noStore limiter'dan
// once calisir; boylece 404, 429 ve 500 yanitlari da ara bellege alinmaz.
// ipLimiti nil ise kayit aninda panik eder.
func (h *SosyalHandler) PublicRotalar(r fiber.Router, ipLimiti fiber.Handler) {
	if ipLimiti == nil {
		panic("handler: PublicRotalar icin IP limiteri doldurulmali")
	}
	r.Get("/s/:slug", noStore, ipLimiti, h.Onizleme)
}

// noStore, yanitin (durumu ne olursa olsun) ara bellege alinmamasini
// isteyen basligi yazar ve zinciri surdurur.
func noStore(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Next()
}

// Onizleme handles GET /api/public/s/:slug (oturumsuz). Yalnizca herkes ve
// link yayinlari; digerleri 404 bulunamadi. Yanit imzali bir adres
// tasidigi icin ara bellege alinmaz (Cache-Control rotadaki noStore'dan).
func (h *SosyalHandler) Onizleme(c *fiber.Ctx) error {
	o, err := h.servis.Preview(c.Context(), c.Params("slug"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, o)
}
