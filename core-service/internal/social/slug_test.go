package social

import "testing"

func TestGenerateSlug_FormatAndVariety(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := generateSlug()
		if err != nil {
			t.Fatalf("generateSlug: %v", err)
		}
		if !ValidSlug(s) {
			t.Fatalf("invalid slug %q", s)
		}
		seen[s] = true
	}
	if len(seen) < 199 {
		t.Fatalf("slugs repeat: %d distinct", len(seen))
	}
}

func TestValidSlug(t *testing.T) {
	for _, s := range []string{"", "abc", "abcdefg!", "abcdefghi", "abcd-efg"} {
		if ValidSlug(s) {
			t.Errorf("%q must be invalid", s)
		}
	}
	if !ValidSlug("Ab3dEf9h") {
		t.Error("Ab3dEf9h must be valid")
	}
}
