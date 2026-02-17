package middleware

import (
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
	verifier  auth.TokenVerifier
	jwtSecret string // fallback for legacy tokens
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
