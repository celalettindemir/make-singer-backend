package sosyal

import (
	"errors"
	"strings"
	"testing"
)

func TestKullaniciAdiGecerli(t *testing.T) {
	gecerli := []string{"abc", "ali_veli", "a.b.c", "x123", "ab_", "_ab", "abcdefghijklmnopqrst"}
	gecersiz := []string{"", "ab", ".abc", "abc.", "Abc", "ali veli", "ali-veli", "abcdefghijklmnopqrstu", "çağrı"}
	for _, ad := range gecerli {
		if !KullaniciAdiGecerli(ad) {
			t.Errorf("%q gecerli olmaliydi", ad)
		}
	}
	for _, ad := range gecersiz {
		if KullaniciAdiGecerli(ad) {
			t.Errorf("%q gecersiz olmaliydi", ad)
		}
	}
}

func TestAvatarRengi_SabitVeAralikta(t *testing.T) {
	kimlik := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	ilk := AvatarRengi(kimlik)
	if ilk < 0 || ilk > 7 {
		t.Fatalf("renk araligin disinda: %d", ilk)
	}
	if AvatarRengi(kimlik) != ilk {
		t.Fatal("ayni kimlik ayni rengi vermeli")
	}
	// fnv32a("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a") % 8 = 2, bagimsiz
	// hesaplanan sabit: formul degisirse herkesin rengi degisir.
	if ilk != 2 {
		t.Fatalf("renk = %d, beklenen 2", ilk)
	}
}

func TestAdAdaylari_SiraVeUzunluk(t *testing.T) {
	adaylar := adAdaylari("ali")
	if len(adaylar) != 98 || adaylar[0] != "ali2" || adaylar[97] != "ali99" {
		t.Fatalf("adaylar = %v ... (%d)", adaylar[:2], len(adaylar))
	}
	uzun := strings.Repeat("a", 20)
	for _, a := range adAdaylari(uzun) {
		if len(a) > 20 || !KullaniciAdiGecerli(a) {
			t.Fatalf("gecersiz aday %q", a)
		}
	}
	if got := adAdaylari(uzun)[0]; got != strings.Repeat("a", 19)+"2" {
		t.Fatalf("kirpilmis ilk aday = %q", got)
	}
	if got := adAdaylari(uzun)[8]; got != strings.Repeat("a", 18)+"10" {
		t.Fatalf("iki haneli ek = %q", got)
	}
}

func TestProfilGirdisiniDuzenle(t *testing.T) {
	g, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "  Ali  ", Bio: " merhaba "})
	if err != nil || g.GorunenAd != "Ali" || g.Bio != "merhaba" {
		t.Fatalf("g = %+v, err = %v", g, err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "A", GorunenAd: "x"}); !errors.Is(err, ErrGecersizKullaniciAdi) {
		t.Fatalf("gecersiz ad: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "   "}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bos gorunen ad: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: strings.Repeat("ş", 41)}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("41 karakter gorunen ad: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: strings.Repeat("ş", 40), Bio: strings.Repeat("ğ", 160)}); err != nil {
		t.Fatalf("sinirdaki rune sayilari kabul edilmeli: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "a", Bio: strings.Repeat("ğ", 161)}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("161 karakter bio: %v", err)
	}
}

func TestKimlikCoz(t *testing.T) {
	if got, ok := kimlikCoz("6F1C2A9E-1B2C-4D3E-8F9A-0B1C2D3E4F5A"); !ok || got != "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a" {
		t.Fatalf("kimlikCoz = %q, %v", got, ok)
	}
	if _, ok := kimlikCoz("x"); ok {
		t.Fatal("gecersiz kimlik kabul edilmemeli")
	}
}

func TestProfilGirdisiniDuzenle_NULKarakteriReddedilir(t *testing.T) {
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "A\x00li"}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("NUL iceren gorunen ad: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "Ali", Bio: "mer\x00haba"}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("NUL iceren bio: %v", err)
	}
}
