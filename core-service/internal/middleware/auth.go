package middleware

import (
	"crypto/rsa"
	"fmt"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/auth"
	"github.com/makeasinger/api/pkg/response"
)

// UserClaims is an alias for auth.LegacyClaims for backwards compatibility
type UserClaims = auth.LegacyClaims

// AuthMiddleware handles JWT authentication
type AuthMiddleware struct {
	op        *opDogrulayici // kendi OP'umuzun jetonlari (en oncelikli mod)
	verifier  auth.TokenVerifier
	jwtSecret string // fallback for legacy tokens
}

// opDogrulayici, kendi OP'umuzun urettigi RS256 access token'ini
// dogrular. Acik anahtar surec icinden gelir; kendi JWKS ucumuza ag
// uzerinden gitmek servisi kendi baslangicina bagimli kilardi
// (yumurta-tavuk: API kendi OP'sinin ayakta olmasini beklerdi).
type opDogrulayici struct {
	issuer string
	acik   *rsa.PublicKey
	// istemci: "aud" claim'i bu client ID'yi icermek ZORUNDA. Bos
	// birakilamaz; bkz. kurulumHatasi.
	istemci string
	// kurulumHatasi doluysa hicbir jeton kabul edilmez. Eksik
	// yapilandirma sessizce "denetim kapali" moduna DUSMEMELI: iki
	// ayri constructor'in yan yana durdugu onceki tasarimda yanlis
	// cagri yeri secimi aud denetimini sessizce dusurebiliyordu ve
	// hicbir test kirilmiyordu.
	kurulumHatasi error
}

// NewOPAuthMiddleware, kendi OP'umuzun access token'ini dogrulayan
// middleware'i kurar. Access token'lar bizim client'a hedefli oldugu
// icin "aud" denetimi zorunludur: istemci (client ID) bos birakilamaz.
// Bos/eksik yapilandirmada dogrulayici sessizce gevsemez, HER istegi
// reddeder (imza *AuthMiddleware donduruyor, hata donduremiyor; guvenli
// varsayilan "kapali" olmak).
func NewOPAuthMiddleware(issuer, istemci string, acik *rsa.PublicKey) *AuthMiddleware {
	d := &opDogrulayici{issuer: issuer, istemci: istemci, acik: acik}
	switch {
	case issuer == "":
		d.kurulumHatasi = fmt.Errorf("OP auth yapilandirmasi eksik: issuer bos")
	case istemci == "":
		d.kurulumHatasi = fmt.Errorf("OP auth yapilandirmasi eksik: istemci (client ID) bos")
	case acik == nil:
		d.kurulumHatasi = fmt.Errorf("OP auth yapilandirmasi eksik: acik anahtar nil")
	}
	if d.kurulumHatasi != nil {
		// Hata metni sir tasimaz (yalnizca hangi alanin bos oldugunu
		// soyler), bu yuzden loglanabilir ve loglanmasi gerekir.
		log.Printf("Hata: %v — tum /api/* istekleri reddedilecek", d.kurulumHatasi)
	}
	return &AuthMiddleware{op: d}
}

// dogrula, jetonu dogrular ve (sub, email, name) dondurur.
func (d *opDogrulayici) dogrula(jetonMetni string) (string, string, string, error) {
	if d.kurulumHatasi != nil {
		return "", "", "", d.kurulumHatasi
	}
	ek := jwt.MapClaims{}
	secenekler := []jwt.ParserOption{
		// Imza algoritmasi ACIKCA kisitlanir: aksi halde "alg: none"
		// veya HMAC'e dusurme saldirisi mumkun olur.
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(d.issuer),
		jwt.WithExpirationRequired(),
		// iat varsa gelecekte olmamali (nbf zaten her zaman denetlenir).
		jwt.WithIssuedAt(),
		// "aud" bizim client ID'mizi icermek zorunda: baska bir istemci
		// icin uretilmis jeton API'mize girmemeli. Kosulsuz uygulanir.
		jwt.WithAudience(d.istemci),
	}
	jeton, err := jwt.ParseWithClaims(jetonMetni, ek,
		func(t *jwt.Token) (any, error) {
			// Kutuphanenin alg'i jetondan okumasina guvenmeyip beklenen
			// yontemi burada da zorluyoruz (iki kath savunma).
			if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
				return nil, fmt.Errorf("beklenmeyen imza yontemi: %v", t.Header["alg"])
			}
			// Savunma derinligi: nil anahtar crypto/rsa icinde PANIGE
			// yol acar. kurulumHatasi sayesinde buraya nil ile
			// gelinmemesi gerekir, yine de kontrol ediyoruz.
			if d.acik == nil {
				return nil, fmt.Errorf("acik anahtar nil")
			}
			return d.acik, nil
		}, secenekler...)
	if err != nil {
		return "", "", "", err
	}
	if !jeton.Valid {
		return "", "", "", fmt.Errorf("jeton gecersiz")
	}
	// Jetonun KULLANIM TURU denetlenir: id_token bir access token
	// DEGILDIR ve API erisimi vermemeli. Ikisi de ayni anahtarla,
	// ayni iss ve aud ile imzalandigi icin yukaridaki denetimlerin
	// hicbiri ikisini ayirt etmez. Ayirt edici claim'ler
	// (zitadel/oidc v3.51.8, pkg/oidc/token.go):
	//   access token: jti VAR, azp/at_hash YOK   (NewAccessTokenClaims)
	//   id_token    : azp VAR (+at_hash), jti YOK (NewIDTokenClaims)
	// Somut etki: id_token omru 1 saat, access token 15 dakika; ayrica
	// id_token istemcide daha gevsek tasinir (cache, analytics, crash
	// log) ve OP tarafinda kaydi tutulmaz.
	if jti, _ := ek["jti"].(string); strings.TrimSpace(jti) == "" {
		return "", "", "", fmt.Errorf("jti claim'i yok: access token degil")
	}
	if _, varmi := ek["azp"]; varmi {
		return "", "", "", fmt.Errorf("azp claim'i var: id_token access token olarak kullanilamaz")
	}
	if _, varmi := ek["at_hash"]; varmi {
		return "", "", "", fmt.Errorf("at_hash claim'i var: id_token access token olarak kullanilamaz")
	}

	sub, _ := ek["sub"].(string)
	// Yalnizca "" degil, bosluktan olusan sub da reddedilir: aksi halde
	// userId "   " olur ve rate limit anahtari anlamsizlasir.
	if strings.TrimSpace(sub) == "" {
		return "", "", "", fmt.Errorf("sub claim'i bos")
	}
	eposta, _ := ek["email"].(string)
	ad, _ := ek["name"].(string)
	return sub, eposta, ad, nil
}

// NewAuthMiddleware creates a new auth middleware with Zitadel JWKS verification
func NewAuthMiddleware(verifier auth.TokenVerifier) *AuthMiddleware {
	return &AuthMiddleware{
		verifier: verifier,
	}
}

// NewAuthMiddlewareWithFallback creates auth middleware with both JWKS and legacy HMAC support
func NewAuthMiddlewareWithFallback(verifier auth.TokenVerifier, jwtSecret string) *AuthMiddleware {
	return &AuthMiddleware{
		verifier:  verifier,
		jwtSecret: jwtSecret,
	}
}

// NewLegacyAuthMiddleware creates auth middleware using only HMAC signing (for testing/dev)
func NewLegacyAuthMiddleware(jwtSecret string) *AuthMiddleware {
	return &AuthMiddleware{
		jwtSecret: jwtSecret,
	}
}

// Authenticate validates JWT token from Authorization header
func (m *AuthMiddleware) Authenticate() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return response.Unauthorized(c, "Missing authorization header")
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return response.Unauthorized(c, "Invalid authorization header format")
		}

		tokenString := parts[1]

		// Kendi OP'umuz yapilandirilmissa yalnizca o gecerlidir: burada
		// basarisizlik dogrudan 401'dir, legacy/gateway yoluna dusulmez.
		if m.op != nil {
			sub, eposta, ad, err := m.op.dogrula(tokenString)
			if err != nil {
				// Hata metni jeton icerigi tasiyabilir; loglanmaz.
				return response.Unauthorized(c, "Invalid or expired token")
			}
			c.Locals("userId", sub)
			c.Locals("email", eposta)
			c.Locals("name", ad)
			return c.Next()
		}

		// Try Zitadel JWKS verification first
		if m.verifier != nil {
			claims, err := m.verifier.Validate(tokenString)
			if err == nil {
				c.Locals("userId", claims.UserID)
				c.Locals("email", claims.Email)
				c.Locals("name", claims.Name)
				c.Locals("claims", claims)
				return c.Next()
			}
			// If JWKS verification fails and no fallback, return error
			if m.jwtSecret == "" {
				return response.Unauthorized(c, "Invalid or expired token")
			}
		}

		// Fallback to legacy HMAC verification
		if m.jwtSecret != "" {
			claims, err := m.validateLegacyToken(tokenString)
			if err != nil {
				return response.Unauthorized(c, "Invalid or expired token")
			}

			c.Locals("userId", claims.UserID)
			c.Locals("email", claims.Email)
			c.Locals("claims", claims)
			return c.Next()
		}

		return response.Unauthorized(c, "Authentication not configured")
	}
}

// validateLegacyToken validates a token using HMAC signing
func (m *AuthMiddleware) validateLegacyToken(tokenString string) (*UserClaims, error) {
	return auth.ValidateLegacyToken(tokenString, m.jwtSecret)
}

// GetUserID extracts user ID from context
func GetUserID(c *fiber.Ctx) string {
	if userID, ok := c.Locals("userId").(string); ok {
		return userID
	}
	return ""
}

// GetUserEmail extracts user email from context
func GetUserEmail(c *fiber.Ctx) string {
	if email, ok := c.Locals("email").(string); ok {
		return email
	}
	return ""
}

// GetUserName extracts user name from context
func GetUserName(c *fiber.Ctx) string {
	if name, ok := c.Locals("name").(string); ok {
		return name
	}
	return ""
}

// GenerateToken creates a new legacy JWT token (useful for testing)
func (m *AuthMiddleware) GenerateToken(userID, email string) (string, error) {
	if m.jwtSecret == "" {
		return "", jwt.ErrTokenNotValidYet
	}

	claims := UserClaims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "makeasinger-api",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.jwtSecret))
}
