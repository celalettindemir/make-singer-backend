package identity

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// SigningKey carries the RS256 signing key and satisfies both the
// SigningKey and the Key interfaces of the op package.
type SigningKey struct {
	private *rsa.PrivateKey
	kid     string
}

// LoadSigningKey reads a PEM-encoded RSA private key. Both PKCS#1 and
// PKCS#8 are accepted; which format the key was generated in depends on
// the operations side.
func LoadSigningKey(pemText string) (*SigningKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil || len(block.Bytes) == 0 {
		return nil, fmt.Errorf("gecerli bir PEM blogu bulunamadi")
	}
	var private *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		private = k
	} else {
		anyKey, err8 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err8 != nil {
			return nil, fmt.Errorf("RSA ozel anahtari cozulemedi (PKCS#1: %v, PKCS#8: %w)", err, err8)
		}
		rsaKey, ok := anyKey.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("anahtar RSA degil: %T", anyKey)
		}
		private = rsaKey
	}
	if private.N.BitLen() < 2048 {
		return nil, fmt.Errorf("anahtar %d bit, en az 2048 olmali", private.N.BitLen())
	}
	return &SigningKey{private: private, kid: deriveKID(&private.PublicKey)}, nil
}

// deriveKID ties the kid to the contents of the public key. A random kid
// changes on every restart and invalidates the JWKS that clients have
// cached.
func deriveKID(pub *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		// MarshalPKIXPublicKey does not fail for RSA; we still fall back
		// to the modulus so that we do not stay silent.
		der = pub.N.Bytes()
	}
	digest := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(digest[:8])
}

func (k *SigningKey) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k *SigningKey) ID() string                                  { return k.kid }

// Key returns the private key for op.SigningKey. The op package uses it
// when signing; PublicKey().Key() returns the public key for publishing
// the JWKS.
func (k *SigningKey) Key() any { return k.private }

// PublicKey returns the wrapper used as op.Key when publishing the JWKS.
func (k *SigningKey) PublicKey() *PublicKey { return &PublicKey{key: k} }

type PublicKey struct{ key *SigningKey }

func (k *PublicKey) ID() string                         { return k.key.kid }
func (k *PublicKey) Algorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k *PublicKey) Use() string                        { return "sig" }
func (k *PublicKey) Key() any                           { return &k.key.private.PublicKey }

// Check interface conformance at compile time
var (
	_ op.SigningKey = (*SigningKey)(nil)
	_ op.Key        = (*PublicKey)(nil)
)

// CryptoKey returns exactly 32 bytes for op.Config.CryptoKey. A short
// secret is never padded silently: the encryption would weaken, and it
// would do so silently.
func CryptoKey(s string) ([32]byte, error) {
	var b [32]byte
	if len(s) != 32 {
		return b, fmt.Errorf("crypto anahtari %d bayt, tam 32 olmali", len(s))
	}
	copy(b[:], s)
	return b, nil
}
