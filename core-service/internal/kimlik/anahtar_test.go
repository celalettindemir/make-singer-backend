package kimlik

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
	anahtar, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("anahtar uretilemedi: %v", err)
	}
	blok := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(anahtar)}
	return string(pem.EncodeToMemory(blok))
}

func TestAnahtarYuklenirVeArayuzleriKarsilar(t *testing.T) {
	a, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	var _ op.SigningKey = a
	var _ op.Key = a

	if a.SignatureAlgorithm() != jose.RS256 {
		t.Errorf("algoritma = %v, beklenen RS256", a.SignatureAlgorithm())
	}
	if a.Use() != "sig" {
		t.Errorf("Use() = %q, beklenen %q", a.Use(), "sig")
	}
	if a.ID() == "" {
		t.Error("anahtar ID'si bos")
	}
}

// Anahtar ID'si icerikten turetilmeli: ayni PEM her zaman ayni kid
// vermeli, aksi halde her yeniden baslatmada istemcilerin onbellekledigi
// JWKS gecersizlesir.
func TestAnahtarIDKararli(t *testing.T) {
	p := testPEM(t)
	a1, err := AnahtarYukle(p)
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	a2, err := AnahtarYukle(p)
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	if a1.ID() != a2.ID() {
		t.Errorf("ayni PEM farkli kid verdi: %q, %q", a1.ID(), a2.ID())
	}
}

func TestAnahtarIDFarkliAnahtarlardaFarkli(t *testing.T) {
	a1, _ := AnahtarYukle(testPEM(t))
	a2, _ := AnahtarYukle(testPEM(t))
	if a1.ID() == a2.ID() {
		t.Error("farkli anahtarlar ayni kid verdi")
	}
}

func TestBozukPEMReddedilir(t *testing.T) {
	for ad, girdi := range map[string]string{
		"bos":         "",
		"cop":         "bu bir PEM degil",
		"govde yok":   "-----BEGIN RSA PRIVATE KEY-----\n-----END RSA PRIVATE KEY-----\n",
	} {
		if _, err := AnahtarYukle(girdi); err == nil {
			t.Errorf("%s: hata beklenirken nil dondu", ad)
		}
	}
}

// Sifreleme anahtari tam 32 bayt olmali; kisa bir sir sessizce
// sifirlarla doldurulursa sifreleme zayiflar.
func TestCryptoAnahtarUzunlugu(t *testing.T) {
	if _, err := CryptoAnahtar(strings.Repeat("a", 31)); err == nil {
		t.Error("31 baytlik anahtar kabul edildi")
	}
	if _, err := CryptoAnahtar(strings.Repeat("a", 33)); err == nil {
		t.Error("33 baytlik anahtar kabul edildi")
	}
	b, err := CryptoAnahtar(strings.Repeat("a", 32))
	if err != nil {
		t.Fatalf("32 baytlik anahtar reddedildi: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("uzunluk = %d, beklenen 32", len(b))
	}
}

// JWKS'e ozel anahtar asla girmemeli.
func TestAcikAnahtarOzelAnahtarSizdirmaz(t *testing.T) {
	a, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	var _ op.Key = a.AcikAnahtar()
	switch a.AcikAnahtar().Key().(type) {
	case *rsa.PublicKey:
		// dogru
	default:
		t.Fatalf("AcikAnahtar().Key() tipi %T, *rsa.PublicKey olmali", a.AcikAnahtar().Key())
	}
}
