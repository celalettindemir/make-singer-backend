package social

import (
	"errors"
	"strings"
	"testing"
)

func TestValidUsername(t *testing.T) {
	valid := []string{"abc", "ali_veli", "a.b.c", "x123", "ab_", "_ab", "abcdefghijklmnopqrst"}
	invalid := []string{"", "ab", ".abc", "abc.", "Abc", "ali veli", "ali-veli", "abcdefghijklmnopqrstu", "çağrı"}
	for _, username := range valid {
		if !ValidUsername(username) {
			t.Errorf("%q gecerli olmaliydi", username)
		}
	}
	for _, username := range invalid {
		if ValidUsername(username) {
			t.Errorf("%q gecersiz olmaliydi", username)
		}
	}
}

func TestAvatarColor_StableAndInRange(t *testing.T) {
	userID := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	first := AvatarColor(userID)
	if first < 0 || first > 7 {
		t.Fatalf("renk araligin disinda: %d", first)
	}
	if AvatarColor(userID) != first {
		t.Fatal("ayni kimlik ayni rengi vermeli")
	}
	// fnv32a("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a") % 8 = 2, an independently
	// computed constant: if the formula changes, everyone's color changes.
	if first != 2 {
		t.Fatalf("renk = %d, beklenen 2", first)
	}
}

func TestUsernameCandidates_OrderAndLength(t *testing.T) {
	candidates := usernameCandidates("ali")
	if len(candidates) != 98 || candidates[0] != "ali2" || candidates[97] != "ali99" {
		t.Fatalf("adaylar = %v ... (%d)", candidates[:2], len(candidates))
	}
	long := strings.Repeat("a", 20)
	for _, a := range usernameCandidates(long) {
		if len(a) > 20 || !ValidUsername(a) {
			t.Fatalf("gecersiz aday %q", a)
		}
	}
	if got := usernameCandidates(long)[0]; got != strings.Repeat("a", 19)+"2" {
		t.Fatalf("kirpilmis ilk aday = %q", got)
	}
	if got := usernameCandidates(long)[8]; got != strings.Repeat("a", 18)+"10" {
		t.Fatalf("iki haneli ek = %q", got)
	}
}

func TestNormalizeProfileInput(t *testing.T) {
	norm, err := normalizeProfileInput(ProfileInput{Username: "ali", DisplayName: "  Ali  ", Bio: " merhaba "})
	if err != nil || norm.DisplayName != "Ali" || norm.Bio != "merhaba" {
		t.Fatalf("g = %+v, err = %v", norm, err)
	}
	if _, err := normalizeProfileInput(ProfileInput{Username: "A", DisplayName: "x"}); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("gecersiz ad: %v", err)
	}
	if _, err := normalizeProfileInput(ProfileInput{Username: "ali", DisplayName: "   "}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bos gorunen ad: %v", err)
	}
	if _, err := normalizeProfileInput(ProfileInput{Username: "ali", DisplayName: strings.Repeat("ş", 41)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("41 karakter gorunen ad: %v", err)
	}
	if _, err := normalizeProfileInput(ProfileInput{Username: "ali", DisplayName: strings.Repeat("ş", 40), Bio: strings.Repeat("ğ", 160)}); err != nil {
		t.Fatalf("sinirdaki rune sayilari kabul edilmeli: %v", err)
	}
	if _, err := normalizeProfileInput(ProfileInput{Username: "ali", DisplayName: "a", Bio: strings.Repeat("ğ", 161)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("161 karakter bio: %v", err)
	}
}

func TestParseID(t *testing.T) {
	if got, ok := parseID("6F1C2A9E-1B2C-4D3E-8F9A-0B1C2D3E4F5A"); !ok || got != "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a" {
		t.Fatalf("kimlikCoz = %q, %v", got, ok)
	}
	if _, ok := parseID("x"); ok {
		t.Fatal("gecersiz kimlik kabul edilmemeli")
	}
}

func TestNormalizeProfileInput_RejectsNUL(t *testing.T) {
	if _, err := normalizeProfileInput(ProfileInput{Username: "ali", DisplayName: "A\x00li"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("NUL iceren gorunen ad: %v", err)
	}
	if _, err := normalizeProfileInput(ProfileInput{Username: "ali", DisplayName: "Ali", Bio: "mer\x00haba"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("NUL iceren bio: %v", err)
	}
}
