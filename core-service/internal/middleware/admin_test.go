package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const testAdminClientID = "admin-web"

type fakeLookup struct {
	email    string
	verified bool
	err      error
}

func (f fakeLookup) AdminView(context.Context, string) (string, bool, error) {
	return f.email, f.verified, f.err
}

func newAdminTestApp(t *testing.T, lookup UserLookup, emails []string) (*fiber.App, func(aud string) string) {
	t.Helper()
	priv := testKey(t)
	app := fiber.New()
	app.Get("/api/admin/me", NewAdminAuth(testIssuer, testAdminClientID, &priv.PublicKey), AdminGate(lookup, emails),
		func(c *fiber.Ctx) error { return c.SendString(GetAdminEmail(c)) })
	mint := func(aud string) string {
		return signTestToken(t, priv, jwt.MapClaims{
			"iss": testIssuer, "sub": "u1", "aud": []string{aud}, "jti": "j1",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
	}
	return app, mint
}

func adminGet(t *testing.T, app *fiber.App, token string) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/admin/me", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(r, -1)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 512)
	n, _ := resp.Body.Read(b)
	return resp.StatusCode, string(b[:n])
}

func TestAdminAuth_RejectsMobileAudience(t *testing.T) {
	app, mint := newAdminTestApp(t, fakeLookup{"a@x.com", true, nil}, []string{"a@x.com"})
	if st, _ := adminGet(t, app, mint(testClientID)); st != 401 {
		t.Errorf("mobile-audience token: status = %d, want 401", st)
	}
	if st, _ := adminGet(t, app, ""); st != 401 {
		t.Errorf("no token: status = %d, want 401", st)
	}
	if st, _ := adminGet(t, app, mint(testAdminClientID)); st != 200 {
		t.Errorf("admin-audience token: status = %d, want 200", st)
	}
}

// The mobile chain must not accept an admin-audience token either.
func TestAdminAuth_MobileChainRejectsAdminAudience(t *testing.T) {
	priv := testKey(t)
	token := signTestToken(t, priv, jwt.MapClaims{
		"iss": testIssuer, "sub": "u1", "aud": []string{testAdminClientID}, "jti": "j1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if st, _ := doRequest(t, newApp(t, priv), "Bearer "+token); st != 401 {
		t.Errorf("status = %d, want 401", st)
	}
}

func TestAdminAuth_EmptyClientIDRejectsEverything(t *testing.T) {
	priv := testKey(t)
	app := fiber.New()
	app.Get("/x", NewAdminAuth(testIssuer, "", &priv.PublicKey), func(c *fiber.Ctx) error { return nil })
	token := signTestToken(t, priv, jwt.MapClaims{
		"iss": testIssuer, "sub": "u1", "aud": []string{""}, "jti": "j1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(r, -1)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdminAuth_RequiresVerifiedListedEmail(t *testing.T) {
	cases := []struct {
		name   string
		lookup fakeLookup
		emails []string
		want   int
	}{
		{"unverified", fakeLookup{"a@x.com", false, nil}, []string{"a@x.com"}, 403},
		{"unlisted", fakeLookup{"b@x.com", true, nil}, []string{"a@x.com"}, 403},
		{"empty list", fakeLookup{"a@x.com", true, nil}, nil, 403},
		{"empty email never matches", fakeLookup{"", true, nil}, []string{"", " "}, 403},
		{"unknown user", fakeLookup{"", false, nil}, []string{"a@x.com"}, 403},
		{"listed, case-insensitive", fakeLookup{"A@X.com", true, nil}, []string{" a@X.COM "}, 200},
		{"store failure fails closed", fakeLookup{"a@x.com", true, errors.New("db down")}, []string{"a@x.com"}, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, mint := newAdminTestApp(t, tc.lookup, tc.emails)
			st, body := adminGet(t, app, mint(testAdminClientID))
			if st != tc.want {
				t.Fatalf("status = %d (%s), want %d", st, body, tc.want)
			}
			if tc.want == 403 && !strings.Contains(body, "ADMIN_REQUIRED") {
				t.Errorf("body %q lacks ADMIN_REQUIRED", body)
			}
			if tc.want == 200 && body != "a@x.com" {
				t.Errorf("adminEmail = %q, want a@x.com", body)
			}
		})
	}
}
