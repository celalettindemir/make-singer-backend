package identity

import (
	"net/http"
	"net/url"
	"testing"
)

// This file measures the provider hint on /authorize (R-AUTH-03): the native
// app's Apple/Google buttons add saglayici=<name> and, if that provider is
// CONFIGURED, the OP skips its own login page and goes straight to the
// provider's federation start. Every other value is ignored.
//
// Same pattern as federation_flow_test.go: assertions rest on the OBSERVABLE
// result (the Location, the cookie, the code, the token), never on a single
// layer's call.

// authorizeWithHint opens /authorize with the given saglayici value in the
// given browser context and returns ONLY the first response (not followed),
// so the test can measure exactly where /authorize points.
func (b *fedBrowser) authorizeWithHint(t *testing.T, hint string, ctx cookieContext) *responseRecord {
	t.Helper()
	target, err := url.Parse(b.env.authorizeURLWithScope(b.challenge, "S256", fedScopes))
	if err != nil {
		t.Fatalf("could not parse the authorize address: %v", err)
	}
	q := target.Query()
	q.Set(paramProviderHint, hint)
	target.RawQuery = q.Encode()
	return b.do(t, http.MethodGet, target.String(), nil, ctx)
}

// sessionCookieSet says whether the response wrote the session binding cookie.
func sessionCookieSet(resp *responseRecord) bool {
	for _, c := range resp.Cookies() {
		if c.Name == cookieNameSession && c.Value != "" {
			return true
		}
	}
	return false
}

// redirectTarget parses the response's Location; it fails the test if the
// response is not a 302 with a parseable Location.
func redirectTarget(t *testing.T, resp *responseRecord) *url.URL {
	t.Helper()
	if resp.Status != http.StatusFound {
		t.Fatalf("/authorize status = %d, want 302. body: %s", resp.Status, resp.Body())
	}
	u, err := url.Parse(resp.Location())
	if err != nil {
		t.Fatalf("could not parse Location: %v", err)
	}
	return u
}

// A configured provider hint: /authorize goes STRAIGHT to the federation
// start, and the whole flow completes to a code and a token without ever
// rendering /giris.
func TestProviderHint_ConfiguredGoesStraightToFederation(t *testing.T) {
	const email = "ipucu-kullanici@ornek.com"
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         email,
		EmailVerified: true,
		Name:          "Ipucu Kullanici",
		Sub:           "google-sub-ipucu",
	})
	b := env.NewBrowser(t)

	resp := b.authorizeWithHint(t, providerGoogle, ctxUserInitiated)
	target := redirectTarget(t, resp)
	if target.Path != federationStartPath(providerGoogle) {
		t.Fatalf("Location path = %q, want %q", target.Path, federationStartPath(providerGoogle))
	}
	requestID := target.Query().Get("authRequestID")
	if requestID == "" {
		t.Fatal("no authRequestID in the rewritten Location")
	}
	// Positive control: the binding was REALLY set on the same response; the
	// start leg requires it, so without it the rest of this test would be
	// measuring a 403.
	if !sessionCookieSet(resp) {
		t.Fatal("/authorize did not write the session cookie on the rewritten redirect")
	}

	last := b.follow(t, resp)
	code := last.LocationCode()
	if code == "" {
		t.Fatalf("hinted flow did not produce a code; last status %d, location %q, body: %s",
			last.Status, last.Location(), last.Body())
	}
	if !env.RequestDone(t, requestID) {
		t.Error("the auth request was not bound to an identity")
	}
	status, token := env.exchangeToken(t, code, b.verifier)
	if status != http.StatusOK {
		t.Fatalf("token status = %d, body: %v", status, token)
	}
	idToken, _ := token["id_token"].(string)
	if idToken == "" {
		t.Fatal("no id_token")
	}
	_, claims := jwtClaims(t, idToken)
	if claims["email"] != email {
		t.Errorf("id_token email = %v, want %q", claims["email"], email)
	}
	if amr := amrValues(claims["amr"]); len(amr) != 1 || amr[0] != amrFederation {
		t.Errorf("amr = %v, want [%s]", amr, amrFederation)
	}
}

// Apple's start goes through the same rewrite; its form_post return is
// already covered by TestE2E_AppleLoginFormPost.
func TestProviderHint_Apple(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{})
	b := env.NewBrowser(t)
	target := redirectTarget(t, b.authorizeWithHint(t, providerApple, ctxUserInitiated))
	if target.Path != federationStartPath(providerApple) {
		t.Fatalf("Location path = %q, want %q", target.Path, federationStartPath(providerApple))
	}
}

// Every value that does not EXACTLY name a configured provider is ignored:
// the hosted login page opens as today, with its binding.
//
// Case-sensitivity is a DECISION, not an accident: the contract fixes the
// wire values as lowercase "apple"/"google" and the provider names in the
// mux are lowercase, so "Google" is just an unknown value.
func TestProviderHint_IgnoredValuesFallBackToLogin(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{})
	for _, hint := range []string{"", "facebook", "Google", "GOOGLE", " google", "google/../apple"} {
		t.Run(hint, func(t *testing.T) {
			b := env.NewBrowser(t)
			resp := b.authorizeWithHint(t, hint, ctxUserInitiated)
			target := redirectTarget(t, resp)
			if target.Path != pathLogin {
				t.Fatalf("Location path = %q, want %q", target.Path, pathLogin)
			}
			if !sessionCookieSet(resp) {
				t.Error("the ignored hint dropped the session binding")
			}
		})
	}
}

// A hint naming a provider that is NOT CONFIGURED (no federation at all
// here) is ignored: rewriting to an unmounted start path would be a 404.
func TestProviderHint_UnconfiguredProviderIgnored(t *testing.T) {
	env := newTestServer(t)
	b := env.NewBrowser(t)
	resp := b.authorizeWithHint(t, providerGoogle, ctxUserInitiated)
	target := redirectTarget(t, resp)
	if target.Path != pathLogin {
		t.Fatalf("Location path = %q, want %q", target.Path, pathLogin)
	}
	if !sessionCookieSet(resp) {
		t.Error("the ignored hint dropped the session binding")
	}
}

// Cross-site /authorize with a hint behaves EXACTLY as without one: no
// binding, no rewrite. The login-CSRF invariant ("the request is created but
// no browser is bound to it") must not get a shortcut through the hint.
func TestProviderHint_CrossSiteNotRewritten(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{})
	b := env.NewBrowser(t)

	resp := b.authorizeWithHint(t, providerGoogle, ctxCrossSiteGet)
	target := redirectTarget(t, resp)
	if target.Path != pathLogin {
		t.Fatalf("Location path = %q, want %q (cross-site must not be rewritten)",
			target.Path, pathLogin)
	}
	if sessionCookieSet(resp) {
		t.Fatal("cross-site /authorize wrote a session cookie")
	}
	requestID := target.Query().Get("authRequestID")
	if requestID == "" {
		t.Fatal("no authRequestID in Location")
	}
	// Even if the browser goes to the start leg by itself, there is no
	// binding to pass.
	start := b.StartFederation(t, providerGoogle, requestID)
	if start.Status != http.StatusForbidden {
		t.Errorf("start without binding status = %d, want 403", start.Status)
	}
	if env.RequestDone(t, requestID) {
		t.Error("the cross-site request was completed")
	}
}
