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
		t.Fatalf("anahtar uretilemedi: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block))
}

func TestLoadSigningKey_SatisfiesInterfaces(t *testing.T) {
	a, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	var _ op.SigningKey = a

	if a.SignatureAlgorithm() != jose.RS256 {
		t.Errorf("algoritma = %v, beklenen RS256", a.SignatureAlgorithm())
	}
	if a.ID() == "" {
		t.Error("anahtar ID'si bos")
	}
}

// The key ID must be derived from the content: the same PEM must always
// give the same kid, otherwise the JWKS that clients cached becomes
// invalid on every restart.
func TestSigningKey_IDStable(t *testing.T) {
	p := testPEM(t)
	a1, err := LoadSigningKey(p)
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	a2, err := LoadSigningKey(p)
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	if a1.ID() != a2.ID() {
		t.Errorf("ayni PEM farkli kid verdi: %q, %q", a1.ID(), a2.ID())
	}
}

func TestSigningKey_IDDiffersPerKey(t *testing.T) {
	a1, _ := LoadSigningKey(testPEM(t))
	a2, _ := LoadSigningKey(testPEM(t))
	if a1.ID() == a2.ID() {
		t.Error("farkli anahtarlar ayni kid verdi")
	}
}

func TestLoadSigningKey_RejectsBadPEM(t *testing.T) {
	for name, input := range map[string]string{
		"bos":       "",
		"cop":       "bu bir PEM degil",
		"govde yok": "-----BEGIN RSA PRIVATE KEY-----\n-----END RSA PRIVATE KEY-----\n",
	} {
		if _, err := LoadSigningKey(input); err == nil {
			t.Errorf("%s: hata beklenirken nil dondu", name)
		}
	}
}

// The encryption key must be exactly 32 bytes; if a short secret were
// silently padded with zeros, the encryption would be weakened.
func TestCryptoKey_Length(t *testing.T) {
	if _, err := CryptoKey(strings.Repeat("a", 31)); err == nil {
		t.Error("31 baytlik anahtar kabul edildi")
	}
	if _, err := CryptoKey(strings.Repeat("a", 33)); err == nil {
		t.Error("33 baytlik anahtar kabul edildi")
	}
	secret := strings.Repeat("a", 32)
	b, err := CryptoKey(secret)
	if err != nil {
		t.Fatalf("32 baytlik anahtar reddedildi: %v", err)
	}
	// The length assertion was an empty assertion: CryptoKey returns a
	// [32]byte, so len(b) is the compile-time constant 32 and can never
	// fail. What matters is that the secret is copied byte by byte -- if
	// the copy is dropped, the key becomes all zeros and the encryption
	// silently falls back to a constant key.
	if string(b[:]) != secret {
		t.Error("anahtar icerigi girdiyle ayni degil")
	}
}

// Check that an RSA key generated in PKCS#8 format is accepted.
func TestLoadSigningKey_AcceptsPKCS8(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("anahtar uretilemedi: %v", err)
	}
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("PKCS#8 encode hatasi: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes}
	pemText := string(pem.EncodeToMemory(block))

	a, err := LoadSigningKey(pemText)
	if err != nil {
		t.Fatalf("PKCS#8 anahtari yuklemedi: %v", err)
	}
	if a.ID() == "" {
		t.Error("PKCS#8 anahtarinin ID'si bos")
	}
}

// A 1024-bit key must be rejected; RSA needs at least 2048 bits.
func TestLoadSigningKey_RejectsShortKey(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("anahtar uretilemedi: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}
	pemText := string(pem.EncodeToMemory(block))

	_, err = LoadSigningKey(pemText)
	if err == nil {
		t.Error("1024 bitlik anahtar kabul edildi")
	}
	if err != nil && err.Error() != "anahtar 1024 bit, en az 2048 olmali" {
		t.Errorf("hata mesaji beklenen metni icermiyor: %v", err)
	}
}

// The private key must never appear in the JWKS.
func TestPublicKey_DoesNotLeakPrivateKey(t *testing.T) {
	a, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	var _ op.Key = a.PublicKey()
	switch a.PublicKey().Key().(type) {
	case *rsa.PublicKey:
		// correct
	default:
		t.Fatalf("AcikAnahtar().Key() tipi %T, *rsa.PublicKey olmali", a.PublicKey().Key())
	}
}
