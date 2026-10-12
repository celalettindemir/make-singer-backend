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

const testIssuer = "https://kimlik.ornek.dev"
const testClientID = "makesinger-mobil"

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	return a
}

func signTestToken(t *testing.T, privKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	j := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := j.SignedString(privKey)
	if err != nil {
		t.Fatalf("imzalama: %v", err)
	}
	return signed
}

func newApp(t *testing.T, privKey *rsa.PrivateKey) *fiber.App {
	t.Helper()
	return newAppWithMiddleware(t, NewOPAuthMiddleware(testIssuer, testClientID, &privKey.PublicKey))
}

func newAppWithMiddleware(t *testing.T, mw *AuthMiddleware) *fiber.App {
	t.Helper()
	app := fiber.New()
	app.Get("/korumali", mw.Authenticate(), func(c *fiber.Ctx) error {
		return c.SendString(GetUserID(c))
	})
	return app
}

func doRequest(t *testing.T, app *fiber.App, authHeader string) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/korumali", nil)
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	resp, err := app.Test(r, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	body := make([]byte, 256)
	n, _ := resp.Body.Read(body)
	return resp.StatusCode, string(body[:n])
}

func TestAuthenticate_AcceptsValidToken(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer,
		"sub": "kullanici-1",
		"aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	status, body := doRequest(t, newApp(t, privKey), "Bearer "+token)
	if status != 200 {
		t.Fatalf("durum = %d, beklenen 200", status)
	}
	if body != "kullanici-1" {
		t.Errorf("userId = %q, beklenen kullanici-1", body)
	}
}

// A token without an exp claim would be valid forever. That was exactly the
// bug of the old HMAC path; here it is mandatory.
func TestAuthenticate_RejectsTokenWithoutExp(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

func TestAuthenticate_RejectsExpiredToken(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(-time.Minute).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// A token signed with another key must be rejected.
func TestAuthenticate_RejectsTokenSignedByOtherKey(t *testing.T) {
	privKey := testKey(t)
	otherKey := testKey(t)
	token := signTestToken(t, otherKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// A wrong issuer must be rejected: another OP's token must not get into
// our API.
func TestAuthenticate_RejectsWrongIssuer(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": "https://baska.ornek.dev", "sub": "k1",
		"aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// A missing iss claim must be rejected too.
func TestAuthenticate_RejectsTokenWithoutIssuer(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// An unsigned token with "alg: none" must not be accepted.
//
// CAUTION: OUR ALG ENFORCEMENT IS CURRENTLY NOT COVERED BY TESTS. Measured:
// this test AND TestAuthenticate_RejectsHS256AlgConfusion still PASS when
// BOTH layers in the verifier (jwt.WithValidMethods and the alg check inside
// the keyfunc) are removed together. The reason is not our code but jwt/v5's
// key-type check: the keyfunc returns *rsa.PublicKey, HS256's Verify expects
// []byte, and "none"'s Verify expects jwt.UnsafeAllowNoneSignatureType; the
// library rejects on the type mismatch.
//
// So these two tests pass because the protection comes from the library. Our
// two-layer enforcement is deliberate defense in depth (it is the only
// protection left if library behavior changes or the keyfunc starts returning
// a different type later) but it is NOT MEASURED by a test right now. Anyone
// assessing the alg guarantee must not rely on these tests as evidence that
// "our enforcement works".
func TestAuthenticate_RejectsAlgNone(t *testing.T) {
	privKey := testKey(t)
	j := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	unsigned, err := j.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("imzasiz jeton uretilemedi: %v", err)
	}
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+unsigned); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// The classic algorithm confusion attack: the attacker sets alg to HS256 and
// uses the (publicly available) RSA public key as the HMAC secret. Since we
// enforce the expected algorithm, it must be rejected.
func TestAuthenticate_RejectsHS256AlgConfusion(t *testing.T) {
	privKey := testKey(t)
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("acik anahtar kodlanamadi: %v", err)
	}
	j := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	// The public key is used as the HMAC secret.
	forgedToken, err := j.SignedString(pubDER)
	if err != nil {
		t.Fatalf("HS256 jetonu uretilemedi: %v", err)
	}
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+forgedToken); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

func TestAuthenticate_MissingOrMalformedHeader(t *testing.T) {
	privKey := testKey(t)
	app := newApp(t, privKey)
	for name, authHeader := range map[string]string{
		"header yok":  "",
		"Bearer yok":  "abc.def.ghi",
		"bos jeton":   "Bearer ",
		"yanlis sema": "Basic abc",
		"bozuk jeton": "Bearer not.a.jwt",
	} {
		if status, _ := doRequest(t, app, authHeader); status != 401 {
			t.Errorf("%s: durum = %d, beklenen 401", name, status)
		}
	}
}

// If sub is empty userId stays empty and the rate limit key collapses; it must be rejected.
func TestAuthenticate_RejectsEmptySub(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// If nbf has not arrived yet the token must not be accepted.
func TestAuthenticate_RejectsFutureNbf(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"nbf": time.Now().Add(time.Hour).Unix(),
		"exp": time.Now().Add(2 * time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// If iat is in the future the token is inconsistent; it must be rejected.
func TestAuthenticate_RejectsFutureIat(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"iat": time.Now().Add(time.Hour).Unix(),
		"exp": time.Now().Add(2 * time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// A token that does not contain our client ID is rejected.
func TestAuthenticate_RejectsWrongAud(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{"baska-istemci"},
		// jti is present so that the only reason for rejection is the wrong aud.
		"jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// A missing aud claim is rejected too.
func TestAuthenticate_RejectsTokenWithoutAud(t *testing.T) {
	privKey := testKey(t)
	// aud is deliberately absent; jti is present so that the only reason for rejection is aud.
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, _ := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d, beklenen 401", status)
	}
}

// A token with the right aud passes and the claims are written to the context.
func TestAuthenticate_AcceptsMatchingAud(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "kullanici-9",
		"aud": []string{testClientID, "baska"}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	status, body := doRequest(t, newApp(t, privKey), "Bearer "+token)
	if status != 200 {
		t.Fatalf("durum = %d, beklenen 200", status)
	}
	if body != "kullanici-9" {
		t.Errorf("userId = %q, beklenen kullanici-9", body)
	}
}

// An id_token is NOT an access token: it must not grant API access. Both are
// signed with the same key and the same iss/aud, so none of the other checks
// tells them apart; the distinction is made via jti/azp/at_hash.
func TestAuthenticate_RejectsIDTokenAsAccessToken(t *testing.T) {
	privKey := testKey(t)
	cases := map[string]jwt.MapClaims{
		// Real id_token shape: azp + at_hash + nonce present, jti absent.
		"tam id_token sekli": {
			"iss": testIssuer, "sub": "kullanici-1",
			"aud": []string{testClientID},
			"azp": testClientID, "at_hash": "eR1cMQ0Zo5ZhQnQ8Bnp2dA",
			"nonce": "n-0S6_WzA2Mj", "auth_time": time.Now().Unix(),
			"exp": time.Now().Add(time.Hour).Unix(),
		},
		// jti absent: it cannot be an access token.
		"jti yok": {
			"iss": testIssuer, "sub": "kullanici-1",
			"aud": []string{testClientID},
			"exp": time.Now().Add(time.Hour).Unix(),
		},
		// jti added but azp is still there: still an id_token.
		"jti var ama azp var": {
			"iss": testIssuer, "sub": "kullanici-1",
			"aud": []string{testClientID}, "jti": "jeton-1",
			"azp": testClientID,
			"exp": time.Now().Add(time.Hour).Unix(),
		},
		// jti added but at_hash is still there.
		"jti var ama at_hash var": {
			"iss": testIssuer, "sub": "kullanici-1",
			"aud": []string{testClientID}, "jti": "jeton-1",
			"at_hash": "eR1cMQ0Zo5ZhQnQ8Bnp2dA",
			"exp":     time.Now().Add(time.Hour).Unix(),
		},
		// jti is made of whitespace: treated as absent.
		"jti bos": {
			"iss": testIssuer, "sub": "kullanici-1",
			"aud": []string{testClientID}, "jti": "   ",
			"exp": time.Now().Add(time.Hour).Unix(),
		},
	}
	for name, claims := range cases {
		token := signTestToken(t, privKey, claims)
		if status, body := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
			t.Errorf("%s: durum = %d (govde %q), beklenen 401", name, status, body)
		}
	}
}

// A sub made of whitespace, not only "", must also be rejected; otherwise
// userId becomes "   " and the rate limit key becomes meaningless.
func TestAuthenticate_RejectsWhitespaceSub(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "   ",
		"aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, body := doRequest(t, newApp(t, privKey), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d (govde %q), beklenen 401", status, body)
	}
}

// With missing configuration there must be NO SILENT relaxation: since the aud
// check cannot be made, the verifier rejects every request. An empty client
// ID, an empty issuer and a nil public key are tried in turn; in all three a
// fully valid token must be rejected.
func TestNewOPAuthMiddleware_IncompleteConfigRejectsAll(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	cases := map[string]*AuthMiddleware{
		"istemci bos":      NewOPAuthMiddleware(testIssuer, "", &privKey.PublicKey),
		"issuer bos":       NewOPAuthMiddleware("", testClientID, &privKey.PublicKey),
		"acik anahtar nil": NewOPAuthMiddleware(testIssuer, testClientID, nil),
	}
	for name, mw := range cases {
		if status, _ := doRequest(t, newAppWithMiddleware(t, mw), "Bearer "+token); status != 401 {
			t.Errorf("%s: durum = %d, beklenen 401", name, status)
		}
	}
}

// A middleware whose op field is empty (zero value) must accept no token:
// the old HMAC/JWKS paths were removed, there is no other path to fall back to.
func TestAuthenticate_ZeroValueMiddlewareRejectsAll(t *testing.T) {
	privKey := testKey(t)
	token := signTestToken(t, privKey, jwt.MapClaims{
		"iss": testIssuer, "sub": "k1", "aud": []string{testClientID}, "jti": "jeton-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if status, body := doRequest(t, newAppWithMiddleware(t, &AuthMiddleware{}), "Bearer "+token); status != 401 {
		t.Errorf("durum = %d (govde %q), beklenen 401", status, body)
	}
}
