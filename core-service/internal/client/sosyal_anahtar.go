package client

import (
	"fmt"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// sosyalOnek, yayin kopyalarinin koku. Bir kullaniciya degil bir yayina
// aittir: sahiplik anahtarda degil, sosyal.yayin satirindadir. Bu yuzden
// sahipliTurler'de YOKTUR ve /api/files/sign bu anahtarlari imzalamaz;
// imzali adresler yalnizca gorunurluk kuralini uygulayan sosyal uclardan
// verilir.
const sosyalOnek = "sosyal"

// yayinKimligiGecerli, yayin kimliginin tek bir anahtar segmenti olabildigini
// dogrular. Nokta iceremez: bir dosya adi degil, bir kimliktir.
func yayinKimligiGecerli(yayinID string) bool {
	return segmentDeseni.MatchString(yayinID) && !strings.Contains(yayinID, ".")
}

// SosyalAnahtari, "sosyal/<yayinId>/<parcalar...>" anahtarini kurar. Her
// parca KullaniciAnahtari ile ayni segment kuralindan gecer; "/" ya da
// ".." tasiyan bir parca reddedilir. Duzen:
//
//	sosyal/{yayinId}/master.{ext}
//	sosyal/{yayinId}/onizleme.mp3
//	sosyal/{yayinId}/stems/{stemId}.{ext}
func SosyalAnahtari(yayinID string, parcalar ...string) (string, error) {
	if !yayinKimligiGecerli(yayinID) {
		return "", ErrGecersizSegment
	}
	if len(parcalar) == 0 {
		return "", fmt.Errorf("%w: dosya adi yok", ErrGecersizSegment)
	}
	for _, p := range parcalar {
		if !segmentDeseni.MatchString(p) {
			return "", ErrGecersizSegment
		}
	}
	return sosyalOnek + "/" + yayinID + "/" + strings.Join(parcalar, "/"), nil
}

// SosyalOneki, bir yayinin tum dosyalarini kapsayan "sosyal/<yayinId>/"
// onekidir. DeletePrefix'in uc parca kuralina uyar.
func SosyalOneki(yayinID string) (string, error) {
	if !yayinKimligiGecerli(yayinID) {
		return "", ErrGecersizSegment
	}
	return sosyalOnek + "/" + yayinID + "/", nil
}

// SahipliAnahtarVeyaURLCoz, SahipliAnahtarCoz'un ciplak anahtari da kabul
// eden halidir. Render ve master sonuclarinin fileUrl'i tam adres ya da
// (mock/dev yollarinda ve eski kayitlarda) ciplak anahtar olabilir;
// KeyFromURL konaksiz bir dizgeyi ErrGecersizURL ile reddeder.
//
// Ciplak anahtar "<tur>/<userId>/..." biciminde bilinen bir turle
// baslamiyorsa ErrGecersizURL, baskasinin onekindeyse ErrYabanciAnahtar
// doner.
func SahipliAnahtarVeyaURLCoz(ham, userID string, cfg *config.R2Config) (string, error) {
	if strings.Contains(ham, "://") {
		return SahipliAnahtarCoz(ham, userID, cfg)
	}
	parcalar := strings.Split(ham, "/")
	if len(parcalar) < 3 || !sahipliTurler[parcalar[0]] {
		return "", fmt.Errorf("%w: anahtar ya da adres degil", ErrGecersizURL)
	}
	if err := AnahtarSahibiDogrula(ham, userID); err != nil {
		return "", err
	}
	return ham, nil
}
