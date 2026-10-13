package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

func TestSosyal_TakipYaniti(t *testing.T) {
	for _, d := range []struct {
		yontem string
		takip  bool
	}{{http.MethodPut, true}, {http.MethodDelete, false}} {
		s := &sahteSosyal{takip: &social.TakipDurumu{Takipci: 4, TakipEdiyorMuyum: d.takip}}
		durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), d.yontem, "/api/social/profiller/ayse/takip", "")
		if durum != http.StatusOK || govde["takipci"] != float64(4) || govde["takipEdiyorMuyum"] != d.takip {
			t.Fatalf("%s: durum=%d govde=%v", d.yontem, durum, govde)
		}
		if s.sonAd != "ayse" || s.sonTakip != d.takip || s.sonKullanici != sosyalKullanici {
			t.Fatalf("%s: iletilen ad=%q takip=%v", d.yontem, s.sonAd, s.sonTakip)
		}
	}
}

func TestSosyal_KendiniTakip422(t *testing.T) {
	s := &sahteSosyal{hata: social.ErrSelfFollow}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/profiller/ben/takip", "")
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "kendini_takip" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_TakipProfilYok404(t *testing.T) {
	s := &sahteSosyal{hata: social.ErrNotFound}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/profiller/yok/takip", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_OnerilerDizi(t *testing.T) {
	s := &sahteSosyal{oneriler: []social.ProfilOzeti{{KullaniciAdi: "ayse", GorunenAd: "Ayse", AvatarRengi: 2}}}
	app := sosyalUygulamasi(s, sosyalKullanici)
	durum, ham := sosyalHamIstek(t, app, http.MethodGet, "/api/social/oneriler", "")
	if durum != http.StatusOK || ham != `[{"kullaniciAdi":"ayse","gorunenAd":"Ayse","avatarRengi":2}]` {
		t.Fatalf("durum=%d govde=%s", durum, ham)
	}
	s.oneriler = []social.ProfilOzeti{}
	if _, ham := sosyalHamIstek(t, app, http.MethodGet, "/api/social/oneriler", ""); ham != `[]` {
		t.Fatalf("bos oneri [] olmali: %s", ham)
	}
}

// sosyalHamIstek, istegi atar; durum kodunu ve ham govdeyi doner (dizi
// donen uclar icin).
func sosyalHamIstek(t *testing.T, app *fiber.App, yontem, yol, govde string) (int, string) {
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
	return resp.StatusCode, string(ham)
}
