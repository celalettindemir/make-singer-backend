package middleware

import (
	"crypto/rsa"
	"fmt"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// AuthMiddleware authenticates /api/* requests with the RS256 access
// token issued by our own OP. There is no other verification path.
type AuthMiddleware struct {
	op *opVerifier
}

// opVerifier verifies the RS256 access token issued by our own OP. The
// public key comes from inside the process; fetching our own JWKS endpoint
// over the network would make the service depend on its own startup
// (chicken and egg: the API would wait for its own OP to be up).
type opVerifier struct {
	issuer    string
	publicKey *rsa.PublicKey
	// clientID: the "aud" claim MUST contain this client ID. It cannot be
	// left empty; see setupErr.
	clientID string
	// When setupErr is set, no token is accepted. Missing configuration
	// must NOT silently fall into a "checks disabled" mode: in the earlier
	// design with two constructors side by side, picking the wrong call
	// site could silently drop the aud check without breaking any test.
	setupErr error
}

// NewOPAuthMiddleware builds the middleware that verifies access tokens
// issued by our own OP. Access tokens are targeted at our client, so the
// "aud" check is mandatory: clientID must not be empty. With an empty or
// missing configuration the verifier does not silently relax; it rejects
// EVERY request (the signature returns *AuthMiddleware and cannot return
// an error, so the safe default is "closed").
func NewOPAuthMiddleware(issuer, clientID string, publicKey *rsa.PublicKey) *AuthMiddleware {
	d := &opVerifier{issuer: issuer, clientID: clientID, publicKey: publicKey}
	switch {
	case issuer == "":
		d.setupErr = fmt.Errorf("OP auth configuration incomplete: issuer is empty")
	case clientID == "":
		d.setupErr = fmt.Errorf("OP auth configuration incomplete: client ID is empty")
	case publicKey == nil:
		d.setupErr = fmt.Errorf("OP auth configuration incomplete: public key is nil")
	}
	if d.setupErr != nil {
		// The error text carries no secret (it only says which field is
		// empty), so it can be logged and should be.
		log.Printf("Error: %v — all /api/* requests will be rejected", d.setupErr)
	}
	return &AuthMiddleware{op: d}
}

// verify validates the token and returns (sub, email, name).
func (d *opVerifier) verify(tokenString string) (string, string, string, error) {
	if d.setupErr != nil {
		return "", "", "", d.setupErr
	}
	claims := jwt.MapClaims{}
	options := []jwt.ParserOption{
		// The signing algorithm is restricted EXPLICITLY: otherwise an
		// "alg: none" or HMAC downgrade attack would be possible.
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(d.issuer),
		jwt.WithExpirationRequired(),
		// iat, when present, must not be in the future (nbf is always
		// checked anyway).
		jwt.WithIssuedAt(),
		// "aud" must contain our client ID: a token minted for another
		// client must not get into our API. Applied unconditionally.
		jwt.WithAudience(d.clientID),
	}
	token, err := jwt.ParseWithClaims(tokenString, claims,
		func(t *jwt.Token) (any, error) {
			// Do not rely on the library reading alg from the token; enforce
			// the expected method here as well (two layers of defense).
			if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			// Defense in depth: a nil key leads to a PANIC inside crypto/rsa.
			// Thanks to setupErr we should never get here with nil, but we
			// check anyway.
			if d.publicKey == nil {
				return nil, fmt.Errorf("public key is nil")
			}
			return d.publicKey, nil
		}, options...)
	if err != nil {
		return "", "", "", err
	}
	if !token.Valid {
		return "", "", "", fmt.Errorf("token is invalid")
	}
	// The token's USAGE TYPE is checked: an id_token is NOT an access
	// token and must not grant API access. Both are signed with the same
	// key and the same iss and aud, so none of the checks above tells them
	// apart. The distinguishing claims (zitadel/oidc v3.51.8,
	// pkg/oidc/token.go):
	//   access token: jti PRESENT, azp/at_hash ABSENT   (NewAccessTokenClaims)
	//   id_token    : azp PRESENT (+at_hash), jti ABSENT (NewIDTokenClaims)
	// Concrete impact: an id_token lives 1 hour, an access token 15
	// minutes; also the id_token is handled more loosely on the client
	// (cache, analytics, crash logs) and is not recorded on the OP side.
	if jti, _ := claims["jti"].(string); strings.TrimSpace(jti) == "" {
		return "", "", "", fmt.Errorf("jti claim missing: not an access token")
	}
	if _, present := claims["azp"]; present {
		return "", "", "", fmt.Errorf("azp claim present: an id_token cannot be used as an access token")
	}
	if _, present := claims["at_hash"]; present {
		return "", "", "", fmt.Errorf("at_hash claim present: an id_token cannot be used as an access token")
	}

	sub, _ := claims["sub"].(string)
	// Not only "" but also a whitespace-only sub is rejected: otherwise
	// userId would be "   " and the rate limit key would become meaningless.
	if strings.TrimSpace(sub) == "" {
		return "", "", "", fmt.Errorf("sub claim is empty")
	}
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)
	return sub, email, name, nil
}

// Authenticate validates JWT token from Authorization header
func (m *AuthMiddleware) Authenticate() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return response.Unauthorized(c, i18n.KeyAuthHeaderMissing)
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return response.Unauthorized(c, i18n.KeyAuthHeaderInvalid)
		}

		tokenString := parts[1]

		// Our own OP is the only path. If op is nil (possible only with a
		// zero-value AuthMiddleware) the request is rejected: there is no
		// other verification path to fall back to.
		if m.op == nil {
			return response.Unauthorized(c, i18n.KeyTokenInvalid)
		}
		sub, email, name, err := m.op.verify(tokenString)
		if err != nil {
			// The error text may carry token content; it is not logged.
			return response.Unauthorized(c, i18n.KeyTokenInvalid)
		}
		c.Locals("userId", sub)
		c.Locals("email", email)
		c.Locals("name", name)
		return c.Next()
	}
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
