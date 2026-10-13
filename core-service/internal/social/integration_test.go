package social

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// jsonAlanlari, degeri JSON'a cevirip ust duzey alanlarini doner.
func jsonAlanlari(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json: %v", err)
	}
	return m
}

// Gercek Servis + PostgresDepo ile yayin yasam dongusu: profil, yayinla,
// hazirlik sonucu, detay, akis, begeni, yama, silme. Is (worker) yerine
// depo.Yayinla dogrudan cagrilir.
func TestButunlesik_YayinYasamDongusu(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	q := &sahteKuyruk{}
	s := NewServis(d, q, nil, testR2())
	ctx := context.Background()
	sahip, dinleyici := uuid.NewString(), uuid.NewString()

	if _, err := s.YayinOlustur(ctx, sahip, YayinIstegi{}); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("profilsiz yayin: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, sahip, ProfilGirdisi{KullaniciAdi: "sahip", GorunenAd: "Sahip"}); err != nil {
		t.Fatalf("profil: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, dinleyici, ProfilGirdisi{KullaniciAdi: "dinleyici", GorunenAd: "D"}); err != nil {
		t.Fatalf("profil: %v", err)
	}

	ist := gecerliIstek()
	ist.MasterURL = "masters/" + sahip + "/p1/m1.wav"
	ist.Stemler[0].URL = "stems/" + sahip + "/p1/drums.mp3"
	yanit, err := s.YayinOlustur(ctx, sahip, ist)
	if err != nil {
		t.Fatalf("YayinOlustur: %v", err)
	}
	if len(q.gorevler) != 1 {
		t.Fatalf("hazirla isi kurulmali")
	}

	// Hazirlanirken yalnizca sahibine gorunur.
	if y, err := s.YayinGoster(ctx, sahip, yanit.ID); err != nil || y.Durum != DurumHazirlaniyor {
		t.Fatalf("sahip hazirlanani gormeli: %+v, %v", y, err)
	}
	if _, err := s.YayinGoster(ctx, dinleyici, yanit.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dinleyici hazirlanani gormemeli: %v", err)
	}

	h, err := d.GetPreparation(ctx, yanit.ID)
	if err != nil || h.Hazirlik.MasterKey != ist.MasterURL {
		t.Fatalf("hazirlik = %+v, %v", h, err)
	}
	master, _ := MasterHedefi(yanit.ID, h.Hazirlik.MasterKey)
	dalga := make([]float64, 64)
	if err := d.Publish(ctx, yanit.ID, HazirlikSonucu{MasterAnahtari: master, DalgaFormu: dalga}); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}

	detay, err := s.SlugGoster(ctx, dinleyici, yanit.Slug)
	if err != nil {
		t.Fatalf("SlugGoster: %v", err)
	}
	m := jsonAlanlari(t, detay)
	for _, alan := range []string{"id", "slug", "baslik", "sahip", "tur", "bpm", "ton", "sureSn", "dalgaFormu",
		"begeniSayisi", "yorumSayisi", "coverSayisi", "begendimMi", "altyapiAcik", "kaynak", "olusturma",
		"aciklama", "yorumAcik", "sozler", "yapi", "durum", "benimMi", "gorunurluk"} {
		if _, ok := m[alan]; !ok {
			t.Errorf("YayinDetayi.%s yok", alan)
		}
	}
	if m["kaynak"] != nil || m["durum"] != "yayinda" || m["benimMi"] != false || len(m["dalgaFormu"].([]any)) != 64 {
		t.Fatalf("detay = %v", m)
	}
	if sahipJSON := m["sahip"].(map[string]any); sahipJSON["kullaniciAdi"] != "sahip" {
		t.Fatalf("sahip = %v", sahipJSON)
	}

	akis, err := s.Akis(ctx, dinleyici, SekmeSeninIcin, "", 20)
	if err != nil || len(akis.Ogeler) != 1 || akis.SonrakiImlec != nil {
		t.Fatalf("senin icin = %+v, %v", akis, err)
	}
	if kendi, _ := s.Akis(ctx, sahip, SekmeSeninIcin, "", 20); len(kendi.Ogeler) != 0 {
		t.Fatalf("kendi yayinim senin icin'de olmamali: %+v", kendi.Ogeler)
	}
	if b, err := s.Begeni(ctx, dinleyici, yanit.ID, true); err != nil || b.BegeniSayisi != 1 || !b.BegendimMi {
		t.Fatalf("begeni = %+v, %v", b, err)
	}

	dosya, err := s.Dosyalar(ctx, dinleyici, yanit.ID)
	if err != nil || dosya.Master.URL == "" {
		t.Fatalf("dosyalar = %+v, %v", dosya, err)
	}

	takipcilere := GorunurlukTakipciler
	if _, err := s.YayinGuncelle(ctx, sahip, yanit.ID, YayinYamasi{Gorunurluk: &takipcilere}); err != nil {
		t.Fatalf("yama: %v", err)
	}
	if _, err := s.YayinGoster(ctx, dinleyici, yanit.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("takipciler yayini takip etmeyene gorunmemeli: %v", err)
	}
	p, err := s.ProfilGoster(ctx, dinleyici, "sahip")
	if err != nil || p.Istatistik.Sarki != 0 {
		t.Fatalf("takip etmeyen icin sarki sayaci 0 olmali: %+v, %v", p, err)
	}
	ben, _ := s.ProfilBen(ctx, sahip)
	if ben.Istatistik.Sarki != 1 || !ben.BenMiyim {
		t.Fatalf("sahibin sayaci = %+v", ben)
	}

	if err := s.YayinSil(ctx, sahip, yanit.ID); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if err := s.YayinSil(ctx, sahip, yanit.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci silme: %v", err)
	}
	if len(q.gorevler) != 2 || q.gorevler[1].Type() != TaskTypeDosyaSil {
		t.Fatalf("dosya silme isi kurulmali: %d", len(q.gorevler))
	}
}

func TestButunlesik_AdUygunlugu(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	s := NewServis(d, &sahteKuyruk{}, nil, testR2())
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	for i, ad := range []string{"ali", "ali2"} {
		id := []string{a, b}[i]
		if _, err := s.ProfilKaydet(ctx, id, ProfilGirdisi{KullaniciAdi: ad, GorunenAd: "x"}); err != nil {
			t.Fatalf("profil: %v", err)
		}
	}
	u, err := s.AdUygunMu(ctx, uuid.NewString(), "ali")
	if err != nil || u.Uygun || u.Oneri == nil || *u.Oneri != "ali3" {
		t.Fatalf("uygunluk = %+v, %v", u, err)
	}
	if _, err := s.ProfilKaydet(ctx, b, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "x"}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("alinmis ad: %v", err)
	}
}
