package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// generateTestP8 generates a one-off ECDSA P-256 private key for tests
// and returns it as PKCS#8 PEM. A real key is NEVER committed; it is
// regenerated on every test run.
func generateTestP8(t *testing.T) (p8PEM string, publicKey *ecdsa.PublicKey) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("test anahtari uretilemedi: %v", err)
	}
	derBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("test anahtari PKCS8'e cevrilemedi: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: derBytes}
	return string(pem.EncodeToMemory(block)), &privateKey.PublicKey
}

// The Apple client_secret is a JWT signed with ES256. We verify the
// signature with the PUBLIC half of the private key generated from the
// .p8 and check the fields -- so signing correctly is required to pass.
func TestAppleClientSecret_Claims(t *testing.T) {
	p8, publicKey := generateTestP8(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	clientSecret, err := appleClientSecret("EKIP123", "ANAHTAR9", "tr.makesinger.giris", p8, now)
	if err != nil {
		t.Fatalf("client_secret uretilemedi: %v", err)
	}

	// WithoutClaimsValidation is REQUIRED: jwt.Parse validates exp against
	// the WALL CLOCK, but we use a fixed `now`. Without it this test would
	// fail FOREVER with "token is expired" after 2026-10-07T12:05:00Z --
	// and once a test is permanently red its assertions are DEAD, so K5's
	// lifetime decision would be left without regression protection.
	// We lose nothing here: we assert iat and exp OURSELVES below.
	token, err := jwt.Parse(clientSecret, func(t *jwt.Token) (any, error) { return publicKey, nil },
		jwt.WithValidMethods([]string{"ES256"}), jwt.WithoutClaimsValidation())
	if err != nil {
		t.Fatalf("client_secret dogrulanamadi: %v", err)
	}
	c := token.Claims.(jwt.MapClaims)
	if c["iss"] != "EKIP123" {
		t.Errorf("iss = %v", c["iss"])
	}
	if c["sub"] != "tr.makesinger.giris" {
		t.Errorf("sub = %v", c["sub"])
	}
	if c["aud"] != "https://appleid.apple.com" {
		t.Errorf("aud = %v", c["aud"])
	}
	if token.Header["kid"] != "ANAHTAR9" {
		t.Errorf("kid = %v", token.Header["kid"])
	}
	if token.Header["alg"] != "ES256" {
		t.Errorf("alg = %v", token.Header["alg"])
	}
}

// K5: lifetime is 5 minutes. Apple caps it at 6 months; since we
// regenerate on every exchange, keeping it short is allowed and correct.
func TestAppleClientSecret_Lifetime(t *testing.T) {
	p8, publicKey := generateTestP8(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	clientSecret, err := appleClientSecret("EKIP123", "ANAHTAR9", "tr.makesinger.giris", p8, now)
	if err != nil {
		t.Fatalf("client_secret uretilemedi: %v", err)
	}
	// WithoutClaimsValidation: see the rationale in TestAppleClientSecret_Claims
	// -- validating against the wall clock with a fixed `now` is a time
	// bomb, and we make the lifetime assertion ourselves below.
	token, err := jwt.Parse(clientSecret, func(t *jwt.Token) (any, error) { return publicKey, nil },
		jwt.WithValidMethods([]string{"ES256"}), jwt.WithoutClaimsValidation())
	if err != nil {
		t.Fatalf("client_secret dogrulanamadi: %v", err)
	}
	c := token.Claims.(jwt.MapClaims)
	iat, _ := c.GetIssuedAt()
	exp, _ := c.GetExpirationTime()
	if iat == nil || exp == nil {
		t.Fatalf("iat/exp eksik: iat=%v exp=%v", iat, exp)
	}
	if !iat.Equal(now) {
		t.Errorf("iat = %v, beklenen %v", iat.Time, now)
	}
	if got := exp.Sub(iat.Time); got != 5*time.Minute {
		t.Errorf("exp - iat = %v, beklenen 5m", got)
	}
}

// An RSA key cannot sign with ES256 via a .p8; it must not pass silently.
func TestAppleClientSecret_RejectsWrongKeyType(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa anahtari uretilemedi: %v", err)
	}
	derBytes, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("rsa anahtari pkcs8'e cevrilemedi: %v", err)
	}
	p8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derBytes}))

	_, err = appleClientSecret("EKIP123", "ANAHTAR9", "tr.makesinger.giris", p8, time.Now())
	if err == nil {
		t.Fatal("rsa anahtariyla ES256 imzalama basarili oldu, hata bekleniyordu")
	}
}

func TestAppleClientSecret_RejectsBadPEM(t *testing.T) {
	_, err := appleClientSecret("EKIP123", "ANAHTAR9", "tr.makesinger.giris", "bu-pem-degil", time.Now())
	if err == nil {
		t.Fatal("bozuk PEM ile hata bekleniyordu, nil geldi")
	}
}
