package middleware

import (
	"context"
	"crypto/rsa"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// NewAdminAuth authenticates /api/admin requests. It is the same strict
// verification as the mobile chain (RS256, issuer, expiry, access token
// only) but the audience is the admin-web client id: a mobile-app token is
// rejected with 401. An empty id rejects every request (fail closed).
func NewAdminAuth(issuer, adminClientID string, publicKey *rsa.PublicKey) fiber.Handler {
	return NewOPAuthMiddleware(issuer, adminClientID, publicKey).Authenticate()
}

// UserLookup reads the identity record the admin gate decides on.
// An unknown user is reported as unverified, not as an error.
type UserLookup interface {
	AdminView(ctx context.Context, id string) (email string, verified bool, err error)
}

// LocalAdminEmail is the c.Locals key AdminGate fills with the admin's
// lower-cased e-mail.
const LocalAdminEmail = "adminEmail"

// AdminGate lets a request through only when the signed-in user (sub of the
// already verified token) has a VERIFIED e-mail on the admin list. The
// e-mail comes from the identity store, never from the token. Everything
// else is 403 ADMIN_REQUIRED; a store failure is 500 (fail closed).
func AdminGate(users UserLookup, emails []string) fiber.Handler {
	allowed := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			allowed[e] = struct{}{}
		}
	}
	return func(c *fiber.Ctx) error {
		id := GetUserID(c)
		if id == "" {
			return response.Unauthorized(c, i18n.KeyUnauthenticated)
		}
		email, verified, err := users.AdminView(c.UserContext(), id)
		if err != nil {
			log.Printf("admin gate: user lookup failed: %v", err)
			return response.ServiceError(c, i18n.KeyInternal)
		}
		email = strings.ToLower(strings.TrimSpace(email))
		if _, ok := allowed[email]; !verified || email == "" || !ok {
			return response.AdminRequired(c)
		}
		c.Locals(LocalAdminEmail, email)
		return c.Next()
	}
}

// GetAdminEmail returns the e-mail AdminGate admitted, or "".
func GetAdminEmail(c *fiber.Ctx) string {
	if e, ok := c.Locals(LocalAdminEmail).(string); ok {
		return e
	}
	return ""
}
