package handler

import (
	"net/http"
	"testing"

	"github.com/makeasinger/api/internal/social"
)

func TestSosyal_AkisSayfaZarfi(t *testing.T) {
	imlec := "sonraki"
	s := &sahteSosyal{sayfa: &social.Sayfa[social.YayinOzeti]{Ogeler: []social.YayinOzeti{}, SonrakiImlec: &imlec}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/akis?sekme=yeni&imlec=abc", "")
	if durum != http.StatusOK || govde["sonrakiImlec"] != "sonraki" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
	if _, dizi := govde["ogeler"].([]any); !dizi {
		t.Fatalf("ogeler dizi olmali: %v", govde)
	}
	if s.sonSekme != "yeni" || s.sonImlec != "abc" || s.sonLimit != 20 {
		t.Fatalf("iletilen = sekme %q imlec %q limit %d", s.sonSekme, s.sonImlec, s.sonLimit)
	}
}

func TestSosyal_AkisSonSayfaImlecNull(t *testing.T) {
	s := &sahteSosyal{sayfa: &social.Sayfa[social.YayinOzeti]{Ogeler: []social.YayinOzeti{}}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/akis?sekme=senin-icin&limit=999", "")
	if durum != http.StatusOK {
		t.Fatalf("durum=%d", durum)
	}
	if v, var_ := govde["sonrakiImlec"]; !var_ || v != nil {
		t.Fatalf("sonrakiImlec null olarak bulunmali: %v", govde)
	}
	if s.sonLimit != 50 {
		t.Fatalf("limit 50 ile sinirlanmali: %d", s.sonLimit)
	}
}

func TestSosyal_AkisGecersizSekme422(t *testing.T) {
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: social.ErrValidation}, sosyalKullanici), http.MethodGet, "/api/social/akis?sekme=x", "")
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "dogrulama" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_ProfilYayinlari(t *testing.T) {
	s := &sahteSosyal{sayfa: &social.Sayfa[social.YayinOzeti]{Ogeler: []social.YayinOzeti{}}}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profiller/veli/yayinlar?tur=coverlar&limit=5", "")
	if durum != http.StatusOK || s.sonAd != "veli" || s.sonTur != "coverlar" || s.sonLimit != 5 {
		t.Fatalf("durum=%d ad=%q tur=%q limit=%d", durum, s.sonAd, s.sonTur, s.sonLimit)
	}
}

func TestSosyal_BegeniPutVeDelete(t *testing.T) {
	s := &sahteSosyal{begeni: &social.BegeniDurumu{BegendimMi: true, BegeniSayisi: 3}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/yayinlar/y1/begeni", "")
	if durum != http.StatusOK || govde["begendimMi"] != true || govde["begeniSayisi"] != float64(3) || !s.sonBegen || s.sonYayin != "y1" {
		t.Fatalf("PUT durum=%d govde=%v", durum, govde)
	}
	s.begeni = &social.BegeniDurumu{BegeniSayisi: 2}
	durum, govde = sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yayinlar/y1/begeni", "")
	if durum != http.StatusOK || govde["begendimMi"] != false || s.sonBegen {
		t.Fatalf("DELETE durum=%d govde=%v", durum, govde)
	}
	durum, _ = sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: social.ErrNotFound}, sosyalKullanici), http.MethodPut, "/api/social/yayinlar/y1/begeni", "")
	if durum != http.StatusNotFound {
		t.Fatalf("gorulmeyen yayin 404: %d", durum)
	}
}
