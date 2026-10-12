package handler

import (
	"context"
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// SosyalServisi, Kesfet uclarinin kullandigi servis yuzudur
// (*sosyal.Servis uygular). Arayuz, handler testlerinin servisi elle
// yazilmis bir sahteyle degistirebilmesi icindir.
type SosyalServisi interface {
	ProfilBen(ctx context.Context, kullaniciID string) (*sosyal.Profil, error)
	ProfilKaydet(ctx context.Context, kullaniciID string, g sosyal.ProfilGirdisi) (*sosyal.Profil, error)
	AdUygunMu(ctx context.Context, kullaniciID, ad string) (*sosyal.AdUygunlugu, error)
	ProfilGoster(ctx context.Context, izleyiciID, ad string) (*sosyal.Profil, error)
}

var _ SosyalServisi = (*sosyal.Servis)(nil)

// SosyalHandler, /api/social altindaki uclardir.
type SosyalHandler struct {
	servis SosyalServisi
}

func NewSosyalHandler(servis SosyalServisi) *SosyalHandler {
	return &SosyalHandler{servis: servis}
}

// Rotalar, uclari verilen gruba baglar. yayinLimiti yalnizca
// POST /yayinlar'a uygulanir (kullanici basina saatlik).
func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
	r.Get("/profil/ben", h.ProfilBen)
	r.Put("/profil/ben", h.ProfilKaydet)
	r.Get("/profil/kullanici-adi/uygun", h.AdUygun)
	r.Get("/profiller/:ad", h.Profil)
}

// sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
// Sira onemlidir: ilk eslesen kazanir.
var sosyalHatalari = []struct {
	hata  error
	durum int
	kod   string
}{
	{sosyal.ErrBulunamadi, fiber.StatusNotFound, "bulunamadi"},
	{sosyal.ErrProfilYok, fiber.StatusConflict, "profil_yok"},
	{sosyal.ErrGecersizKullaniciAdi, fiber.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
	{sosyal.ErrKullaniciAdiAlinmis, fiber.StatusConflict, "kullanici_adi_alinmis"},
	{sosyal.ErrDogrulama, fiber.StatusUnprocessableEntity, "dogrulama"},
	{sosyal.ErrSahiplik, fiber.StatusForbidden, "sahiplik"},
	{sosyal.ErrKaynakBulunamadi, fiber.StatusNotFound, "kaynak_bulunamadi"},
	{sosyal.ErrAltyapiKapali, fiber.StatusForbidden, "altyapi_kapali"},
	{sosyal.ErrStemYok, fiber.StatusUnprocessableEntity, "stem_yok"},
	{sosyal.ErrDurum, fiber.StatusConflict, "durum"},
}

// sosyalHata, servis hatasini sozlesme govdesine cevirir:
// {"error":{"code","message"}}. Taninmayan hata 500 sunucu_hatasi olur
// ve ayrintisi yalnizca loga yazilir.
func sosyalHata(c *fiber.Ctx, err error) error {
	if errors.Is(err, sosyal.ErrKimlik) {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	for _, e := range sosyalHatalari {
		if errors.Is(err, e.hata) {
			return response.Error(c, e.durum, e.kod, err.Error(), nil)
		}
	}
	log.Printf("sosyal: %s %s: %v", c.Method(), c.Route().Path, err)
	return response.Error(c, fiber.StatusInternalServerError, "sunucu_hatasi", "Islem tamamlanamadi", nil)
}

// govdeHatasi, cozulemeyen JSON govdesidir: sosyal uclarda dogrulama
// hatasi 400 degil 422 dogrulama'dir.
func govdeHatasi(c *fiber.Ctx) error {
	return response.Error(c, fiber.StatusUnprocessableEntity, "dogrulama", "Gecersiz istek govdesi", nil)
}
