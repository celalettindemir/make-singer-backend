package googleplay

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testAud   = "https://api.example.com/push"
	testEmail = "pubsub@proj.iam.gserviceaccount.com"
)

type pushFixture struct {
	key *rsa.PrivateKey
	v   *PushVerifier
}

func newPushFixture(t *testing.T) *pushFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	v, err := NewPushVerifier(ctx, srv.URL, testAud, testEmail)
	if err != nil {
		t.Fatal(err)
	}
	return &pushFixture{key: key, v: v}
}

func (f *pushFixture) token(t *testing.T, aud, email string, verified bool, iss string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": iss, "aud": aud, "email": email, "email_verified": verified,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Minute).Unix(), "sub": "123",
	})
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPushVerifier_Valid(t *testing.T) {
	f := newPushFixture(t)
	for _, iss := range []string{"accounts.google.com", "https://accounts.google.com"} {
		if err := f.v.Verify("Bearer " + f.token(t, testAud, testEmail, true, iss)); err != nil {
			t.Fatalf("iss %s: %v", iss, err)
		}
	}
}

func TestPushVerifier_WrongAudience(t *testing.T) {
	f := newPushFixture(t)
	err := f.v.Verify("Bearer " + f.token(t, "other", testEmail, true, "accounts.google.com"))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestPushVerifier_WrongEmail(t *testing.T) {
	f := newPushFixture(t)
	for name, tok := range map[string]string{
		"email":      f.token(t, testAud, "evil@x.com", true, "accounts.google.com"),
		"unverified": f.token(t, testAud, testEmail, false, "accounts.google.com"),
		"issuer":     f.token(t, testAud, testEmail, true, "https://evil.example"),
	} {
		if err := f.v.Verify("Bearer " + tok); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestPushVerifier_Missing(t *testing.T) {
	f := newPushFixture(t)
	for _, h := range []string{"", "Bearer ", "Basic abc", "Bearer garbage"} {
		if err := f.v.Verify(h); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%q: got %v", h, err)
		}
	}
}

func TestPushVerifier_WrongKey(t *testing.T) {
	f := newPushFixture(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	g := &pushFixture{key: other, v: f.v}
	if err := f.v.Verify("Bearer " + g.token(t, testAud, testEmail, true, "accounts.google.com")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func pushBody(t *testing.T, payload string) []byte {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"message":      map[string]string{"data": base64.StdEncoding.EncodeToString([]byte(payload)), "messageId": "1"},
		"subscription": "projects/p/subscriptions/s",
	})
	return b
}

func TestDecodePush_AllThreeKinds(t *testing.T) {
	n, err := DecodePush(pushBody(t, `{"packageName":"com.x","subscriptionNotification":{"notificationType":4,"purchaseToken":"t1","subscriptionId":"s1"}}`))
	if err != nil || n.PackageName != "com.x" || n.Subscription == nil || n.Subscription.NotificationType != 4 ||
		n.Subscription.PurchaseToken != "t1" || n.Subscription.SubscriptionID != "s1" || n.OneTime != nil || n.Voided != nil {
		t.Fatalf("sub: %+v %v", n, err)
	}
	n, err = DecodePush(pushBody(t, `{"packageName":"com.x","oneTimeProductNotification":{"notificationType":1,"purchaseToken":"t2","sku":"pack"}}`))
	if err != nil || n.OneTime == nil || n.OneTime.SKU != "pack" || n.OneTime.PurchaseToken != "t2" || n.Subscription != nil {
		t.Fatalf("one: %+v %v", n, err)
	}
	n, err = DecodePush(pushBody(t, `{"packageName":"com.x","voidedPurchaseNotification":{"purchaseToken":"t3","orderId":"GPA.9","productType":2}}`))
	if err != nil || n.Voided == nil || n.Voided.OrderID != "GPA.9" || n.Voided.ProductType != 2 || n.Voided.PurchaseToken != "t3" {
		t.Fatalf("void: %+v %v", n, err)
	}
	n, err = DecodePush(pushBody(t, `{"packageName":"com.x","testNotification":{"version":"1.0"}}`))
	if err != nil || !n.Test {
		t.Fatalf("test: %+v %v", n, err)
	}
}

func TestDecodePush_Malformed(t *testing.T) {
	for _, b := range [][]byte{[]byte(`nope`), []byte(`{"message":{}}`), []byte(`{"message":{"data":"!!!"}}`), pushBody(t, `not json`)} {
		if _, err := DecodePush(b); err == nil {
			t.Fatalf("expected error for %s", b)
		}
	}
}
