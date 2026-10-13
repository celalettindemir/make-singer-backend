package social

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

const (
	yayinX = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yayinY = "9b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func profilliDepo() *sahteDepo {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali", GorunenAd: "Ali"}
	return d
}

func sahteYayin(id, sahip, gorunurluk, durum string) *YayinKaydi {
	k := &YayinKaydi{SahipID: sahip}
	k.ID, k.Slug, k.Baslik = id, "Ab3dEf9h", "Orijinal"
	k.Sahip = ProfilOzeti{KullaniciAdi: "sahip"}
	k.Gorunurluk, k.Durum, k.AltyapiAcik, k.StemVar = gorunurluk, durum, true, true
	return k
}

// yayinIstegiA, kullaniciA'nin kendi dosyalariyla gecerli bir istek.
func yayinIstegiA() YayinIstegi {
	ist := gecerliIstek()
	ist.MasterURL = "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/" + kullaniciA + "/p1/m1.wav?X-Amz-Signature=x"
	ist.Stemler[0].URL = "stems/" + kullaniciA + "/p1/drums.mp3"
	return ist
}

func secenek(opts []asynq.Option, tur asynq.OptionType) any {
	for _, o := range opts {
		if o.Type() == tur {
			return o.Value()
		}
	}
	return nil
}

func TestServis_YayinOlustur_ProfilYok(t *testing.T) {
	s, q := testServisi(yeniSahteDepo())
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("ErrProfilYok bekleniyordu: %v", err)
	}
	if len(q.gorevler) != 0 {
		t.Fatal("profil yokken is kurulmamali")
	}
}

func TestServis_YayinOlustur_KaydederVeKuyrugaKoyar(t *testing.T) {
	d := profilliDepo()
	s, q := testServisi(d)
	yanit, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA())
	if err != nil {
		t.Fatalf("YayinOlustur: %v", err)
	}
	if yanit.Durum != DurumHazirlaniyor || !SlugGecerli(yanit.Slug) {
		t.Fatalf("yanit = %+v", yanit)
	}
	if len(d.eklenen) != 1 {
		t.Fatalf("tek yayin eklenmeli: %d", len(d.eklenen))
	}
	y := d.eklenen[0]
	if y.ID != yanit.ID || y.Slug != yanit.Slug || y.SahipID != kullaniciA || !y.AltyapiAcik || y.KaynakYayinID != "" {
		t.Fatalf("yeni yayin = %+v", y)
	}
	if y.Hazirlik.MasterKey != "masters/"+kullaniciA+"/p1/m1.wav" {
		t.Fatalf("master anahtari = %q", y.Hazirlik.MasterKey)
	}
	if len(y.Hazirlik.Stems) != 1 || y.Hazirlik.Stems[0] != (HazirlikStemi{ID: "drums", Enstruman: "drums", Sure: 184, Key: "stems/" + kullaniciA + "/p1/drums.mp3"}) {
		t.Fatalf("stemler = %+v", y.Hazirlik.Stems)
	}
	if len(q.gorevler) != 1 || q.gorevler[0].Type() != TaskTypeHazirla {
		t.Fatalf("hazirla isi kurulmali: %v", q.gorevler)
	}
	var yuk GorevYuku
	if err := json.Unmarshal(q.gorevler[0].Payload(), &yuk); err != nil || yuk.YayinID != yanit.ID {
		t.Fatalf("yuk = %s", q.gorevler[0].Payload())
	}
	opts := q.secenekler[0]
	if secenek(opts, asynq.QueueOpt) != KuyrukSosyal || secenek(opts, asynq.MaxRetryOpt) != 3 || secenek(opts, asynq.RetentionOpt) != 24*time.Hour {
		t.Fatalf("secenekler = %v", opts)
	}
}

func TestServis_YayinOlustur_AdresHatalari(t *testing.T) {
	durumlar := []struct {
		ad   string
		boz  func(*YayinIstegi)
		hata error
	}{
		{"yabanci master", func(i *YayinIstegi) { i.MasterURL = "masters/" + kullaniciB + "/p/m.wav" }, ErrOwnership},
		{"yabanci stem", func(i *YayinIstegi) { i.Stemler[0].URL = "stems/" + kullaniciB + "/p/s.mp3" }, ErrOwnership},
		{"bozuk adres", func(i *YayinIstegi) { i.MasterURL = "https://kotu.example.com/masters/x/m.wav" }, ErrValidation},
		{"ciplak anlamsiz", func(i *YayinIstegi) { i.MasterURL = "merhaba" }, ErrValidation},
		{"public dosya", func(i *YayinIstegi) { i.MasterURL = "exports/" + kullaniciA + "/e1.mp3" }, ErrValidation},
	}
	for _, dr := range durumlar {
		d := profilliDepo()
		s, q := testServisi(d)
		ist := yayinIstegiA()
		dr.boz(&ist)
		if _, err := s.YayinOlustur(context.Background(), kullaniciA, ist); !errors.Is(err, dr.hata) {
			t.Errorf("%s: %v bekleniyordu, gelen %v", dr.ad, dr.hata, err)
		}
		if len(d.eklenen) != 0 || len(q.gorevler) != 0 {
			t.Errorf("%s: hatali istek kaydedilmemeli", dr.ad)
		}
	}
}

func TestServis_YayinOlustur_SlugCakismasiYenidenDenenir(t *testing.T) {
	d := profilliDepo()
	d.slugCakismasi = 2
	s, _ := testServisi(d)
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); err != nil {
		t.Fatalf("iki cakismadan sonra basarmali: %v", err)
	}
	d = profilliDepo()
	d.slugCakismasi = 5
	s, _ = testServisi(d)
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); err == nil {
		t.Fatal("bes cakismada vazgecmeli")
	}
}

func TestServis_YayinOlustur_KuyrukHatasindaGeriAlinir(t *testing.T) {
	d := profilliDepo()
	s, q := testServisi(d)
	q.hata = errors.New("redis kapali")
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if len(d.kaldirilan) != 1 || d.kaldirilan[0] != d.eklenen[0].ID {
		t.Fatalf("eklenen yayin geri alinmali: %v", d.kaldirilan)
	}
}

// Istek baglami kuyruk hatasi sirasinda iptal edilse de geri alma yapilir.
func TestServis_YayinOlustur_IptalEdilenBaglamdaGeriAlinir(t *testing.T) {
	d := profilliDepo()
	s, q := testServisi(d)
	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	q.once = iptal
	q.hata = errors.New("redis kapali")
	if _, err := s.YayinOlustur(ctx, kullaniciA, yayinIstegiA()); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if len(d.kaldirilan) != 1 || d.kaldirilan[0] != d.eklenen[0].ID {
		t.Fatalf("iptal edilen baglamda da yayin geri alinmali: %v", d.kaldirilan)
	}
}

func TestServis_YayinOlustur_Cover(t *testing.T) {
	coverIstegi := func(kaynak string) YayinIstegi {
		ist := yayinIstegiA()
		ist.KaynakYayinID = &kaynak
		return ist
	}
	durumlar := []struct {
		ad     string
		kaynak *YayinKaydi
		kimlik string
		hata   error
	}{
		{"yok", nil, yayinX, ErrSourceNotFound},
		{"gecersiz kimlik", nil, "x", ErrSourceNotFound},
		{"gizli", sahteYayin(yayinX, kullaniciB, GorunurlukTakipciler, DurumYayinda), yayinX, ErrSourceNotFound},
		{"hazirlaniyor", sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumHazirlaniyor), yayinX, ErrSourceNotFound},
		{"cover'in cover'i", func() *YayinKaydi {
			k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
			k.KaynakYayinID = yayinY
			return k
		}(), yayinX, ErrSourceNotFound},
		{"altyapi kapali", func() *YayinKaydi {
			k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
			k.AltyapiAcik = false
			return k
		}(), yayinX, ErrBackingDisabled},
	}
	for _, dr := range durumlar {
		d := profilliDepo()
		if dr.kaynak != nil {
			d.yayinlar[dr.kaynak.ID] = dr.kaynak
		}
		s, _ := testServisi(d)
		if _, err := s.YayinOlustur(context.Background(), kullaniciA, coverIstegi(dr.kimlik)); !errors.Is(err, dr.hata) {
			t.Errorf("%s: %v bekleniyordu, gelen %v", dr.ad, dr.hata, err)
		}
	}

	d := profilliDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciB, GorunurlukLink, DurumYayinda)
	s, _ := testServisi(d)
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, coverIstegi(yayinX)); err != nil {
		t.Fatalf("link yayini cover'lanabilir: %v", err)
	}
	y := d.eklenen[0]
	if y.AltyapiAcik || len(y.Hazirlik.Stems) != 0 || y.KaynakYayinID != yayinX || y.KaynakBaslik != "Orijinal" || y.KaynakSahipAdi != "sahip" {
		t.Fatalf("cover = %+v", y)
	}
}

func TestServis_YayinGosterGorunurluk(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumHata)
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukLink, DurumYayinda)
	d.yayinlar[yayinY].Slug = "Zz9yYx8w"
	s, _ := testServisi(d)
	ctx := context.Background()

	if y, err := s.YayinGoster(ctx, kullaniciA, yayinX); err != nil || !y.BenimMi || y.Durum != DurumHata {
		t.Fatalf("sahip hata yayinini gormeli: %+v, %v", y, err)
	}
	if _, err := s.YayinGoster(ctx, kullaniciB, yayinX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("yabanci hata yayinini gormemeli: %v", err)
	}
	if y, err := s.YayinGoster(ctx, kullaniciB, yayinY); err != nil || y.BenimMi {
		t.Fatalf("link detayda gorunur: %+v, %v", y, err)
	}
	if _, err := s.YayinGoster(ctx, kullaniciB, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gecersiz id: %v", err)
	}
	if y, err := s.SlugGoster(ctx, kullaniciB, "Zz9yYx8w"); err != nil || y.ID != yayinY {
		t.Fatalf("slug: %+v, %v", y, err)
	}
	if _, err := s.SlugGoster(ctx, kullaniciB, "kisa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gecersiz slug: %v", err)
	}
}

func TestServis_YayinGuncelle(t *testing.T) {
	d := yeniSahteDepo()
	kapali := sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumYayinda)
	kapali.AltyapiAcik = false
	d.yayinlar[yayinX] = kapali
	s, _ := testServisi(d)
	ctx := context.Background()
	ac, link := true, GorunurlukLink

	if _, err := s.YayinGuncelle(ctx, kullaniciB, yayinX, YayinYamasi{Gorunurluk: &link}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi guncelleyemez: %v", err)
	}
	// Stem kopyasi var: altyapi yeniden acilabilir.
	y, err := s.YayinGuncelle(ctx, kullaniciA, yayinX, YayinYamasi{AltyapiAcik: &ac, Gorunurluk: &link})
	if err != nil || !y.AltyapiAcik || y.Gorunurluk != GorunurlukLink {
		t.Fatalf("guncelle = %+v, %v", y, err)
	}

	stemsiz := sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumYayinda)
	stemsiz.AltyapiAcik, stemsiz.StemVar = false, false
	d.yayinlar[yayinY] = stemsiz
	if _, err := s.YayinGuncelle(ctx, kullaniciA, yayinY, YayinYamasi{AltyapiAcik: &ac}); !errors.Is(err, ErrNoStems) {
		t.Fatalf("stemsiz yayin: %v", err)
	}
	stemsiz.StemVar, stemsiz.KaynakYayinID = true, yayinX
	if _, err := s.YayinGuncelle(ctx, kullaniciA, yayinY, YayinYamasi{AltyapiAcik: &ac}); !errors.Is(err, ErrNoStems) {
		t.Fatalf("cover'in altyapisi acilamaz: %v", err)
	}
	kotu := "gizli"
	if _, err := s.YayinGuncelle(ctx, kullaniciA, yayinX, YayinYamasi{Gorunurluk: &kotu}); !errors.Is(err, ErrValidation) {
		t.Fatalf("gecersiz gorunurluk: %v", err)
	}
}

func TestServis_YayinSil(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumYayinda)
	s, q := testServisi(d)
	ctx := context.Background()
	if err := s.YayinSil(ctx, kullaniciB, yayinX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi silemez: %v", err)
	}
	if err := s.YayinSil(ctx, kullaniciA, yayinX); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if len(q.gorevler) != 1 || q.gorevler[0].Type() != TaskTypeDosyaSil {
		t.Fatalf("dosya silme isi kurulmali: %v", q.gorevler)
	}
	opts := q.secenekler[0]
	if secenek(opts, asynq.QueueOpt) != "bakim" || secenek(opts, asynq.ProcessInOpt) != 24*time.Hour {
		t.Fatalf("secenekler = %v", opts)
	}
	if err := s.YayinSil(ctx, kullaniciA, yayinX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci silme 404: %v", err)
	}
	if _, err := s.YayinGoster(ctx, kullaniciA, yayinX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("silinen yayin sahibine de gorunmez: %v", err)
	}

	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumYayinda)
	q.hata = errors.New("redis kapali")
	if err := s.YayinSil(ctx, kullaniciA, yayinY); err != nil {
		t.Fatalf("kuyruk hatasi silmeyi bozmamali: %v", err)
	}
}

func TestServis_TekrarDene(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumHata)
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumYayinda)
	s, q := testServisi(d)
	ctx := context.Background()
	if _, err := s.TekrarDene(ctx, kullaniciB, yayinX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi: %v", err)
	}
	if _, err := s.TekrarDene(ctx, kullaniciA, yayinY); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("yayindaki yayin: %v", err)
	}
	y, err := s.TekrarDene(ctx, kullaniciA, yayinX)
	if err != nil || y.Durum != DurumHazirlaniyor || y.ID != yayinX {
		t.Fatalf("tekrar dene = %+v, %v", y, err)
	}
	if len(q.gorevler) != 1 || q.gorevler[0].Type() != TaskTypeHazirla {
		t.Fatalf("hazirla isi yeniden kurulmali: %v", q.gorevler)
	}

	d.yayinlar[yayinX].Durum = DurumHata
	q.hata = errors.New("redis kapali")
	if _, err := s.TekrarDene(ctx, kullaniciA, yayinX); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if d.yayinlar[yayinX].Durum != DurumHata {
		t.Fatalf("kuyruga girmeyen yayin hataya donmeli: %s", d.yayinlar[yayinX].Durum)
	}
}

// Istek baglami kuyruk hatasi sirasinda iptal edilse de yayin hataya doner.
func TestServis_TekrarDene_IptalEdilenBaglamdaHatayaDoner(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumHata)
	s, q := testServisi(d)
	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	q.once = iptal
	q.hata = errors.New("redis kapali")
	if _, err := s.TekrarDene(ctx, kullaniciA, yayinX); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if d.yayinlar[yayinX].Durum != DurumHata {
		t.Fatalf("iptal edilen baglamda da yayin hataya donmeli: %s", d.yayinlar[yayinX].Durum)
	}
}

type imzaliDepolama struct{}

func (imzaliDepolama) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (imzaliDepolama) Delete(context.Context, string) error { return nil }
func (imzaliDepolama) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (imzaliDepolama) URLFor(_ context.Context, key string) (string, time.Time, error) {
	return "https://imzali.example/" + key, time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC), nil
}

func TestServis_Dosyalar(t *testing.T) {
	d := yeniSahteDepo()
	hazir := sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumYayinda)
	hazir.MasterAnahtari = "sosyal/" + yayinX + "/master.wav"
	d.yayinlar[yayinX] = hazir
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumHazirlaniyor)
	ctx := context.Background()

	s, _ := testServisi(d)
	dosya, err := s.Dosyalar(ctx, kullaniciB, yayinX)
	if err != nil || !strings.HasSuffix(dosya.Master.URL, "/sosyal/"+yayinX+"/master.wav") || dosya.Master.ExpiresAt != nil {
		t.Fatalf("R2'siz adres = %+v, %v", dosya, err)
	}
	imzali := NewServis(d, &sahteKuyruk{}, imzaliDepolama{}, testR2())
	dosya, err = imzali.Dosyalar(ctx, kullaniciB, yayinX)
	if err != nil || dosya.Master.URL != "https://imzali.example/sosyal/"+yayinX+"/master.wav" || dosya.Master.ExpiresAt == nil {
		t.Fatalf("imzali adres = %+v, %v", dosya, err)
	}
	if _, err := s.Dosyalar(ctx, kullaniciA, yayinY); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("hazirlanan yayinin dosyasi yok: %v", err)
	}
	if _, err := s.Dosyalar(ctx, kullaniciB, yayinY); !errors.Is(err, ErrNotFound) {
		t.Fatalf("yabanci hazirlanan yayini gormez: %v", err)
	}
}
