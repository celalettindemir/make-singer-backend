package identity

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// providerApple, providerGoogle: constants used in the Upstream.Name field.
// They are used instead of bare strings so that a typo in the provider name
// can be caught at compile time (at least in a single place of definition).
const (
	providerApple  = "apple"
	providerGoogle = "google"
)

// appleClientSecretTTL: K5. Kept short because it is regenerated on every
// exchange; Apple puts a 6-month upper limit.
const appleClientSecretTTL = 5 * time.Minute

// appleClientSecret produces the ES256 JWT that Apple expects as the
// client_secret. now is a parameter so that tests can pin the time.
func appleClientSecret(teamID, keyID, clientID, p8PEM string, now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(p8PEM))
	if block == nil {
		return "", fmt.Errorf("apple .p8 key could not be parsed as PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("could not read the apple .p8 key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("apple key is not ECDSA (%T); cannot sign with ES256", parsed)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": teamID,
		"iat": now.Unix(),
		"exp": now.Add(appleClientSecretTTL).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": clientID,
	})
	token.Header["kid"] = keyID
	return token.SignedString(key)
}
