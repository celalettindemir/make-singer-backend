package appstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/makeasinger/api/internal/config"
)

func testP8(t *testing.T) (string, *ecdsa.PublicKey) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), &k.PublicKey
}

const statusBody = `{"data":[{"lastTransactions":[
 {"originalTransactionId":"other","status":2,"signedTransactionInfo":"x","signedRenewalInfo":"y"},
 {"originalTransactionId":"1000","status":1,"signedTransactionInfo":"stx","signedRenewalInfo":"srn"}]}]}`

func TestAPI_FallsBackToSandboxOn404(t *testing.T) {
	p8, _ := testP8(t)
	var prodHits, sandboxHits int
	prod := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prodHits++
		http.NotFound(w, r)
	}))
	defer prod.Close()
	sandbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sandboxHits++
		if r.URL.Path != "/inApps/v1/subscriptions/1000" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(statusBody))
	}))
	defer sandbox.Close()

	a := NewAPI(config.AppStoreConfig{BundleID: testBundle, IssuerID: "iss", KeyID: "KID", PrivateKey: p8}, nil)
	a.prodURL, a.sandboxURL = prod.URL, sandbox.URL
	status, stx, srn, err := a.SubscriptionStatus(context.Background(), "1000")
	if err != nil {
		t.Fatal(err)
	}
	if status != 1 || stx != "stx" || srn != "srn" {
		t.Fatalf("got %d %q %q", status, stx, srn)
	}
	if prodHits != 1 || sandboxHits != 1 {
		t.Fatalf("hits prod=%d sandbox=%d", prodHits, sandboxHits)
	}
}

func TestAPI_JWTClaims(t *testing.T) {
	p8, pub := testP8(t)
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(statusBody))
	}))
	defer srv.Close()

	a := NewAPI(config.AppStoreConfig{BundleID: testBundle, IssuerID: "iss", KeyID: "KID", PrivateKey: p8}, srv.Client())
	a.prodURL = srv.URL
	if _, _, _, err := a.SubscriptionStatus(context.Background(), "1000"); err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimPrefix(auth, "Bearer ")
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return pub, nil },
		jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Header["kid"] != "KID" {
		t.Errorf("kid = %v", tok.Header["kid"])
	}
	if claims["aud"] != "appstoreconnect-v1" || claims["bid"] != testBundle || claims["iss"] != "iss" {
		t.Errorf("claims = %v", claims)
	}
	iat, _ := claims["iat"].(float64)
	exp, _ := claims["exp"].(float64)
	if exp-iat != 20*60 {
		t.Errorf("exp-iat = %v", exp-iat)
	}
}

func TestAPI_NotFoundEverywhereIsError(t *testing.T) {
	p8, _ := testP8(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	a := NewAPI(config.AppStoreConfig{PrivateKey: p8}, nil)
	a.prodURL, a.sandboxURL = srv.URL, srv.URL
	if _, _, _, err := a.SubscriptionStatus(context.Background(), "1000"); err == nil {
		t.Fatal("expected error")
	}
}
