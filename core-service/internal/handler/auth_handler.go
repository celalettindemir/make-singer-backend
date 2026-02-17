package handler

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/auth"
)

// AuthHandler handles ForwardAuth verification for the API gateway
type AuthHandler struct {
	verifier  auth.TokenVerifier
	jwtSecret string
}

// NewAuthHandler creates a new auth handler for ForwardAuth verification
func NewAuthHandler(verifier auth.TokenVerifier, jwtSecret string) *AuthHandler {
	return &AuthHandler{
		verifier:  verifier,
		jwtSecret: jwtSecret,
	}
}

// Verify handles GET /auth/verify — called by Traefik ForwardAuth.
// Returns 200 with X-User-* headers on success, 401 on failure.
func (h *AuthHandler) Verify(c *fiber.Ctx) error {
	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return c.SendStatus(fiber.StatusUnauthorized)
	}

	tokenString := parts[1]

	// Try Zitadel JWKS verification first
	if h.verifier != nil {
		claims, err := h.verifier.Validate(tokenString)
		if err == nil {
			c.Set("X-User-Id", claims.UserID)
			c.Set("X-User-Email", claims.Email)
			c.Set("X-User-Name", claims.Name)
			return c.SendStatus(fiber.StatusOK)
		}
		if h.jwtSecret == "" {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
	}

	// Fallback to legacy HMAC verification
	if h.jwtSecret != "" {
		claims, err := auth.ValidateLegacyToken(tokenString, h.jwtSecret)
		if err == nil {
			c.Set("X-User-Id", claims.UserID)
			c.Set("X-User-Email", claims.Email)
			return c.SendStatus(fiber.StatusOK)
		}
	}

	return c.SendStatus(fiber.StatusUnauthorized)
}
