package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func testPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("could not generate key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block))
}

func TestLoadSigningKey_SatisfiesInterfaces(t *testing.T) {
	a, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	var _ op.SigningKey = a

	if a.SignatureAlgorithm() != jose.RS256 {
		t.Errorf("algorithm = %v, want RS256", a.SignatureAlgorithm())
	}
	if a.ID() == "" {
		t.Error("key ID is empty")
	}
}

// The key ID must be derived from the content: the same PEM must always
// give the same kid, otherwise the JWKS that clients cached becomes
// invalid on every restart.
func TestSigningKey_IDStable(t *testing.T) {
	p := testPEM(t)
	a1, err := LoadSigningKey(p)
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	a2, err := LoadSigningKey(p)
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	if a1.ID() != a2.ID() {
		t.Errorf("the same PEM gave a different kid: %q, %q", a1.ID(), a2.ID())
	}
}

func TestSigningKey_IDDiffersPerKey(t *testing.T) {
	a1, _ := LoadSigningKey(testPEM(t))
	a2, _ := LoadSigningKey(testPEM(t))
	if a1.ID() == a2.ID() {
		t.Error("different keys gave the same kid")
	}
}

func TestLoadSigningKey_RejectsBadPEM(t *testing.T) {
	for name, input := range map[string]string{
		"empty":   "",
		"garbage": "bu bir PEM degil",
		"no body": "-----BEGIN RSA PRIVATE KEY-----\n-----END RSA PRIVATE KEY-----\n",
	} {
		if _, err := LoadSigningKey(input); err == nil {
			t.Errorf("%s: nil was returned while an error was expected", name)
		}
	}
}

// The encryption key must be exactly 32 bytes; if a short secret were
// silently padded with zeros, the encryption would be weakened.
func TestCryptoKey_Length(t *testing.T) {
	if _, err := CryptoKey(strings.Repeat("a", 31)); err == nil {
		t.Error("a 31-byte key was accepted")
	}
	if _, err := CryptoKey(strings.Repeat("a", 33)); err == nil {
		t.Error("a 33-byte key was accepted")
	}
	secret := strings.Repeat("a", 32)
	b, err := CryptoKey(secret)
	if err != nil {
		t.Fatalf("a 32-byte key was rejected: %v", err)
	}
	// The length assertion was an empty assertion: CryptoKey returns a
	// [32]byte, so len(b) is the compile-time constant 32 and can never
	// fail. What matters is that the secret is copied byte by byte -- if
	// the copy is dropped, the key becomes all zeros and the encryption
	// silently falls back to a constant key.
	if string(b[:]) != secret {
		t.Error("key content is the same as the input")
	}
}

// Check that an RSA key generated in PKCS#8 format is accepted.
func TestLoadSigningKey_AcceptsPKCS8(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("could not generate key: %v", err)
	}
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("PKCS#8 encode error: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes}
	pemText := string(pem.EncodeToMemory(block))

	a, err := LoadSigningKey(pemText)
	if err != nil {
		t.Fatalf("PKCS#8 key did not load: %v", err)
	}
	if a.ID() == "" {
		t.Error("PKCS#8 key ID is empty")
	}
}

// A 1024-bit key must be rejected; RSA needs at least 2048 bits.
func TestLoadSigningKey_RejectsShortKey(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("could not generate key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}
	pemText := string(pem.EncodeToMemory(block))

	_, err = LoadSigningKey(pemText)
	if err == nil {
		t.Error("a 1024-bit key was accepted")
	}
	if err != nil && err.Error() != "key is 1024 bits, must be at least 2048" {
		t.Errorf("error message does not contain the expected text: %v", err)
	}
}

// The private key must never appear in the JWKS.
func TestPublicKey_DoesNotLeakPrivateKey(t *testing.T) {
	a, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	var _ op.Key = a.PublicKey()
	switch a.PublicKey().Key().(type) {
	case *rsa.PublicKey:
		// correct
	default:
		t.Fatalf("PublicKey().Key() type is %T, want *rsa.PublicKey", a.PublicKey().Key())
	}
}
