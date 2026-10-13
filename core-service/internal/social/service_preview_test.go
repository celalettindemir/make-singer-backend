package social

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
	d.onizlemeler = map[string]*PreviewRecord{
		"Ab3dEf9h": {
			Preview: Preview{Title: "Sarki", Owner: ProfileSummary{Username: "ali"}, DurationSec: 180,
				Waveform: []float64{0.5}, Genre: "pop"},
			OwnerID: kullaniciA, PreviewKey: "sosyal/" + yayinX + "/onizleme.mp3",
		},
		"Zz99Zz99": {Preview: Preview{Title: "Onizlemesiz", Waveform: []float64{}}},
	}
	return d
}

func TestServis_OnizlemeImzali1Saat(t *testing.T) {
	dep := &sureliDepolama{}
	s := NewService(onizlemeDeposu(), &sahteKuyruk{}, dep, testR2())
	o, err := s.Preview(context.Background(), "Ab3dEf9h")
	if err != nil {
		t.Fatalf("Onizleme: %v", err)
	}
	if o.PreviewURL == nil || *o.PreviewURL != "https://imzali.example/sosyal/"+yayinX+"/onizleme.mp3?sure" {
		t.Fatalf("onizlemeUrl = %v", o.PreviewURL)
	}
	if dep.sure != time.Hour {
		t.Fatalf("imza suresi = %v, beklenen 1 saat", dep.sure)
	}
	if o.Title != "Sarki" || o.DurationSec != 180 || o.Genre != "pop" || o.Owner.Username != "ali" {
		t.Fatalf("onizleme = %+v", o)
	}

	yok, err := s.Preview(context.Background(), "Zz99Zz99")
	if err != nil || yok.PreviewURL != nil {
		t.Fatalf("onizlemesiz yayin = %+v, %v", yok, err)
	}

	dep.hata = errors.New("imza hatasi")
	if _, err := s.Preview(context.Background(), "Ab3dEf9h"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("imza hatasi sunucu hatasi olmali: %v", err)
	}
}

func TestServis_OnizlemeR2siz(t *testing.T) {
	s, _ := testServisi(onizlemeDeposu())
	o, err := s.Preview(context.Background(), "Ab3dEf9h")
	if err != nil || o.PreviewURL == nil || !strings.HasSuffix(*o.PreviewURL, "/sosyal/"+yayinX+"/onizleme.mp3") {
		t.Fatalf("R2'siz adres = %+v, %v", o, err)
	}
}

func TestServis_OnizlemeBulunamadi(t *testing.T) {
	s, _ := testServisi(onizlemeDeposu())
	if _, err := s.Preview(context.Background(), "Yok12345"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bilinmeyen slug: %v, beklenen ErrBulunamadi", err)
	}

	// Gecersiz slug sorgusuz 404'tur: depo her cagride baska bir hata
	// doner, yani bir cagri sonucu ErrBulunamadi'dan ayirirdi.
	d := onizlemeDeposu()
	d.hata = errors.New("depo cagrilmamali")
	s, _ = testServisi(d)
	for _, slug := range []string{"kisa", "Ab3dEf9h/", "../../xx", ""} {
		if _, err := s.Preview(context.Background(), slug); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v, beklenen ErrBulunamadi (depo cagrilmadan)", slug, err)
		}
	}
}
