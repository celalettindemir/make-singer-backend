package social

import "testing"

func TestSlugUret_BicimVeCesitlilik(t *testing.T) {
	gorulen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := generateSlug()
		if err != nil {
			t.Fatalf("slugUret: %v", err)
		}
		if !ValidSlug(s) {
			t.Fatalf("gecersiz slug %q", s)
		}
		gorulen[s] = true
	}
	if len(gorulen) < 199 {
		t.Fatalf("slug'lar tekrar ediyor: %d farkli", len(gorulen))
	}
}

func TestSlugGecerli(t *testing.T) {
	for _, s := range []string{"", "abc", "abcdefg!", "abcdefghi", "abcd-efg"} {
		if ValidSlug(s) {
			t.Errorf("%q gecersiz olmali", s)
		}
	}
	if !ValidSlug("Ab3dEf9h") {
		t.Error("Ab3dEf9h gecerli olmali")
	}
}
