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

	YayinOlustur(ctx context.Context, kullaniciID string, ist sosyal.YayinIstegi) (*sosyal.YayinYaniti, error)
	YayinGoster(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinDetayi, error)
	SlugGoster(ctx context.Context, kullaniciID, slug string) (*sosyal.YayinDetayi, error)
	YayinGuncelle(ctx context.Context, kullaniciID, yayinID string, yama sosyal.YayinYamasi) (*sosyal.YayinDetayi, error)
	YayinSil(ctx context.Context, kullaniciID, yayinID string) error
	TekrarDene(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinYaniti, error)
	Dosyalar(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinDosyalari, error)

	Akis(ctx context.Context, kullaniciID, sekme, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
	ProfilYayinlari(ctx context.Context, izleyiciID, ad, tur, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
	Begeni(ctx context.Context, kullaniciID, yayinID string, begen bool) (*sosyal.BegeniDurumu, error)

	Takip(ctx context.Context, kullaniciID, ad string, takip bool) (*sosyal.TakipDurumu, error)
	Oneriler(ctx context.Context, kullaniciID string) ([]sosyal.ProfilOzeti, error)

	Yorumlar(ctx context.Context, kullaniciID, yayinID, imlec string, limit int) (*sosyal.Sayfa[sosyal.Yorum], error)
	YorumYaz(ctx context.Context, kullaniciID, yayinID string, g sosyal.YorumGirdisi) (*sosyal.Yorum, error)
	YorumSil(ctx context.Context, kullaniciID, yorumID string) error

	Sikayet(ctx context.Context, kullaniciID string, g sosyal.SikayetGirdisi) error
}

var _ SosyalServisi = (*sosyal.Servis)(nil)

// SosyalHandler, /api/social altindaki uclardir.
type SosyalHandler struct {
	servis SosyalServisi
}

func NewSosyalHandler(servis SosyalServisi) *SosyalHandler {
	return &SosyalHandler{servis: servis}
}

// SosyalLimitleri, tek tek uclara uygulanan kullanici basina limitlerdir.
// Hepsi doldurulmalidir.
type SosyalLimitleri struct {
	Yayin fiber.Handler // POST /yayinlar (saatlik)
	Yorum fiber.Handler // POST /yayinlar/:id/yorumlar (dakikalik, 429 yavasla)
}

// Rotalar, uclari verilen gruba baglar; limitler yalnizca ilgili uclara
// uygulanir.
func (h *SosyalHandler) Rotalar(r fiber.Router, l SosyalLimitleri) {
	r.Get("/profil/ben", h.ProfilBen)
	r.Put("/profil/ben", h.ProfilKaydet)
	r.Get("/profil/kullanici-adi/uygun", h.AdUygun)
	r.Get("/profiller/:ad", h.Profil)
	r.Get("/profiller/:ad/yayinlar", h.ProfilYayinlari)
	r.Get("/akis", h.Akis)

	r.Post("/yayinlar", l.Yayin, h.YayinOlustur)
	r.Get("/yayinlar/:id", h.Yayin)
	r.Patch("/yayinlar/:id", h.YayinGuncelle)
	r.Delete("/yayinlar/:id", h.YayinSil)
	r.Post("/yayinlar/:id/tekrar-dene", h.TekrarDene)
	r.Post("/yayinlar/:id/dosyalar", h.Dosyalar)
	r.Get("/s/:slug", h.Slug)
	r.Put("/yayinlar/:id/begeni", h.Begen)
	r.Delete("/yayinlar/:id/begeni", h.BegeniyiKaldir)

	r.Put("/profiller/:ad/takip", h.TakipEt)
	r.Delete("/profiller/:ad/takip", h.TakibiBirak)
	r.Get("/oneriler", h.Oneriler)

	r.Get("/yayinlar/:id/yorumlar", h.Yorumlar)
	r.Post("/yayinlar/:id/yorumlar", l.Yorum, h.YorumYaz)
	r.Delete("/yorumlar/:id", h.YorumSil)

	r.Post("/sikayetler", h.Sikayet)
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
	{sosyal.ErrKendiniTakip, fiber.StatusUnprocessableEntity, "kendini_takip"},
	{sosyal.ErrYorumKapali, fiber.StatusForbidden, "yorum_kapali"},
	{sosyal.ErrYetki, fiber.StatusForbidden, "yetki"},
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
