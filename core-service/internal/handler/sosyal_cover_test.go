package handler

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
)

func TestSosyal_CoverBaslatSozlesme(t *testing.T) {
	s := &sahteSosyal{cover: &sosyal.CoverBaslangici{
		Kaynak: sosyal.KaynakOzeti{YayinID: "y1", Baslik: "B", SahipKullaniciAdi: "ali"},
		Tur:    "pop", Bpm: 120, Ton: sosyal.Ton{Tonik: "A", Mod: "minor"}, SureSn: 180,
		Yapi: []sosyal.YapiOgesi{}, Sozler: sosyal.Sozler{Bolumler: []sosyal.SozBolumu{}},
		Stemler: []sosyal.CoverStemi{{ID: "drums", Enstruman: "Davul", FileURL: "https://x/stems/u/k/drums.mp3", Sure: 3}},
	}}
	durum, ham := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/cover-baslat", "")
	beklenen := `{"kaynak":{"yayinId":"y1","baslik":"B","sahipKullaniciAdi":"ali","silindi":false},"tur":"pop","bpm":120,` +
		`"ton":{"tonik":"A","mod":"minor"},"sureSn":180,"yapi":[],"sozler":{"bolumler":[]},` +
		`"stemler":[{"id":"drums","enstruman":"Davul","fileUrl":"https://x/stems/u/k/drums.mp3","sure":3}]}`
	if durum != http.StatusOK || ham != beklenen || s.sonYayin != "y1" {
		t.Fatalf("durum=%d govde=%s", durum, ham)
	}
}

func TestSosyal_CoverHataKodlari(t *testing.T) {
	for _, d := range []struct {
		hata  error
		durum int
		kod   string
	}{
		{sosyal.ErrAltyapiKapali, http.StatusForbidden, "altyapi_kapali"},
		{sosyal.ErrBulunamadi, http.StatusNotFound, "bulunamadi"},
		{sosyal.ErrDurum, http.StatusConflict, "durum"},
	} {
		durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: d.hata}, sosyalKullanici), http.MethodPost,
			"/api/social/yayinlar/y1/cover-baslat", "")
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}

func TestSosyal_CoverlarSayfasiVeLimit(t *testing.T) {
	s := &sahteSosyal{
		sayfa: &sosyal.Sayfa[sosyal.YayinOzeti]{Ogeler: []sosyal.YayinOzeti{}},
		cover: &sosyal.CoverBaslangici{},
	}
	sinir := func(c *fiber.Ctx) error { return c.SendStatus(http.StatusTooManyRequests) }
	app := sosyalUygulamasiLimitli(s, sosyalKullanici, SosyalLimitleri{Yayin: gecir, Yorum: gecir, Cover: sinir})
	durum, govde := sosyalIstek(t, app, http.MethodGet, "/api/social/yayinlar/y1/coverlar?imlec=i", "")
	if durum != http.StatusOK || s.sonImlec != "i" || s.sonYayin != "y1" {
		t.Fatalf("liste: durum=%d govde=%v", durum, govde)
	}
	if v, var_ := govde["sonrakiImlec"]; !var_ || v != nil {
		t.Fatalf("sonrakiImlec null olmali: %v", govde)
	}
	if durum, _ := sosyalHamIstek(t, app, http.MethodPost, "/api/social/yayinlar/y1/cover-baslat", ""); durum != http.StatusTooManyRequests {
		t.Fatalf("cover limiti cover-baslat'a uygulanmali: %d", durum)
	}
}
