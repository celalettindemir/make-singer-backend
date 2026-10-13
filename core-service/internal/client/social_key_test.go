package client

import (
	"errors"
	"testing"
)

func TestSocialKey_Layout(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"master.wav"}, "sosyal/y1/master.wav"},
		{[]string{"onizleme.mp3"}, "sosyal/y1/onizleme.mp3"},
		{[]string{"stems", "s1.mp3"}, "sosyal/y1/stems/s1.mp3"},
	}
	for _, tc := range cases {
		got, err := SocialKey("y1", tc.parts...)
		if err != nil || got != tc.want {
			t.Errorf("SocialKey(%v) = %q, %v; want %q", tc.parts, got, err, tc.want)
		}
	}
}

func TestSocialKey_RejectsInvalidSegment(t *testing.T) {
	cases := []struct {
		pubID string
		parts []string
	}{
		{"", []string{"master.wav"}},
		{"y.1", []string{"master.wav"}},
		{"../y", []string{"master.wav"}},
		{"y1", nil},
		{"y1", []string{"../x"}},
		{"y1", []string{"stems", "a/b.mp3"}},
		{"y1", []string{".."}},
	}
	for _, tc := range cases {
		if _, err := SocialKey(tc.pubID, tc.parts...); !errors.Is(err, ErrInvalidSegment) {
			t.Errorf("SocialKey(%q, %v) should have been rejected, err=%v", tc.pubID, tc.parts, err)
		}
	}
}

func TestSocialPrefix_ThreeSegments(t *testing.T) {
	got, err := SocialPrefix("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")
	if err != nil || got != "sosyal/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/" {
		t.Fatalf("SosyalOneki = %q, %v", got, err)
	}
	if _, err := SocialPrefix(""); err == nil {
		t.Fatal("an empty publication ID must not produce a prefix")
	}
}

// sosyal/ keys are not user-owned: files/sign does not sign them.
func TestSocialKey_NotUserOwned(t *testing.T) {
	if err := VerifyKeyOwner("sosyal/u1/master.wav", "u1"); !errors.Is(err, ErrKeyNotOwned) {
		t.Fatalf("a social key must not be counted as owned, err=%v", err)
	}
}

func TestResolveOwnedKeyOrURL(t *testing.T) {
	cfg := testR2Cfg()
	cases := []struct {
		name, raw, want string
		wantErr         error
	}{
		{"full URL", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/u1/p1/m.wav?X-Amz-Signature=x", "masters/u1/p1/m.wav", nil},
		{"bare key", "masters/u1/p1/m.wav", "masters/u1/p1/m.wav", nil},
		{"bare stem", "stems/u1/p1/s1.mp3", "stems/u1/p1/s1.mp3", nil},
		{"foreign bare key", "masters/u2/p1/m.wav", "", ErrKeyNotOwned},
		{"foreign URL", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/u2/p1/m.wav", "", ErrKeyNotOwned},
		{"unknown kind", "sosyal/y1/master.wav", "", ErrInvalidURL},
		{"short", "masters/u1", "", ErrInvalidURL},
		{"empty", "", "", ErrInvalidURL},
		{"traversal", "masters/u1/../u2/m.wav", "", ErrKeyNotOwned},
		{"foreign host", "https://kotu.example.com/masters/u1/p1/m.wav", "", ErrInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveOwnedKeyOrURL(tc.raw, "u1", cfg)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("= %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
