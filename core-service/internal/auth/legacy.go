package auth

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

// LegacyClaims represents legacy JWT claims (HMAC-signed tokens)
type LegacyClaims struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// ValidateLegacyToken validates a token using HMAC signing
func ValidateLegacyToken(tokenString, secret string) (*LegacyClaims, error) {
	// Bos sir ile dogrulama YAPILMAZ. Keyfunc kosulsuz []byte(secret)
	// donduruyor; bos sir gecildiginde, bos sirla imzalanmis bir HS256
	// jetonu gecerli sayilirdi. Bugun cagri yerlerindeki
	// `jwtSecret != ""` kapilari koruyor (handler/auth_handler.go,
	// middleware/auth.go) ama tek kapi kirilgandir: bir refactor o
	// kapiyi kaldirirsa acik geri doner. Savunma derinligi olarak kapi
	// fonksiyonun kendisinde de var.
	if secret == "" {
		return nil, errors.New("legacy dogrulama icin sir yapilandirilmamis")
	}
	token, err := jwt.ParseWithClaims(tokenString, &LegacyClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*LegacyClaims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}

	return claims, nil
}
