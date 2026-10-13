package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

const sosyalKullanici = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

// sahteSosyal, SosyalServisi'nin elle yazilmis sahtesidir. Her alan
// ilgili metodun ne donecegini belirler; son cagrinin girdileri kaydedilir.
type sahteSosyal struct {
	hata error

	profil      *social.Profile
	adUygunlugu *social.UsernameAvailability
	yayinYaniti *social.PublicationResponse
	detay       *social.PublicationDetail
	dosyalar    *social.PublicationFiles
	sayfa       *social.Page[social.PublicationSummary]
	begeni      *social.LikeStatus
	takip       *social.FollowStatus
	oneriler    []social.ProfileSummary
	yorum       *social.Comment
	yorumSayfa  *social.Page[social.Comment]
	cover       *social.CoverStart
	onizleme    *social.Preview

	sonKullanici string
	sonAd        string
	sonProfil    social.ProfileInput
	sonYayin     string
	sonIstek     social.PublicationRequest
	sonYama      social.PublicationPatch
	sonSekme     string
	sonTur       string
	sonImlec     string
	sonLimit     int
	sonBegen     bool
	sonTakip     bool
	sonYorum     social.CommentInput
	sonSikayet   social.ReportInput
}

func (s *sahteSosyal) MyProfile(_ context.Context, k string) (*social.Profile, error) {
	s.sonKullanici = k
	return s.profil, s.hata
}

func (s *sahteSosyal) SaveProfile(_ context.Context, k string, g social.ProfileInput) (*social.Profile, error) {
	s.sonKullanici, s.sonProfil = k, g
	return s.profil, s.hata
}

func (s *sahteSosyal) CheckUsername(_ context.Context, k, ad string) (*social.UsernameAvailability, error) {
	s.sonKullanici, s.sonAd = k, ad
	return s.adUygunlugu, s.hata
}

func (s *sahteSosyal) Profile(_ context.Context, k, ad string) (*social.Profile, error) {
	s.sonKullanici, s.sonAd = k, ad
	return s.profil, s.hata
}

func (s *sahteSosyal) CreatePublication(_ context.Context, k string, ist social.PublicationRequest) (*social.PublicationResponse, error) {
	s.sonKullanici, s.sonIstek = k, ist
	return s.yayinYaniti, s.hata
}

func (s *sahteSosyal) Publication(_ context.Context, k, id string) (*social.PublicationDetail, error) {
	s.sonKullanici, s.sonYayin = k, id
	return s.detay, s.hata
}

func (s *sahteSosyal) PublicationBySlug(_ context.Context, k, slug string) (*social.PublicationDetail, error) {
	s.sonKullanici, s.sonYayin = k, slug
	return s.detay, s.hata
}

func (s *sahteSosyal) UpdatePublication(_ context.Context, k, id string, y social.PublicationPatch) (*social.PublicationDetail, error) {
	s.sonKullanici, s.sonYayin, s.sonYama = k, id, y
	return s.detay, s.hata
}

func (s *sahteSosyal) DeletePublication(_ context.Context, k, id string) error {
	s.sonKullanici, s.sonYayin = k, id
	return s.hata
}

func (s *sahteSosyal) Retry(_ context.Context, k, id string) (*social.PublicationResponse, error) {
	s.sonKullanici, s.sonYayin = k, id
	return s.yayinYaniti, s.hata
}

func (s *sahteSosyal) Files(_ context.Context, k, id string) (*social.PublicationFiles, error) {
	s.sonKullanici, s.sonYayin = k, id
	return s.dosyalar, s.hata
}

func (s *sahteSosyal) Feed(_ context.Context, k, sekme, imlec string, limit int) (*social.Page[social.PublicationSummary], error) {
	s.sonKullanici, s.sonSekme, s.sonImlec, s.sonLimit = k, sekme, imlec, limit
	return s.sayfa, s.hata
}

func (s *sahteSosyal) ProfilePublications(_ context.Context, k, ad, tur, imlec string, limit int) (*social.Page[social.PublicationSummary], error) {
	s.sonKullanici, s.sonAd, s.sonTur, s.sonImlec, s.sonLimit = k, ad, tur, imlec, limit
	return s.sayfa, s.hata
}

func (s *sahteSosyal) SetLike(_ context.Context, k, id string, begen bool) (*social.LikeStatus, error) {
	s.sonKullanici, s.sonYayin, s.sonBegen = k, id, begen
	return s.begeni, s.hata
}

func (s *sahteSosyal) SetFollow(_ context.Context, k, ad string, takip bool) (*social.FollowStatus, error) {
	s.sonKullanici, s.sonAd, s.sonTakip = k, ad, takip
	return s.takip, s.hata
}

func (s *sahteSosyal) Suggestions(_ context.Context, k string) ([]social.ProfileSummary, error) {
	s.sonKullanici = k
	return s.oneriler, s.hata
}

func (s *sahteSosyal) Comments(_ context.Context, k, id, imlec string, limit int) (*social.Page[social.Comment], error) {
	s.sonKullanici, s.sonYayin, s.sonImlec, s.sonLimit = k, id, imlec, limit
	return s.yorumSayfa, s.hata
}

func (s *sahteSosyal) PostComment(_ context.Context, k, id string, g social.CommentInput) (*social.Comment, error) {
	s.sonKullanici, s.sonYayin, s.sonYorum = k, id, g
	return s.yorum, s.hata
}

func (s *sahteSosyal) DeleteComment(_ context.Context, k, id string) error {
	s.sonKullanici, s.sonYayin = k, id
	return s.hata
}

func (s *sahteSosyal) Report(_ context.Context, k string, g social.ReportInput) error {
	s.sonKullanici, s.sonSikayet = k, g
	return s.hata
}

func (s *sahteSosyal) Covers(_ context.Context, k, id, imlec string, limit int) (*social.Page[social.PublicationSummary], error) {
	s.sonKullanici, s.sonYayin, s.sonImlec, s.sonLimit = k, id, imlec, limit
	return s.sayfa, s.hata
}

func (s *sahteSosyal) StartCover(_ context.Context, k, id string) (*social.CoverStart, error) {
	s.sonKullanici, s.sonYayin = k, id
	return s.cover, s.hata
}

func (s *sahteSosyal) Preview(_ context.Context, slug string) (*social.Preview, error) {
	s.sonYayin = slug
	return s.onizleme, s.hata
}

func gecir(c *fiber.Ctx) error { return c.Next() }

// sosyalUygulamasi, /api/social grubunu sahte servisle ve gecirgen
// limitlerle kurar.
func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
	return sosyalUygulamasiLimitli(s, userID, SosyalLimitleri{Yayin: gecir, Yorum: gecir, Cover: gecir})
}

// sosyalUygulamasiLimitli, verilen limitlerle kurar.
func sosyalUygulamasiLimitli(s *sahteSosyal, userID string, l SosyalLimitleri) *fiber.App {
	app := fiber.New()
	grup := app.Group("/api/social", asUser(userID))
	NewSosyalHandler(s).Rotalar(grup, l)
	return app
}

// sosyalIstek, istegi atar; durum kodunu ve cozulmus JSON govdesini doner.
func sosyalIstek(t *testing.T, app *fiber.App, yontem, yol, govde string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if govde != "" {
		r = strings.NewReader(govde)
	}
	istek := httptest.NewRequest(yontem, yol, r)
	if govde != "" {
		istek.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(istek)
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	ham, _ := io.ReadAll(resp.Body)
	var cikti map[string]any
	if len(ham) > 0 {
		if err := json.Unmarshal(ham, &cikti); err != nil {
			t.Fatalf("govde JSON degil (%d): %s", resp.StatusCode, ham)
		}
	}
	return resp.StatusCode, cikti
}

func hataKodu(govde map[string]any) string {
	e, _ := govde["error"].(map[string]any)
	kod, _ := e["code"].(string)
	return kod
}

func TestSosyal_ProfilBenYoksa404ProfilYok(t *testing.T) {
	s := &sahteSosyal{hata: social.ErrNoProfile}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/ben", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "profil_yok" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
	if s.sonKullanici != sosyalKullanici {
		t.Fatalf("kullanici iletilmedi: %q", s.sonKullanici)
	}
}

func TestSosyal_ProfilBenSozlesmeAlanlari(t *testing.T) {
	s := &sahteSosyal{profil: &social.Profile{
		ProfileSummary: social.ProfileSummary{Username: "ali", DisplayName: "Ali", AvatarColor: 3},
		Bio:            "b", IsMe: true,
	}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/ben", "")
	if durum != http.StatusOK {
		t.Fatalf("durum=%d", durum)
	}
	for _, alan := range []string{"kullaniciAdi", "gorunenAd", "avatarRengi", "bio", "istatistik", "takipEdiyorMuyum", "benMiyim"} {
		if _, ok := govde[alan]; !ok {
			t.Errorf("%s alani yok: %v", alan, govde)
		}
	}
	ist := govde["istatistik"].(map[string]any)
	for _, alan := range []string{"sarki", "takipci", "takip", "cover"} {
		if _, ok := ist[alan]; !ok {
			t.Errorf("istatistik.%s yok", alan)
		}
	}
}

func TestSosyal_ProfilKaydetHataKodlari(t *testing.T) {
	durumlar := []struct {
		hata  error
		durum int
		kod   string
	}{
		{social.ErrUsernameTaken, http.StatusConflict, "kullanici_adi_alinmis"},
		{social.ErrInvalidUsername, http.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
		{fmtDogrulama("gorunenAd"), http.StatusUnprocessableEntity, "dogrulama"},
		{errors.New("pg kapali"), http.StatusInternalServerError, "sunucu_hatasi"},
	}
	for _, d := range durumlar {
		s := &sahteSosyal{hata: d.hata}
		durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/profil/ben",
			`{"kullaniciAdi":"ali","gorunenAd":"Ali","bio":""}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}

func fmtDogrulama(alan string) error { return errors.Join(social.ErrValidation, errors.New(alan)) }

func TestSosyal_ProfilKaydetGovdeyiIletir(t *testing.T) {
	s := &sahteSosyal{profil: &social.Profile{}}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/profil/ben",
		`{"kullaniciAdi":"ali","gorunenAd":"Ali","bio":"selam"}`)
	if durum != http.StatusOK || s.sonProfil != (social.ProfileInput{Username: "ali", DisplayName: "Ali", Bio: "selam"}) {
		t.Fatalf("durum=%d girdi=%+v", durum, s.sonProfil)
	}
}

func TestSosyal_BozukGovde422(t *testing.T) {
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{}, sosyalKullanici), http.MethodPut, "/api/social/profil/ben", `{"kullaniciAdi":`)
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "dogrulama" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_AdUygunOneriNullVeyaDolu(t *testing.T) {
	s := &sahteSosyal{adUygunlugu: &social.UsernameAvailability{Available: true}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/kullanici-adi/uygun?ad=ali", "")
	if durum != http.StatusOK || govde["uygun"] != true || s.sonAd != "ali" {
		t.Fatalf("durum=%d govde=%v ad=%q", durum, govde, s.sonAd)
	}
	if oneri, var_ := govde["oneri"]; !var_ || oneri != nil {
		t.Fatalf("oneri alani null olarak bulunmali: %v", govde)
	}
}

func TestSosyal_ProfilYok404Bulunamadi(t *testing.T) {
	s := &sahteSosyal{hata: social.ErrNotFound}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profiller/yok", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" || s.sonAd != "yok" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_KimliksizIstek401(t *testing.T) {
	durum, _ := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{}, ""), http.MethodGet, "/api/social/profil/ben", "")
	if durum != http.StatusUnauthorized {
		t.Fatalf("durum=%d", durum)
	}
}
