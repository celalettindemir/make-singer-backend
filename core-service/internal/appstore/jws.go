// Package appstore verifies Apple StoreKit 2 / App Store Server
// Notification v2 JWS payloads and calls the App Store Server API.
package appstore

import (
	"crypto/ecdsa"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// appleRootG3 is Apple Root CA - G3 (DER), downloaded from
// https://www.apple.com/certificateauthority/AppleRootCA-G3.cer.
// SHA-256 fingerprint:
// 63:34:3A:BF:B8:9A:6A:03:EB:B5:7E:9B:3F:5F:A7:BE:7C:4F:5C:75:6F:30:17:B3:A8:C4:88:C3:65:3E:91:79
//
//go:embed AppleRootCA-G3.cer
var appleRootG3 []byte

// ErrInvalid is wrapped by every verification failure.
var ErrInvalid = errors.New("appstore: invalid signed payload")

var (
	// oidLeafMarker marks the Mac App Store receipt signing leaf certificate.
	oidLeafMarker = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 11, 1}
	// oidIntermediateMarker marks the WWDR intermediate certificate.
	oidIntermediateMarker = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 1}
)

// AppleRoots returns a pool holding the embedded Apple Root CA - G3.
func AppleRoots() *x509.CertPool {
	cert, err := x509.ParseCertificate(appleRootG3)
	if err != nil {
		panic("appstore: embedded Apple root certificate is corrupt: " + err.Error())
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool
}

// Verifier validates Apple JWS payloads against a trusted root pool.
type Verifier struct {
	roots    *x509.CertPool
	bundleID string
	now      func() time.Time
}

// NewVerifier builds a Verifier. now drives certificate validity checks.
func NewVerifier(roots *x509.CertPool, bundleID string, now func() time.Time) *Verifier {
	if now == nil {
		now = time.Now
	}
	return &Verifier{roots: roots, bundleID: bundleID, now: now}
}

// Transaction verifies a signed transaction and checks its bundle id.
func (v *Verifier) Transaction(jws string) (*Transaction, error) {
	var t Transaction
	if err := v.verify(jws, &t); err != nil {
		return nil, err
	}
	if t.BundleID != v.bundleID {
		return nil, fmt.Errorf("%w: bundle id mismatch", ErrInvalid)
	}
	return &t, nil
}

// Renewal verifies a signed renewal info payload.
func (v *Verifier) Renewal(jws string) (*RenewalInfo, error) {
	var r RenewalInfo
	if err := v.verify(jws, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Notification verifies a signed notification and checks data.bundleId.
func (v *Verifier) Notification(signedPayload string) (*Notification, error) {
	var w struct {
		NotificationType string `json:"notificationType"`
		Subtype          string `json:"subtype"`
		NotificationUUID string `json:"notificationUUID"`
		Data             struct {
			Environment           string `json:"environment"`
			BundleID              string `json:"bundleId"`
			SignedTransactionInfo string `json:"signedTransactionInfo"`
			SignedRenewalInfo     string `json:"signedRenewalInfo"`
		} `json:"data"`
	}
	if err := v.verify(signedPayload, &w); err != nil {
		return nil, err
	}
	if w.Data.BundleID != v.bundleID {
		return nil, fmt.Errorf("%w: bundle id mismatch", ErrInvalid)
	}
	return &Notification{
		Type:                  w.NotificationType,
		Subtype:               w.Subtype,
		UUID:                  w.NotificationUUID,
		Environment:           w.Data.Environment,
		BundleID:              w.Data.BundleID,
		SignedTransactionInfo: w.Data.SignedTransactionInfo,
		SignedRenewalInfo:     w.Data.SignedRenewalInfo,
	}, nil
}

// verify checks the x5c chain and ES256 signature, then decodes the payload
// into out.
func (v *Verifier) verify(jws string, out any) error {
	var claims jwt.MapClaims
	_, err := jwt.NewParser(jwt.WithValidMethods([]string{"ES256"})).
		ParseWithClaims(jws, &claims, v.keyFunc)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func (v *Verifier) keyFunc(tok *jwt.Token) (any, error) {
	x5c, ok := tok.Header["x5c"].([]any)
	if !ok || len(x5c) != 3 {
		return nil, errors.New("x5c must hold exactly 3 certificates")
	}
	certs := make([]*x509.Certificate, 3)
	for i, e := range x5c {
		s, ok := e.(string)
		if !ok {
			return nil, errors.New("x5c entry is not a string")
		}
		der, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("x5c entry: %w", err)
		}
		if certs[i], err = x509.ParseCertificate(der); err != nil {
			return nil, fmt.Errorf("x5c entry: %w", err)
		}
	}
	leaf, inter := certs[0], certs[1]
	if !hasExtension(leaf, oidLeafMarker) {
		return nil, errors.New("leaf certificate lacks the Apple marker extension")
	}
	if !hasExtension(inter, oidIntermediateMarker) {
		return nil, errors.New("intermediate certificate lacks the Apple marker extension")
	}
	intermediates := x509.NewCertPool()
	intermediates.AddCert(inter)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: intermediates,
		CurrentTime:   v.now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("certificate chain: %w", err)
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("leaf public key is not ECDSA")
	}
	return pub, nil
}

func hasExtension(c *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return true
		}
	}
	return false
}
