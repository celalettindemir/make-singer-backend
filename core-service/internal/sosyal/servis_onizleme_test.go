package sosyal

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// sureliDepolama, GetSignedURL'e gelen sureyi kaydeder.
type sureliDepolama struct {
	sure time.Duration
	hata error
}

func (*sureliDepolama) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (*sureliDepolama) Delete(context.Context, string) error { return nil }
func (d *sureliDepolama) GetSignedURL(_ context.Context, key string, sure time.Duration) (string, error) {
	d.sure = sure
	return "https://imzali.example/" + key + "?sure", d.hata
}
func (*sureliDepolama) URLFor(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, errors.New("onizleme URLFor kullanmamali")
}

func onizlemeDeposu() *sahteDepo {
	d := yeniSahteDepo()
	d.onizlemeler = map[string]*OnizlemeKaydi{
		"Ab3dEf9h": {
			Onizleme: Onizleme{Baslik: "Sarki", Sahip: ProfilOzeti{KullaniciAdi: "ali"}, SureSn: 180,
				DalgaFormu: []float64{0.5}, Tur: "pop"},
			SahipID: kullaniciA, OnizlemeAnahtari: "sosyal/" + yayinX + "/onizleme.mp3",
		},
		"Zz99Zz99": {Onizleme: Onizleme{Baslik: "Onizlemesiz", DalgaFormu: []float64{}}},
	}
	return d
}

func TestServis_OnizlemeImzali1Saat(t *testing.T) {
	dep := &sureliDepolama{}
	s := NewServis(onizlemeDeposu(), &sahteKuyruk{}, dep, testR2())
	o, err := s.Onizleme(context.Background(), "Ab3dEf9h")
	if err != nil {
		t.Fatalf("Onizleme: %v", err)
	}
	if o.OnizlemeURL == nil || *o.OnizlemeURL != "https://imzali.example/sosyal/"+yayinX+"/onizleme.mp3?sure" {
		t.Fatalf("onizlemeUrl = %v", o.OnizlemeURL)
	}
	if dep.sure != time.Hour {
		t.Fatalf("imza suresi = %v, beklenen 1 saat", dep.sure)
	}
	if o.Baslik != "Sarki" || o.SureSn != 180 || o.Tur != "pop" || o.Sahip.KullaniciAdi != "ali" {
		t.Fatalf("onizleme = %+v", o)
	}

	yok, err := s.Onizleme(context.Background(), "Zz99Zz99")
	if err != nil || yok.OnizlemeURL != nil {
		t.Fatalf("onizlemesiz yayin = %+v, %v", yok, err)
	}

	dep.hata = errors.New("imza hatasi")
	if _, err := s.Onizleme(context.Background(), "Ab3dEf9h"); err == nil || errors.Is(err, ErrBulunamadi) {
		t.Fatalf("imza hatasi sunucu hatasi olmali: %v", err)
	}
}

func TestServis_OnizlemeR2siz(t *testing.T) {
	s, _ := testServisi(onizlemeDeposu())
	o, err := s.Onizleme(context.Background(), "Ab3dEf9h")
	if err != nil || o.OnizlemeURL == nil || !strings.HasSuffix(*o.OnizlemeURL, "/sosyal/"+yayinX+"/onizleme.mp3") {
		t.Fatalf("R2'siz adres = %+v, %v", o, err)
	}
}

func TestServis_OnizlemeBulunamadi(t *testing.T) {
	s, _ := testServisi(onizlemeDeposu())
	for _, slug := range []string{"Yok12345", "kisa", "Ab3dEf9h/", "../../xx"} {
		if _, err := s.Onizleme(context.Background(), slug); !errors.Is(err, ErrBulunamadi) {
			t.Errorf("%q: %v, beklenen ErrBulunamadi", slug, err)
		}
	}
}
