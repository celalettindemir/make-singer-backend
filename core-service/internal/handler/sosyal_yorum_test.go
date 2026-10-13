package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

func TestSosyal_YorumYaz201VeSozlesme(t *testing.T) {
	s := &sahteSosyal{yorum: &social.Yorum{
		ID: "y1", Yazar: social.ProfilOzeti{KullaniciAdi: "ali", GorunenAd: "Ali", AvatarRengi: 1},
		Metin: "guzel", Olusturma: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), SilebilirMiyim: true,
	}}
	durum, ham := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"guzel","zamanMs":null}`)
	beklenen := `{"id":"y1","yazar":{"kullaniciAdi":"ali","gorunenAd":"Ali","avatarRengi":1},"metin":"guzel","zamanMs":null,"olusturma":"2026-10-12T09:00:00Z","silebilirMiyim":true}`
	if durum != http.StatusCreated || ham != beklenen {
		t.Fatalf("durum=%d govde=%s", durum, ham)
	}
	if s.sonYayin != "abc" || s.sonYorum.Metin != "guzel" || s.sonYorum.ZamanMs != nil {
		t.Fatalf("iletilen = %q %+v", s.sonYayin, s.sonYorum)
	}

	sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"an","zamanMs":1500}`)
	if s.sonYorum.ZamanMs == nil || *s.sonYorum.ZamanMs != 1500 {
		t.Fatalf("zamanMs iletilmedi: %+v", s.sonYorum)
	}
}

func TestSosyal_YorumHataKodlari(t *testing.T) {
	durumlar := []struct {
		hata  error
		durum int
		kod   string
	}{
		{social.ErrCommentsDisabled, http.StatusForbidden, "yorum_kapali"},
		{social.ErrNoProfile, http.StatusConflict, "profil_yok"},
		{social.ErrNotFound, http.StatusNotFound, "bulunamadi"},
		{fmtDogrulama("metin"), http.StatusUnprocessableEntity, "dogrulama"},
	}
	for _, d := range durumlar {
		s := &sahteSosyal{hata: d.hata}
		durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost,
			"/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":null}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("POST %v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
	s := &sahteSosyal{hata: social.ErrCommentsDisabled}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/yayinlar/abc/yorumlar", "")
	if durum != http.StatusForbidden || hataKodu(govde) != "yorum_kapali" {
		t.Fatalf("GET: durum=%d govde=%v", durum, govde)
	}
	durum, govde = sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{}, sosyalKullanici), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":1.5}`)
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "dogrulama" {
		t.Fatalf("kesirli zamanMs: durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_YorumlarSayfasi(t *testing.T) {
	s := &sahteSosyal{yorumSayfa: &social.Sayfa[social.Yorum]{Ogeler: []social.Yorum{}}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet,
		"/api/social/yayinlar/abc/yorumlar?imlec=xyz", "")
	if durum != http.StatusOK || s.sonImlec != "xyz" || s.sonLimit != 20 || s.sonYayin != "abc" {
		t.Fatalf("durum=%d imlec=%q limit=%d", durum, s.sonImlec, s.sonLimit)
	}
	if v, var_ := govde["sonrakiImlec"]; !var_ || v != nil {
		t.Fatalf("sonrakiImlec null olmali: %v", govde)
	}
}

func TestSosyal_YorumSil(t *testing.T) {
	s := &sahteSosyal{}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yorumlar/y9", "")
	if durum != http.StatusNoContent || s.sonYayin != "y9" {
		t.Fatalf("durum=%d id=%q", durum, s.sonYayin)
	}
	s.hata = social.ErrForbidden
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yorumlar/y9", "")
	if durum != http.StatusForbidden || hataKodu(govde) != "yetki" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

// Yorum limiti yalnizca POST yorumlar'a baglidir.
func TestSosyal_YorumLimitiYalnizcaYazmada(t *testing.T) {
	yavasla := func(c *fiber.Ctx) error {
		return response.Error(c, fiber.StatusTooManyRequests, "yavasla", "Biraz yavasla", nil)
	}
	s := &sahteSosyal{
		yorumSayfa:  &social.Sayfa[social.Yorum]{Ogeler: []social.Yorum{}},
		yayinYaniti: &social.YayinYaniti{},
	}
	app := sosyalUygulamasiLimitli(s, sosyalKullanici, SosyalLimitleri{Yayin: gecir, Yorum: yavasla, Cover: gecir})
	durum, govde := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":null}`)
	if durum != http.StatusTooManyRequests || hataKodu(govde) != "yavasla" {
		t.Fatalf("POST yorum: durum=%d govde=%v", durum, govde)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodGet, "/api/social/yayinlar/abc/yorumlar", ""); durum != http.StatusOK {
		t.Fatalf("GET yorumlar limitlenmemeli: %d", durum)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodDelete, "/api/social/yorumlar/y1", ""); durum != http.StatusNoContent {
		t.Fatalf("DELETE yorum limitlenmemeli: %d", durum)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); durum != http.StatusAccepted {
		t.Fatalf("yayin yorum limitine takilmamali: %d", durum)
	}
}
