package handler

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

func TestSosyal_CoverBaslatSozlesme(t *testing.T) {
	s := &sahteSosyal{cover: &social.CoverStart{
		Source: social.SourceSummary{PublicationID: "y1", Title: "B", OwnerUsername: "ali"},
		Genre:  "pop", BPM: 120, MusicalKey: social.MusicalKey{Tonic: "A", Mode: "minor"}, DurationSec: 180,
		Structure: []social.StructureItem{}, Lyrics: social.Lyrics{Sections: []social.LyricsSection{}},
		Stems: []social.CoverStem{{ID: "drums", Instrument: "Davul", FileURL: "https://x/stems/u/k/drums.mp3", Duration: 3}},
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
		{social.ErrBackingDisabled, http.StatusForbidden, "altyapi_kapali"},
		{social.ErrNotFound, http.StatusNotFound, "bulunamadi"},
		{social.ErrInvalidStatus, http.StatusConflict, "durum"},
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
		sayfa: &social.Page[social.PublicationSummary]{Items: []social.PublicationSummary{}},
		cover: &social.CoverStart{},
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
