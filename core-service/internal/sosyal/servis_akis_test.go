package sosyal

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func ozetler(n int) []YayinOzeti {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	o := make([]YayinOzeti, n)
	for i := range o {
		o[i].ID = fmt.Sprintf("%08d-1b2c-4d3e-8f9a-0b1c2d3e4f5a", i)
		o[i].Olusturma = t0.Add(-time.Duration(i) * time.Minute)
	}
	return o
}

func TestServis_AkisYeniSayfalari(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Akis(ctx, kullaniciA, SekmeYeni, "", 2)
	if err != nil || len(sayfa.Ogeler) != 2 || sayfa.SonrakiImlec == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.tur != "yeni" || d.sonAkis.limit != 2 || d.sonAkis.zaman != nil || d.sonAkis.izleyici != kullaniciA {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}
	if _, err := s.Akis(ctx, kullaniciA, SekmeYeni, *sayfa.SonrakiImlec, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if d.sonAkis.zaman == nil || d.sonAkis.zaman.ID != d.akisOgeleri[1].ID || !d.sonAkis.zaman.T.Equal(d.akisOgeleri[1].Olusturma) {
		t.Fatalf("imlec son ogeyi gostermeli: %+v", d.sonAkis.zaman)
	}

	d.akisOgeleri = ozetler(2)
	son, err := s.Akis(ctx, kullaniciA, SekmeTakip, "", 2)
	if err != nil || len(son.Ogeler) != 2 || son.SonrakiImlec != nil || d.sonAkis.tur != "takip" {
		t.Fatalf("son sayfa = %+v, %v", son, err)
	}
	d.akisOgeleri = nil
	bos, err := s.Akis(ctx, kullaniciA, SekmeYeni, "", 20)
	if err != nil || bos.Ogeler == nil || len(bos.Ogeler) != 0 {
		t.Fatalf("bos sayfa ogeler [] olmali: %+v, %v", bos, err)
	}
}

func TestServis_AkisSeninIcinReferansSabit(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	an := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	s.simdi = func() time.Time { return an }
	ctx := context.Background()

	sayfa, err := s.Akis(ctx, kullaniciA, SekmeSeninIcin, "", 2)
	if err != nil || len(sayfa.Ogeler) != 2 || sayfa.SonrakiImlec == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if !d.sonAkis.referans.Equal(an) || d.sonAkis.puan != nil {
		t.Fatalf("ilk sayfa referansi simdi olmali: %+v", d.sonAkis)
	}
	s.simdi = func() time.Time { return an.Add(time.Hour) }
	if _, err := s.Akis(ctx, kullaniciA, SekmeSeninIcin, *sayfa.SonrakiImlec, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if !d.sonAkis.referans.Equal(an) {
		t.Fatalf("sonraki sayfa ayni referansi kullanmali: %v", d.sonAkis.referans)
	}
	if d.sonAkis.puan == nil || d.sonAkis.puan.P != 99 || d.sonAkis.puan.ID != d.akisOgeleri[1].ID {
		t.Fatalf("puan imleci = %+v", d.sonAkis.puan)
	}
}

// Gelecekteki bir referans zamani simdiye kirpilir; istemci puanlamayi
// kaydiramaz ve sonraki imlec kirpilmis zamani tasir.
func TestServis_AkisSeninIcinGelecekReferansKirpilir(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	an := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	s.simdi = func() time.Time { return an }
	gelecek := imlecKodla(PuanImleci{P: 5, ID: d.akisOgeleri[0].ID, N: an.Add(24 * time.Hour).UnixMilli()})
	sayfa, err := s.Akis(context.Background(), kullaniciA, SekmeSeninIcin, *gelecek, 2)
	if err != nil {
		t.Fatalf("gelecek imlec: %v", err)
	}
	if !d.sonAkis.referans.Equal(an) {
		t.Fatalf("referans simdiye kirpilmali: %v", d.sonAkis.referans)
	}
	sonraki, err := puanImleciCoz(*sayfa.SonrakiImlec)
	if err != nil || sonraki.N != an.UnixMilli() {
		t.Fatalf("sonraki imlec kirpilmis zamani tasimali: %+v, %v", sonraki, err)
	}
}

func TestServis_AkisHatalari(t *testing.T) {
	s, _ := testServisi(yeniSahteDepo())
	ctx := context.Background()
	if _, err := s.Akis(ctx, kullaniciA, "populer", "", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("gecersiz sekme: %v", err)
	}
	if _, err := s.Akis(ctx, kullaniciA, SekmeYeni, "bozuk!", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bozuk imlec: %v", err)
	}
	if _, err := s.Akis(ctx, kullaniciA, SekmeSeninIcin, "bozuk!", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bozuk puan imleci: %v", err)
	}
}

func TestServis_ProfilYayinlari(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciB] = &ProfilKaydi{KullaniciID: kullaniciB, KullaniciAdi: "veli"}
	d.akisOgeleri = ozetler(1)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.ProfilYayinlari(ctx, kullaniciA, "veli", "coverlar", "", 20)
	if err != nil || len(sayfa.Ogeler) != 1 || sayfa.SonrakiImlec != nil {
		t.Fatalf("sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.sahip != kullaniciB || d.sonAkis.izleyici != kullaniciA || !d.sonAkis.coverlar {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}
	if _, err := s.ProfilYayinlari(ctx, kullaniciA, "veli", "", "", 20); err != nil || d.sonAkis.coverlar {
		t.Fatalf("bos tur sarkilar olmali: %v %+v", err, d.sonAkis)
	}
	if _, err := s.ProfilYayinlari(ctx, kullaniciA, "veli", "videolar", "", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("gecersiz tur: %v", err)
	}
	if _, err := s.ProfilYayinlari(ctx, kullaniciA, "yok", "sarkilar", "", 20); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan profil: %v", err)
	}
}

func TestServis_Begeni(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciB, GorunurlukTakipciler, DurumYayinda)
	s, _ := testServisi(d)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		b, err := s.Begeni(ctx, kullaniciA, yayinX, true)
		if err != nil || !b.BegendimMi || b.BegeniSayisi != 1 {
			t.Fatalf("begen %d = %+v, %v", i, b, err)
		}
	}
	b, err := s.Begeni(ctx, kullaniciA, yayinX, false)
	if err != nil || b.BegendimMi || b.BegeniSayisi != 0 {
		t.Fatalf("kaldir = %+v, %v", b, err)
	}
	if _, err := s.Begeni(ctx, kullaniciA, yayinY, true); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorulmeyen yayin begenilemez: %v", err)
	}
}
