package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/config"
)

// TokenVerifier defines the interface for JWT token verification
type TokenVerifier interface {
	Validate(tokenString string) (*Claims, error)
	Close() error
}

// Claims represents the JWT claims from Zitadel
type Claims struct {
	UserID            string   `json:"sub"`
	Email             string   `json:"email,omitempty"`
	EmailVerified     bool     `json:"email_verified,omitempty"`
	Name              string   `json:"name,omitempty"`
	PreferredUsername string   `json:"preferred_username,omitempty"`
	Roles             []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

// JWKSVerifier implements TokenVerifier using JWKS
type JWKSVerifier struct {
	jwks     keyfunc.Keyfunc
	issuer   string
	audience string
}

// NewJWKSVerifier creates a new JWKS-based token verifier for Zitadel
func NewJWKSVerifier(cfg *config.ZitadelConfig) (*JWKSVerifier, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("zitadel issuer is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	jwksURL, err := discoverJWKSURL(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to discover JWKS URL: %w", err)
	}

	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS keyfunc: %w", err)
	}

	return &JWKSVerifier{
		jwks:     jwks,
		issuer:   cfg.Issuer,
		audience: cfg.ClientID,
	}, nil
}

// discoverJWKSURL fetches the OIDC discovery document and extracts the jwks_uri.
func discoverJWKSURL(ctx context.Context, issuer string) (string, error) {
	discoveryURL := fmt.Sprintf("%s/.well-known/openid-configuration", issuer)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create discovery request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch discovery document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery endpoint returned status %d", resp.StatusCode)
	}

	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("failed to decode discovery document: %w", err)
	}

	if doc.JWKSURI == "" {
		return "", fmt.Errorf("jwks_uri not found in discovery document")
	}

	return doc.JWKSURI, nil
}

// Validate validates a JWT token and returns the claims
func (v *JWKSVerifier) Validate(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, v.jwks.Keyfunc,
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Validate audience if configured
	if v.audience != "" {
		aud, err := claims.GetAudience()
		if err != nil {
			return nil, fmt.Errorf("failed to get audience: %w", err)
		}
		if !contains(aud, v.audience) {
			return nil, fmt.Errorf("invalid audience")
		}
	}

	return claims, nil
}

// Close releases resources used by the verifier
func (v *JWKSVerifier) Close() error {
	// keyfunc.Keyfunc is managed internally; no explicit cleanup needed
	return nil
}

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
