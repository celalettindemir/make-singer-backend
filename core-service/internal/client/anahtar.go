package client

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// ErrGecersizSegment: istemciden gelen bir kimlik anahtar segmenti olamaz.
// Mesaj degeri tasimaz; hata Redis is kaydina ve yanita dusebilir.
var ErrGecersizSegment = errors.New("gecersiz anahtar segmenti")

// ErrGecersizURL: istemcinin gonderdigi URL cozulemedi ya da taninmayan bir
// konaga ait. Uclar bunu 400'e cevirir. Mesaj URL degerini tasimaz.
var ErrGecersizURL = errors.New("gecersiz URL")

// ErrYabanciAnahtar: nesne, istegi yapan kullanicinin oneki disinda. Uclar
// bunu 404'e cevirir; 403 nesnenin var oldugunu dogrulardi.
var ErrYabanciAnahtar = errors.New("nesne bu kullaniciya ait degil")

// sahipliTurler: ilk segmenti bunlardan biri olan anahtarlarin ikinci
// segmenti her zaman sahibin userId'sidir.
var sahipliTurler = map[string]bool{
	"vocals":   true,
	"masters":  true,
	"stems":    true,
	"exports":  true,
	"previews": true,
}

// segmentDeseni: "/" ve ".." tasiyamaz, harf/rakamla baslar, tek bir
// uzanti noktasina izin verir.
var segmentDeseni = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}(\.[a-z0-9]{1,5})?$`)

// KullaniciAnahtari, "<tur>/<userId>/<parcalar...>" anahtarini kurar. Son
// parca uzantili dosya adidir. Sahiplik boylece yapisaldir: kimse kendi
// oneki disina yazamaz, kontrol edilecek ayri bir tablo yoktur.
func KullaniciAnahtari(tur, userID string, parcalar ...string) (string, error) {
	if !sahipliTurler[tur] {
		return "", fmt.Errorf("bilinmeyen anahtar turu: %s", tur)
	}
	if len(parcalar) == 0 {
		return "", fmt.Errorf("%w: dosya adi yok", ErrGecersizSegment)
	}
	hepsi := append([]string{userID}, parcalar...)
	for _, p := range hepsi {
		if !segmentDeseni.MatchString(p) {
			return "", ErrGecersizSegment
		}
	}
	return tur + "/" + strings.Join(hepsi, "/"), nil
}

// AnahtarSahibiDogrula, anahtarin userID'nin onekinde oldugunu dogrular.
// Eski (oneksiz) duzendeki anahtarlar da reddedilir.
func AnahtarSahibiDogrula(key, userID string) error {
	parcalar := strings.Split(key, "/")
	if userID == "" || len(parcalar) < 3 || !sahipliTurler[parcalar[0]] || parcalar[1] != userID {
		return ErrYabanciAnahtar
	}
	for _, p := range parcalar[1:] {
		if !segmentDeseni.MatchString(p) {
			return ErrYabanciAnahtar
		}
	}
	return nil
}

// SahipliAnahtarCoz, istemciden gelen URL'yi anahtara cevirir ve anahtarin
// istegi yapan kullaniciya ait oldugunu dogrular. Worker ve audio-service
// bu anahtari OKUDUGU icin denetimsiz bir URL baskasinin dosyasini okutur.
func SahipliAnahtarCoz(rawURL, userID string, cfg *config.R2Config) (string, error) {
	key, err := KeyFromURL(rawURL, cfg)
	if err != nil {
		return "", err
	}
	if err := AnahtarSahibiDogrula(key, userID); err != nil {
		return "", err
	}
	return key, nil
}

// KullaniciOnekleri, bir kullanicinin R2'deki tum sahipli oneklerini
// sabit sirayla dondurur. Hesap silme bu onekleri toptan temizler; bu
// yuzden id, KullaniciAnahtari ile ayni segment kuralindan gecer ve
// nokta iceremez (bir dosya adi degil, bir kimliktir). Bos ya da
// gecersiz id'de onek uretilmez: "vocals//" gibi bir onek baska
// kullanicilarin nesnelerini kapsardi.
func KullaniciOnekleri(userID string) ([]string, error) {
	if !segmentDeseni.MatchString(userID) || strings.Contains(userID, ".") {
		return nil, ErrGecersizSegment
	}
	turler := make([]string, 0, len(sahipliTurler))
	for tur := range sahipliTurler {
		turler = append(turler, tur)
	}
	slices.Sort(turler)
	onekler := make([]string, 0, len(turler))
	for _, tur := range turler {
		onekler = append(onekler, tur+"/"+userID+"/")
	}
	return onekler, nil
}
