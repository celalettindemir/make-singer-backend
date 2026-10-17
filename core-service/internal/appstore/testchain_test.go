package appstore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"math/big"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type testChain struct {
	rootCert *x509.Certificate
	rootDER  []byte
	interDER []byte
	leafDER  []byte
	leafKey  *ecdsa.PrivateKey
}

type chainOpts struct {
	leafMarker    bool
	noInterMarker bool
	leafNotAfter  time.Time
}

func newTestChain(t *testing.T, now time.Time, o chainOpts) *testChain {
	t.Helper()
	gen := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	marker := func(oid asn1.ObjectIdentifier) pkix.Extension {
		return pkix.Extension{Id: oid, Value: []byte{0x05, 0x00}}
	}
	mk := func(serial int64, cn string, pub *ecdsa.PublicKey, parent *x509.Certificate, signer *ecdsa.PrivateKey, ca bool, notAfter time.Time, ext []pkix.Extension) (*x509.Certificate, []byte) {
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(serial),
			Subject:               pkix.Name{CommonName: cn},
			NotBefore:             now.Add(-24 * time.Hour),
			NotAfter:              notAfter,
			BasicConstraintsValid: true,
			IsCA:                  ca,
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			ExtraExtensions:       ext,
		}
		if parent == nil {
			parent = tmpl
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
		if err != nil {
			t.Fatal(err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return c, der
	}
	far := now.Add(10 * 365 * 24 * time.Hour)
	rootKey, interKey, leafKey := gen(), gen(), gen()
	root, rootDER := mk(1, "Test Root", &rootKey.PublicKey, nil, rootKey, true, far, nil)
	var interExt []pkix.Extension
	if !o.noInterMarker {
		interExt = []pkix.Extension{marker(oidIntermediateMarker)}
	}
	inter, interDER := mk(2, "Test Intermediate", &interKey.PublicKey, root, rootKey, true, far, interExt)
	var leafExt []pkix.Extension
	if o.leafMarker {
		leafExt = []pkix.Extension{marker(oidLeafMarker)}
	}
	na := o.leafNotAfter
	if na.IsZero() {
		na = far
	}
	_, leafDER := mk(3, "Test Leaf", &leafKey.PublicKey, inter, interKey, false, na, leafExt)
	return &testChain{rootCert: root, rootDER: rootDER, interDER: interDER, leafDER: leafDER, leafKey: leafKey}
}

func (c *testChain) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.rootCert)
	return p
}

func (c *testChain) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["x5c"] = []string{
		base64.StdEncoding.EncodeToString(c.leafDER),
		base64.StdEncoding.EncodeToString(c.interDER),
		base64.StdEncoding.EncodeToString(c.rootDER),
	}
	s, err := tok.SignedString(c.leafKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
