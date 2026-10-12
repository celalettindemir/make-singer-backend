package sosyal

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
)

const (
	slugAlfabesi = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	slugBoyu     = 8
)

var slugDeseni = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

// errSlugCakisti, depo katmaninin slug UNIQUE ihlalidir; servis yeni slug
// ile yeniden dener. Disari sizmaz.
var errSlugCakisti = errors.New("slug cakismasi")

// SlugGecerli, slug'in 8 karakter [A-Za-z0-9] oldugunu soyler.
func SlugGecerli(slug string) bool { return slugDeseni.MatchString(slug) }

// slugUret, crypto/rand ile 8 karakterlik slug uretir (62^8 ~ 2.2e14).
// rand.Int tekdüze dagilim verir; modulo yanliligi yoktur.
func slugUret() (string, error) {
	b := make([]byte, slugBoyu)
	sinir := big.NewInt(int64(len(slugAlfabesi)))
	for i := range b {
		n, err := rand.Int(rand.Reader, sinir)
		if err != nil {
			return "", err
		}
		b[i] = slugAlfabesi[n.Int64()]
	}
	return string(b), nil
}
