package identity

import (
	"net/http"
	"net/url"
	"testing"
)

// This file tests the MEASURING INSTRUMENT: cookieJar and its helpers
// (sendable, cookiePathMatches, stepContext) are the FOUNDATION of the
// federation attack tests.
//
// WHY IT IS NEEDED: if the jar is too LOOSE, the attack tests give FALSE
// GREENS and nothing turns them red. Example: if sendable always
// returns true, the Apple form_post test degrades into a copy of the
// Google GET flow and the live proof of the SameSite=None requirement
// (K2) is lost SILENTLY. Here the jar itself is measured directly, so
// such loosening shows up at once.
//
// The jar's behavior cannot be measured indirectly anywhere in the
// end-to-end tests: when the flow stays green, nobody knows whether the
// jar enforced the rule or never applied it at all.

// The fixed address used in tests: the jar selects by host + path and
// does not use the scheme.
const jarTarget = "http://kimlik.ornek/federasyon/google/geri"

// TestCookieJar_EnforcesSameSite checks that sendable REALLY enforces the
// browser rule. The standard net/http/cookiejar only STORES SameSite and
// does not use it when selecting for a request; that gap is the whole
// reason this jar exists.
func TestCookieJar_EnforcesSameSite(t *testing.T) {
	statuses := []struct {
		name     string
		sameSite http.SameSite
		ctx      cookieContext
		want     bool
	}{
		{
			// This is exactly Apple's form_post return: a Lax cookie is NOT SENT,
			// so the federation cookie has to be None (K2).
			name:     "Lax is not carried on a cross-site POST",
			sameSite: http.SameSiteLaxMode, ctx: ctxCrossSitePost, want: false,
		},
		{
			name:     "Lax is carried on same-site",
			sameSite: http.SameSiteLaxMode, ctx: ctxSameSite, want: true,
		},
		{
			name:     "None is carried on a cross-site POST",
			sameSite: http.SameSiteNoneMode, ctx: ctxCrossSitePost, want: true,
		},
		{
			// Google's 302 return is a top-level cross-site GET:
			// Lax is sent, Strict is not.
			name:     "Strict is not carried on a cross-site GET",
			sameSite: http.SameSiteStrictMode, ctx: ctxCrossSiteGet, want: false,
		},
		{
			name:     "Lax is carried on a cross-site GET",
			sameSite: http.SameSiteLaxMode, ctx: ctxCrossSiteGet, want: true,
		},
		{
			// Navigation the user started themselves (a native app opens the
			// system browser): everything is sent.
			name:     "Strict is carried on a start navigation",
			sameSite: http.SameSiteStrictMode, ctx: ctxUserInitiated, want: true,
		},
	}

	to, err := url.Parse(jarTarget)
	if err != nil {
		t.Fatalf("could not parse target: %v", err)
	}

	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			jar := newCookieJar()
			jar.SetCookies(to, []*http.Cookie{{
				Name: "deneme", Value: "deger", Path: "/federasyon", SameSite: d.sameSite,
			}})
			jar.setContext(d.ctx)
			carried := len(jar.Cookies(to)) == 1
			if carried != d.want {
				t.Errorf("carried = %v, want %v", carried, d.want)
			}
		})
	}
}

// TestCookieJar_PathScope checks that cookiePathMatches applies the
// RFC 6265 path prefix. The federation cookie is written with
// Path=/federasyon: if the jar ignores the path, the claim that the
// cookie's surface is limited (TestE2E_AppleLoginFormPost) goes UNMEASURED.
func TestCookieJar_PathScope(t *testing.T) {
	statuses := []struct {
		cookiePath  string
		requestPath string
		want        bool
	}{
		{"/federasyon", "/federasyon", true},
		{"/federasyon", "/federasyon/google/geri", true},
		{"/federasyon/", "/federasyon/google", true},
		// Apply the path prefix by SEGMENT, not by CHARACTER:
		// /federasyonlar is not under /federasyon.
		{"/federasyon", "/federasyonlar", false},
		{"/federasyon", "/giris", false},
		{"/", "/herhangi", true},
	}

	for _, d := range statuses {
		if res := cookiePathMatches(d.cookiePath, d.requestPath); res != d.want {
			t.Errorf("cookiePathMatches(%q, %q) = %v, want %v",
				d.cookiePath, d.requestPath, res, d.want)
		}
	}

	// The path must be enforced IN THE JAR too: the helper being right alone
	// does not prove that Cookies() calls it.
	writer, err := url.Parse("http://kimlik.ornek/federasyon/google/baslat")
	if err != nil {
		t.Fatalf("could not parse address: %v", err)
	}
	jar := newCookieJar()
	jar.SetCookies(writer, []*http.Cookie{{
		Name: "deneme", Value: "deger", Path: "/federasyon", SameSite: http.SameSiteNoneMode,
	}})
	jar.setContext(ctxSameSite)
	outsidePath, err := url.Parse("http://kimlik.ornek/giris")
	if err != nil {
		t.Fatalf("could not parse address: %v", err)
	}
	if n := len(jar.Cookies(outsidePath)); n != 0 {
		t.Errorf("%d cookies carried to /giris, want 0 (Path=/federasyon)", n)
	}
	if n := len(jar.Cookies(writer)); n != 1 {
		t.Errorf("%d cookies carried to the /federasyon/... path, want 1", n)
	}
}

// TestCookieJar_HostIsolation checks that cookies are kept per host: the
// fake upstream and our own listener are SEPARATE hosts, and one's
// cookie must NOT go to the other.
func TestCookieJar_HostIsolation(t *testing.T) {
	ours, err := url.Parse("http://kimlik.ornek/federasyon")
	if err != nil {
		t.Fatalf("could not parse address: %v", err)
	}
	upstream, err := url.Parse("http://yukari.ornek/federasyon")
	if err != nil {
		t.Fatalf("could not parse address: %v", err)
	}
	jar := newCookieJar()
	jar.SetCookies(ours, []*http.Cookie{{
		Name: "deneme", Value: "deger", Path: "/federasyon", SameSite: http.SameSiteNoneMode,
	}})
	jar.setContext(ctxSameSite)
	if n := len(jar.Cookies(upstream)); n != 0 {
		t.Errorf("%d cookies carried to another host, want 0", n)
	}
	if n := len(jar.Cookies(ours)); n != 1 {
		t.Errorf("%d cookies carried to its own host, want 1", n)
	}
}

// TestCookieJar_Delete checks that deleteCookie deletes from ALL hosts:
// the claim "cannot complete without the cookie" relies on the cookie
// being the only variable (TestAttack_CallbackWithoutCookieRejected).
func TestCookieJar_Delete(t *testing.T) {
	to, err := url.Parse(jarTarget)
	if err != nil {
		t.Fatalf("could not parse target: %v", err)
	}
	jar := newCookieJar()
	jar.SetCookies(to, []*http.Cookie{
		{Name: cookieNameFederation, Value: "deger", Path: "/federasyon", SameSite: http.SameSiteNoneMode},
		{Name: "baska", Value: "kalsin", Path: "/", SameSite: http.SameSiteNoneMode},
	})
	jar.setContext(ctxSameSite)
	if n := len(jar.Cookies(to)); n != 2 {
		t.Fatalf("%d cookies before deletion, want 2", n)
	}
	jar.deleteCookie(cookieNameFederation)
	remaining := jar.Cookies(to)
	if len(remaining) != 1 || remaining[0].Name != "baska" {
		t.Errorf("remaining after deletion = %v, want only \"baska\"", remaining)
	}
	if jar.cookieValue(cookieNameFederation) != "" {
		t.Error("cookieValue still returns the deleted cookie")
	}
}

// TestStepContext checks that the cookie context at each step of the
// redirect chain is classified correctly by source/target host and
// method. A wrong classification would count a cross-site return as
// same-site, and the SameSite rule would never apply.
func TestStepContext(t *testing.T) {
	parse := func(raw string) *url.URL {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("could not parse address (%q): %v", raw, err)
		}
		return u
	}
	ours := parse("http://kimlik.ornek/federasyon/google/baslat")
	otherSite := parse("http://kimlik.ornek/authorize/callback")
	upstream := parse("http://yukari.ornek/authorize")

	statuses := []struct {
		name   string
		from   *url.URL
		to     *url.URL
		method string
		want   cookieContext
	}{
		{"same host GET same-site", ours, otherSite, http.MethodGet, ctxSameSite},
		{"same host POST same-site", ours, otherSite, http.MethodPost, ctxSameSite},
		{"other host GET cross-site GET", upstream, otherSite, http.MethodGet, ctxCrossSiteGet},
		// Apple's form_post return: a cross-site POST for us.
		{"other host POST cross-site POST", upstream, otherSite, http.MethodPost, ctxCrossSitePost},
		// The first step of the chain has NO source; being counted as cross-site
		// is the SAFE direction (fewer cookies are sent).
		{"no source GET cross-site GET", nil, ours, http.MethodGet, ctxCrossSiteGet},
	}

	for _, d := range statuses {
		if res := stepContext(d.from, d.to, d.method); res != d.want {
			t.Errorf("%s: stepContext = %v, want %v", d.name, res, d.want)
		}
	}
}
