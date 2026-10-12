package client

import (
	"errors"
	"testing"
)

func TestSosyalAnahtari_Duzen(t *testing.T) {
	durumlar := []struct {
		parcalar []string
		beklenen string
	}{
		{[]string{"master.wav"}, "sosyal/y1/master.wav"},
		{[]string{"onizleme.mp3"}, "sosyal/y1/onizleme.mp3"},
		{[]string{"stems", "s1.mp3"}, "sosyal/y1/stems/s1.mp3"},
	}
	for _, d := range durumlar {
		got, err := SosyalAnahtari("y1", d.parcalar...)
		if err != nil || got != d.beklenen {
			t.Errorf("SosyalAnahtari(%v) = %q, %v; beklenen %q", d.parcalar, got, err, d.beklenen)
		}
	}
}

func TestSosyalAnahtari_GecersizSegmentReddedilir(t *testing.T) {
	durumlar := []struct {
		yayin    string
		parcalar []string
	}{
		{"", []string{"master.wav"}},
		{"y.1", []string{"master.wav"}},
		{"../y", []string{"master.wav"}},
		{"y1", nil},
		{"y1", []string{"../x"}},
		{"y1", []string{"stems", "a/b.mp3"}},
		{"y1", []string{".."}},
	}
	for _, d := range durumlar {
		if _, err := SosyalAnahtari(d.yayin, d.parcalar...); !errors.Is(err, ErrGecersizSegment) {
			t.Errorf("SosyalAnahtari(%q, %v) reddedilmeliydi, err=%v", d.yayin, d.parcalar, err)
		}
	}
}

func TestSosyalOneki_UcParca(t *testing.T) {
	got, err := SosyalOneki("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")
	if err != nil || got != "sosyal/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/" {
		t.Fatalf("SosyalOneki = %q, %v", got, err)
	}
	if _, err := SosyalOneki(""); err == nil {
		t.Fatal("bos yayin kimligi onek uretmemeli")
	}
}

// sosyal/ anahtarlari kullanici sahipli degildir: files/sign imzalamaz.
func TestSosyalAnahtari_KullaniciSahipliDegil(t *testing.T) {
	if err := AnahtarSahibiDogrula("sosyal/u1/master.wav", "u1"); !errors.Is(err, ErrYabanciAnahtar) {
		t.Fatalf("sosyal anahtari sahipli sayilmamali, err=%v", err)
	}
}

func TestSahipliAnahtarVeyaURLCoz(t *testing.T) {
	cfg := testR2Cfg()
	durumlar := []struct {
		ad, ham, beklenen string
		hata              error
	}{
		{"tam adres", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/u1/p1/m.wav?X-Amz-Signature=x", "masters/u1/p1/m.wav", nil},
		{"ciplak anahtar", "masters/u1/p1/m.wav", "masters/u1/p1/m.wav", nil},
		{"ciplak stem", "stems/u1/p1/s1.mp3", "stems/u1/p1/s1.mp3", nil},
		{"yabanci ciplak", "masters/u2/p1/m.wav", "", ErrYabanciAnahtar},
		{"yabanci adres", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/u2/p1/m.wav", "", ErrYabanciAnahtar},
		{"bilinmeyen tur", "sosyal/y1/master.wav", "", ErrGecersizURL},
		{"kisa", "masters/u1", "", ErrGecersizURL},
		{"bos", "", "", ErrGecersizURL},
		{"tirmanma", "masters/u1/../u2/m.wav", "", ErrYabanciAnahtar},
		{"yabanci konak", "https://kotu.example.com/masters/u1/p1/m.wav", "", ErrGecersizURL},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			got, err := SahipliAnahtarVeyaURLCoz(d.ham, "u1", cfg)
			if d.hata != nil {
				if !errors.Is(err, d.hata) {
					t.Fatalf("hata = %v, beklenen %v", err, d.hata)
				}
				return
			}
			if err != nil || got != d.beklenen {
				t.Fatalf("= %q, %v; beklenen %q", got, err, d.beklenen)
			}
		})
	}
}
