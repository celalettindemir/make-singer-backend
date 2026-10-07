package kimlik

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

// testP8Uret, testler icin tek seferlik bir ECDSA P-256 ozel anahtari
// uretir ve PKCS#8 PEM olarak doner. Gercek anahtar HICBIR ZAMAN
// commit edilmez; her test calismasinda yeniden uretilir.
func testP8Uret(t *testing.T) (p8PEM string, acikAnahtar *ecdsa.PublicKey) {
	t.Helper()
	ozel, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("test anahtari uretilemedi: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ozel)
	if err != nil {
		t.Fatalf("test anahtari PKCS8'e cevrilemedi: %v", err)
	}
	blok := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(blok)), &ozel.PublicKey
}

// Apple client_secret'i ES256 ile imzalanmis bir JWT'dir. Imzayi
// .p8'den uretilen ozel anahtarin ACIK esiyle dogrulayarak alanlari
// kontrol ediyoruz -- yani testi gecmek icin dogru imzalamak sart.
func TestAppleIstemciSirAlanlari(t *testing.T) {
	p8, acikAnahtar := testP8Uret(t)
	simdi := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	sir, err := appleIstemciSir("EKIP123", "ANAHTAR9", "tr.makesinger.giris", p8, simdi)
	if err != nil {
		t.Fatalf("client_secret uretilemedi: %v", err)
	}

	jeton, err := jwt.Parse(sir, func(t *jwt.Token) (any, error) { return acikAnahtar, nil },
		jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		t.Fatalf("client_secret dogrulanamadi: %v", err)
	}
	c := jeton.Claims.(jwt.MapClaims)
	if c["iss"] != "EKIP123" {
		t.Errorf("iss = %v", c["iss"])
	}
	if c["sub"] != "tr.makesinger.giris" {
		t.Errorf("sub = %v", c["sub"])
	}
	if c["aud"] != "https://appleid.apple.com" {
		t.Errorf("aud = %v", c["aud"])
	}
	if jeton.Header["kid"] != "ANAHTAR9" {
		t.Errorf("kid = %v", jeton.Header["kid"])
	}
	if jeton.Header["alg"] != "ES256" {
		t.Errorf("alg = %v", jeton.Header["alg"])
	}
}

// K5: omur 5 dakika. Apple ustten 6 ay siniri koyuyor; biz her
// degisimde yeniden urettigimiz icin kisa tutmak serbest ve dogrudur.
func TestAppleIstemciSirOmru(t *testing.T) {
	p8, acikAnahtar := testP8Uret(t)
	simdi := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	sir, err := appleIstemciSir("EKIP123", "ANAHTAR9", "tr.makesinger.giris", p8, simdi)
	if err != nil {
		t.Fatalf("client_secret uretilemedi: %v", err)
	}
	jeton, err := jwt.Parse(sir, func(t *jwt.Token) (any, error) { return acikAnahtar, nil },
		jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		t.Fatalf("client_secret dogrulanamadi: %v", err)
	}
	c := jeton.Claims.(jwt.MapClaims)
	iat, _ := c.GetIssuedAt()
	exp, _ := c.GetExpirationTime()
	if iat == nil || exp == nil {
		t.Fatalf("iat/exp eksik: iat=%v exp=%v", iat, exp)
	}
	if !iat.Time.Equal(simdi) {
		t.Errorf("iat = %v, beklenen %v", iat.Time, simdi)
	}
	if got := exp.Time.Sub(iat.Time); got != 5*time.Minute {
		t.Errorf("exp - iat = %v, beklenen 5m", got)
	}
}

// RSA bir .p8 ile ES256 imzalanamaz; sessizce gecmemeli.
func TestAppleIstemciSirYanlisAnahtarTipiReddeder(t *testing.T) {
	rsaAnahtar, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa anahtari uretilemedi: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(rsaAnahtar)
	if err != nil {
		t.Fatalf("rsa anahtari pkcs8'e cevrilemedi: %v", err)
	}
	p8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	_, err = appleIstemciSir("EKIP123", "ANAHTAR9", "tr.makesinger.giris", p8, time.Now())
	if err == nil {
		t.Fatal("rsa anahtariyla ES256 imzalama basarili oldu, hata bekleniyordu")
	}
}

func TestAppleIstemciSirBozukPEMReddeder(t *testing.T) {
	_, err := appleIstemciSir("EKIP123", "ANAHTAR9", "tr.makesinger.giris", "bu-pem-degil", time.Now())
	if err == nil {
		t.Fatal("bozuk PEM ile hata bekleniyordu, nil geldi")
	}
}
