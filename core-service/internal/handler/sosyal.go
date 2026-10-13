package handler

import (
	"context"
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// SosyalServisi, Kesfet uclarinin kullandigi servis yuzudur
// (*sosyal.Servis uygular). Arayuz, handler testlerinin servisi elle
// yazilmis bir sahteyle degistirebilmesi icindir.
type SosyalServisi interface {
	MyProfile(ctx context.Context, kullaniciID string) (*social.Profile, error)
	SaveProfile(ctx context.Context, kullaniciID string, g social.ProfileInput) (*social.Profile, error)
	CheckUsername(ctx context.Context, kullaniciID, ad string) (*social.UsernameAvailability, error)
	Profile(ctx context.Context, izleyiciID, ad string) (*social.Profile, error)

	CreatePublication(ctx context.Context, kullaniciID string, ist social.PublicationRequest) (*social.PublicationResponse, error)
	Publication(ctx context.Context, kullaniciID, yayinID string) (*social.PublicationDetail, error)
	PublicationBySlug(ctx context.Context, kullaniciID, slug string) (*social.PublicationDetail, error)
	UpdatePublication(ctx context.Context, kullaniciID, yayinID string, yama social.PublicationPatch) (*social.PublicationDetail, error)
	DeletePublication(ctx context.Context, kullaniciID, yayinID string) error
	Retry(ctx context.Context, kullaniciID, yayinID string) (*social.PublicationResponse, error)
	Files(ctx context.Context, kullaniciID, yayinID string) (*social.PublicationFiles, error)

	Feed(ctx context.Context, kullaniciID, sekme, imlec string, limit int) (*social.Page[social.PublicationSummary], error)
	ProfilePublications(ctx context.Context, izleyiciID, ad, tur, imlec string, limit int) (*social.Page[social.PublicationSummary], error)
	SetLike(ctx context.Context, kullaniciID, yayinID string, begen bool) (*social.LikeStatus, error)

	SetFollow(ctx context.Context, kullaniciID, ad string, takip bool) (*social.FollowStatus, error)
	Suggestions(ctx context.Context, kullaniciID string) ([]social.ProfileSummary, error)

	Comments(ctx context.Context, kullaniciID, yayinID, imlec string, limit int) (*social.Page[social.Comment], error)
	PostComment(ctx context.Context, kullaniciID, yayinID string, g social.CommentInput) (*social.Comment, error)
	DeleteComment(ctx context.Context, kullaniciID, yorumID string) error

	Report(ctx context.Context, kullaniciID string, g social.ReportInput) error

	Covers(ctx context.Context, kullaniciID, yayinID, imlec string, limit int) (*social.Page[social.PublicationSummary], error)
	StartCover(ctx context.Context, kullaniciID, yayinID string) (*social.CoverStart, error)

	Preview(ctx context.Context, slug string) (*social.Preview, error)
}

var _ SosyalServisi = (*social.Service)(nil)

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
	Cover fiber.Handler // POST /yayinlar/:id/cover-baslat (saatlik)
}

// Rotalar, uclari verilen gruba baglar; limitler yalnizca ilgili uclara
// uygulanir. Eksik bir limiter kayit aninda panik eder: Fiber nil
// handler'i denetlemez, aksi halde ilk istekte 500 olurdu.
func (h *SosyalHandler) Rotalar(r fiber.Router, l SosyalLimitleri) {
	if l.Yayin == nil || l.Yorum == nil || l.Cover == nil {
		panic("handler: SosyalLimitleri eksik; Yayin, Yorum ve Cover limiterlarinin hepsi doldurulmali")
	}
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

	r.Get("/yayinlar/:id/coverlar", h.Coverlar)
	r.Post("/yayinlar/:id/cover-baslat", l.Cover, h.CoverBaslat)
}

// sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
// Sira onemlidir: ilk eslesen kazanir.
var sosyalHatalari = []struct {
	hata  error
	durum int
	kod   string
}{
	{social.ErrNotFound, fiber.StatusNotFound, "bulunamadi"},
	{social.ErrNoProfile, fiber.StatusConflict, "profil_yok"},
	{social.ErrInvalidUsername, fiber.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
	{social.ErrUsernameTaken, fiber.StatusConflict, "kullanici_adi_alinmis"},
	{social.ErrValidation, fiber.StatusUnprocessableEntity, "dogrulama"},
	{social.ErrOwnership, fiber.StatusForbidden, "sahiplik"},
	{social.ErrSourceNotFound, fiber.StatusNotFound, "kaynak_bulunamadi"},
	{social.ErrBackingDisabled, fiber.StatusForbidden, "altyapi_kapali"},
	{social.ErrNoStems, fiber.StatusUnprocessableEntity, "stem_yok"},
	{social.ErrInvalidStatus, fiber.StatusConflict, "durum"},
	{social.ErrSelfFollow, fiber.StatusUnprocessableEntity, "kendini_takip"},
	{social.ErrCommentsDisabled, fiber.StatusForbidden, "yorum_kapali"},
	{social.ErrForbidden, fiber.StatusForbidden, "yetki"},
}

// sosyalHata, servis hatasini sozlesme govdesine cevirir:
// {"error":{"code","message"}}. Taninmayan hata 500 sunucu_hatasi olur
// ve ayrintisi yalnizca loga yazilir.
func sosyalHata(c *fiber.Ctx, err error) error {
	if errors.Is(err, social.ErrInvalidIdentity) {
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
