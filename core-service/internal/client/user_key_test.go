package client

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func TestUserKey_Layout(t *testing.T) {
	got, err := UserKey("vocals", "kullanici-a", "p1", "s1", "t1.wav")
	if err != nil {
		t.Fatalf("KullaniciAnahtari: %v", err)
	}
	if got != "vocals/kullanici-a/p1/s1/t1.wav" {
		t.Errorf("anahtar = %q", got)
	}
}

func TestUserKey_RejectsInvalidSegment(t *testing.T) {
	cases := map[string][]string{
		"bos kullanici":   {"", "p1", "t.wav"},
		"egik cizgi":      {"kullanici-a", "p1/../b", "t.wav"},
		"iki nokta":       {"kullanici-a", "..", "t.wav"},
		"nokta ile basla": {"kullanici-a", ".gizli", "t.wav"},
		"bosluk":          {"kullanici-a", "p 1", "t.wav"},
		"cok uzun":        {"kullanici-a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "t.wav"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := UserKey("vocals", tc[0], tc[1:]...)
			if !errors.Is(err, ErrInvalidSegment) {
				t.Errorf("ErrGecersizSegment bekleniyordu, geldi: %v", err)
			}
		})
	}
}

func TestUserKey_UnknownKindOrMissingFileErrors(t *testing.T) {
	if _, err := UserKey("gizli", "kullanici-a", "t.wav"); err == nil {
		t.Error("bilinmeyen tur icin hata bekleniyordu")
	}
	if _, err := UserKey("vocals", "kullanici-a"); !errors.Is(err, ErrInvalidSegment) {
		t.Errorf("dosya adi yokken ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
}

func TestVerifyKeyOwner(t *testing.T) {
	cases := []struct {
		name, key, user string
		valid           bool
	}{
		{"kendi stem'i", "stems/kullanici-a/p1/s1.mp3", "kullanici-a", true},
		{"kendi export'u", "exports/kullanici-a/e1.zip", "kullanici-a", true},
		{"baskasinin stem'i", "stems/kullanici-b/p1/s1.mp3", "kullanici-a", false},
		{"eski duzen", "stems/p1/s1.mp3", "kullanici-a", false},
		{"bilinmeyen tur", "gizli/kullanici-a/x.wav", "kullanici-a", false},
		{"kisa anahtar", "stems/kullanici-a", "kullanici-a", false},
		{"bos kullanici", "stems//p1/s1.mp3", "", false},
		{"iki nokta segmenti", "stems/kullanici-a/../kullanici-b/s1.mp3", "kullanici-a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyKeyOwner(tc.key, tc.user)
			if tc.valid && err != nil {
				t.Errorf("gecerli olmaliydi: %v", err)
			}
			if !tc.valid && !errors.Is(err, ErrKeyNotOwned) {
				t.Errorf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
			}
		})
	}
}

func TestResolveOwnedKey(t *testing.T) {
	cfg := &config.R2Config{AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private"}
	own := UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg) + "?X-Amz-Signature=imza"

	key, err := ResolveOwnedKey(own, "kullanici-a", cfg)
	if err != nil || key != "stems/kullanici-a/p1/s1.mp3" {
		t.Fatalf("kendi anahtari cozulemedi: %q, %v", key, err)
	}
	if _, err := ResolveOwnedKey(own, "kullanici-b", cfg); !errors.Is(err, ErrKeyNotOwned) {
		t.Errorf("baska kullanici icin ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if _, err := ResolveOwnedKey("https://kotu.example.com/stems/kullanici-a/p1/s1.mp3", "kullanici-a", cfg); err == nil || errors.Is(err, ErrKeyNotOwned) {
		t.Errorf("taninmayan konak KeyFromURL hatasi vermeli, geldi: %v", err)
	}
}

func TestUserPrefixes(t *testing.T) {
	got, err := UserPrefixes("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	want := []string{
		"exports/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"masters/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"previews/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"stems/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"vocals/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("onekler = %v, beklenen %v", got, want)
	}
}

func TestUserPrefixes_InvalidID(t *testing.T) {
	for _, id := range []string{"", "..", "a/b", "x.wav", strings.Repeat("a", 65)} {
		if _, err := UserPrefixes(id); !errors.Is(err, ErrInvalidSegment) {
			t.Errorf("%q icin ErrGecersizSegment bekleniyordu, gelen %v", id, err)
		}
	}
}
