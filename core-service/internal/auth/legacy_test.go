package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Bos sir ile dogrulama yapilmamali. Keyfunc kosulsuz []byte(secret)
// donduruyor, yani bu kapi olmadan bos sirla imzalanmis bir HS256 jetonu
// GECERLI sayilirdi. Cagri yerlerindeki `jwtSecret != ""` kapilari da
// koruyor ama tek kapi kirilgan: savunma derinligi.
func TestValidateLegacyTokenBosSirReddedilir(t *testing.T) {
	jeton, err := jwt.NewWithClaims(jwt.SigningMethodHS256, LegacyClaims{
		UserID: "saldirgan",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString([]byte(""))
	if err != nil {
		t.Fatalf("jeton uretilemedi: %v", err)
	}
	if talepler, err := ValidateLegacyToken(jeton, ""); err == nil {
		t.Errorf("bos sir kabul edildi, talepler = %+v", talepler)
	}
}

// Dolu sirla mutlu yol calismaya devam etmeli (kapinin fazla genis
// olmadigini dogrular).
func TestValidateLegacyTokenDoluSirCalisir(t *testing.T) {
	const sir = "test-sirri-yalnizca-bellekte"
	jeton, err := jwt.NewWithClaims(jwt.SigningMethodHS256, LegacyClaims{
		UserID: "kullanici-1",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString([]byte(sir))
	if err != nil {
		t.Fatalf("jeton uretilemedi: %v", err)
	}
	talepler, err := ValidateLegacyToken(jeton, sir)
	if err != nil {
		t.Fatalf("dolu sir reddedildi: %v", err)
	}
	if talepler.UserID != "kullanici-1" {
		t.Errorf("UserID = %q, beklenen kullanici-1", talepler.UserID)
	}
}
