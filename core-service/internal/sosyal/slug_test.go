package sosyal

import "testing"

func TestSlugUret_BicimVeCesitlilik(t *testing.T) {
	gorulen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := slugUret()
		if err != nil {
			t.Fatalf("slugUret: %v", err)
		}
		if !SlugGecerli(s) {
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
		if SlugGecerli(s) {
			t.Errorf("%q gecersiz olmali", s)
		}
	}
	if !SlugGecerli("Ab3dEf9h") {
		t.Error("Ab3dEf9h gecerli olmali")
	}
}
