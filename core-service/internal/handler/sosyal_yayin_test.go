package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

func ornekDetay() *social.PublicationDetail {
	d := &social.PublicationDetail{Status: social.StatusPublished, Visibility: social.VisibilityEveryone}
	d.ID, d.Slug, d.Title = "y1", "Ab3dEf9h", "Gece"
	d.Waveform = []float64{}
	d.Structure = []social.StructureItem{}
	d.Lyrics = social.Lyrics{Sections: []social.LyricsSection{}}
	d.CreatedAt = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	return d
}

func TestSosyal_YayinOlustur202(t *testing.T) {
	s := &sahteSosyal{yayinYaniti: &social.PublicationResponse{ID: "y1", Slug: "Ab3dEf9h", Status: social.StatusPreparing}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar",
		`{"baslik":"Gece","gorunurluk":"herkes","altyapiAcik":true,"masterUrl":"masters/u/p/m.wav","stemler":[{"id":"d","enstruman":"drums","url":"stems/u/p/d.mp3","sure":12.5}],"bpm":120,"sureSn":184,"ton":{"tonik":"A","mod":"minor"},"kaynakYayinId":null}`)
	if durum != http.StatusAccepted || govde["durum"] != "hazirlaniyor" || govde["id"] != "y1" || govde["slug"] != "Ab3dEf9h" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
	if s.sonIstek.Title != "Gece" || len(s.sonIstek.Stems) != 1 || s.sonIstek.Stems[0].Duration != 12.5 || s.sonIstek.SourcePublicationID != nil {
		t.Fatalf("istek = %+v", s.sonIstek)
	}
}

func TestSosyal_YayinOlusturHataKodlari(t *testing.T) {
	durumlar := []struct {
		hata  error
		durum int
		kod   string
	}{
		{social.ErrNoProfile, http.StatusConflict, "profil_yok"},
		{social.ErrOwnership, http.StatusForbidden, "sahiplik"},
		{social.ErrSourceNotFound, http.StatusNotFound, "kaynak_bulunamadi"},
		{social.ErrBackingDisabled, http.StatusForbidden, "altyapi_kapali"},
		{social.ErrValidation, http.StatusUnprocessableEntity, "dogrulama"},
	}
	for _, d := range durumlar {
		durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: d.hata}, sosyalKullanici), http.MethodPost, "/api/social/yayinlar", `{}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}

func TestSosyal_YayinOlusturHizSiniri(t *testing.T) {
	app := fiber.New()
	grup := app.Group("/api/social", kullaniciOlarak(sosyalKullanici))
	NewSosyalHandler(&sahteSosyal{}).Rotalar(grup, SosyalLimitleri{Yayin: func(c *fiber.Ctx) error {
		return c.Status(http.StatusTooManyRequests).JSON(fiber.Map{"error": fiber.Map{"code": "RATE_LIMITED", "message": "x"}})
	}, Yorum: gecir, Cover: gecir})
	if durum, _ := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); durum != http.StatusTooManyRequests {
		t.Fatalf("yayin limiti POST /yayinlar'a uygulanmali: %d", durum)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodGet, "/api/social/yayinlar/y1", ""); durum == http.StatusTooManyRequests {
		t.Fatal("yayin limiti okuma uclarina uygulanmamali")
	}
}

// YayinDetayi sozlesmesi: gorunurluk her zaman, kaynak null olarak,
// dalgaFormu dizi olarak bulunmali.
func TestSosyal_YayinDetaySozlesmeAlanlari(t *testing.T) {
	s := &sahteSosyal{detay: ornekDetay()}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/yayinlar/y1", "")
	if durum != http.StatusOK || s.sonYayin != "y1" {
		t.Fatalf("durum=%d", durum)
	}
	for _, alan := range []string{"id", "slug", "baslik", "sahip", "tur", "bpm", "ton", "sureSn", "dalgaFormu",
		"begeniSayisi", "yorumSayisi", "coverSayisi", "begendimMi", "altyapiAcik", "kaynak", "olusturma",
		"aciklama", "yorumAcik", "sozler", "yapi", "durum", "benimMi", "gorunurluk"} {
		if _, ok := govde[alan]; !ok {
			t.Errorf("%s alani yok", alan)
		}
	}
	if govde["kaynak"] != nil {
		t.Errorf("kaynak null olmali: %v", govde["kaynak"])
	}
	if _, dizi := govde["dalgaFormu"].([]any); !dizi {
		t.Errorf("dalgaFormu dizi olmali: %v", govde["dalgaFormu"])
	}
	if govde["olusturma"] != "2026-10-12T10:00:00Z" {
		t.Errorf("olusturma RFC3339 olmali: %v", govde["olusturma"])
	}
	ton := govde["ton"].(map[string]any)
	if _, ok := ton["tonik"]; !ok {
		t.Errorf("ton.tonik yok: %v", ton)
	}
}

func TestSosyal_SlugVeYok404(t *testing.T) {
	s := &sahteSosyal{detay: ornekDetay()}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/s/Ab3dEf9h", ""); durum != http.StatusOK || s.sonYayin != "Ab3dEf9h" {
		t.Fatalf("slug durum=%d son=%q", durum, s.sonYayin)
	}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: social.ErrNotFound}, sosyalKullanici), http.MethodGet, "/api/social/s/Ab3dEf9h", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_YayinGuncelleYalnizcaGonderilenAlanlar(t *testing.T) {
	s := &sahteSosyal{detay: ornekDetay()}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPatch, "/api/social/yayinlar/y1", `{"gorunurluk":"link"}`)
	if durum != http.StatusOK {
		t.Fatalf("durum=%d", durum)
	}
	if s.sonYama.Visibility == nil || *s.sonYama.Visibility != "link" || s.sonYama.Description != nil || s.sonYama.BackingEnabled != nil || s.sonYama.CommentsEnabled != nil {
		t.Fatalf("yama = %+v", s.sonYama)
	}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: social.ErrNoStems}, sosyalKullanici), http.MethodPatch, "/api/social/yayinlar/y1", `{"altyapiAcik":true}`)
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "stem_yok" {
		t.Fatalf("stem_yok: durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_YayinSil204Ve404(t *testing.T) {
	s := &sahteSosyal{}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yayinlar/y1", ""); durum != http.StatusNoContent || s.sonYayin != "y1" {
		t.Fatalf("durum=%d", durum)
	}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: social.ErrNotFound}, sosyalKullanici), http.MethodDelete, "/api/social/yayinlar/y1", ""); durum != http.StatusNotFound {
		t.Fatalf("durum=%d", durum)
	}
}

func TestSosyal_TekrarDene202Ve409(t *testing.T) {
	s := &sahteSosyal{yayinYaniti: &social.PublicationResponse{ID: "y1", Slug: "Ab3dEf9h", Status: social.StatusPreparing}}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/tekrar-dene", ""); durum != http.StatusAccepted {
		t.Fatalf("durum=%d", durum)
	}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: social.ErrInvalidStatus}, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/tekrar-dene", "")
	if durum != http.StatusConflict || hataKodu(govde) != "durum" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_DosyalarMasterURL(t *testing.T) {
	bitis := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	s := &sahteSosyal{dosyalar: &social.PublicationFiles{Master: social.FileLink{URL: "https://imzali/x", ExpiresAt: &bitis}}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/dosyalar", "")
	master, _ := govde["master"].(map[string]any)
	if durum != http.StatusOK || master["url"] != "https://imzali/x" || master["expiresAt"] != "2026-10-12T11:00:00Z" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}
