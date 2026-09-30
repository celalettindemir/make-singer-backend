package middleware

import (
	"crypto/rsa"
	"fmt"
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
	// istemci bos degilse "aud" claim'i bu client ID'yi icermek zorunda.
	istemci string
}

// NewOPAuthMiddleware, kendi OP'umuzun access token'ini dogrulayan
// middleware'i kurar.
func NewOPAuthMiddleware(issuer string, acik *rsa.PublicKey) *AuthMiddleware {
	return &AuthMiddleware{op: &opDogrulayici{issuer: issuer, acik: acik}}
}

// NewOPAuthMiddlewareIleIstemci, ek olarak "aud" claim'inin bizim client
// ID'mizi icermesini de sart kosar. Uretimde bu varyant kullanilir:
// baska bir istemci icin uretilmis jeton API'mize girmemeli.
func NewOPAuthMiddlewareIleIstemci(issuer, istemci string, acik *rsa.PublicKey) *AuthMiddleware {
	return &AuthMiddleware{op: &opDogrulayici{issuer: issuer, acik: acik, istemci: istemci}}
}

// dogrula, jetonu dogrular ve (sub, email, name) dondurur.
func (d *opDogrulayici) dogrula(jetonMetni string) (string, string, string, error) {
	ek := jwt.MapClaims{}
	secenekler := []jwt.ParserOption{
		// Imza algoritmasi ACIKCA kisitlanir: aksi halde "alg: none"
		// veya HMAC'e dusurme saldirisi mumkun olur.
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(d.issuer),
		jwt.WithExpirationRequired(),
		// iat varsa gelecekte olmamali (nbf zaten her zaman denetlenir).
		jwt.WithIssuedAt(),
	}
	if d.istemci != "" {
		secenekler = append(secenekler, jwt.WithAudience(d.istemci))
	}
	jeton, err := jwt.ParseWithClaims(jetonMetni, ek,
		func(t *jwt.Token) (any, error) {
			// Kutuphanenin alg'i jetondan okumasina guvenmeyip beklenen
			// yontemi burada da zorluyoruz (iki kath savunma).
			if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
				return nil, fmt.Errorf("beklenmeyen imza yontemi: %v", t.Header["alg"])
			}
			return d.acik, nil
		}, secenekler...)
	if err != nil {
		return "", "", "", err
	}
	if !jeton.Valid {
		return "", "", "", fmt.Errorf("jeton gecersiz")
	}
	sub, _ := ek["sub"].(string)
	if sub == "" {
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
