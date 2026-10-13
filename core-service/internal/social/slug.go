package social

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
)

const (
	slugAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	slugLen      = 8
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

// errSlugConflict is the store layer's slug UNIQUE violation; the service
// retries with a new slug. It never leaks outside.
var errSlugConflict = errors.New("slug cakismasi")

// ValidSlug reports whether the slug is 8 characters of [A-Za-z0-9].
func ValidSlug(slug string) bool { return slugPattern.MatchString(slug) }

// generateSlug generates an 8-character slug with crypto/rand (62^8 ~ 2.2e14).
// rand.Int gives a uniform distribution; there is no modulo bias.
func generateSlug() (string, error) {
	buf := make([]byte, slugLen)
	bound := big.NewInt(int64(len(slugAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, bound)
		if err != nil {
			return "", err
		}
		buf[i] = slugAlphabet[n.Int64()]
	}
	return string(buf), nil
}
