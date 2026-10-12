package sosyal

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// kullaniciAdiDeseni, spec §3: kucuk harf, rakam, alt cizgi, nokta;
// 3-20 karakter. Bas ve sondaki nokta ayrica reddedilir.
var kullaniciAdiDeseni = regexp.MustCompile(`^[a-z0-9_.]{3,20}$`)

const (
	kullaniciAdiEnUzun = 20
	gorunenAdEnUzun    = 40
	bioEnUzun          = 160
	avatarPaletBoyu    = 8
)

// KullaniciAdiGecerli, adin sozlesme kuralina uyup uymadigini soyler.
func KullaniciAdiGecerli(ad string) bool {
	return kullaniciAdiDeseni.MatchString(ad) &&
		!strings.HasPrefix(ad, ".") && !strings.HasSuffix(ad, ".")
}

// AvatarRengi, kullanici kimliginden 0-7 arasi sabit bir palet sirasi
// turetir: fnv32a(kullanici_id) % 8. Saklanmaz; ayni kimlik her zaman
// ayni rengi verir.
func AvatarRengi(kullaniciID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(kullaniciID))
	return int(h.Sum32() % avatarPaletBoyu)
}

// adAdaylari, alinmis bir ad icin sirayla denenecek onerileri uretir:
// ad2, ad3, ... ad99. Sonuc 20 karakteri asacaksa taban kirpilir.
func adAdaylari(ad string) []string {
	adaylar := make([]string, 0, 98)
	for n := 2; n <= 99; n++ {
		ek := strconv.Itoa(n)
		taban := ad
		if len(taban)+len(ek) > kullaniciAdiEnUzun {
			taban = taban[:kullaniciAdiEnUzun-len(ek)]
		}
		aday := taban + ek
		if KullaniciAdiGecerli(aday) {
			adaylar = append(adaylar, aday)
		}
	}
	return adaylar
}

// profilGirdisiniDuzenle, gorunen adi ve bio'yu kirpar ve kurallari
// uygular. Kullanici adi KIRPILMAZ ve kucuk harfe cevrilmez: mobil ayni
// deseni uygular; sessiz duzeltme kullaniciya gosterilenden farkli bir
// ad kaydederdi.
func profilGirdisiniDuzenle(g ProfilGirdisi) (ProfilGirdisi, error) {
	if !KullaniciAdiGecerli(g.KullaniciAdi) {
		return g, ErrGecersizKullaniciAdi
	}
	g.GorunenAd = strings.TrimSpace(g.GorunenAd)
	g.Bio = strings.TrimSpace(g.Bio)
	if n := utf8.RuneCountInString(g.GorunenAd); n < 1 || n > gorunenAdEnUzun {
		return g, dogrulamaHatasi("gorunenAd 1-40 karakter olmali")
	}
	if utf8.RuneCountInString(g.Bio) > bioEnUzun {
		return g, dogrulamaHatasi("bio en cok 160 karakter olabilir")
	}
	return g, nil
}

// kimlikCoz, bir kullanici ya da yayin kimliginin uuid oldugunu dogrular
// ve kanonik bicimini doner.
func kimlikCoz(ham string) (string, bool) {
	u, err := uuid.Parse(ham)
	if err != nil {
		return "", false
	}
	return u.String(), true
}
