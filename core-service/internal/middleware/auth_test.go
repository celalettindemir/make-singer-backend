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
	"github.com/makeasinger/api/internal/auth"
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
	return uygulamaMW(t, NewOPAuthMiddleware(denemeIssuer, denemeIstemci, &ozel.PublicKey))
}

func uygulamaMW(t *testing.T, mw *AuthMiddleware) *fiber.App {
	t.Helper()
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
		"aud": []string{denemeIstemci}, "jti": "jeton-1",
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
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

func TestSuresiGecmisJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
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
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
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
		"aud": []string{denemeIstemci}, "jti": "jeton-1",
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
		"sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// "alg: none" ile imzasiz jeton kabul edilmemeli.
//
// DIKKAT — bu test TEK BASINA bizim kodumuzu kanitlamaz: kendi
// WithValidMethods + keyfunc alg kontrolumuzu kaldirsak bile PASS kalir,
// cunku jwt/v5 "none" yontemi icin keyfunc'un
// jwt.UnsafeAllowNoneSignatureType dondurmesini sart kosuyor ve bizim
// keyfunc *rsa.PublicKey donduruyor. Yani koruma kismen kutuphaneden
// geliyor. Bizim alg zorlamamizin asil kaniti
// TestHS256AlgKarisikligiReddedilir'dir; alg guvencesini degerlendiren
// biri bu teste tek basina guvenmemeli.
func TestAlgNoneReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	j := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
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
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
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
		"iss": denemeIssuer, "sub": "", "aud": []string{denemeIstemci}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// nbf henuz gelmediyse jeton kabul edilmemeli.
func TestGelecekNbfReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
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
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
		"iat": time.Now().Add(time.Hour).Unix(),
		"exp": time.Now().Add(2 * time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Bizim client ID'mizi icermeyen jeton reddedilir.
func TestYanlisAudReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "aud": []string{"baska-istemci"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// aud claim'i hic yoksa da reddedilir.
func TestAudsuzJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	// aud BILEREK yok; jti var ki tek reddetme sebebi aud olsun.
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Dogru aud ile jeton gecer ve talepler context'e yazilir.
func TestDogruAudKabulEdilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "kullanici-9",
		"aud": []string{denemeIstemci, "baska"}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	durum, govde := istek(t, uygulama(t, ozel), "Bearer "+jeton)
	if durum != 200 {
		t.Fatalf("durum = %d, beklenen 200", durum)
	}
	if govde != "kullanici-9" {
		t.Errorf("userId = %q, beklenen kullanici-9", govde)
	}
}

// sayanVerifier, Zitadel JWKS yolunun CAGRILIP cagrilmadigini sayar.
type sayanVerifier struct{ cagri int }

func (v *sayanVerifier) Validate(string) (*auth.Claims, error) {
	v.cagri++
	return &auth.Claims{UserID: "jwks-kullanicisi"}, nil
}

func (v *sayanVerifier) Close() error { return nil }

// OP modunda legacy HMAC ve JWKS yollarina DUSULMEZ.
//
// Onceki hali vacuous idi: legacy jetonu RASTGELE bir sirla
// imzaladigi icin "fallback hic yok" ile "fallback var ama sir farkli"
// arasini ayirt edemiyordu; "OP basarisizsa legacy'ye ak" mutasyonunda
// PASS kaliyordu. Artik middleware paket icinden, op + jwtSecret +
// verifier UCU BIRLIKTE dolu kurulur ve legacy jeton TAM O SIRLA
// imzalanir. Ayrica verifier'in hic cagrilmadigi sayacla dogrulanir.
func TestOPModundaLegacyYolaDusulmez(t *testing.T) {
	ozel := denemeAnahtar(t)
	const bilinenSir = "bu-sir-middleware-a-verilenin-AYNISI"
	sahte := &sayanVerifier{}
	mw := &AuthMiddleware{
		op:        NewOPAuthMiddleware(denemeIssuer, denemeIstemci, &ozel.PublicKey).op,
		jwtSecret: bilinenSir,
		verifier:  sahte,
	}
	app := uygulamaMW(t, mw)

	// Legacy yol acik olsaydi bu jeton kabul edilirdi: dogru sir, gecerli
	// exp, HS256. OP modunda reddedilmeli.
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"userId": "legacy-kullanicisi", "sub": "legacy-kullanicisi",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	imzali, err := legacy.SignedString([]byte(bilinenSir))
	if err != nil {
		t.Fatalf("legacy jeton uretilemedi: %v", err)
	}
	if durum, govde := istek(t, app, "Bearer "+imzali); durum != 401 {
		t.Errorf("legacy jeton: durum = %d (govde %q), beklenen 401", durum, govde)
	}

	// Zitadel JWKS yolu da denenmemeli: sahte verifier HER jetonu kabul
	// ediyor, yani cagrilsaydi 200 donerdi.
	if durum, _ := istek(t, app, "Bearer bozuk.jeton.metni"); durum != 401 {
		t.Errorf("JWKS yolu: durum = %d, beklenen 401", durum)
	}
	if sahte.cagri != 0 {
		t.Errorf("verifier %d kez cagrildi, beklenen 0", sahte.cagri)
	}
}

// id_token bir access token DEGILDIR: API erisimi vermemeli. Ikisi de
// ayni anahtarla, ayni iss/aud ile imzalandigi icin diger denetimlerin
// hicbiri ayirt etmez; ayrim jti/azp/at_hash uzerinden yapilir.
func TestIDTokenAccessTokenOlarakReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	durumlar := map[string]jwt.MapClaims{
		// Gercek id_token sekli: azp + at_hash + nonce var, jti yok.
		"tam id_token sekli": {
			"iss": denemeIssuer, "sub": "kullanici-1",
			"aud": []string{denemeIstemci},
			"azp": denemeIstemci, "at_hash": "eR1cMQ0Zo5ZhQnQ8Bnp2dA",
			"nonce": "n-0S6_WzA2Mj", "auth_time": time.Now().Unix(),
			"exp": time.Now().Add(time.Hour).Unix(),
		},
		// jti yok: access token olamaz.
		"jti yok": {
			"iss": denemeIssuer, "sub": "kullanici-1",
			"aud": []string{denemeIstemci},
			"exp": time.Now().Add(time.Hour).Unix(),
		},
		// jti eklenmis ama azp duruyor: yine id_token.
		"jti var ama azp var": {
			"iss": denemeIssuer, "sub": "kullanici-1",
			"aud": []string{denemeIstemci}, "jti": "jeton-1",
			"azp": denemeIstemci,
			"exp": time.Now().Add(time.Hour).Unix(),
		},
		// jti eklenmis ama at_hash duruyor.
		"jti var ama at_hash var": {
			"iss": denemeIssuer, "sub": "kullanici-1",
			"aud": []string{denemeIstemci}, "jti": "jeton-1",
			"at_hash": "eR1cMQ0Zo5ZhQnQ8Bnp2dA",
			"exp":     time.Now().Add(time.Hour).Unix(),
		},
		// jti bosluktan olusuyor: yok sayilir.
		"jti bos": {
			"iss": denemeIssuer, "sub": "kullanici-1",
			"aud": []string{denemeIstemci}, "jti": "   ",
			"exp": time.Now().Add(time.Hour).Unix(),
		},
	}
	for ad, talepler := range durumlar {
		jeton := jetonUretTest(t, ozel, talepler)
		if durum, govde := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
			t.Errorf("%s: durum = %d (govde %q), beklenen 401", ad, durum, govde)
		}
	}
}

// sub yalnizca "" degil, bosluktan olusuyorsa da reddedilmeli; aksi
// halde userId "   " olur ve rate limit anahtari anlamsizlasir.
func TestSubBoslukIseReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "   ",
		"aud": []string{denemeIstemci}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, govde := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d (govde %q), beklenen 401", durum, govde)
	}
}

// Eksik yapilandirmada SESSIZ gevseme olmamali: aud denetimi yapilamayacagi
// icin dogrulayici her istegi reddeder. Sirasiyla bos istemci, bos issuer
// ve nil acik anahtar denenir; ucunde de tamamen gecerli bir jeton
// reddedilmelidir.
func TestEksikYapilandirmadaHerIstekReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "aud": []string{denemeIstemci}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	durumlar := map[string]*AuthMiddleware{
		"istemci bos":      NewOPAuthMiddleware(denemeIssuer, "", &ozel.PublicKey),
		"issuer bos":       NewOPAuthMiddleware("", denemeIstemci, &ozel.PublicKey),
		"acik anahtar nil": NewOPAuthMiddleware(denemeIssuer, denemeIstemci, nil),
	}
	for ad, mw := range durumlar {
		if durum, _ := istek(t, uygulamaMW(t, mw), "Bearer "+jeton); durum != 401 {
			t.Errorf("%s: durum = %d, beklenen 401", ad, durum)
		}
	}
}
