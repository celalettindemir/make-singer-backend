package appstore

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testBundle = "com.example.app"

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return testNow }

func txClaims(bundle, env string) jwt.MapClaims {
	return jwt.MapClaims{
		"transactionId":         "2000",
		"originalTransactionId": "1000",
		"productId":             "com.example.app.plan.monthly",
		"bundleId":              bundle,
		"type":                  "Auto-Renewable Subscription",
		"environment":           env,
		"appAccountToken":       "11111111-2222-3333-4444-555555555555",
		"purchaseDate":          testNow.UnixMilli(),
		"expiresDate":           testNow.Add(30 * 24 * time.Hour).UnixMilli(),
	}
}

func TestVerifier_ValidTransaction(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	tx, err := v.Transaction(c.sign(t, txClaims(testBundle, "Production")))
	if err != nil {
		t.Fatal(err)
	}
	if tx.OriginalTransactionID != "1000" || tx.TransactionID != "2000" || tx.AppAccountToken == "" {
		t.Fatalf("unexpected tx: %+v", tx)
	}
	if tx.PurchaseDate == nil || !tx.PurchaseDate.Equal(testNow) {
		t.Fatalf("purchase date = %v", tx.PurchaseDate)
	}
	if tx.ExpiresDate == nil || tx.RevocationDate != nil {
		t.Fatalf("dates wrong: %+v", tx)
	}
}

func TestVerifier_WrongRoot(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	other := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(other.pool(), testBundle, fixedNow)
	_, err := v.Transaction(c.sign(t, txClaims(testBundle, "Production")))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestVerifier_MissingLeafOID(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: false})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	_, err := v.Transaction(c.sign(t, txClaims(testBundle, "Production")))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestVerifier_WrongBundle(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	_, err := v.Transaction(c.sign(t, txClaims("com.other.app", "Production")))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestVerifier_AlgNoneRejected(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, txClaims(testBundle, "Production"))
	s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Transaction(s); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestVerifier_TamperedPayload(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	good := c.sign(t, txClaims(testBundle, "Production"))
	other := c.sign(t, jwt.MapClaims{"bundleId": testBundle, "productId": "evil"})
	parts := strings.Split(good, ".")
	parts[1] = strings.Split(other, ".")[1]
	if _, err := v.Transaction(strings.Join(parts, ".")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestVerifier_ExpiredLeafAtSigningTime(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true, leafNotAfter: testNow.Add(time.Hour)})
	s := c.sign(t, txClaims(testBundle, "Production"))
	if _, err := NewVerifier(c.pool(), testBundle, fixedNow).Transaction(s); err != nil {
		t.Fatalf("valid before expiry: %v", err)
	}
	late := func() time.Time { return testNow.Add(2 * time.Hour) }
	if _, err := NewVerifier(c.pool(), testBundle, late).Transaction(s); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestVerifier_SandboxAccepted(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	tx, err := v.Transaction(c.sign(t, txClaims(testBundle, "Sandbox")))
	if err != nil {
		t.Fatal(err)
	}
	if tx.Environment != "Sandbox" {
		t.Fatalf("environment = %q", tx.Environment)
	}
}

func TestVerifier_NotificationUnwrapsData(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	claims := jwt.MapClaims{
		"notificationType": "DID_RENEW",
		"subtype":          "BILLING_RECOVERY",
		"notificationUUID": "uuid-1",
		"data": map[string]any{
			"environment":           "Sandbox",
			"bundleId":              testBundle,
			"signedTransactionInfo": "stx",
			"signedRenewalInfo":     "srn",
		},
	}
	n, err := v.Notification(c.sign(t, claims))
	if err != nil {
		t.Fatal(err)
	}
	want := Notification{Type: "DID_RENEW", Subtype: "BILLING_RECOVERY", UUID: "uuid-1", Environment: "Sandbox",
		BundleID: testBundle, SignedTransactionInfo: "stx", SignedRenewalInfo: "srn"}
	if *n != want {
		t.Fatalf("got %+v, want %+v", *n, want)
	}

	claims["data"].(map[string]any)["bundleId"] = "com.other.app"
	if _, err := v.Notification(c.sign(t, claims)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong bundle err = %v, want ErrInvalid", err)
	}
}

func TestVerifier_Renewal(t *testing.T) {
	c := newTestChain(t, testNow, chainOpts{leafMarker: true})
	v := NewVerifier(c.pool(), testBundle, fixedNow)
	grace := testNow.Add(16 * 24 * time.Hour).UnixMilli()
	r, err := v.Renewal(c.sign(t, jwt.MapClaims{"autoRenewStatus": 1, "gracePeriodExpiresDate": grace}))
	if err != nil {
		t.Fatal(err)
	}
	if r.AutoRenewStatus != 1 || r.GracePeriodExpiresDate == nil || r.GracePeriodExpiresDate.UnixMilli() != grace {
		t.Fatalf("unexpected renewal: %+v", r)
	}
}

func TestAppleRoots_ParsesEmbeddedG3(t *testing.T) {
	subjects := AppleRoots().Subjects() //nolint:staticcheck // only way to inspect the pool
	if len(subjects) != 1 || !strings.Contains(string(subjects[0]), "Apple Root CA - G3") {
		t.Fatalf("unexpected subjects: %q", subjects)
	}
}
