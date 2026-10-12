package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// FlexibleBool is a bool that may arrive in JSON as either true/false or
// "true"/"false". In some flows Apple sends email_verified as a STRING; code
// that only tries to decode it as a bool fails there and sign-in breaks.
type FlexibleBool bool

// UnmarshalJSON first tries a plain bool; if that fails it accepts the string
// "true"/"false". Any other value (a number, or a malformed string such as
// "maybe") is an EXPLICIT error: silently falling back to false would risk a
// malformed email_verified value being treated as verified.
func (e *FlexibleBool) UnmarshalJSON(b []byte) error {
	var parsed bool
	if err := json.Unmarshal(b, &parsed); err == nil {
		*e = FlexibleBool(parsed)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("email_verified ne bool ne string")
	}
	switch s {
	case "true":
		*e = true
	case "false":
		*e = false
	default:
		return fmt.Errorf("email_verified beklenmeyen string")
	}
	return nil
}

// Claims holds the id_token claims of upstream providers (Apple, Google);
// that is why the Nonce field is carried here.
type Claims struct {
	UserID            string       `json:"sub"`
	Email             string       `json:"email,omitempty"`
	EmailVerified     FlexibleBool `json:"email_verified,omitempty"`
	Name              string       `json:"name,omitempty"`
	PreferredUsername string       `json:"preferred_username,omitempty"`
	Roles             []string     `json:"roles,omitempty"`
	// Nonce is the nonce returned in the upstream provider's id_token.
	// Only UpstreamVerifier.VerifyWithNonce checks it.
	Nonce string `json:"nonce,omitempty"`
	jwt.RegisteredClaims
}

// UpstreamVerifier verifies the id_tokens of an UPSTREAM provider (Apple,
// Google). It is used in the federation callback flow: the id_token obtained
// after the user authorizes at Apple/Google is checked here for iss, aud, alg
// and nonce.
type UpstreamVerifier struct {
	jwks     keyfunc.Keyfunc
	issuer   string
	audience string
}

// NewUpstreamVerifier builds a verifier for the id_tokens of an UPSTREAM
// provider (Apple, Google). The caller supplies jwksURL: the jwks_uri of Apple
// and Google is fixed, so fetching discovery on every sign-in would be an
// unnecessary network round trip and a source of failures.
//
// ctx is passed to keyfunc.NewDefaultCtx AS IS and must live as long as the
// CALLER. Building a 30-second timeout ctx here and cancelling it at once with
// "defer cancel()" is WRONG -- but IMPORTANT: the cost of that is narrower
// than a first reading suggests.
//
// ctx ONLY bounds the lifetime of the hourly periodic background refresh (see
// jwkset@v0.5.17 http.go:89 directly: "NewDefaultHTTPClientCtx is the same as
// NewDefaultHTTPClient, but with a context that can end the refresh
// goroutine."; storage.go:278-295 shows that this goroutine EXITS on
// options.Ctx's Done()). Rotation itself -- seeing a new kid -- DOES NOT
// DEPEND ON THIS CTX: jwkset's default behaviour (http.go:83, "3. Refresh
// remote HTTP resources if a key with an unknown key ID is trying to be read,
// with a rate limit of 5 minutes.") re-fetches an unknown kid IMMEDIATELY,
// with an internal context that is independent of ctx.
// (TestUpstreamVerifier_FollowsKeyRotation measures this -- and it passes even
// when ctx is cancelled; see the note next to that test.)
//
// If ctx is cancelled or left dead, this is the real cost:
//
//	(a) a key that the provider NO LONGER PUBLISHES (e.g. withdrawn because
//	    it was compromised) stays VALID in memory forever, until the process
//	    restarts -- it is never removed;
//	(b) because the hourly proactive refresh is dead, if TWO NEW kids arrive
//	    back to back within the 5-minute window the second cannot be verified
//	    (the same rate limiter does not allow a second immediate refresh
//	    before it has been replenished).
//
// Both are serious but not as dramatic as "all sign-ins break at rotation
// time"; it is enough for this method to keep ctx as the caller gave it, and
// no configuration or clock injection is NEEDED.
func NewUpstreamVerifier(ctx context.Context, jwksURL, issuer, audience string) (*UpstreamVerifier, error) {
	if jwksURL == "" || issuer == "" || audience == "" {
		return nil, fmt.Errorf("jwksURL, issuer ve audience zorunlu")
	}
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("JWKS kurulamadi: %w", err)
	}
	return &UpstreamVerifier{jwks: jwks, issuer: issuer, audience: audience}, nil
}

// VerifyWithNonce verifies the id_token and also requires the nonce to match
// the EXPECTED value exactly.
//
// Without the nonce: an attacker injects a valid id_token obtained from their
// own Google/Apple authorization into the victim's half-finished flow, and the
// victim's browser ends up signed in to the attacker's account. The signature,
// iss and aud are CORRECT for this token too; the nonce is the only thing that
// rejects it. The comparison is constant-time so that it leaks nothing to an
// attacker who tries to guess the nonce piece by piece from timing
// differences.
func (v *UpstreamVerifier) VerifyWithNonce(tokenString, nonce string) (*Claims, error) {
	if nonce == "" {
		return nil, fmt.Errorf("nonce bos olamaz")
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, v.jwks.Keyfunc,
		// The alg is pinned EXPLICITLY: relying on the library's key-type
		// check for protection would make it breakable on every upgrade.
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		// The error text does NOT include tokenString or raw claim values:
		// the id_token must not leak into logs.
		return nil, fmt.Errorf("id_token dogrulanamadi: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("id_token claim'leri gecersiz")
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return nil, fmt.Errorf("nonce eslesmedi")
	}
	if strings.TrimSpace(claims.UserID) == "" {
		return nil, fmt.Errorf("sub bos")
	}
	return claims, nil
}

// Close releases the verifier's resources.
func (v *UpstreamVerifier) Close() error {
	// keyfunc.Keyfunc resources are released when ctx is cancelled, so no
	// extra cleanup is needed here. The method is kept so that callers can
	// follow a consistent "close" habit.
	return nil
}
