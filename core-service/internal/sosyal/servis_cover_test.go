package sosyal

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// sahteKopyalayici, kopyalari kaydeder; hata doluysa her kopyada onu doner.
type sahteKopyalayici struct {
	kopyalar [][2]string
	hata     error
}

func (k *sahteKopyalayici) Kopyala(_ context.Context, kaynak, hedef string) error {
	if k.hata != nil {
		return k.hata
	}
	k.kopyalar = append(k.kopyalar, [2]string{kaynak, hedef})
	return nil
}

// coverDeposu: yayinX kullaniciB'nin altyapisi acik, iki stem kopyasi
// olan herkes yayinidir.
func coverDeposu() *sahteDepo {
	d := yeniSahteDepo()
	k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
	k.Tur, k.Bpm, k.Ton, k.SureSn = "pop", 120, Ton{Tonik: "A", Mod: "minor"}, 180
	k.Yapi = []YapiOgesi{{ID: "v1", Tur: "verse", Olcu: 8}}
	k.Sozler = Sozler{Bolumler: []SozBolumu{{BolumID: "v1", Tur: "verse", Satirlar: []string{"la"}}}}
	d.yayinlar[yayinX] = k
	d.stemListeleri = map[string][]StemKaydi{yayinX: {
		{ID: "drums", Enstruman: "Davul", Sure: 180.5, Anahtar: "sosyal/" + yayinX + "/stems/drums.mp3"},
		{ID: "bass", Enstruman: "Bas", Sure: 179, Anahtar: "sosyal/" + yayinX + "/stems/bass"},
	}}
	return d
}

func TestServis_CoverBaslat(t *testing.T) {
	d := coverDeposu()
	s, _ := testServisi(d)
	kop := &sahteKopyalayici{}
	s.KopyalayiciAyarla(kop)

	b, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX)
	if err != nil {
		t.Fatalf("CoverBaslat: %v", err)
	}
	if b.Kaynak != (KaynakOzeti{YayinID: yayinX, Baslik: "Orijinal", SahipKullaniciAdi: "sahip"}) {
		t.Fatalf("kaynak = %+v", b.Kaynak)
	}
	if b.Tur != "pop" || b.Bpm != 120 || b.Ton.Mod != "minor" || b.SureSn != 180 || len(b.Yapi) != 1 || len(b.Sozler.Bolumler) != 1 {
		t.Fatalf("baslangic = %+v", b)
	}
	if len(kop.kopyalar) != 2 || len(b.Stemler) != 2 {
		t.Fatalf("kopyalar = %v, stemler = %+v", kop.kopyalar, b.Stemler)
	}
	desen := regexp.MustCompile(`^stems/` + kullaniciA + `/([0-9a-f-]{36})/drums\.mp3$`)
	m := desen.FindStringSubmatch(kop.kopyalar[0][1])
	if kop.kopyalar[0][0] != "sosyal/"+yayinX+"/stems/drums.mp3" || m == nil {
		t.Fatalf("ilk kopya = %v", kop.kopyalar[0])
	}
	if kop.kopyalar[1][1] != "stems/"+kullaniciA+"/"+m[1]+"/bass" {
		t.Fatalf("ikinci kopya ayni klasore, uzantisiz = %v", kop.kopyalar[1])
	}
	st := b.Stemler[0]
	if st.ID != "drums" || st.Enstruman != "Davul" || st.Sure != 180.5 ||
		!strings.HasSuffix(st.FileURL, "/"+kop.kopyalar[0][1]) || !strings.Contains(st.FileURL, "makeasinger-private") {
		t.Fatalf("stem = %+v", st)
	}

	// Ikinci baslatma yeni bir klasore kopyalar.
	if _, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kop.kopyalar[2][1], m[1]) {
		t.Fatalf("her baslatma yeni klasor almali: %v", kop.kopyalar[2])
	}
}

func TestServis_CoverBaslatImzaliAdres(t *testing.T) {
	d := coverDeposu()
	s := NewServis(d, &sahteKuyruk{}, imzaliDepolama{}, testR2()).KopyalayiciAyarla(&sahteKopyalayici{})
	b, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX)
	if err != nil || !strings.HasPrefix(b.Stemler[0].FileURL, "https://imzali.example/stems/"+kullaniciA+"/") {
		t.Fatalf("imzali adres = %+v, %v", b, err)
	}
}

func TestServis_CoverBaslatKopyalayicisiz(t *testing.T) {
	s, _ := testServisi(coverDeposu())
	b, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX)
	if err != nil || len(b.Stemler) != 2 {
		t.Fatalf("R2'siz (dev) baslangic = %+v, %v", b, err)
	}
}

func TestServis_CoverBaslatHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad        string
		hazirla   func(d *sahteDepo)
		kullanici string
		yayin     string
		beklenen  error
	}{
		{"altyapi kapali", func(d *sahteDepo) { d.yayinlar[yayinX].AltyapiAcik = false }, kullaniciA, yayinX, ErrAltyapiKapali},
		{"cover yayini", func(d *sahteDepo) { d.yayinlar[yayinX].KaynakYayinID = yayinY }, kullaniciA, yayinX, ErrAltyapiKapali},
		{"stem kopyasi yok", func(d *sahteDepo) { d.stemListeleri[yayinX] = []StemKaydi{} }, kullaniciA, yayinX, ErrAltyapiKapali},
		{"gorunmeyen", func(d *sahteDepo) { d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler }, kullaniciA, yayinX, ErrBulunamadi},
		{"olmayan", nil, kullaniciA, yayinY, ErrBulunamadi},
		{"bozuk id", nil, kullaniciA, "x", ErrBulunamadi},
		{"sahibinin hazirlanan yayini", func(d *sahteDepo) { d.yayinlar[yayinX].Durum = DurumHazirlaniyor }, kullaniciB, yayinX, ErrDurum},
	}
	for _, c := range durumlar {
		d := coverDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		kop := &sahteKopyalayici{}
		s.KopyalayiciAyarla(kop)
		if _, err := s.CoverBaslat(ctx, c.kullanici, c.yayin); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(kop.kopyalar) != 0 {
			t.Errorf("%s: kopya yapilmamali", c.ad)
		}
	}

	s, _ := testServisi(coverDeposu())
	s.KopyalayiciAyarla(&sahteKopyalayici{hata: errors.New("r2 kapali")})
	if _, err := s.CoverBaslat(ctx, kullaniciA, yayinX); err == nil || errors.Is(err, ErrAltyapiKapali) {
		t.Fatalf("kopya hatasi sunucu hatasi olmali: %v", err)
	}
}

func TestServis_Coverlar(t *testing.T) {
	d := coverDeposu()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Coverlar(ctx, kullaniciA, strings.ToUpper(yayinX), "", 2)
	if err != nil || len(sayfa.Ogeler) != 2 || sayfa.SonrakiImlec == nil {
		t.Fatalf("sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.tur != "cover" || d.sonAkis.sahip != yayinX || d.sonAkis.izleyici != kullaniciA || d.sonAkis.limit != 2 {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}

	d.sonAkis = akisCagrisi{}
	d.yayinlar[yayinX].AltyapiAcik = false
	bos, err := s.Coverlar(ctx, kullaniciA, yayinX, "", 20)
	if err != nil || bos.Ogeler == nil || len(bos.Ogeler) != 0 || bos.SonrakiImlec != nil {
		t.Fatalf("altyapi kapali bos sayfa = %+v, %v", bos, err)
	}
	if d.sonAkis.tur != "" {
		t.Fatal("altyapi kapaliyken depo sorgulanmamali")
	}

	d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler
	if _, err := s.Coverlar(ctx, kullaniciA, yayinX, "", 20); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorunmeyen kaynak: %v", err)
	}
	if _, err := s.Coverlar(ctx, kullaniciA, yayinX, "bozuk!", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bozuk imlec: %v", err)
	}
}
