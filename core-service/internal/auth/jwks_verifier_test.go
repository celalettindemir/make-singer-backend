package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// --- helpers ---------------------------------------------------------------

// kidFor derives a deterministic "kid" from an RSA key: the same key must
// carry the same kid both when it is published in the JWKS and when it signs a
// token, otherwise keyfunc cannot pick the right key.
func kidFor(key *rsa.PrivateKey) string {
	sum := sha256.Sum256(key.N.Bytes())
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func rsaJWK(key *rsa.PrivateKey, kid string) map[string]any {
	pub := key.PublicKey
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// fakeJWKS starts a JWKS endpoint that publishes the given keys and returns
// its URL. If the keys change, the NEXT request sees the new set -- the
// rotation test relies on this.
func fakeJWKS(t *testing.T, keys func() []map[string]any) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys()})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// fakeJWKSRSA is a shortcut for fakeJWKS that takes only a list of RSA keys.
func fakeJWKSRSA(t *testing.T, keys func() []*rsa.PrivateKey) string {
	return fakeJWKS(t, func() []map[string]any {
		var sets []map[string]any
		for _, a := range keys() {
			sets = append(sets, rsaJWK(a, kidFor(a)))
		}
		return sets
	})
}

// signIDToken produces an RS256-signed id_token with the given RSA key.
func signIDToken(t *testing.T, key *rsa.PrivateKey, iss, aud, nonce string) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   iss,
		"aud":   aud,
		"sub":   "kullanici-1",
		"nonce": nonce,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	j := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	j.Header["kid"] = kidFor(key)
	s, err := j.SignedString(key)
	if err != nil {
		t.Fatalf("could not sign token: %v", err)
	}
	return s
}

// --- Step 1: FlexibleBool (K8) ----------------------------------------------

func TestFlexibleBool_AcceptsStringAndBool(t *testing.T) {
	for _, d := range []struct {
		name  string
		input string
		want  bool
	}{
		{"bool true", `{"v":true}`, true},
		{"bool false", `{"v":false}`, false},
		{"string true", `{"v":"true"}`, true}, // Apple may send it like this
		{"string false", `{"v":"false"}`, false},
	} {
		t.Run(d.name, func(t *testing.T) {
			var s struct{ V FlexibleBool }
			if err := json.Unmarshal([]byte(d.input), &s); err != nil {
				t.Fatalf("could not parse: %v", err)
			}
			if bool(s.V) != d.want {
				t.Errorf("= %v, want %v", bool(s.V), d.want)
			}
		})
	}
}

// Apple possibly sending a string must not LET THE BOOL SLIP: a malformed
// value must not silently fall back to false, it must produce an ERROR --
// otherwise a token that arrives with a malformed email_verified is not
// treated as verified, but the flow also does not say why it broke.
func TestFlexibleBool_RejectsMalformedValue(t *testing.T) {
	var s struct{ V FlexibleBool }
	if err := json.Unmarshal([]byte(`{"v":"belki"}`), &s); err == nil {
		t.Error("malformed value was accepted")
	}
	if err := json.Unmarshal([]byte(`{"v":3}`), &s); err == nil {
		t.Error("number was accepted")
	}
}

// --- Step 2: algorithm pinning and nonce tests -------------------------------

const (
	testIssuer   = "https://ornek"
	testClientID = "istemci"
)

// TestUpstreamVerifier_PinsAlgorithm verifies that a token signed with an
// algorithm other than RS256 is not accepted.
//
// A plain "sign with HS256 and see it rejected" test is NOT enough: jwt/v5's
// key-type check (keyfunc returns *rsa.PublicKey, HS256 Verify() expects
// []byte) catches that even without WithValidMethods, so it proves nothing in
// a mutation test. Instead we model the scenario where the provider's JWKS
// has been COMPROMISED (DNS/cache poisoning): the fake JWKS also publishes a
// SYMMETRIC ("oct") key next to the RSA key, and keyfunc/jwkset can REALLY
// decode it into a []byte secret (because it is fetched with Private: true).
// Without WithValidMethods an HS256 token signed with that secret is
// ACCEPTED; pinning rejects it by looking at the alg name, without looking at
// the key type at all.
func TestUpstreamVerifier_PinsAlgorithm(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("could not generate RSA key: %v", err)
	}
	octKid := "oct-anahtar"
	octSecret := []byte("saldirganin-ele-gecirdigi-jwks-sirri-0123456789")

	url := fakeJWKS(t, func() []map[string]any {
		return []map[string]any{
			rsaJWK(rsaKey, kidFor(rsaKey)),
			{
				"kty": "oct",
				"kid": octKid,
				"alg": "HS256",
				"k":   base64.RawURLEncoding.EncodeToString(octSecret),
			},
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := NewUpstreamVerifier(ctx, url, testIssuer, testClientID)
	if err != nil {
		t.Fatalf("could not set up verifier: %v", err)
	}
	defer v.Close()

	// Sanity check: a correct RS256 token must pass.
	goodToken := signIDToken(t, rsaKey, testIssuer, testClientID, "n0")
	if _, err := v.VerifyWithNonce(goodToken, "n0"); err != nil {
		t.Fatalf("valid RS256 token was rejected: %v", err)
	}

	// An attacker token signed with HS256, using the oct secret from the
	// compromised JWKS.
	now := time.Now()
	badToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":   testIssuer,
		"aud":   testClientID,
		"sub":   "saldirgan",
		"nonce": "n-saldiri",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	badToken.Header["kid"] = octKid
	signedBad, err := badToken.SignedString(octSecret)
	if err != nil {
		t.Fatalf("attacker could not sign token: %v", err)
	}

	if _, err := v.VerifyWithNonce(signedBad, "n-saldiri"); err == nil {
		t.Error("token signed with HS256 was accepted: alg pinning does not work")
	}

	// The "none" algorithm must be rejected in the same way.
	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss":   testIssuer,
		"aud":   testClientID,
		"sub":   "saldirgan2",
		"nonce": "n-saldiri2",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	unsigned, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("could not produce unsigned token: %v", err)
	}
	if _, err := v.VerifyWithNonce(unsigned, "n-saldiri2"); err == nil {
		t.Error("token with alg none was accepted")
	}
}

func TestUpstreamVerifier_RejectsNonceMismatch(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := fakeJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{key} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := NewUpstreamVerifier(ctx, url, testIssuer, testClientID)
	if err != nil {
		t.Fatalf("could not set up verifier: %v", err)
	}
	defer v.Close()

	// A token with a correct signature and correct iss/aud but a DIFFERENT
	// nonce must get an error from VerifyWithNonce: otherwise an attacker could
	// inject a valid id_token obtained from their own authorization into the
	// victim's flow (token replay).
	token := signIDToken(t, key, testIssuer, testClientID, "saldirganin-nonce-u")
	if _, err := v.VerifyWithNonce(token, "kurbanin-bekledigi-nonce"); err == nil {
		t.Error("mismatching nonce was accepted")
	}
}

func TestUpstreamVerifier_RejectsEmptyNonce(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := fakeJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{key} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := NewUpstreamVerifier(ctx, url, testIssuer, testClientID)
	if err != nil {
		t.Fatalf("could not set up verifier: %v", err)
	}
	defer v.Close()

	// If both the token's nonce and the expected nonce are empty, they must not
	// count as "equal": a provider response that sends no nonce has no replay
	// protection, and silently accepting it would remove the protection.
	token := signIDToken(t, key, testIssuer, testClientID, "")
	if _, err := v.VerifyWithNonce(token, ""); err == nil {
		t.Error("empty nonce was accepted")
	}
}

func TestUpstreamVerifier_RequiresIssuerAndAudience(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := fakeJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{key} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := NewUpstreamVerifier(ctx, url, testIssuer, testClientID)
	if err != nil {
		t.Fatalf("could not set up verifier: %v", err)
	}
	defer v.Close()

	t.Run("other iss is rejected", func(t *testing.T) {
		token := signIDToken(t, key, "https://baska-saglayici.ornek", testClientID, "n1")
		if _, err := v.VerifyWithNonce(token, "n1"); err == nil {
			t.Error("token with another issuer was accepted")
		}
	})

	t.Run("other aud is rejected", func(t *testing.T) {
		// If aud does not contain our client_id it is rejected -- so a Google
		// id_token issued to another application does not sign anyone in here.
		token := signIDToken(t, key, testIssuer, "baska-istemci", "n2")
		if _, err := v.VerifyWithNonce(token, "n2"); err == nil {
			t.Error("token with another audience was accepted")
		}
	})
}

func TestUpstreamVerifier_RejectsExpiredToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := fakeJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{key} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := NewUpstreamVerifier(ctx, url, testIssuer, testClientID)
	if err != nil {
		t.Fatalf("could not set up verifier: %v", err)
	}
	defer v.Close()

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   testIssuer,
		"aud":   testClientID,
		"sub":   "kullanici-1",
		"nonce": "n1",
		"iat":   now.Add(-2 * time.Hour).Unix(),
		"exp":   now.Add(-time.Hour).Unix(),
	}
	j := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	j.Header["kid"] = kidFor(key)
	expiredToken, err := j.SignedString(key)
	if err != nil {
		t.Fatalf("could not sign token: %v", err)
	}

	if _, err := v.VerifyWithNonce(expiredToken, "n1"); err == nil {
		t.Error("expired token was accepted")
	}
}

// --- the constructor's required fields ---------------------------------------

func TestNewUpstreamVerifier_RequiredFields(t *testing.T) {
	ctx := context.Background()
	if _, err := NewUpstreamVerifier(ctx, "", testIssuer, testClientID); err == nil {
		t.Error("empty jwksURL was accepted")
	}
	if _, err := NewUpstreamVerifier(ctx, "https://ornek/jwks", "", testClientID); err == nil {
		t.Error("empty issuer was accepted")
	}
	if _, err := NewUpstreamVerifier(ctx, "https://ornek/jwks", testIssuer, ""); err == nil {
		t.Error("empty audience was accepted")
	}
}

// --- Step 3: key rotation ---------------------------------------------------

// WHAT TestUpstreamVerifier_FollowsKeyRotation MEASURES: an id_token signed
// with a kid that has never been seen before is accepted -- that is, jwkset's
// "unknown kid" immediate re-fetch behaviour (the default, rate limited to
// once per 5 minutes) works.
//
// This test does NOT measure the regression of the ctx given to
// NewUpstreamVerifier being cancelled or left dead: whether it did was
// checked (see the NewUpstreamVerifier comment in jwks_verifier.go and
// jwkset@v0.5.17 http.go:83,89) -- the unknown-kid refresh uses its own
// internal context, independent of ctx. If inside NewUpstreamVerifier you
// replace ctx again with "context.WithTimeout(ctx, 30*time.Second); defer
// cancel()" and run this test, it PASSES -- the test does not catch that
// mutation. This is deliberate: the real cost of ctx (the old key staying in
// the cache forever, a second new kid being rejected within the 5-minute
// window) cannot be observed within this test's duration; a fake "preserves
// ctx" label would be worse than having no test at all.
func TestUpstreamVerifier_FollowsKeyRotation(t *testing.T) {
	oldKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	newKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	keySet := []*rsa.PrivateKey{oldKey}
	var mu sync.Mutex
	url := fakeJWKSRSA(t, func() []*rsa.PrivateKey {
		mu.Lock()
		defer mu.Unlock()
		return keySet
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := NewUpstreamVerifier(ctx, url, testIssuer, testClientID)
	if err != nil {
		t.Fatalf("could not set up verifier: %v", err)
	}
	defer v.Close()

	// A token signed with the old key passes.
	if _, err := v.VerifyWithNonce(signIDToken(t, oldKey, testIssuer, testClientID, "n1"), "n1"); err != nil {
		t.Fatalf("token with the old key was rejected: %v", err)
	}

	// The provider rotates the key.
	mu.Lock()
	keySet = []*rsa.PrivateKey{newKey}
	mu.Unlock()

	// A token signed with the new key must pass too: because the kid has not
	// been seen before, jwkset triggers its unknown-kid refresh. That refresh
	// uses its own internal context (independent of ctx) -- the note at the top
	// of this test explains why it is not measured.
	if _, err := v.VerifyWithNonce(signIDToken(t, newKey, testIssuer, testClientID, "n2"), "n2"); err != nil {
		t.Fatalf("token rejected after rotation (is refresh dead?): %v", err)
	}
}
