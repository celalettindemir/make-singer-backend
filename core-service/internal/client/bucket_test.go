package client

import (
	"context"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func testCfg() *config.R2Config {
	return &config.R2Config{
		AccountID:     "hesap123",
		PublicBucket:  "makeasinger-public",
		PrivateBucket: "makeasinger-private",
		PublicURL:     "https://makesinger-cdn.celalettindemir.dev",
	}
}

func TestBucketFor(t *testing.T) {
	cfg := testCfg()
	durumlar := []struct {
		anahtar  string
		beklenen string
	}{
		{"exports/abc.mp3", "makeasinger-public"},
		{"exports/abc.wav", "makeasinger-public"},
		{"exports/abc.zip", "makeasinger-public"},
		{"vocals/p1/s1/t1.wav", "makeasinger-private"},
		{"masters/p1/m1.wav", "makeasinger-private"},
		{"stems/p1/s1.wav", "makeasinger-private"},
		{"bilinmeyen/x.bin", "makeasinger-private"},
		{"", "makeasinger-private"},
		{"exportsabc.mp3", "makeasinger-private"},
	}
	for _, d := range durumlar {
		if got := BucketFor(d.anahtar, cfg); got != d.beklenen {
			t.Errorf("BucketFor(%q) = %q, beklenen %q", d.anahtar, got, d.beklenen)
		}
	}
}

func TestKeyFromURL(t *testing.T) {
	cfg := testCfg()
	durumlar := []struct {
		ad       string
		url      string
		beklenen string
	}{
		{"cdn adresi", "https://makesinger-cdn.celalettindemir.dev/exports/a.mp3", "exports/a.mp3"},
		{"r2 endpoint", "https://makeasinger-private.r2.cloudflarestorage.com/vocals/p/s/t.wav", "vocals/p/s/t.wav"},
		{"presigned", "https://makeasinger-private.r2.cloudflarestorage.com/masters/p/m.wav?X-Amz-Signature=abc&X-Amz-Expires=3600", "masters/p/m.wav"},
		// AWS SDK presigner'in gercekte urettigi bicim: bucket VE hesap
		// kimligi konakta. Elle yazilmis "bucket.r2..." testleri bunu
		// yakalamiyordu.
		{"hesap kapsamli konak", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/vocals/p/s/t.wav", "vocals/p/s/t.wav"},
		{"hesap kapsamli public", "https://makeasinger-public.hesap123.r2.cloudflarestorage.com/exports/a.mp3", "exports/a.mp3"},
	}
	for _, d := range durumlar {
		got, err := KeyFromURL(d.url, cfg)
		if err != nil {
			t.Errorf("%s: beklenmeyen hata: %v", d.ad, err)
			continue
		}
		if got != d.beklenen {
			t.Errorf("%s: KeyFromURL = %q, beklenen %q", d.ad, got, d.beklenen)
		}
	}
}

func TestKeyFromURL_TaninmayanKonak(t *testing.T) {
	cfg := testCfg()
	kotuler := []string{
		"https://kotu.example.com/vocals/p/s/t.wav",
		// Sonek/icerik eslesmesine kacilirsa bunlar kazara gecerdi:
		"https://kotu-makeasinger-private.r2.cloudflarestorage.com/vocals/a.wav",
		"https://makeasinger-private.r2.cloudflarestorage.com.kotu.com/vocals/a.wav",
		"https://makeasinger-private.baskahesap.r2.cloudflarestorage.com/vocals/a.wav",
		"https://makesinger-cdn.celalettindemir.dev.kotu.com/exports/a.mp3",
	}
	for _, u := range kotuler {
		if _, err := KeyFromURL(u, cfg); err == nil {
			t.Errorf("taninmayan konak icin hata bekleniyordu: %s", u)
		}
	}
}

// TestKeyFromURL_HataMesajindaImzaliURLYok, hata mesajlarinin Redis is
// kaydina ve WebSocket yayinina dustugunu varsayar: imza oraya sizmamali.
func TestKeyFromURL_HataMesajindaImzaliURLYok(t *testing.T) {
	cfg := testCfg()
	kotu := "https://kotu.example.com/vocals/a.wav?X-Amz-Signature=cokgizli"
	_, err := KeyFromURL(kotu, cfg)
	if err == nil {
		t.Fatal("hata bekleniyordu")
	}
	if strings.Contains(err.Error(), "cokgizli") || strings.Contains(err.Error(), "X-Amz-Signature") {
		t.Errorf("hata mesajinda imza var: %v", err)
	}
}

// TestKeyFromURL_GidisDonus, ASIL kural: kodun kendi urettigi adres, ayni
// kodla anahtara geri cozulebilmeli. Elle yazilmis URL dizeleri gercek
// presigner ciktisini temsil etmiyordu.
func TestKeyFromURL_GidisDonus(t *testing.T) {
	cfg := testR2Cfg()
	c, err := NewR2Client(cfg)
	if err != nil {
		t.Fatalf("NewR2Client: %v", err)
	}

	for _, anahtar := range []string{"exports/abc.mp3", "vocals/p1/s1/t1.wav", "masters/p1/m1.wav", "stems/p1/s1.wav"} {
		url, _, err := c.URLFor(context.Background(), anahtar)
		if err != nil {
			t.Fatalf("URLFor(%q): %v", anahtar, err)
		}
		got, err := KeyFromURL(url, cfg)
		if err != nil {
			t.Errorf("KeyFromURL(URLFor(%q)) hata verdi: %v", anahtar, err)
			continue
		}
		if got != anahtar {
			t.Errorf("gidis donus bozuk: %q -> %q", anahtar, got)
		}
	}
}

// TestUnsignedURL_GidisDonus, mock/dev yollarinin urettigi adresin de
// ayni kurallarla cozulebildigini dogrular.
func TestUnsignedURL_GidisDonus(t *testing.T) {
	cfg := testCfg()
	for _, anahtar := range []string{"exports/a.mp3", "masters/p/m.wav", "previews/p.mp3"} {
		got, err := KeyFromURL(UnsignedURL(anahtar, cfg), cfg)
		if err != nil {
			t.Errorf("UnsignedURL(%q) cozulemedi: %v", anahtar, err)
			continue
		}
		if got != anahtar {
			t.Errorf("gidis donus bozuk: %q -> %q", anahtar, got)
		}
	}
}

func TestKeyFromURL_BosVeBozuk(t *testing.T) {
	cfg := testCfg()
	for _, u := range []string{"", "://bozuk", "https://makesinger-cdn.celalettindemir.dev/"} {
		if _, err := KeyFromURL(u, cfg); err == nil {
			t.Errorf("%q icin hata bekleniyordu", u)
		}
	}
}
