package sosyal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	kullaniciC = "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yorum1     = "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yorumC     = "c21c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

// yorumDeposu: kullaniciA'nin profili var; yayinX kullaniciB'nin herkes
// yayinidir (180 sn, yorumlar acik).
func yorumDeposu() *sahteDepo {
	d := profilliDepo()
	k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
	k.YorumAcik, k.SureSn = true, 180
	d.yayinlar[yayinX] = k
	return d
}

func intp(n int) *int { return &n }

func TestServis_YorumYaz(t *testing.T) {
	d := yorumDeposu()
	s, _ := testServisi(d)
	y, err := s.YorumYaz(context.Background(), kullaniciA, yayinX, YorumGirdisi{Metin: "  harika  ", ZamanMs: intp(180000)})
	if err != nil {
		t.Fatalf("YorumYaz: %v", err)
	}
	if !y.SilebilirMiyim || y.Metin != "harika" || y.Yazar.KullaniciAdi != "ali" || *y.ZamanMs != 180000 {
		t.Fatalf("yorum = %+v", y)
	}
	if len(d.eklenenYorumlar) != 1 || d.eklenenYorumlar[0].YayinID != yayinX || d.eklenenYorumlar[0].YazarID != kullaniciA {
		t.Fatalf("eklenen = %+v", d.eklenenYorumlar)
	}
	if _, err := s.YorumYaz(context.Background(), kullaniciA, yayinX, YorumGirdisi{Metin: "damgasiz"}); err != nil {
		t.Fatalf("damgasiz yorum: %v", err)
	}
	if d.eklenenYorumlar[1].ZamanMs != nil {
		t.Fatal("damgasiz yorumun zamani nil kalmali")
	}
}

func TestServis_YorumYazHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad        string
		hazirla   func(d *sahteDepo)
		kullanici string
		yayin     string
		girdi     YorumGirdisi
		beklenen  error
	}{
		{"bos metin", nil, kullaniciA, yayinX, YorumGirdisi{Metin: "   "}, ErrDogrulama},
		{"uzun metin", nil, kullaniciA, yayinX, YorumGirdisi{Metin: strings.Repeat("ş", 501)}, ErrDogrulama},
		{"NUL iceren metin", nil, kullaniciA, yayinX, YorumGirdisi{Metin: "a\x00b"}, ErrDogrulama},
		{"negatif zaman", nil, kullaniciA, yayinX, YorumGirdisi{Metin: "a", ZamanMs: intp(-1)}, ErrDogrulama},
		{"sureyi asan zaman", nil, kullaniciA, yayinX, YorumGirdisi{Metin: "a", ZamanMs: intp(180001)}, ErrDogrulama},
		{"gorunmeyen yayin", func(d *sahteDepo) { d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler }, kullaniciA, yayinX, YorumGirdisi{Metin: "a"}, ErrBulunamadi},
		{"olmayan yayin", nil, kullaniciA, yayinY, YorumGirdisi{Metin: "a"}, ErrBulunamadi},
		{"bozuk yayin id", nil, kullaniciA, "x", YorumGirdisi{Metin: "a"}, ErrBulunamadi},
		{"yorum kapali", func(d *sahteDepo) { d.yayinlar[yayinX].YorumAcik = false }, kullaniciA, yayinX, YorumGirdisi{Metin: "a"}, ErrYorumKapali},
		{"profil yok", nil, kullaniciC, yayinX, YorumGirdisi{Metin: "a"}, ErrProfilYok},
	}
	for _, c := range durumlar {
		d := yorumDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		if _, err := s.YorumYaz(ctx, c.kullanici, c.yayin, c.girdi); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(d.eklenenYorumlar) != 0 {
			t.Errorf("%s: yorum yazilmamali", c.ad)
		}
	}
}

// Yorum kapaliyken sahibi de 403 alir; gorunurluk yorum_kapali'dan once gelir.
func TestServis_YorumlarKapaliVeGorunurluk(t *testing.T) {
	d := yorumDeposu()
	d.yayinlar[yayinX].YorumAcik = false
	s, _ := testServisi(d)
	ctx := context.Background()
	for _, k := range []string{kullaniciA, kullaniciB} {
		if _, err := s.Yorumlar(ctx, k, yayinX, "", 20); !errors.Is(err, ErrYorumKapali) {
			t.Errorf("%s: %v, beklenen ErrYorumKapali", k, err)
		}
	}
	d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler
	if _, err := s.Yorumlar(ctx, kullaniciA, yayinX, "", 20); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorunmeyen yayin: %v", err)
	}
}

func TestServis_YorumlarSayfaVeImlec(t *testing.T) {
	d := yorumDeposu()
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	d.yorumOgeleri = []Yorum{
		{ID: yorum1, ZamanMs: intp(500), Olusturma: t0},
		{ID: yorumC, Olusturma: t0.Add(time.Minute)},
		{ID: "c31c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", Olusturma: t0},
	}
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Yorumlar(ctx, kullaniciA, yayinX, "", 1)
	if err != nil || len(sayfa.Ogeler) != 1 || sayfa.SonrakiImlec == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if _, err := s.Yorumlar(ctx, kullaniciA, yayinX, *sayfa.SonrakiImlec, 1); err != nil {
		t.Fatal(err)
	}
	im := d.sonYorumImleci
	if im == nil || im.G != 0 || im.Z == nil || *im.Z != 500 || im.ID != yorum1 || !im.T.Equal(t0) {
		t.Fatalf("zamanli imlec = %+v", im)
	}

	sayfa, err = s.Yorumlar(ctx, kullaniciA, yayinX, "", 2)
	if err != nil || sayfa.SonrakiImlec == nil {
		t.Fatal(err)
	}
	if _, err := s.Yorumlar(ctx, kullaniciA, yayinX, *sayfa.SonrakiImlec, 2); err != nil {
		t.Fatal(err)
	}
	im = d.sonYorumImleci
	if im == nil || im.G != 1 || im.Z != nil || im.ID != yorumC {
		t.Fatalf("damgasiz imlec = %+v", im)
	}

	d.yorumOgeleri = nil
	bos, err := s.Yorumlar(ctx, kullaniciA, yayinX, "", 20)
	if err != nil || bos.Ogeler == nil || bos.SonrakiImlec != nil {
		t.Fatalf("bos sayfa = %+v, %v", bos, err)
	}
}

func TestServis_YorumImleciDogrulama(t *testing.T) {
	gecerliID := "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	for _, ham := range []string{
		"bozuk!",
		*imlecKodla(YorumImleci{G: 0, T: time.Now(), ID: gecerliID}),             // G=0 ama Z yok
		*imlecKodla(YorumImleci{G: 1, Z: intp(3), T: time.Now(), ID: gecerliID}), // G=1 ama Z var
		*imlecKodla(YorumImleci{G: 2, T: time.Now(), ID: gecerliID}),
		*imlecKodla(YorumImleci{G: 0, Z: intp(-1), T: time.Now(), ID: gecerliID}),
		*imlecKodla(YorumImleci{G: 1, T: time.Now(), ID: "uuid-degil"}),
		*imlecKodla(YorumImleci{G: 1, ID: gecerliID}),
	} {
		if _, err := yorumImleciCoz(ham); !errors.Is(err, ErrDogrulama) {
			t.Errorf("%q: %v, beklenen ErrDogrulama", ham, err)
		}
	}
}

func TestServis_YorumSil(t *testing.T) {
	ctx := context.Background()
	kayit := func() *YorumKaydi {
		return &YorumKaydi{ID: yorum1, YayinID: yayinX, YazarID: kullaniciA, YayinSahibiID: kullaniciB}
	}
	// Yazar ve yayin sahibi silebilir.
	for _, k := range []string{kullaniciA, kullaniciB} {
		d := yorumDeposu()
		d.yorumKayitlari[yorum1] = kayit()
		s, _ := testServisi(d)
		if err := s.YorumSil(ctx, k, yorum1); err != nil {
			t.Fatalf("%s silemedi: %v", k, err)
		}
		if err := s.YorumSil(ctx, k, yorum1); !errors.Is(err, ErrBulunamadi) {
			t.Fatalf("tekrar silme 404 olmali: %v", err)
		}
	}
	// Ucuncu kisi: yayini goruyorsa 403, gormuyorsa 404.
	d := yorumDeposu()
	d.yorumKayitlari[yorum1] = kayit()
	s, _ := testServisi(d)
	if err := s.YorumSil(ctx, kullaniciC, yorum1); !errors.Is(err, ErrYetki) {
		t.Fatalf("baskasi: %v, beklenen ErrYetki", err)
	}
	d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler
	if err := s.YorumSil(ctx, kullaniciC, yorum1); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorunmeyen yayin: %v, beklenen ErrBulunamadi", err)
	}
	if len(d.silinenYorumlar) != 0 {
		t.Fatal("yetkisiz silme yazilmamali")
	}
	for _, id := range []string{"x", yorumC} {
		if err := s.YorumSil(ctx, kullaniciA, id); !errors.Is(err, ErrBulunamadi) {
			t.Errorf("%s: %v, beklenen ErrBulunamadi", id, err)
		}
	}
}
