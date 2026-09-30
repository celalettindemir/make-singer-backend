package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const denemeIssuer = "https://kimlik.ornek.dev"
const denemeIstemci = "makesinger-mobil"

func denemeAnahtar(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	return a
}

func jetonUretTest(t *testing.T, ozel *rsa.PrivateKey, talepler jwt.MapClaims) string {
	t.Helper()
	j := jwt.NewWithClaims(jwt.SigningMethodRS256, talepler)
	imzali, err := j.SignedString(ozel)
	if err != nil {
		t.Fatalf("imzalama: %v", err)
	}
	return imzali
}

func uygulama(t *testing.T, ozel *rsa.PrivateKey) *fiber.App {
	t.Helper()
	mw := NewOPAuthMiddleware(denemeIssuer, &ozel.PublicKey)
	app := fiber.New()
	app.Get("/korumali", mw.Authenticate(), func(c *fiber.Ctx) error {
		return c.SendString(GetUserID(c))
	})
	return app
}

// uygulamaIstemci, aud dogrulamasinin da acik oldugu varyanti kurar.
func uygulamaIstemci(t *testing.T, ozel *rsa.PrivateKey) *fiber.App {
	t.Helper()
	mw := NewOPAuthMiddlewareIleIstemci(denemeIssuer, denemeIstemci, &ozel.PublicKey)
	app := fiber.New()
	app.Get("/korumali", mw.Authenticate(), func(c *fiber.Ctx) error {
		return c.SendString(GetUserID(c))
	})
	return app
}

func istek(t *testing.T, app *fiber.App, yetki string) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/korumali", nil)
	if yetki != "" {
		r.Header.Set("Authorization", yetki)
	}
	y, err := app.Test(r, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	govde := make([]byte, 256)
	n, _ := y.Body.Read(govde)
	return y.StatusCode, string(govde[:n])
}

func TestGecerliJetonKabulEdilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer,
		"sub": "kullanici-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	durum, govde := istek(t, uygulama(t, ozel), "Bearer "+jeton)
	if durum != 200 {
		t.Fatalf("durum = %d, beklenen 200", durum)
	}
	if govde != "kullanici-1" {
		t.Errorf("userId = %q, beklenen kullanici-1", govde)
	}
}

// exp claim'i olmayan jeton sonsuza kadar gecerli olur. Legacy yolun
// hatasi tam buydu; yeni yolda zorunlu.
func TestExpsizJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{"iss": denemeIssuer, "sub": "k1"})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

func TestSuresiGecmisJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1",
		"exp": time.Now().Add(-time.Minute).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Baska bir anahtarla imzalanmis jeton reddedilmeli.
func TestBaskaAnahtarlaImzaliJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	sahte := denemeAnahtar(t)
	jeton := jetonUretTest(t, sahte, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Yanlis issuer reddedilmeli: baska bir OP'nin jetonu bizim API'mize
// girmemeli.
func TestYanlisIssuerReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": "https://baska.ornek.dev", "sub": "k1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// iss claim'i hic yoksa da reddedilmeli.
func TestIssizJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"sub": "k1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// "alg: none" ile imzasiz jeton kabul edilmemeli.
func TestAlgNoneReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	j := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	imzasiz, err := j.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("imzasiz jeton uretilemedi: %v", err)
	}
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+imzasiz); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Klasik algoritma karisikligi saldirisi: saldirgan alg'i HS256 yapar ve
// (herkese acik olan) RSA acik anahtarini HMAC sirri olarak kullanir.
// Beklenen algoritmayi biz zorladigimiz icin reddedilmeli.
func TestHS256AlgKarisikligiReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	acikDER, err := x509.MarshalPKIXPublicKey(&ozel.PublicKey)
	if err != nil {
		t.Fatalf("acik anahtar kodlanamadi: %v", err)
	}
	j := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	// Acik anahtar HMAC sirri gibi kullanilir.
	sahteJeton, err := j.SignedString(acikDER)
	if err != nil {
		t.Fatalf("HS256 jetonu uretilemedi: %v", err)
	}
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+sahteJeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

func TestEksikVeBozukHeader(t *testing.T) {
	ozel := denemeAnahtar(t)
	app := uygulama(t, ozel)
	for ad, yetki := range map[string]string{
		"header yok":  "",
		"Bearer yok":  "abc.def.ghi",
		"bos jeton":   "Bearer ",
		"yanlis sema": "Basic abc",
		"bozuk jeton": "Bearer not.a.jwt",
	} {
		if durum, _ := istek(t, app, yetki); durum != 401 {
			t.Errorf("%s: durum = %d, beklenen 401", ad, durum)
		}
	}
}

// sub bos ise userId bos kalir ve rate limit anahtari cokur; reddedilmeli.
func TestSubBossaReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// nbf henuz gelmediyse jeton kabul edilmemeli.
func TestGelecekNbfReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1",
		"nbf": time.Now().Add(time.Hour).Unix(),
		"exp": time.Now().Add(2 * time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// iat gelecekte ise jeton tutarsizdir; reddedilmeli.
func TestGelecekIatReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1",
		"iat": time.Now().Add(time.Hour).Unix(),
		"exp": time.Now().Add(2 * time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// aud dogrulamasi acikken bizim client ID'mizi icermeyen jeton reddedilir.
func TestYanlisAudReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "aud": []string{"baska-istemci"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulamaIstemci(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// aud dogrulamasi acikken aud hic yoksa da reddedilir.
func TestAudsuzJetonIstemciModundaReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulamaIstemci(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Dogru aud ile jeton gecer ve talepler context'e yazilir.
func TestDogruAudKabulEdilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "kullanici-9",
		"aud": []string{denemeIstemci, "baska"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	durum, govde := istek(t, uygulamaIstemci(t, ozel), "Bearer "+jeton)
	if durum != 200 {
		t.Fatalf("durum = %d, beklenen 200", durum)
	}
	if govde != "kullanici-9" {
		t.Errorf("userId = %q, beklenen kullanici-9", govde)
	}
}

// OP modunda legacy HMAC yoluna dusulmez: dogru sir ile imzalanmis bir
// legacy jeton bile reddedilmeli.
func TestOPModundaLegacyYolaDusulmez(t *testing.T) {
	ozel := denemeAnahtar(t)
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "k1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	sir := make([]byte, 32)
	if _, err := rand.Read(sir); err != nil {
		t.Fatalf("sir uretilemedi: %v", err)
	}
	imzali, err := legacy.SignedString(sir)
	if err != nil {
		t.Fatalf("legacy jeton uretilemedi: %v", err)
	}
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+imzali); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}
