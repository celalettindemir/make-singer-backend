package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// publicUygulama, main'deki sirayi taklit eder: once kimliksiz uc, sonra
// her istegi 401 ile kesen bir /api grubu (auth yerine).
func publicUygulama(s *sahteSosyal, ipLimiti fiber.Handler) *fiber.App {
	app := fiber.New()
	h := NewSosyalHandler(s)
	h.PublicRotalar(app.Group("/api/public"), ipLimiti)
	api := app.Group("/api", func(c *fiber.Ctx) error { return response.Unauthorized(c, "auth") })
	h.Rotalar(api.Group("/social"), SosyalLimitleri{Yayin: gecir, Yorum: gecir, Cover: gecir})
	return app
}

func TestSosyal_OnizlemeKimliksizVeSozlesme(t *testing.T) {
	s := &sahteSosyal{onizleme: &social.Onizleme{
		Baslik: "Sarki", Sahip: social.ProfilOzeti{KullaniciAdi: "ali", GorunenAd: "Ali", AvatarRengi: 5},
		SureSn: 180, DalgaFormu: []float64{0.5, 1}, Tur: "pop",
	}}
	app := publicUygulama(s, gecir)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/public/s/Ab3dEf9h", nil))
	if err != nil {
		t.Fatal(err)
	}
	ham, _ := io.ReadAll(resp.Body)
	beklenen := `{"baslik":"Sarki","sahip":{"kullaniciAdi":"ali","gorunenAd":"Ali","avatarRengi":5},"sureSn":180,"dalgaFormu":[0.5,1],"onizlemeUrl":null,"tur":"pop"}`
	if resp.StatusCode != http.StatusOK || string(ham) != beklenen {
		t.Fatalf("durum=%d govde=%s", resp.StatusCode, ham)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || s.sonYayin != "Ab3dEf9h" {
		t.Fatalf("Cache-Control=%q slug=%q", resp.Header.Get("Cache-Control"), s.sonYayin)
	}
	// Ayni uygulamada kimlik isteyen uclar hala 401'dir.
	if durum, _ := sosyalIstek(t, app, http.MethodGet, "/api/social/s/Ab3dEf9h", ""); durum != http.StatusUnauthorized {
		t.Fatalf("auth grubu atlanmamali: %d", durum)
	}
}

func TestSosyal_Onizleme404JSON(t *testing.T) {
	durum, govde := sosyalIstek(t, publicUygulama(&sahteSosyal{hata: social.ErrNotFound}, gecir),
		http.MethodGet, "/api/public/s/Yok12345", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_OnizlemeIPLimitiUygulanir(t *testing.T) {
	sinir := func(c *fiber.Ctx) error { return response.RateLimited(c) }
	durum, govde := sosyalIstek(t, publicUygulama(&sahteSosyal{onizleme: &social.Onizleme{}}, sinir),
		http.MethodGet, "/api/public/s/Ab3dEf9h", "")
	if durum != http.StatusTooManyRequests || hataKodu(govde) != "RATE_LIMITED" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

// Cache-Control: no-store yalnizca 200'de degil; 404 ve limiter'in 429'u
// da bir vekil/CDN tarafindan ara bellege alinmamali.
func TestSosyal_OnizlemeNoStoreHerYanitta(t *testing.T) {
	sinir := func(c *fiber.Ctx) error { return response.RateLimited(c) }
	durumlar := []struct {
		ad       string
		app      *fiber.App
		beklenen int
	}{
		{"404", publicUygulama(&sahteSosyal{hata: social.ErrNotFound}, gecir), http.StatusNotFound},
		{"429", publicUygulama(&sahteSosyal{onizleme: &social.Onizleme{}}, sinir), http.StatusTooManyRequests},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			resp, err := d.app.Test(httptest.NewRequest(http.MethodGet, "/api/public/s/Ab3dEf9h", nil))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != d.beklenen || resp.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("durum=%d Cache-Control=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
			}
		})
	}
}

// panikBekle, f'nin panik ettigini dogrular.
func panikBekle(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("eksik limiter ile kayit panik etmeliydi")
		}
	}()
	f()
}

// P1: unutulan limiter ilk istekte degil, kayit aninda patlamali.
func TestSosyal_EksikLimiterKayittaPanikEder(t *testing.T) {
	h := NewSosyalHandler(&sahteSosyal{})
	durumlar := map[string]SosyalLimitleri{
		"hepsi-bos": {},
		"yayin-bos": {Yorum: gecir, Cover: gecir},
		"yorum-bos": {Yayin: gecir, Cover: gecir},
		"cover-bos": {Yayin: gecir, Yorum: gecir},
	}
	for ad, l := range durumlar {
		t.Run(ad, func(t *testing.T) {
			panikBekle(t, func() { h.Rotalar(fiber.New().Group("/api/social"), l) })
		})
	}
	t.Run("ip-limiti-bos", func(t *testing.T) {
		panikBekle(t, func() { h.PublicRotalar(fiber.New().Group("/api/public"), nil) })
	})
}
