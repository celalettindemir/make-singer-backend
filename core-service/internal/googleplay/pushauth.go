package googleplay

import (
	"context"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// PushVerifier verifies the OIDC bearer token Pub/Sub attaches to push requests.
type PushVerifier struct {
	jwks     keyfunc.Keyfunc
	audience string
	email    string
}

type pushClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	jwt.RegisteredClaims
}

// NewPushVerifier builds a verifier. ctx must live as long as the verifier: it
// bounds the JWKS background refresh (see auth.NewUpstreamVerifier).
func NewPushVerifier(ctx context.Context, jwksURL, audience, email string) (*PushVerifier, error) {
	if jwksURL == "" || audience == "" || email == "" {
		return nil, fmt.Errorf("googleplay: jwksURL, audience and email are required")
	}
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("googleplay: could not set up JWKS: %w", err)
	}
	return &PushVerifier{jwks: jwks, audience: audience, email: email}, nil
}

// Verify checks an "Authorization: Bearer <jwt>" header. Any failure returns
// ErrUnauthorized.
func (v *PushVerifier) Verify(authorizationHeader string) error {
	const prefix = "bearer "
	h := strings.TrimSpace(authorizationHeader)
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ErrUnauthorized
	}
	claims := &pushClaims{}
	_, err := jwt.ParseWithClaims(strings.TrimSpace(h[len(prefix):]), claims, v.jwks.Keyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return ErrUnauthorized
	}
	if claims.Issuer != "accounts.google.com" && claims.Issuer != "https://accounts.google.com" {
		return ErrUnauthorized
	}
	if !claims.EmailVerified || claims.Email != v.email {
		return ErrUnauthorized
	}
	return nil
}
