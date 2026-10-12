package client

import (
	"context"
	"errors"
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
	cases := []struct {
		key  string
		want string
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
	for _, tc := range cases {
		if got := BucketFor(tc.key, cfg); got != tc.want {
			t.Errorf("BucketFor(%q) = %q, beklenen %q", tc.key, got, tc.want)
		}
	}
}

func TestKeyFromURL(t *testing.T) {
	cfg := testCfg()
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"cdn adresi", "https://makesinger-cdn.celalettindemir.dev/exports/a.mp3", "exports/a.mp3"},
		{"r2 endpoint", "https://makeasinger-private.r2.cloudflarestorage.com/vocals/p/s/t.wav", "vocals/p/s/t.wav"},
		{"presigned", "https://makeasinger-private.r2.cloudflarestorage.com/masters/p/m.wav?X-Amz-Signature=abc&X-Amz-Expires=3600", "masters/p/m.wav"},
		// The form the AWS SDK presigner really produces: bucket AND account id in
		// the host. Hand-written "bucket.r2..." tests did not catch this.
		{"hesap kapsamli konak", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/vocals/p/s/t.wav", "vocals/p/s/t.wav"},
		{"hesap kapsamli public", "https://makeasinger-public.hesap123.r2.cloudflarestorage.com/exports/a.mp3", "exports/a.mp3"},
	}
	for _, tc := range cases {
		got, err := KeyFromURL(tc.url, cfg)
		if err != nil {
			t.Errorf("%s: beklenmeyen hata: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: KeyFromURL = %q, beklenen %q", tc.name, got, tc.want)
		}
	}
}

func TestKeyFromURL_UnknownHost(t *testing.T) {
	cfg := testCfg()
	badHosts := []string{
		"https://kotu.example.com/vocals/p/s/t.wav",
		// If it fell back to suffix/substring matching these would pass by accident:
		"https://kotu-makeasinger-private.r2.cloudflarestorage.com/vocals/a.wav",
		"https://makeasinger-private.r2.cloudflarestorage.com.kotu.com/vocals/a.wav",
		"https://makeasinger-private.baskahesap.r2.cloudflarestorage.com/vocals/a.wav",
		"https://makesinger-cdn.celalettindemir.dev.kotu.com/exports/a.mp3",
	}
	for _, u := range badHosts {
		if _, err := KeyFromURL(u, cfg); err == nil {
			t.Errorf("taninmayan konak icin hata bekleniyordu: %s", u)
		}
	}
}

// TestKeyFromURL_ErrorOmitsSignedURL assumes error messages land in the Redis
// job record and the WebSocket broadcast: the signature must not leak there.
func TestKeyFromURL_ErrorOmitsSignedURL(t *testing.T) {
	cfg := testCfg()
	badURL := "https://kotu.example.com/vocals/a.wav?X-Amz-Signature=cokgizli"
	_, err := KeyFromURL(badURL, cfg)
	if err == nil {
		t.Fatal("hata bekleniyordu")
	}
	if strings.Contains(err.Error(), "cokgizli") || strings.Contains(err.Error(), "X-Amz-Signature") {
		t.Errorf("hata mesajinda imza var: %v", err)
	}
}

// TestKeyFromURL_RoundTrip is the REAL rule: a URL the code itself produces
// must resolve back to the key with the same code. Hand-written URL strings
// did not represent real presigner output.
func TestKeyFromURL_RoundTrip(t *testing.T) {
	cfg := testR2Cfg()
	c, err := NewR2Client(cfg)
	if err != nil {
		t.Fatalf("NewR2Client: %v", err)
	}

	for _, key := range []string{"exports/abc.mp3", "vocals/p1/s1/t1.wav", "masters/p1/m1.wav", "stems/p1/s1.wav"} {
		url, _, err := c.URLFor(context.Background(), key)
		if err != nil {
			t.Fatalf("URLFor(%q): %v", key, err)
		}
		got, err := KeyFromURL(url, cfg)
		if err != nil {
			t.Errorf("KeyFromURL(URLFor(%q)) hata verdi: %v", key, err)
			continue
		}
		if got != key {
			t.Errorf("gidis donus bozuk: %q -> %q", key, got)
		}
	}
}

// TestUnsignedURL_RoundTrip verifies that the URL produced by the mock/dev
// paths resolves under the same rules too.
func TestUnsignedURL_RoundTrip(t *testing.T) {
	cfg := testCfg()
	for _, key := range []string{"exports/a.mp3", "masters/p/m.wav", "previews/p.mp3"} {
		got, err := KeyFromURL(UnsignedURL(key, cfg), cfg)
		if err != nil {
			t.Errorf("UnsignedURL(%q) cozulemedi: %v", key, err)
			continue
		}
		if got != key {
			t.Errorf("gidis donus bozuk: %q -> %q", key, got)
		}
	}
}

func TestKeyFromURL_EmptyAndMalformed(t *testing.T) {
	cfg := testCfg()
	for _, u := range []string{"", "://bozuk", "https://makesinger-cdn.celalettindemir.dev/"} {
		if _, err := KeyFromURL(u, cfg); err == nil {
			t.Errorf("%q icin hata bekleniyordu", u)
		}
	}
}

func TestKeyFromURL_ErrorsWrapErrInvalidURL(t *testing.T) {
	cfg := testCfg()
	for _, u := range []string{"", "://bozuk", "/sadece/yol", "https://cdn.example.com/stems/a.mp3", "https://makesinger-cdn.celalettindemir.dev/"} {
		_, err := KeyFromURL(u, cfg)
		if !errors.Is(err, ErrInvalidURL) {
			t.Errorf("%q: ErrGecersizURL bekleniyordu, geldi: %v", u, err)
		}
	}
}
