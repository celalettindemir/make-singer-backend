package kimlik

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// saglayiciApple, saglayiciGoogle: YukariAkis.Ad alaninda kullanilan
// sabitler. Bare string yerine bunlar kullanilir ki saglayici adi
// yazim hatasina karsi derleme zamaninda (en azindan tek bir tanim
// yerinde) yakalanabilsin.
const (
	saglayiciApple  = "apple"
	saglayiciGoogle = "google"
)

// appleIstemciSirOmru: K5. Her degisimde yeniden uretildigi icin kisa
// tutulur; Apple ustten 6 ay siniri koyar.
const appleIstemciSirOmru = 5 * time.Minute

// appleIstemciSir, Apple'in client_secret olarak bekledigi ES256 JWT'yi
// uretir. simdi parametre: test zamani sabitleyebilsin.
func appleIstemciSir(ekipID, anahtarID, istemciID, p8PEM string, simdi time.Time) (string, error) {
	blok, _ := pem.Decode([]byte(p8PEM))
	if blok == nil {
		return "", fmt.Errorf("apple .p8 anahtari PEM olarak cozumlenemedi")
	}
	ham, err := x509.ParsePKCS8PrivateKey(blok.Bytes)
	if err != nil {
		return "", fmt.Errorf("apple .p8 anahtari okunamadi: %w", err)
	}
	anahtar, ok := ham.(*ecdsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("apple anahtari ECDSA degil (%T); ES256 imzalanamaz", ham)
	}
	jeton := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": ekipID,
		"iat": simdi.Unix(),
		"exp": simdi.Add(appleIstemciSirOmru).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": istemciID,
	})
	jeton.Header["kid"] = anahtarID
	return jeton.SignedString(anahtar)
}
