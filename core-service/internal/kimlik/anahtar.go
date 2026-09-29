package kimlik

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// Anahtar, RS256 imzalama anahtarini tasir ve op paketinin hem
// SigningKey hem Key arayuzunu karsilar.
type Anahtar struct {
	ozel *rsa.PrivateKey
	kid  string
}

// AnahtarYukle, PEM kodlu RSA ozel anahtarini okur. PKCS#1 ve PKCS#8
// formatlarinin ikisi de kabul edilir; hangi formatta uretildigi
// isletim tarafina gore degisir.
func AnahtarYukle(pemMetni string) (*Anahtar, error) {
	blok, _ := pem.Decode([]byte(pemMetni))
	if blok == nil || len(blok.Bytes) == 0 {
		return nil, fmt.Errorf("gecerli bir PEM blogu bulunamadi")
	}
	var ozel *rsa.PrivateKey
	if a, err := x509.ParsePKCS1PrivateKey(blok.Bytes); err == nil {
		ozel = a
	} else {
		herhangi, err8 := x509.ParsePKCS8PrivateKey(blok.Bytes)
		if err8 != nil {
			return nil, fmt.Errorf("RSA ozel anahtari cozulemedi (PKCS#1: %v, PKCS#8: %w)", err, err8)
		}
		rsaAnahtar, uygun := herhangi.(*rsa.PrivateKey)
		if !uygun {
			return nil, fmt.Errorf("anahtar RSA degil: %T", herhangi)
		}
		ozel = rsaAnahtar
	}
	if ozel.N.BitLen() < 2048 {
		return nil, fmt.Errorf("anahtar %d bit, en az 2048 olmali", ozel.N.BitLen())
	}
	return &Anahtar{ozel: ozel, kid: kidUret(&ozel.PublicKey)}, nil
}

// kidUret, kid'i acik anahtarin icerigine baglar. Rastgele bir kid her
// yeniden baslatmada degisir ve istemcilerin onbellekledigi JWKS'i
// gecersiz kilar.
func kidUret(acik *rsa.PublicKey) string {
	turetilmis, err := x509.MarshalPKIXPublicKey(acik)
	if err != nil {
		// MarshalPKIXPublicKey RSA icin hata vermez; yine de sessiz
		// kalmamak icin modulus'e duseriz.
		turetilmis = acik.N.Bytes()
	}
	ozet := sha256.Sum256(turetilmis)
	return base64.RawURLEncoding.EncodeToString(ozet[:8])
}

func (a *Anahtar) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (a *Anahtar) ID() string                                  { return a.kid }

// Key, op.SigningKey icin ozel anahtari dondurur. op paketi SigningKey'i
// imzalarken kullanir; AcikAnahtar().Key() ise acik anahtari JWKS yayini
// icin dondurur.
func (a *Anahtar) Key() any { return a.ozel }

// AcikAnahtar, JWKS yayini icin op.Key olarak kullanilacak sarmalayiciyi
// dondurur.
func (a *Anahtar) AcikAnahtar() *AcikAnahtarKey { return &AcikAnahtarKey{a: a} }

type AcikAnahtarKey struct{ a *Anahtar }

func (k *AcikAnahtarKey) ID() string                         { return k.a.kid }
func (k *AcikAnahtarKey) Algorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k *AcikAnahtarKey) Use() string                        { return "sig" }
func (k *AcikAnahtarKey) Key() any                           { return &k.a.ozel.PublicKey }

// Tiklamak ve derleme zamanında arayuz uyumunu kontrol et
var (
	_ op.SigningKey = (*Anahtar)(nil)
	_ op.Key        = (*AcikAnahtarKey)(nil)
)

// CryptoAnahtar, op.Config.CryptoKey icin tam 32 bayt dondurur. Kisa bir
// sir sessizce doldurulmaz: sifreleme zayiflar ve bu sessizce olur.
func CryptoAnahtar(s string) ([32]byte, error) {
	var b [32]byte
	if len(s) != 32 {
		return b, fmt.Errorf("crypto anahtari %d bayt, tam 32 olmali", len(s))
	}
	copy(b[:], s)
	return b, nil
}
