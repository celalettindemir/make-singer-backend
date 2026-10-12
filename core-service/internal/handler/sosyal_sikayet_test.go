package handler

import (
	"net/http"
	"testing"

	"github.com/makeasinger/api/internal/sosyal"
)

func TestSosyal_Sikayet201Govdesiz(t *testing.T) {
	s := &sahteSosyal{}
	durum, ham := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/sikayetler",
		`{"hedefTuru":"yorum","hedefId":"c1","neden":"spam","aciklama":"x"}`)
	if durum != http.StatusCreated || ham != "" {
		t.Fatalf("durum=%d govde=%q", durum, ham)
	}
	if s.sonSikayet != (sosyal.SikayetGirdisi{HedefTuru: "yorum", HedefID: "c1", Neden: "spam", Aciklama: "x"}) {
		t.Fatalf("iletilen = %+v", s.sonSikayet)
	}
	// aciklama istege bagli.
	if durum, _ := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/sikayetler",
		`{"hedefTuru":"yayin","hedefId":"y1","neden":"diger"}`); durum != http.StatusCreated || s.sonSikayet.Aciklama != "" {
		t.Fatalf("aciklamasiz: durum=%d %+v", durum, s.sonSikayet)
	}
}

func TestSosyal_SikayetHatalari(t *testing.T) {
	for _, d := range []struct {
		hata  error
		durum int
		kod   string
	}{
		{sosyal.ErrBulunamadi, http.StatusNotFound, "bulunamadi"},
		{fmtDogrulama("neden"), http.StatusUnprocessableEntity, "dogrulama"},
	} {
		durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: d.hata}, sosyalKullanici), http.MethodPost,
			"/api/social/sikayetler", `{"hedefTuru":"yayin","hedefId":"y1","neden":"spam"}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}
