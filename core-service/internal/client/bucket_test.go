package client

import (
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func testCfg() *config.R2Config {
	return &config.R2Config{
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
	if _, err := KeyFromURL("https://kotu.example.com/vocals/p/s/t.wav", cfg); err == nil {
		t.Error("taninmayan konak icin hata bekleniyordu, nil geldi")
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
