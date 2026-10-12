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
	"github.com/makeasinger/api/internal/sosyal"
)

const sosyalKullanici = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

// sahteSosyal, SosyalServisi'nin elle yazilmis sahtesidir. Her alan
// ilgili metodun ne donecegini belirler; son cagrinin girdileri kaydedilir.
type sahteSosyal struct {
	hata error

	profil      *sosyal.Profil
	adUygunlugu *sosyal.AdUygunlugu
	yayinYaniti *sosyal.YayinYaniti
	detay       *sosyal.YayinDetayi
	dosyalar    *sosyal.YayinDosyalari
	sayfa       *sosyal.Sayfa[sosyal.YayinOzeti]
	begeni      *sosyal.BegeniDurumu

	sonKullanici string
	sonAd        string
	sonProfil    sosyal.ProfilGirdisi
	sonYayin     string
	sonIstek     sosyal.YayinIstegi
	sonYama      sosyal.YayinYamasi
	sonSekme     string
	sonTur       string
	sonImlec     string
	sonLimit     int
	sonBegen     bool
}

func (s *sahteSosyal) ProfilBen(_ context.Context, k string) (*sosyal.Profil, error) {
	s.sonKullanici = k
	return s.profil, s.hata
}

func (s *sahteSosyal) ProfilKaydet(_ context.Context, k string, g sosyal.ProfilGirdisi) (*sosyal.Profil, error) {
	s.sonKullanici, s.sonProfil = k, g
	return s.profil, s.hata
}

func (s *sahteSosyal) AdUygunMu(_ context.Context, k, ad string) (*sosyal.AdUygunlugu, error) {
	s.sonKullanici, s.sonAd = k, ad
	return s.adUygunlugu, s.hata
}

func (s *sahteSosyal) ProfilGoster(_ context.Context, k, ad string) (*sosyal.Profil, error) {
	s.sonKullanici, s.sonAd = k, ad
	return s.profil, s.hata
}

func (s *sahteSosyal) YayinOlustur(_ context.Context, k string, ist sosyal.YayinIstegi) (*sosyal.YayinYaniti, error) {
	s.sonKullanici, s.sonIstek = k, ist
	return s.yayinYaniti, s.hata
}

func (s *sahteSosyal) YayinGoster(_ context.Context, k, id string) (*sosyal.YayinDetayi, error) {
	s.sonKullanici, s.sonYayin = k, id
	return s.detay, s.hata
}

func (s *sahteSosyal) SlugGoster(_ context.Context, k, slug string) (*sosyal.YayinDetayi, error) {
	s.sonKullanici, s.sonYayin = k, slug
	return s.detay, s.hata
}

func (s *sahteSosyal) YayinGuncelle(_ context.Context, k, id string, y sosyal.YayinYamasi) (*sosyal.YayinDetayi, error) {
	s.sonKullanici, s.sonYayin, s.sonYama = k, id, y
	return s.detay, s.hata
}

func (s *sahteSosyal) YayinSil(_ context.Context, k, id string) error {
	s.sonKullanici, s.sonYayin = k, id
	return s.hata
}

func (s *sahteSosyal) TekrarDene(_ context.Context, k, id string) (*sosyal.YayinYaniti, error) {
	s.sonKullanici, s.sonYayin = k, id
	return s.yayinYaniti, s.hata
}

func (s *sahteSosyal) Dosyalar(_ context.Context, k, id string) (*sosyal.YayinDosyalari, error) {
	s.sonKullanici, s.sonYayin = k, id
	return s.dosyalar, s.hata
}

func (s *sahteSosyal) Akis(_ context.Context, k, sekme, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error) {
	s.sonKullanici, s.sonSekme, s.sonImlec, s.sonLimit = k, sekme, imlec, limit
	return s.sayfa, s.hata
}

func (s *sahteSosyal) ProfilYayinlari(_ context.Context, k, ad, tur, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error) {
	s.sonKullanici, s.sonAd, s.sonTur, s.sonImlec, s.sonLimit = k, ad, tur, imlec, limit
	return s.sayfa, s.hata
}

func (s *sahteSosyal) Begeni(_ context.Context, k, id string, begen bool) (*sosyal.BegeniDurumu, error) {
	s.sonKullanici, s.sonYayin, s.sonBegen = k, id, begen
	return s.begeni, s.hata
}

// sosyalUygulamasi, /api/social grubunu sahte servisle kurar.
func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
	app := fiber.New()
	grup := app.Group("/api/social", kullaniciOlarak(userID))
	NewSosyalHandler(s).Rotalar(grup, func(c *fiber.Ctx) error { return c.Next() })
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
	s := &sahteSosyal{hata: sosyal.ErrProfilYok}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/ben", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "profil_yok" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
	if s.sonKullanici != sosyalKullanici {
		t.Fatalf("kullanici iletilmedi: %q", s.sonKullanici)
	}
}

func TestSosyal_ProfilBenSozlesmeAlanlari(t *testing.T) {
	s := &sahteSosyal{profil: &sosyal.Profil{
		ProfilOzeti: sosyal.ProfilOzeti{KullaniciAdi: "ali", GorunenAd: "Ali", AvatarRengi: 3},
		Bio:         "b", BenMiyim: true,
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
		{sosyal.ErrKullaniciAdiAlinmis, http.StatusConflict, "kullanici_adi_alinmis"},
		{sosyal.ErrGecersizKullaniciAdi, http.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
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

func fmtDogrulama(alan string) error { return errors.Join(sosyal.ErrDogrulama, errors.New(alan)) }

func TestSosyal_ProfilKaydetGovdeyiIletir(t *testing.T) {
	s := &sahteSosyal{profil: &sosyal.Profil{}}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/profil/ben",
		`{"kullaniciAdi":"ali","gorunenAd":"Ali","bio":"selam"}`)
	if durum != http.StatusOK || s.sonProfil != (sosyal.ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "Ali", Bio: "selam"}) {
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
	s := &sahteSosyal{adUygunlugu: &sosyal.AdUygunlugu{Uygun: true}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/kullanici-adi/uygun?ad=ali", "")
	if durum != http.StatusOK || govde["uygun"] != true || s.sonAd != "ali" {
		t.Fatalf("durum=%d govde=%v ad=%q", durum, govde, s.sonAd)
	}
	if oneri, var_ := govde["oneri"]; !var_ || oneri != nil {
		t.Fatalf("oneri alani null olarak bulunmali: %v", govde)
	}
}

func TestSosyal_ProfilYok404Bulunamadi(t *testing.T) {
	s := &sahteSosyal{hata: sosyal.ErrBulunamadi}
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
