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

	if a.SignatureAlgorithm() != jose.RS256 {
		t.Errorf("algoritma = %v, beklenen RS256", a.SignatureAlgorithm())
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
		"bos":       "",
		"cop":       "bu bir PEM degil",
		"govde yok": "-----BEGIN RSA PRIVATE KEY-----\n-----END RSA PRIVATE KEY-----\n",
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
	gizli := strings.Repeat("a", 32)
	b, err := CryptoAnahtar(gizli)
	if err != nil {
		t.Fatalf("32 baytlik anahtar reddedildi: %v", err)
	}
	// Uzunluk iddiasi bos bir iddiaydi: CryptoAnahtar [32]byte
	// donduruyor, yani len(b) derleme zamani sabiti 32 ve hicbir
	// zaman tutmayamaz. Anlamli olan, sirrin bayt bayt kopyalanmasi
	// -- kopyalama duserse anahtar tamamen sifir olur ve sifreleme
	// sessizce sabit bir anahtara duser.
	if string(b[:]) != gizli {
		t.Error("anahtar icerigi girdiyle ayni degil")
	}
}

// PKCS#8 formatinda uretilmis RSA anahtarinin kabul edildigini dogrula.
func TestPKCS8FormatKabulu(t *testing.T) {
	rsaAnahtar, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("anahtar uretilemedi: %v", err)
	}
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(rsaAnahtar)
	if err != nil {
		t.Fatalf("PKCS#8 encode hatasi: %v", err)
	}
	blok := &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes}
	pemMetni := string(pem.EncodeToMemory(blok))

	a, err := AnahtarYukle(pemMetni)
	if err != nil {
		t.Fatalf("PKCS#8 anahtari yuklemedi: %v", err)
	}
	if a.ID() == "" {
		t.Error("PKCS#8 anahtarinin ID'si bos")
	}
}

// 1024 bitlik anahtar reddedilmeli; RSA icin en az 2048 bit gerekli.
func TestKisaBitAnahtariReddedilir(t *testing.T) {
	rsaAnahtar, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("anahtar uretilemedi: %v", err)
	}
	blok := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaAnahtar)}
	pemMetni := string(pem.EncodeToMemory(blok))

	_, err = AnahtarYukle(pemMetni)
	if err == nil {
		t.Error("1024 bitlik anahtar kabul edildi")
	}
	if err != nil && err.Error() != "anahtar 1024 bit, en az 2048 olmali" {
		t.Errorf("hata mesaji beklenen metni icermiyor: %v", err)
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
