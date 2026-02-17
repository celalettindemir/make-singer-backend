package auth

import (
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
