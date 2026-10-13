package identity

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/auth"
)

// This file carries the test infrastructure needed to run the federation
// flow END TO END:
//
//  1. fakeOP: a local httptest server that imitates the /authorize, /token
//     and /jwks endpoints of an upstream provider (Apple/Google) and can
//     BREAK any of its responses. The tests never reach the real
//     accounts.google.com / appleid.apple.com.
//  2. cookieJar: a cookie jar that REALLY applies SameSite.
//  3. fedBrowser: a client with its own cookie jar that follows redirects
//     and Apple's form_post form like a real browser.
//
// The tests themselves are in federation_flow_test.go.

// ---------------------------------------------------------------------
// Fake upstream OP
// ---------------------------------------------------------------------

// fakeOPConfig exists so that ANY response of the upstream provider can be
// BROKEN: an attack test has to change exactly one field and keep the rest
// right, otherwise it becomes unclear what the test measures.
type fakeOPConfig struct {
	// If Email is EMPTY the email claim is NEVER SENT (the scenario where Apple
	// never gives the email on the second login); a claim that sends an empty
	// string has to be told apart from a claim that is absent altogether.
	Email         string
	EmailVerified bool
	// If EmailVerifiedString is not empty, email_verified is sent as a STRING
	// (Apple's behavior in K8).
	EmailVerifiedString string
	Name                string
	Sub                 string
	// The following are BREAKING switches; when empty/false the right value is used.
	WrongNonce      string
	WrongAudience   string
	WrongIssuer     string
	WrongSigningKey bool
	TokenStatus     int    // 200 if 0
	TokenBody       string // normal {"id_token":...} if empty
	FormPost        bool   // Apple mode: POST on return
}

// fakeOP imitates a single upstream provider.
//
// WHY A SEPARATE TYPE from newFakeProvider: that helper has NO /authorize
// endpoint (the unit tests call the state record and the callback leg by
// hand). The end-to-end flow requires the browser to REALLY go to the
// provider and come back: /authorize, code production and Apple's form_post
// return live here. The unit tests' helper was left UNTOUCHED
// (federation_page_test.go keeps using it).
type fakeOP struct {
	name     string
	clientID string
	server   *httptest.Server

	// publishedKey is the key PUBLISHED in the JWKS; signingKey is the key that
	// REALLY signs the id_token. With WrongSigningKey the two diverge and the
	// signature cannot be verified.
	publishedKey *rsa.PrivateKey
	signingKey   *rsa.PrivateKey

	mu            sync.Mutex
	cfg           fakeOPConfig
	codeNonce     map[string]string
	lastAuthorize url.Values
	codeCount     int
	tokenCount    int
}

// autoFormMarker marks the form on the fake provider's form_post page.
// fedBrowser submits ONLY the form that carries this marker by itself: in a
// real browser too, what auto-submits is the script on the provider's page;
// forms that ask for a PASSWORD such as /baglama are not submitted by
// themselves.
const autoFormMarker = `data-oto="1"`

// newFakeUpstreamOP sets up an httptest server for the given name with its
// own RSA key, its own JWKS endpoint and /authorize and /token endpoints.
//
// The name parameter was ADDED to the signature in the brief: the end-to-end
// flows need BOTH the Google (302 return) and Apple (form_post return)
// providers in the same environment, so the provider name and client ID
// have to be distinguishable.
func newFakeUpstreamOP(t *testing.T, name string, cfg fakeOPConfig) *fakeOP {
	t.Helper()
	pubKey := fedRSAKey(t)
	signer := pubKey
	if cfg.WrongSigningKey {
		// Sign with ANOTHER key that is not published in the JWKS.
		signer = fedRSAKey(t)
	}
	s := &fakeOP{
		name:         name,
		clientID:     name + "-istemci",
		publishedKey: pubKey,
		signingKey:   signer,
		cfg:          cfg,
		codeNonce:    map[string]string{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{fedJWK(s.publishedKey)},
		})
	})
	mux.HandleFunc("/authorize", s.authorize)
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) { s.token(t, w, r) })

	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

// authorize imitates the step where the user approves at the provider: it
// records the request, produces a code and sends the user to OUR return
// address. In form_post mode (Apple) the return is a CROSS-SITE POST and this
// is the whole rationale of K2.
func (s *fakeOP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	s.lastAuthorize = q
	s.codeCount++
	code := fmt.Sprintf("%s-kod-%d", s.name, s.codeCount)
	s.codeNonce[code] = q.Get("nonce")
	// form_post is either forced by the config or read from the request itself:
	// in production what asks for it is Upstream.FormPost.
	formPost := s.cfg.FormPost || q.Get("response_mode") == "form_post"
	s.mu.Unlock()

	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	if formPost {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The real Apple page also submits this form by itself with a script; the
		// marker decides which form the browser helper will submit.
		_, _ = fmt.Fprintf(w,
			`<!doctype html><html><body><form `+autoFormMarker+` method="post" action="%s">`+
				`<input type="hidden" name="state" value="%s">`+
				`<input type="hidden" name="code" value="%s">`+
				`</form></body></html>`,
			redirectURI, state, code)
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "redirect_uri cozulemedi", http.StatusBadRequest)
		return
	}
	query := target.Query()
	query.Set("code", code)
	query.Set("state", state)
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// token turns the code into an id_token. With the config the status code and
// body can be broken entirely so that real cases such as "200 +
// {\"error\":...}" can be measured too.
func (s *fakeOP) token(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	_ = r.ParseForm()
	s.mu.Lock()
	s.tokenCount++
	nonce := s.codeNonce[r.PostFormValue("code")]
	cfg := s.cfg
	s.mu.Unlock()

	if cfg.WrongNonce != "" {
		nonce = cfg.WrongNonce
	}
	w.Header().Set("Content-Type", "application/json")
	if cfg.TokenStatus != 0 {
		w.WriteHeader(cfg.TokenStatus)
	}
	if cfg.TokenBody != "" {
		_, _ = io.WriteString(w, cfg.TokenBody)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": s.idToken(t, nonce)})
}

// idToken produces the id_token the provider will return. The breaking
// switches are applied ONE BY ONE: an attack test changes only one field.
func (s *fakeOP) idToken(t *testing.T, nonce string) string {
	t.Helper()
	s.mu.Lock()
	cfg := s.cfg
	signer := s.signingKey
	s.mu.Unlock()

	iss := s.server.URL
	if cfg.WrongIssuer != "" {
		iss = cfg.WrongIssuer
	}
	aud := s.clientID
	if cfg.WrongAudience != "" {
		aud = cfg.WrongAudience
	}
	sub := cfg.Sub
	if sub == "" {
		sub = s.name + "-sub-1"
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   iss,
		"aud":   aud,
		"sub":   sub,
		"nonce": nonce,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	// If Email is EMPTY the claim is NEVER ADDED: Apple does not send the email
	// on the second login and this is DIFFERENT from sending an empty string.
	if cfg.Email != "" {
		claims["email"] = cfg.Email
	}
	if cfg.Name != "" {
		claims["name"] = cfg.Name
	}
	switch {
	case cfg.EmailVerifiedString != "":
		claims["email_verified"] = cfg.EmailVerifiedString
	default:
		claims["email_verified"] = cfg.EmailVerified
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	// kid is ALWAYS the kid of the PUBLISHED key: even with a wrong signing key
	// the verifier finds the key and the SIGNATURE verification fails (a kid that
	// cannot be found would measure the key lookup, not the signature check).
	token.Header["kid"] = fedKID(s.publishedKey)
	signed, err := token.SignedString(signer)
	if err != nil {
		t.Fatalf("id_token imzalanamadi: %v", err)
	}
	return signed
}

// configure changes the provider's next responses (for example not sending
// the email at all on the second login).
func (s *fakeOP) configure(f func(*fakeOPConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.cfg)
}

// tokenCalls returns how many times the token endpoint was hit.
func (s *fakeOP) tokenCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenCount
}

// lastAuthorizeQuery returns the parameters of the last /authorize request
// that came to the provider (the PKCE test reads them).
func (s *fakeOP) lastAuthorizeQuery() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuthorize
}

// ---------------------------------------------------------------------
// Cookie jar that applies SameSite
// ---------------------------------------------------------------------

// cookieContext says which class a request falls into from the browser's
// point of view. The cookie rule (SameSite) and the Sec-Fetch-Site header are
// derived from it.
type cookieContext int

const (
	// ctxUserInitiated: navigation the user started DIRECTLY (a native app
	// opens the system browser) -> Sec-Fetch-Site: none, all cookies are sent.
	ctxUserInitiated cookieContext = iota
	// ctxSameSite: navigation between our own pages.
	ctxSameSite
	// ctxCrossSiteGet: a TOP-LEVEL GET navigation coming from ANOTHER site
	// (Google's return with a 302). A Lax cookie IS SENT.
	ctxCrossSiteGet
	// ctxCrossSitePost: a POST coming from ANOTHER site (Apple's form_post
	// return). ONLY a SameSite=None cookie is sent.
	ctxCrossSitePost
)

func (b cookieContext) fetchSite() string {
	switch b {
	case ctxUserInitiated:
		return fetchSiteNone
	case ctxSameSite:
		return fetchSiteSameOrigin
	default:
		return fetchSiteCrossSite
	}
}

// cookieJar is an http.CookieJar that REALLY applies SameSite.
//
// WHY THE STANDARD JAR IS NOT ENOUGH: net/http/cookiejar IGNORES the
// SameSite attribute ENTIRELY (Cookies() returns only name/value). An
// "end-to-end" test run with it would stay GREEN even if the federation
// cookie were turned into Lax, i.e. K2's proof would NOT BE MEASURED. This
// jar models the browser rule: Lax is NOT SENT on a cross-site POST, None is
// sent.
//
// The Secure flag is NOT MODELED: the test listener is http and the
// production wiring is set up with secure=false (see setupTestServer).
type cookieJar struct {
	mu      sync.Mutex
	cookies map[string]map[string]*http.Cookie // host -> name\x00path -> cookie
	ctx     cookieContext
}

func newCookieJar() *cookieJar {
	return &cookieJar{cookies: map[string]map[string]*http.Cookie{}}
}

// setContext sets the class of the NEXT request. http.Client does not pass
// the request's "site for cookies" context to the Jar; since a single browser
// runs in a single goroutine, setting the context right before the request
// gives the right behavior.
func (k *cookieJar) setContext(b cookieContext) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ctx = b
}

func (k *cookieJar) SetCookies(u *url.URL, cookieList []*http.Cookie) {
	k.mu.Lock()
	defer k.mu.Unlock()
	host := k.cookies[u.Host]
	if host == nil {
		host = map[string]*http.Cookie{}
		k.cookies[u.Host] = host
	}
	for _, c := range cookieList {
		clone := *c
		if clone.Path == "" {
			clone.Path = "/"
		}
		key := clone.Name + "\x00" + clone.Path
		if clone.MaxAge < 0 {
			delete(host, key)
			continue
		}
		host[key] = &clone
	}
}

// deleteCookie deletes the cookie with the given name from ALL hosts: to make
// the cookie the only variable in the claim "cannot complete without the
// cookie" (changing the browser would change two things at once).
func (k *cookieJar) deleteCookie(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, host := range k.cookies {
		for key, c := range host {
			if c.Name == name {
				delete(host, key)
			}
		}
	}
}

// cookieValue returns the value of the cookie with the given name in the jar
// (an empty string if absent). The tests use it to set up a PRECONDITION: the
// claim "the value did not match" cannot be made without measuring that the
// federation cookies of two browsers REALLY differ.
func (k *cookieJar) cookieValue(name string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, host := range k.cookies {
		for _, c := range host {
			if c.Name == name {
				return c.Value
			}
		}
	}
	return ""
}

func (k *cookieJar) Cookies(u *url.URL) []*http.Cookie {
	k.mu.Lock()
	defer k.mu.Unlock()
	var chosen []*http.Cookie
	for _, c := range k.cookies[u.Host] {
		if !cookiePathMatches(c.Path, u.Path) {
			continue
		}
		if !k.sendable(c) {
			continue
		}
		chosen = append(chosen, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	// The order must be DETERMINISTIC: map iteration is random and if the order
	// in the header changed between tests, debugging would be needlessly hard.
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Name < chosen[j].Name })
	return chosen
}

// sendable applies the browser's SameSite rule.
func (k *cookieJar) sendable(c *http.Cookie) bool {
	switch k.ctx {
	case ctxUserInitiated, ctxSameSite:
		return true
	case ctxCrossSiteGet:
		// Top-level cross-site GET: Lax (and no attribute given = default Lax) IS
		// SENT, Strict is not.
		return c.SameSite != http.SameSiteStrictMode
	case ctxCrossSitePost:
		// Cross-site POST: ONLY None. Apple's form_post return is exactly this;
		// that is why the federation cookie has to be None.
		return c.SameSite == http.SameSiteNoneMode
	}
	return false
}

// cookiePathMatches is RFC 6265 path matching.
func cookiePathMatches(cookiePath, requestPath string) bool {
	if requestPath == "" {
		requestPath = "/"
	}
	if cookiePath == requestPath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || strings.HasPrefix(requestPath[len(cookiePath):], "/")
}

// ---------------------------------------------------------------------
// Response record
// ---------------------------------------------------------------------

// responseRecord holds everything the test needs from a single HTTP response.
// The body is read IN ADVANCE: forgetting to close the body while following
// redirects would cause a leak.
type responseRecord struct {
	Status     int
	requestURL *url.URL
	body       string
	location   string
	cookies    []*http.Cookie
}

func (y *responseRecord) Body() string            { return y.body }
func (y *responseRecord) Location() string        { return y.location }
func (y *responseRecord) Cookies() []*http.Cookie { return y.cookies }

// LocationCode returns the authorization code in the response's Location
// header (an empty string if absent). The END of the flow is the redirect to
// the client's redirect_uri and the code is there.
func (y *responseRecord) LocationCode() string {
	if y.location == "" {
		return ""
	}
	u, err := url.Parse(y.location)
	if err != nil {
		return ""
	}
	return u.Query().Get("code")
}

// fedCookie returns the federation cookie written in the response (nil if absent).
func (y *responseRecord) fedCookie() *http.Cookie {
	for _, c := range y.cookies {
		if c.Name == cookieNameFederation {
			return c
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Browser
// ---------------------------------------------------------------------

// fedBrowser is a browser that has its own cookie jar and follows redirects
// BY HAND: each leg's status code and Location header is what the test
// measures.
//
// WHY the newBrowser() helper in flow_test.go is not enough: it uses the
// standard cookiejar (no SameSite) and does not follow redirects. That helper
// was left UNTOUCHED; this type is its end-to-end version.
type fedBrowser struct {
	env       *flowEnv
	jar       *cookieJar
	client    *http.Client
	verifier  string
	challenge string
}

// NewBrowser returns a new browser with its own cookie jar. Every browser has
// its OWN PKCE pair: the attack tests have to tell "the attacker's verifier"
// from "the victim's verifier".
func (o *flowEnv) NewBrowser(t *testing.T) *fedBrowser {
	t.Helper()
	verifier, challenge := pkceS256(t)
	jar := newCookieJar()
	return &fedBrowser{
		env: o,
		jar: jar,
		client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
			// Redirects are followed BY HAND: the cookie context (SameSite) and
			// Sec-Fetch-Site have to be computed at each step, and http.Client cannot do
			// that.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		verifier:  verifier,
		challenge: challenge,
	}
}

// do makes a single HTTP request. The context determines the cookie rule AND
// the Sec-Fetch-Site header.
func (b *fedBrowser) do(
	t *testing.T, method, target string, form url.Values, ctx cookieContext,
) *responseRecord {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(t.Context(), method, target, body)
	if err != nil {
		t.Fatalf("istek kurulamadi (%s %s): %v", method, target, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set(headerFetchMode, "navigate")
	req.Header.Set(headerFetchSite, ctx.fetchSite())

	b.jar.setContext(ctx)
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s basarisiz: %v", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("govde okunamadi: %v", err)
	}
	return &responseRecord{
		Status:     resp.StatusCode,
		requestURL: req.URL,
		body:       string(raw),
		location:   resp.Header.Get("Location"),
		cookies:    resp.Cookies(),
	}
}

// getWithHeaders makes a GET with extra headers for API calls (for example
// /userinfo). The cookie context is SAME SITE: this is an API call, not one
// of the legs of the flow.
func (b *fedBrowser) getWithHeaders(
	t *testing.T, method, target string, headers map[string]string,
) *responseRecord {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, nil)
	if err != nil {
		t.Fatalf("istek kurulamadi (%s %s): %v", method, target, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	b.jar.setContext(ctxSameSite)
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s basarisiz: %v", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("govde okunamadi: %v", err)
	}
	return &responseRecord{
		Status:     resp.StatusCode,
		requestURL: req.URL,
		body:       string(raw),
		location:   resp.Header.Get("Location"),
		cookies:    resp.Cookies(),
	}
}

// decodeJSON turns a JSON body into a map.
func decodeJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("JSON cozulemedi (%q): %v", body, err)
	}
	return m
}

// autoFormPattern finds the fake provider's auto-submitted form (the marker
// is mandatory, see autoFormMarker).
var autoFormPattern = regexp.MustCompile(`(?s)<form ` + regexp.QuoteMeta(autoFormMarker) + `[^>]*action="([^"]*)"(.*?)</form>`)

var hiddenFieldPattern = regexp.MustCompile(`name="([^"]*)" value="([^"]*)"`)

// parseAutoForm returns (action, fields, true) if there is a form in the body
// that will be submitted by itself.
func parseAutoForm(body string) (string, url.Values, bool) {
	matches := autoFormPattern.FindStringSubmatch(body)
	if matches == nil {
		return "", nil, false
	}
	fields := url.Values{}
	for _, field := range hiddenFieldPattern.FindAllStringSubmatch(matches[2], -1) {
		fields.Set(field[1], field[2])
	}
	return matches[1], fields, true
}

// maxRedirects is the maximum number of steps to follow in the redirect
// chain. The real flow is 4-5 steps; the limit stops the test from hanging in
// a loop.
const maxRedirects = 12

// follow follows redirects and the auto-submitted form_post form like a real
// browser. The cookie context is recomputed at each step (see stepContext):
// the POST coming BACK to us from the provider is cross-site, our own
// redirects after that are same-site.
//
// It STOPS at a custom scheme address (com.makesinger.app:/...): there the
// browser hands over to the native app, i.e. it is the end of the flow and the
// code is in the Location header of that response.
func (b *fedBrowser) follow(t *testing.T, resp *responseRecord) *responseRecord {
	t.Helper()
	source := resp.requestURL
	for step := 0; step < maxRedirects; step++ {
		switch resp.Status {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			if resp.location == "" {
				return resp
			}
			target, err := source.Parse(resp.location)
			if err != nil {
				t.Fatalf("Location cozulemedi (%q): %v", resp.location, err)
			}
			if target.Scheme != "http" && target.Scheme != "https" {
				// The client's redirect_uri: the end of the flow.
				return resp
			}
			resp = b.do(t, http.MethodGet, target.String(), nil, stepContext(source, target, http.MethodGet))
			source = target
		default:
			action, fields, exists := parseAutoForm(resp.body)
			if !exists {
				return resp
			}
			target, err := source.Parse(action)
			if err != nil {
				t.Fatalf("form action cozulemedi (%q): %v", action, err)
			}
			resp = b.do(t, http.MethodPost, target.String(), fields, stepContext(source, target, http.MethodPost))
			source = target
		}
	}
	t.Fatalf("yonlendirme zinciri %d adimda bitmedi", maxRedirects)
	return nil
}

// stepContext computes the cookie context of a step from the source and
// target HOST.
//
// A MODEL, NOT A BROWSER: a real browser's "site" is scheme + registrable
// domain, the port is IRRELEVANT. Here we MODEL host separation as site
// separation, because in production Apple/Google and our own issuer really are
// separate sites, while in the test they all sit on 127.0.0.1 on different
// ports and the only distinguishing thing is the host.
//
// The model is STRICTER than Chrome: Chrome's two-minute Lax+POST exemption
// would SEND a freshly written Lax cookie on Apple's form_post return, this
// jar does not. The deviation is on the SAFE side -- it does not produce a
// false GREEN, it only requires the cookie to be SameSite=None (exactly what
// K2 wants).
func stepContext(source, target *url.URL, method string) cookieContext {
	if source != nil && source.Host == target.Host {
		return ctxSameSite
	}
	if method == http.MethodPost {
		return ctxCrossSitePost
	}
	return ctxCrossSiteGet
}

// fedScopes are the scopes requested in the end-to-end flows. email and
// profile are MANDATORY: at the end of the flow we measure the id_token/
// userinfo claims. offline_access is mandatory too: refresh rotation is part
// of the flow.
const fedScopes = "openid profile email offline_access"

// StartAuthorize imitates a native app opening the system browser
// (Sec-Fetch-Site: none) and returns the authRequestID.
func (b *fedBrowser) StartAuthorize(t *testing.T) string {
	t.Helper()
	return b.startAuthorizeWithScope(t, fedScopes, "S256")
}

// startAuthorizeWithScope is the version of StartAuthorize where the scope
// and the PKCE method can be chosen: the PKCE attack test tries to come to
// /authorize without sending code_challenge AT ALL.
func (b *fedBrowser) startAuthorizeWithScope(t *testing.T, scope, method string) string {
	t.Helper()
	challenge := b.challenge
	if method == "" {
		challenge = ""
	}
	resp := b.do(t, http.MethodGet,
		b.env.authorizeURLWithScope(challenge, method, scope), nil, ctxUserInitiated)
	if resp.Status != http.StatusFound {
		t.Fatalf("/authorize durumu = %d, beklenen 302. govde: %s", resp.Status, resp.Body())
	}
	location, err := url.Parse(resp.Location())
	if err != nil {
		t.Fatalf("Location cozulemedi: %v", err)
	}
	if location.Path != pathLogin {
		t.Fatalf("Location yolu = %q, beklenen %q", location.Path, pathLogin)
	}
	id := location.Query().Get("authRequestID")
	if id == "" {
		t.Fatal("Location'da authRequestID yok")
	}
	return id
}

// RunFederationFlow runs every step from pressing the provider button on the
// login page to the end of the flow and returns the LAST response.
func (b *fedBrowser) RunFederationFlow(t *testing.T, provider, requestID string) *responseRecord {
	t.Helper()
	return b.follow(t, b.StartFederation(t, provider, requestID))
}

// StartFederation runs ONLY the first leg (the start endpoint) and returns
// its response: the attack tests need the state and cookie of this response
// to step into the middle of the chain.
func (b *fedBrowser) StartFederation(t *testing.T, provider, requestID string) *responseRecord {
	t.Helper()
	target := b.env.issuer + federationStartPath(provider) +
		"?" + url.Values{"authRequestID": {requestID}}.Encode()
	// The user presses the button on the login page: a same-site top-level GET.
	return b.do(t, http.MethodGet, target, nil, ctxSameSite)
}

// RequestDone says whether the pending auth request was bound to an identity:
// the store side's definitive answer to the question "did the login happen".
func (b *fedBrowser) RequestDone(t *testing.T, requestID string) bool {
	t.Helper()
	return b.env.RequestDone(t, requestID)
}

// ExchangeCode tries to COLLECT the code and exchange it for a token. It
// returns an error if it fails: this is the proof of the claim "no login
// happened".
func (b *fedBrowser) ExchangeCode(t *testing.T, requestID string) (string, error) {
	t.Helper()
	status, token, err := b.ExchangeCodeFull(t, requestID)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("jeton ucu durumu %d: %v", status, token)
	}
	access, _ := token["access_token"].(string)
	if access == "" {
		return "", errors.New("access_token yok")
	}
	return access, nil
}

// ExchangeCodeFull runs the /authorize/callback leg in this browser and
// returns the WHOLE response of the token endpoint (the happy path tests
// measure the id_token and refresh_token too).
func (b *fedBrowser) ExchangeCodeFull(t *testing.T, requestID string) (int, map[string]any, error) {
	t.Helper()
	resp := b.do(t, http.MethodGet,
		b.env.issuer+"/authorize/callback?"+url.Values{"id": {requestID}}.Encode(),
		nil, ctxSameSite)
	code := resp.LocationCode()
	if code == "" {
		return 0, nil, fmt.Errorf("kod uretilmedi (callback durumu %d)", resp.Status)
	}
	status, body := b.env.exchangeToken(t, code, b.verifier)
	return status, body, nil
}

var pendingIDPattern = regexp.MustCompile(`name="beklemeID" value="([^"]*)"`)

// SubmitLink reads the /baglama form from the GIVEN body and submits it:
// pendingID and the CSRF token come from the form itself, not from values
// the test built by hand.
func (b *fedBrowser) SubmitLink(t *testing.T, body, password string) *responseRecord {
	t.Helper()
	pendingMatch := pendingIDPattern.FindStringSubmatch(body)
	if pendingMatch == nil {
		t.Fatalf("baglama formunda beklemeID yok; govde: %s", body)
	}
	csrfMatch := flowCSRFPattern.FindStringSubmatch(body)
	if csrfMatch == nil {
		t.Fatal("baglama formunda CSRF jetonu yok")
	}
	return b.do(t, http.MethodPost, b.env.issuer+pathLink, url.Values{
		fieldPendingID: {pendingMatch[1]},
		fieldCSRF:      {csrfMatch[1]},
		"sifre":        {password},
	}, ctxSameSite)
}

// ---------------------------------------------------------------------
// Flow environment: production wiring + fake upstreams
// ---------------------------------------------------------------------

// relaxedRateLimits is the config needed so that the end-to-end flows are
// NOT caught by the rate limit: a single end-to-end flow makes a series of
// requests from the same IP (127.0.0.1) and a test that hit the limit would
// measure the 429, not the flow. The rate limit ITSELF is measured with tight
// limits in ratelimit_test.go and TestBuildMux_FederationWiring.
func relaxedRateLimits() RateLimitConfig {
	return RateLimitConfig{
		LoginIPPerMin:     1000,
		LoginEmailPerHour: 1000,
		SignupIPPerHour:   1000,
	}
}

// setupFlowEnv sets up the fake upstreams (Google 302 return, Apple form_post
// return), a REAL buildMux listener and Redis together.
//
// Postgres is NOT USED: the user and federated stores are in-memory fakes.
// The rationale is the distinction kept since Phase 1: the contract tests
// (TestUserStore_Contract, TestFederatedStore_Contract -- both now REALLY run
// in CI) measure that the stores behave EXACTLY like Postgres, while this
// file measures the flow ITSELF.
func setupFlowEnv(t *testing.T, cfg fakeOPConfig) *flowEnv {
	t.Helper()
	return setupTestServerWithUpstream(t, relaxedRateLimits(), true, cfg)
}

// wireFederation sets up the two fake upstreams (google + apple) and their
// verifiers. The federation branch of setupTestServerWithUpstream is fed
// from here, so the production wiring (buildMux) is set up from a single place.
func wireFederation(t *testing.T, cfg fakeOPConfig) (
	map[string]*Upstream,
	map[string]*auth.UpstreamVerifier,
	map[string]*fakeOP,
) {
	t.Helper()
	google := newFakeUpstreamOP(t, providerGoogle, cfg)
	// Apple's return is ALWAYS form_post: this is the real behavior when the
	// name/email scope is requested (K2) and the end-to-end proof comes from
	// exactly there.
	appleCfg := cfg
	appleCfg.FormPost = true
	apple := newFakeUpstreamOP(t, providerApple, appleCfg)
	appleP8, _ := generateTestP8(t)

	flows := map[string]*Upstream{
		providerGoogle: {
			Name: providerGoogle, ClientID: google.clientID, ClientSecret: "google-sir",
			AuthURL: google.server.URL + "/authorize", TokenURL: google.server.URL + "/token",
			JWKSURL: google.server.URL + "/jwks", Issuer: google.server.URL,
			Scopes: []string{"openid", "email", "profile"}, SupportsPKCE: true,
		},
		providerApple: {
			Name: providerApple, ClientID: apple.clientID,
			AuthURL: apple.server.URL + "/authorize", TokenURL: apple.server.URL + "/token",
			JWKSURL: apple.server.URL + "/jwks", Issuer: apple.server.URL,
			Scopes: []string{"openid", "name", "email"}, FormPost: true,
			// Apple's client_secret is produced with ES256 on every exchange (K5), so a
			// REAL .p8 is needed in the test too.
			AppleTeamID: "EKIP123", AppleKeyID: "ANAHTAR9", AppleKeyP8: appleP8,
		},
	}
	ops := map[string]*fakeOP{providerGoogle: google, providerApple: apple}
	verifiers := map[string]*auth.UpstreamVerifier{}
	for name, s := range ops {
		d, err := auth.NewUpstreamVerifier(
			t.Context(), s.server.URL+"/jwks", s.server.URL, s.clientID)
		if err != nil {
			t.Fatalf("%s dogrulayicisi kurulamadi: %v", name, err)
		}
		t.Cleanup(func() { _ = d.Close() })
		verifiers[name] = d
	}
	return flows, verifiers, ops
}

// Upstream returns the fake OP of the given provider: the attack tests break
// its responses from there.
func (o *flowEnv) Upstream(t *testing.T, provider string) *fakeOP {
	t.Helper()
	s, exists := o.upstreams[provider]
	if !exists {
		t.Fatalf("%q icin sahte yukari akis kurulmamis", provider)
	}
	return s
}

// SignUp opens an account with a password through the REAL signup flow:
// /authorize -> GET /kayit -> POST /kayit. Going through the flow instead of
// writing to the store directly is mandatory: it proves the precondition was
// set up through the production path.
func (o *flowEnv) SignUp(t *testing.T, email, name, password string) {
	t.Helper()
	b := o.NewBrowser(t)
	id := b.StartAuthorize(t)
	form := b.do(t, http.MethodGet,
		o.issuer+pathSignup+"?"+url.Values{"authRequestID": {id}}.Encode(), nil, ctxSameSite)
	if form.Status != http.StatusOK {
		t.Fatalf("GET /kayit durumu = %d, beklenen 200", form.Status)
	}
	csrfMatch := flowCSRFPattern.FindStringSubmatch(form.Body())
	if csrfMatch == nil {
		t.Fatal("kayit formunda CSRF jetonu yok")
	}
	resp := b.do(t, http.MethodPost, o.issuer+pathSignup, url.Values{
		"eposta": {email}, "ad": {name}, "sifre": {password},
		"authRequestID": {id}, fieldCSRF: {csrfMatch[1]},
	}, ctxSameSite)
	if resp.Status != http.StatusFound {
		t.Fatalf("POST /kayit durumu = %d, beklenen 302. govde: %s", resp.Status, resp.Body())
	}
}

// LoginWithPassword logs in end to end through the password path and returns
// the access token; if ANY step of the flow does not hold it returns an empty
// string.
//
// WHY AN EMPTY STRING (not an error): this helper is used both for the
// PRECONDITION check ("was the attacker's account really opened") and for
// the negative assertion ("one cannot log in with this password").
func (o *flowEnv) LoginWithPassword(t *testing.T, email, password string) string {
	t.Helper()
	b := o.NewBrowser(t)
	id := b.StartAuthorize(t)
	form := b.do(t, http.MethodGet,
		o.issuer+pathLogin+"?"+url.Values{"authRequestID": {id}}.Encode(), nil, ctxSameSite)
	if form.Status != http.StatusOK {
		return ""
	}
	csrfMatch := flowCSRFPattern.FindStringSubmatch(form.Body())
	if csrfMatch == nil {
		return ""
	}
	resp := b.do(t, http.MethodPost, o.issuer+pathLogin, url.Values{
		"eposta": {email}, "sifre": {password},
		"authRequestID": {id}, fieldCSRF: {csrfMatch[1]},
	}, ctxSameSite)
	if resp.Status != http.StatusFound {
		return ""
	}
	token, err := b.ExchangeCode(t, id)
	if err != nil {
		return ""
	}
	return token
}

// RequestDone says that the pending auth request was bound to an identity.
// If there is no request at all it is false: it means not bound.
func (o *flowEnv) RequestDone(t *testing.T, requestID string) bool {
	t.Helper()
	req, err := o.requests.GetByID(t.Context(), requestID)
	if errors.Is(err, ErrRequestNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	return req.Done()
}

// FederatedLinkCount returns the number of written (provider, sub) -> user
// links: the proof of the claim "NO link was made".
func (o *flowEnv) FederatedLinkCount(t *testing.T) int {
	t.Helper()
	if o.federated == nil {
		t.Fatal("federe depo kurulmamis")
	}
	return o.federated.linkCount()
}

// linkCount returns the number of links in the fake store. For tests only;
// the production interface (FederatedStore) has no such operation and does
// not need one.
func (d *FakeFederatedStore) linkCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.links)
}

// ---------------------------------------------------------------------
// Token claims
// ---------------------------------------------------------------------

// jwtClaims decodes the HEADER and claims of a JWT WITHOUT VERIFYING. The
// signature is not verified here; that alg is RS256 is ASSERTED by reading it
// from the header (see TestE2E_GoogleLogin). The library measures the
// validity of the token's signature in its own tests; what is measured here is
// the content of the claims.
func jwtClaims(t *testing.T, token string) (map[string]any, map[string]any) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("jeton JWT degil (%d parca)", len(parts))
	}
	decode := func(part string) map[string]any {
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			t.Fatalf("jeton parcasi cozulemedi: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("jeton parcasi JSON degil: %v", err)
		}
		return m
	}
	return decode(parts[0]), decode(parts[1])
}

// audContains says whether the aud claim (a string or an array) contains the
// given value.
func audContains(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// amrValues turns the amr claim into a string slice.
func amrValues(amr any) []string {
	raw, ok := amr.([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			values = append(values, s)
		}
	}
	return values
}

// refresh runs the refresh_token flow.
func (o *flowEnv) refresh(t *testing.T, refresh string) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(o.issuer+"/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {"makesinger-mobil"},
	})
	if err != nil {
		t.Fatalf("POST /oauth/token (refresh): %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}
