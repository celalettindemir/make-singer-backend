package identity

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"
)

// This file measures the WHOLE OP end to end over a real TCP listener:
// /authorize -> /giris -> /authorize/callback -> /oauth/token. It needs only
// Redis (the user and token stores are fakes), so it also runs without
// Postgres; if there is no Redis it is skipped with the SAME pattern as
// testRedis (request_test.go).

// testCryptoKey is a 32-byte TEST-LOCAL value for op.Config.CryptoKey. It is
// NOT a production secret and in production it comes from AUTH_CRYPTO_KEY;
// no production secret is written into source code.
const testCryptoKey = "kimlik-test-yerel-crypto-32byte!"

const testRedirectURI = "com.makesinger.app:/oauth2redirect"

type flowEnv struct {
	issuer   string
	users    UserStore
	requests *RequestStore

	// federated and upstreams are filled ONLY in a federated setup (see
	// setupTestServerWithUpstream): the end-to-end tests have to break the
	// upstream's responses and count the links that get written.
	federated *FakeFederatedStore
	upstreams map[string]*fakeOP
}

// newTestServer sets up EXACTLY THE SAME wiring as production with buildMux
// and serves it on a real listener. The issuer is derived from the
// listener's address so that discovery and op.AuthCallbackURL produce the
// right address.
func newTestServer(t *testing.T) *flowEnv {
	t.Helper()
	// The production wiring runs in EVERY flow test (buildMux receives a real
	// *RateLimiter), but the limits are high enough not to affect these tests:
	// the rate limit ITSELF is measured with tight limits in ratelimit_test.go.
	return newTestServerWithLimits(t, RateLimitConfig{
		LoginIPPerMin:     1000,
		LoginEmailPerHour: 1000,
		SignupIPPerHour:   1000,
		TrustedProxies:    0,
	})
}

func newTestServerWithLimits(t *testing.T, cfg RateLimitConfig) *flowEnv {
	t.Helper()
	return setupTestServer(t, cfg, false)
}

// newTestServerFederated sets up the SAME wiring with federation CONFIGURED:
// the "flows are set" branch of buildMux runs, i.e. the federation endpoints
// and the buttons on the login page REALLY exist.
//
// WHY A SEPARATE ENTRY POINT: most flow tests measure the setup without
// federation and loading each of them with the cost of two RSA keys + two
// fake providers is unnecessary. The tests that measure the federation
// wiring call this variant.
func newTestServerFederated(t *testing.T, cfg RateLimitConfig) *flowEnv {
	t.Helper()
	return setupTestServer(t, cfg, true)
}

func setupTestServer(t *testing.T, cfg RateLimitConfig, federated bool) *flowEnv {
	t.Helper()
	// Default fake upstream config: no field is broken.
	return setupTestServerWithUpstream(t, cfg, federated, fakeOPConfig{})
}

// setupTestServerWithUpstream is the variant of setupTestServer that also
// accepts the fake upstream config. A SEPARATE ENTRY POINT: the signatures
// of setupTestServer and newTestServerFederated and their callers stay
// UNTOUCHED, while the end-to-end tests can break any response of the
// upstream.
func setupTestServerWithUpstream(
	t *testing.T, limits RateLimitConfig, federated bool, upstreamCfg fakeOPConfig,
) *flowEnv {
	t.Helper()
	rdb := testRedis(t) // Skipped here if there is no Redis

	key, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	cryptoKey, err := CryptoKey(testCryptoKey)
	if err != nil {
		t.Fatalf("CryptoKey: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not open listener: %v", err)
	}
	issuer := "http://" + listener.Addr().String()

	cfg := testAuthCfg()
	cfg.Issuer = issuer
	cfg.RedirectURIs = []string{testRedirectURI}

	users := NewFakeUserStore()
	reqLog := NewRequestStore(rdb)
	store := NewStorage(cfg, users, NewFakeTokenStore(), reqLog, key)

	opCfg := &op.Config{
		CryptoKey:             cryptoKey,
		CodeMethodS256:        true,
		AuthMethodPost:        false,
		GrantTypeRefreshToken: true,
		SupportedUILocales:    []language.Tag{language.Turkish, language.English},
		SupportedScopes: []string{
			oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess,
		},
	}
	provider, err := op.NewProvider(opCfg, store, op.StaticIssuer(issuer), op.WithAllowInsecure())
	if err != nil {
		t.Fatalf("OpenIDProvider: %v", err)
	}

	rateLimiter, err := NewRateLimiter(rdb, limits, []byte(testCryptoKey))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	// Federation parameters: nil when federated=false (buildMux does NOT wire
	// the federation endpoint AT ALL), filled with a FAKE upstream when true. The
	// fake provider is a local httptest server; the test never reaches out to
	// Apple/Google.
	var (
		flows          map[string]*Upstream
		verifiers      map[string]*auth.UpstreamVerifier
		federatedStore FederatedStore
		fakeFederated  *FakeFederatedStore
		ops            map[string]*fakeOP
	)
	if federated {
		// Google (302 return) and Apple (form_post return) are set up together;
		// both are local httptest servers.
		flows, verifiers, ops = wireFederation(t, upstreamCfg)
		fakeFederated = NewFakeFederatedStore(users)
		federatedStore = fakeFederated
	}

	// secure=false: the test listener is http and a Secure cookie would not be
	// sent.
	srv := &http.Server{
		Handler: buildMux(
			provider, users, reqLog, federatedStore,
			flows, verifiers, issuer, false, rateLimiter,
		),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	return &flowEnv{
		issuer:    issuer,
		users:     users,
		requests:  reqLog,
		federated: fakeFederated,
		upstreams: ops,
	}
}

// newBrowser returns a client that has its own cookie jar and does NOT follow
// redirects AUTOMATICALLY: so that every leg can be measured one by one.
func newBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// pkceS256 produces a (verifier, challenge) pair.
func pkceS256(t *testing.T) (string, string) {
	t.Helper()
	verifier, err := randomToken()
	if err != nil {
		t.Fatalf("could not generate verifier: %v", err)
	}
	return verifier, oidc.NewSHACodeChallenge(verifier)
}

// authorizeURL builds an /authorize address with the given PKCE parameters.
// If challenge or method is passed empty that parameter is NOT ADDED AT ALL
// (to tell a missing parameter from an empty one).
func (o *flowEnv) authorizeURL(challenge, method string) string {
	return o.authorizeURLWithScope(challenge, method, "openid offline_access")
}

// authorizeURLWithScope is the variant of authorizeURL where the scope can be
// chosen too: the end-to-end tests request the email and profile scopes as
// well (the id_token and userinfo claims are measured).
func (o *flowEnv) authorizeURLWithScope(challenge, method, scope string) string {
	q := url.Values{
		"client_id":     {"makesinger-mobil"},
		"redirect_uri":  {testRedirectURI},
		"response_type": {"code"},
		"scope":         {scope},
		"state":         {"durum-1"},
		"nonce":         {"nonce-1"},
	}
	if challenge != "" {
		q.Set("code_challenge", challenge)
	}
	if method != "" {
		q.Set("code_challenge_method", method)
	}
	return o.issuer + "/authorize?" + q.Encode()
}

func mustGet(t *testing.T, c *http.Client, address string) *http.Response {
	t.Helper()
	resp, err := c.Get(address)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("could not read body: %v", err)
	}
	return string(b)
}

// extractAuthRequestID takes the authRequestID from the /authorize redirect
// to the login page.
func extractAuthRequestID(t *testing.T, resp *http.Response) string {
	t.Helper()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/authorize status = %d, want 302", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("could not parse Location: %v", err)
	}
	if location.Path != pathLogin {
		t.Fatalf("Location path = %q, want %q", location.Path, pathLogin)
	}
	id := location.Query().Get("authRequestID")
	if id == "" {
		t.Fatal("no authRequestID in Location")
	}
	return id
}

var flowCSRFPattern = regexp.MustCompile(`name="csrf" value="([^"]*)"`)

// tryLogin TRIES to open the login form in the given browser and submits it
// if the form was shown. Returns: (the POST response or the form response,
// the status code of the GET form).
//
// WHY "TRY": since the binding is now set up ONLY at /authorize, a browser
// that did not start the flow gets 403 on GET /giris and never reaches the
// POST. The attack tests have to be able to measure both cases: in the
// fixed code the form is never shown, in the mutated code (if GET mints
// again) it is shown and the flow completes.
func (o *flowEnv) tryLogin(t *testing.T, c *http.Client, id, email, password string) (*http.Response, int) {
	t.Helper()
	formResp := mustGet(t, c, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id))
	if formResp.StatusCode != http.StatusOK {
		return formResp, formResp.StatusCode
	}
	matches := flowCSRFPattern.FindStringSubmatch(readBody(t, formResp))
	if matches == nil {
		t.Fatal("no CSRF token in the login form")
	}
	form := url.Values{
		"eposta": {email}, "sifre": {password},
		"authRequestID": {id}, fieldCSRF: {matches[1]},
	}
	resp, err := c.PostForm(o.issuer+pathLogin, form)
	if err != nil {
		t.Fatalf("POST /giris: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp, http.StatusOK
}

// openLoginForm opens the login form and returns the CSRF token in the form.
// The test stops if the form is rejected.
func (o *flowEnv) openLoginForm(t *testing.T, c *http.Client, id string) string {
	t.Helper()
	formResp := mustGet(t, c, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id))
	if formResp.StatusCode != http.StatusOK {
		t.Fatalf("login form status = %d", formResp.StatusCode)
	}
	matches := flowCSRFPattern.FindStringSubmatch(readBody(t, formResp))
	if matches == nil {
		t.Fatal("no CSRF token in the login form")
	}
	return matches[1]
}

// submitLogin does POST /giris from an OPEN form. It does NOT reopen the
// form: a real browser does not reload the form after a failed attempt, it
// shows the page that came back with the body.
func (o *flowEnv) submitLogin(t *testing.T, c *http.Client, id, csrf, email, password string) *http.Response {
	t.Helper()
	resp, err := c.PostForm(o.issuer+pathLogin, url.Values{
		"eposta": {email}, "sifre": {password},
		"authRequestID": {id}, fieldCSRF: {csrf},
	})
	if err != nil {
		t.Fatalf("POST /giris: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// login is the variant of tryLogin where the form "MUST BE SHOWN": for the
// happy path tests.
func (o *flowEnv) login(t *testing.T, c *http.Client, id, email, password string) *http.Response {
	t.Helper()
	resp, formStatus := o.tryLogin(t, c, id, email, password)
	if formStatus != http.StatusOK {
		t.Fatalf("login form status = %d", formStatus)
	}
	return resp
}

// extractCode extracts the authorization code from the callback response (an
// empty string if absent).
func extractCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	if resp.StatusCode != http.StatusFound {
		return ""
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return ""
	}
	return location.Query().Get("code")
}

// exchangeToken exchanges the authorization code for a token.
func (o *flowEnv) exchangeToken(t *testing.T, code, verifier string) (int, map[string]any) {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {testRedirectURI},
		"client_id":     {"makesinger-mobil"},
		"code_verifier": {verifier},
	}
	resp, err := http.PostForm(o.issuer+"/oauth/token", form)
	if err != nil {
		t.Fatalf("POST /oauth/token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var bodyJSON map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&bodyJSON)
	return resp.StatusCode, bodyJSON
}

// createTestUser opens an account in the fake store.
func (o *flowEnv) createTestUser(t *testing.T, email, password string) *User {
	t.Helper()
	k, err := o.users.Create(context.Background(), email, "Test", password)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return k
}

// ---- Happy path ----

// End-to-end S256 PKCE flow: /authorize -> /giris -> callback -> token.
// This test is the lock that the C1 and C2 fixes did not break the flow.
func TestFlow_S256EndToEnd(t *testing.T) {
	o := newTestServer(t)
	users := o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	verifier, challenge := pkceS256(t)
	c := newBrowser(t)

	id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256")))

	loginResp := o.login(t, c, id, "kurban@ornek.com", "dogruSifre12")
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("POST /giris status = %d, want 302. body: %s",
			loginResp.StatusCode, readBody(t, loginResp))
	}
	callbackURL := loginResp.Header.Get("Location")
	if !strings.Contains(callbackURL, "/authorize/callback") {
		t.Fatalf("unexpected Location after sign-in: %q", callbackURL)
	}

	cbResp := mustGet(t, c, callbackURL)
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302. body: %s",
			cbResp.StatusCode, readBody(t, cbResp))
	}
	last, err := url.Parse(cbResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("could not parse Location: %v", err)
	}
	code := last.Query().Get("code")
	if code == "" {
		t.Fatalf("no code was issued, Location: %q", last.String())
	}
	if last.Query().Get("state") != "durum-1" {
		t.Errorf("state = %q", last.Query().Get("state"))
	}

	status, token := o.exchangeToken(t, code, verifier)
	if status != http.StatusOK {
		t.Fatalf("token status = %d, body: %v", status, token)
	}
	if token["access_token"] == nil || token["access_token"] == "" {
		t.Error("no access_token")
	}
	if token["refresh_token"] == nil || token["refresh_token"] == "" {
		t.Error("offline_access was requested but there is no refresh_token")
	}
	_ = users
}

// ---- C1: the account takeover attack itself ----
//
// The re-review showed that the previous single attack test (the attacker did
// NOT OPEN the login form AT ALL) modeled only ONE variant of the exploit.
// The real exploit was the attacker minting a binding for THEMSELVES with
// GET /giris; both timing variants obtained a live token. The two tests
// below measure those two variants SEPARATELY.
//
// assertNoTakeover verifies the same result in both variants: the attacker
// gets NO code and the auth request is NEVER bound to the victim's identity.
//
// WHY "NO CODE" AND NOT "403": since the attacker is bound to their own
// request they PASS the cookie check at /authorize/callback; the library
// (pkg/op/auth_request.go AuthorizeCallback) rejects the request because it
// is not Done() and answers with a 302 to the client's redirect_uri with
// error=.... What has to be measured is not the status code but that NO CODE
// WAS PRODUCED and the request was not bound.
func assertNoTakeover(t *testing.T, o *flowEnv, id string, cbResp *http.Response) {
	t.Helper()
	if code := extractCode(t, cbResp); code != "" {
		t.Fatalf("ACCOUNT TAKEOVER: the attacker got a code (status %d)", cbResp.StatusCode)
	}
	req, err := o.requests.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if req.Done() {
		t.Fatal("ACCOUNT TAKEOVER PATH IS OPEN: the attacker's auth request was bound to an identity")
	}
}

// EXPLOIT 1 (pre-binding). The attacker calls /authorize with their own S256
// pair, THEN opens GET /giris in their own browser (does NOT log in, only
// tries to get a binding), sends the link to the victim; the victim tries to
// log in in a SEPARATE cookie jar; the attacker calls the callback.
//
// Expectation: the victim's GET is 403 (no binding, the form is not shown),
// the attacker gets NO code. If GET /giris mints a binding again this test
// FAILS (mutation proof in the report).
func TestExploit1_PreBindingCannotTakeOver(t *testing.T) {
	o := newTestServer(t)
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)

	attacker := newBrowser(t)
	victim := newBrowser(t)

	// 1) The attacker starts the flow (the challenge/state are the ATTACKER's).
	id := extractAuthRequestID(t, mustGet(t, attacker, o.authorizeURL(challenge, "S256")))

	// 2) PRE-BINDING: the attacker opens the form in THEIR OWN browser. This
	// returns 200 (they started the flow, they have a binding) but does NOT log in.
	attackerForm := mustGet(t, attacker, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id))
	if attackerForm.StatusCode != http.StatusOK {
		t.Fatalf("attacker's own form status = %d; scenario could not be set up", attackerForm.StatusCode)
	}

	// 3) The victim opens the same link in a SEPARATE browser and tries to log in.
	victimResp, formStatus := o.tryLogin(t, victim, id, "kurban@ornek.com", "dogruSifre12")
	if formStatus != http.StatusForbidden {
		t.Errorf("victim's GET /giris status = %d, want 403 (no binding, the form must not be shown)", formStatus)
	}
	if victimResp.StatusCode == http.StatusFound {
		t.Error("the victim's sign-in completed the attacker's request")
	}

	// 4) The attacker tries to collect the code in THEIR OWN browser.
	assertNoTakeover(t, o, id, mustGet(t, attacker, o.issuer+"/authorize/callback?id="+url.QueryEscape(id)))
}

// EXPLOIT 2 (post-login binding). The same exploit, but the attacker tries to
// get the binding AFTER the victim's login has COMPLETED: there is not even a
// timing condition. The order is DIFFERENT from EXPLOIT 1, so it is a separate
// test.
func TestExploit2_PostLoginBindingCannotTakeOver(t *testing.T) {
	o := newTestServer(t)
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)

	attacker := newBrowser(t)
	victim := newBrowser(t)

	// 1) The attacker starts the flow.
	id := extractAuthRequestID(t, mustGet(t, attacker, o.authorizeURL(challenge, "S256")))

	// 2) The victim FIRST tries to log in (the attacker has not opened the form
	// yet).
	victimResp, formStatus := o.tryLogin(t, victim, id, "kurban@ornek.com", "dogruSifre12")
	if formStatus != http.StatusForbidden {
		t.Errorf("victim's GET /giris status = %d, want 403", formStatus)
	}
	if victimResp.StatusCode == http.StatusFound {
		t.Error("the victim's sign-in completed the attacker's request")
	}

	// 3) The attacker THEN opens the form (tries to get a binding) and collects
	// the code.
	mustGet(t, attacker, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id))
	assertNoTakeover(t, o, id, mustGet(t, attacker, o.issuer+"/authorize/callback?id="+url.QueryEscape(id)))
}

// In a browser that did NOT START the flow GET /giris does not even show the
// form. (Its unit counterpart is page_test.go
// TestFormGet_WithoutBindingRejected; this one runs over the production mux.)
func TestBuildMux_LoginGetWithoutBindingRejected(t *testing.T) {
	o := newTestServer(t)
	_, challenge := pkceS256(t)
	starter := newBrowser(t)
	foreign := newBrowser(t)

	id := extractAuthRequestID(t, mustGet(t, starter, o.authorizeURL(challenge, "S256")))

	resp := mustGet(t, foreign, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign browser GET /giris status = %d, want 403", resp.StatusCode)
	}
	if strings.Contains(readBody(t, resp), `name="csrf"`) {
		t.Error("the rejected GET showed the form")
	}

	// NEGATIVE CONTROL: in the browser that started the flow the SAME link
	// returns 200, i.e. the 403 is because of the binding (the endpoint is not
	// fully closed).
	if own := mustGet(t, starter, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id)); own.StatusCode != http.StatusOK {
		t.Errorf("GET /giris status in the initiating browser = %d, want 200", own.StatusCode)
	}
}

// PARALLEL FLOW: two /authorize are started in the same browser (the same
// cookie jar); the POST and callback of BOTH must work.
//
// What this test catches: when the binding was moved to /authorize, if the
// cookie -> SINGLE id model had been kept the second /authorize would
// OVERWRITE the first one's binding and the first tab would get 403 (a DoS
// instead of a takeover). See maxSessionBindings.
func TestFlow_TwoParallelFlowsSameBrowser(t *testing.T) {
	o := newTestServer(t)
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	c := newBrowser(t)

	verifier1, challenge1 := pkceS256(t)
	verifier2, challenge2 := pkceS256(t)

	// Two tabs: both are started BEFORE logging in.
	id1 := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge1, "S256")))
	id2 := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge2, "S256")))
	if id1 == id2 {
		t.Fatal("two /authorize calls produced the same authRequestID")
	}

	// Tab 1 completes FIRST: the second /authorize must not have overwritten its
	// binding.
	for _, d := range []struct {
		name     string
		id       string
		verifier string
	}{{"sekme-1", id1, verifier1}, {"sekme-2", id2, verifier2}} {
		t.Run(d.name, func(t *testing.T) {
			loginResp := o.login(t, c, d.id, "kurban@ornek.com", "dogruSifre12")
			if loginResp.StatusCode != http.StatusFound {
				t.Fatalf("POST /giris status = %d, want 302. body: %s",
					loginResp.StatusCode, readBody(t, loginResp))
			}
			cbResp := mustGet(t, c, loginResp.Header.Get("Location"))
			code := extractCode(t, cbResp)
			if code == "" {
				t.Fatalf("no code was issued (callback status %d)", cbResp.StatusCode)
			}
			status, token := o.exchangeToken(t, code, d.verifier)
			if status != http.StatusOK {
				t.Fatalf("token status = %d, body: %v", status, token)
			}
			if token["access_token"] == nil || token["access_token"] == "" {
				t.Error("no access_token")
			}
		})
	}
}

// The victim's OWN browser must be able to complete the flow: the binding
// only cuts off strangers, it does not cut off the legitimate flow.
func TestFlow_OwnBrowserCompletes(t *testing.T) {
	o := newTestServer(t)
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)
	c := newBrowser(t)

	id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256")))
	loginResp := o.login(t, c, id, "kurban@ornek.com", "dogruSifre12")
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("sign-in status = %d", loginResp.StatusCode)
	}
	cbResp := mustGet(t, c, loginResp.Header.Get("Location"))
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", cbResp.StatusCode)
	}
	location, _ := url.Parse(cbResp.Header.Get("Location"))
	if location.Query().Get("code") == "" {
		t.Error("no code was issued in its own browser; the legitimate flow is broken")
	}
}

// ---- C2: PKCE S256 is mandatory ----

// assertPKCERejected verifies that /authorize rejected the request: it either
// returns 400 or returns a 302 to the client's redirect_uri with
// error=invalid_request. In BOTH cases the login page must NOT BE REACHED
// (the user must not enter their password for nothing).
func assertPKCERejected(t *testing.T, resp *http.Response) {
	t.Helper()
	switch resp.StatusCode {
	case http.StatusFound:
		location := resp.Header.Get("Location")
		if strings.Contains(location, pathLogin) {
			t.Fatalf("request was redirected to the login page: %q", location)
		}
		if !strings.Contains(location, "error=invalid_request") {
			t.Fatalf("no error=invalid_request in Location: %q", location)
		}
	case http.StatusBadRequest:
		// Accepted: a direct error.
	default:
		t.Fatalf("status = %d, a rejection was expected", resp.StatusCode)
	}
}

// C2: if there is NO code_challenge AT ALL /authorize is REJECTED. Also M1:
// it used to go to the login page with a 302, the user entered their
// password and the error only appeared at the token endpoint.
func TestAuthorize_MissingCodeChallengeRejected(t *testing.T) {
	o := newTestServer(t)
	c := newBrowser(t)
	assertPKCERejected(t, mustGet(t, c, o.authorizeURL("", "")))
}

// C2: code_challenge is present but method=plain -> REJECTED. With plain
// challenge == verifier; PKCE is effectively disabled.
func TestAuthorize_PlainRejected(t *testing.T) {
	o := newTestServer(t)
	c := newBrowser(t)
	verifier, _ := pkceS256(t)
	assertPKCERejected(t, mustGet(t, c, o.authorizeURL(verifier, "plain")))
}

// C2: method NOT GIVEN AT ALL (empty string) -> REJECTED. The library treats
// an empty method as a plain comparison.
func TestAuthorize_MissingMethodRejected(t *testing.T) {
	o := newTestServer(t)
	c := newBrowser(t)
	verifier, _ := pkceS256(t)
	assertPKCERejected(t, mustGet(t, c, o.authorizeURL(verifier, "")))
}

// C2: an unknown method is REJECTED too.
func TestAuthorize_UnknownMethodRejected(t *testing.T) {
	o := newTestServer(t)
	c := newBrowser(t)
	_, challenge := pkceS256(t)
	assertPKCERejected(t, mustGet(t, c, o.authorizeURL(challenge, "S512")))
}

// C2: method=S256 + a valid challenge WORKS (goes to the login page).
func TestAuthorize_S256Works(t *testing.T) {
	o := newTestServer(t)
	c := newBrowser(t)
	_, challenge := pkceS256(t)
	if id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256"))); id == "" {
		t.Error("authRequestID is empty")
	}
}

// C2 regression lock: in the S256 flow a token cannot be obtained with a
// WRONG code_verifier.
func TestToken_WrongVerifierRejected(t *testing.T) {
	o := newTestServer(t)
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)
	c := newBrowser(t)

	id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256")))
	loginResp := o.login(t, c, id, "kurban@ornek.com", "dogruSifre12")
	cbResp := mustGet(t, c, loginResp.Header.Get("Location"))
	location, _ := url.Parse(cbResp.Header.Get("Location"))
	code := location.Query().Get("code")
	if code == "" {
		t.Fatal("no code was issued")
	}

	wrongVerifier, _ := pkceS256(t)
	status, tokenBody := o.exchangeToken(t, code, wrongVerifier)
	if status == http.StatusOK {
		t.Fatalf("a token was issued with the wrong code_verifier: %v", tokenBody)
	}
}

// C2 SECOND LAYER: even if the /authorize check were bypassed the token
// endpoint is protected. Since GetCodeChallenge is fail-closed, the code of a
// request written DIRECTLY to the store with a challenge that is not S256
// (i.e. one that did not pass the /authorize check) cannot be exchanged for a
// token. If the fail-closed behavior is removed this test FAILS.
func TestToken_PlainChallengeRejected(t *testing.T) {
	o := newTestServer(t)
	ctx := context.Background()
	users := o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")

	// Write a request with a plain challenge BY BYPASSING /authorize.
	verifier, _ := pkceS256(t)
	req, err := o.requests.Create(ctx, &oidc.AuthRequest{
		ClientID:            "makesinger-mobil",
		RedirectURI:         testRedirectURI,
		Scopes:              oidc.SpaceDelimitedArray{oidc.ScopeOpenID},
		ResponseType:        oidc.ResponseTypeCode,
		CodeChallenge:       verifier, // with plain, challenge == verifier
		CodeChallengeMethod: oidc.CodeChallengeMethodPlain,
		State:               "durum-1",
	}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := o.requests.MarkDone(ctx, req.GetID(), users.ID, []string{amrPassword}); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	const code = "test-kod-plain"
	if err := o.requests.SaveCode(ctx, req.GetID(), code); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}

	status, tokenBody := o.exchangeToken(t, code, verifier)
	if status == http.StatusOK {
		t.Fatalf("a token was issued with a plain challenge: %v", tokenBody)
	}
}

// Discovery must advertise only S256 and what it advertises must be what is
// actually enforced (see pkce.go).
func TestDiscovery_AdvertisesOnlyS256(t *testing.T) {
	o := newTestServer(t)
	resp := mustGet(t, newBrowser(t), o.issuer+"/.well-known/openid-configuration")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discovery status = %d", resp.StatusCode)
	}
	var doc struct {
		Methods []string `json:"code_challenge_methods_supported"`
	}
	if err := json.Unmarshal([]byte(readBody(t, resp)), &doc); err != nil {
		t.Fatalf("could not decode discovery: %v", err)
	}
	if len(doc.Methods) != 1 || doc.Methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256]", doc.Methods)
	}
}

// M3: the PUBLISHED discovery document must NOT advertise unsupported flows.
// The reviewer tried all of them live and all are correctly rejected (no
// security problem), but a compliant RP that trusted the metadata and tried
// implicit would get an error for nothing.
//
// This test measures that trimDiscovery is really ON THE PUBLISH PATH: the
// document passes through the library's own op.CreateDiscoveryConfig and
// comes back through our mux.
func TestDiscovery_OmitsUnsupportedFlows(t *testing.T) {
	o := newTestServer(t)
	resp := mustGet(t, newBrowser(t), o.issuer+"/.well-known/openid-configuration")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discovery status = %d", resp.StatusCode)
	}
	var doc struct {
		ResponseTypes  []string `json:"response_types_supported"`
		GrantTypes     []string `json:"grant_types_supported"`
		DeviceEndpoint string   `json:"device_authorization_endpoint"`
		Issuer         string   `json:"issuer"`
		TokenEndpoint  string   `json:"token_endpoint"`
	}
	raw := readBody(t, resp)
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("could not decode discovery: %v", err)
	}

	if len(doc.ResponseTypes) != 1 || doc.ResponseTypes[0] != "code" {
		t.Errorf("response_types_supported = %v, want [code]", doc.ResponseTypes)
	}
	for _, forbidden := range []string{"implicit", "urn:ietf:params:oauth:grant-type:jwt-bearer", "urn:ietf:params:oauth:grant-type:device_code"} {
		for _, g := range doc.GrantTypes {
			if g == forbidden {
				t.Errorf("grant_types_supported contains unsupported %q: %v", forbidden, doc.GrantTypes)
			}
		}
	}
	if doc.DeviceEndpoint != "" {
		t.Errorf("device_authorization_endpoint was advertised: %q", doc.DeviceEndpoint)
	}

	// The REST of the document must not be broken: the issuer and the token
	// endpoint are values produced by the library and must come out right
	// together with the issuer resolved from the request context (does the
	// interceptor.Handler wrapping work).
	if doc.Issuer != o.issuer {
		t.Errorf("issuer = %q, want %q", doc.Issuer, o.issuer)
	}
	if doc.TokenEndpoint != o.issuer+"/oauth/token" {
		t.Errorf("token_endpoint = %q, want %q", doc.TokenEndpoint, o.issuer+"/oauth/token")
	}
}

// ---- LOGIN-CSRF: PKCE injection into /authorize from cross-site ----

// getWithFetchSite is the variant of mustGet with fetch-metadata headers
// added. If fetchSite is passed empty the header is NOT ADDED AT ALL.
func getWithFetchSite(t *testing.T, c *http.Client, address, fetchSite string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address, nil)
	if err != nil {
		t.Fatalf("could not build request: %v", err)
	}
	if fetchSite != "" {
		req.Header.Set(headerFetchSite, fetchSite)
		req.Header.Set(headerFetchMode, "navigate")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// LOGIN-CSRF EXPLOIT (the reviewer's scenario, exactly): the attacker
// redirects the victim to the /authorize address they prepared. Since the
// session cookie is SameSite=Lax it IS SENT on the top-level navigation, i.e.
// the victim's EXISTING cookie goes with the request. Before the fix, the
// authRequestID born with the attacker's code_challenge would be added to the
// victim's binding set; the victim would open the form and log in, and the
// code_verifier of the code produced would be with the ATTACKER (the ONLY
// scenario PKCE protects, RFC 8252 section 8.1, would collapse).
//
// Now: no new binding is ADDED on a cross-site navigation, the victim gets
// 403 for that id and the request CANNOT BE COMPLETED -- the attacker knowing
// the verifier does not help.
func TestLoginCSRF_CrossSiteInjectionCannotComplete(t *testing.T) {
	o := newTestServer(t)
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	victim := newBrowser(t)

	// 1) The victim's LEGITIMATE flow: the native app opens the system browser
	// (Sec-Fetch-Site: none). The cookie and binding are born here.
	victimVerifier, victimChallenge := pkceS256(t)
	legitID := extractAuthRequestID(t, getWithFetchSite(t, victim, o.authorizeURL(victimChallenge, "S256"), fetchSiteNone))

	// 2) The address the attacker prepared is opened in the VICTIM's browser by a
	// cross-site top-level navigation. The code_challenge is the ATTACKER's;
	// only they know the verifier.
	// We DELIBERATELY do not keep the verifier: the proof is that the attacker
	// knowing it does not help -- since NO code is produced below there is
	// NOTHING to exchange (step 4).
	_, attackerChallenge := pkceS256(t)
	injectedID := extractAuthRequestID(t, getWithFetchSite(t, victim, o.authorizeURL(attackerChallenge, "S256"), fetchSiteCrossSite))
	if injectedID == legitID {
		t.Fatal("two /authorize calls produced the same authRequestID")
	}

	// 3) The victim CANNOT open the form with the injected id: no binding was
	// set up -> 403.
	_, formStatus := o.tryLogin(t, victim, injectedID, "kurban@ornek.com", "dogruSifre12")
	if formStatus != http.StatusForbidden {
		t.Fatalf("LOGIN-CSRF IS OPEN: GET /giris status for the injected id = %d, want 403", formStatus)
	}

	// 4) So NO CODE IS PRODUCED for the attacker's request and the request is
	// NOT BOUND to any identity: the attacker's verifier is of no use.
	assertNoTakeover(t, o, injectedID,
		mustGet(t, victim, o.issuer+"/authorize/callback?id="+url.QueryEscape(injectedID)))

	// 5) THE VICTIM'S LEGITIMATE FLOW WAS NOT BROKEN (the check was not turned
	// into a DoS): it completes end to end with the same cookie jar and gets a token.
	csrf := o.openLoginForm(t, victim, legitID)
	loginResp := o.submitLogin(t, victim, legitID, csrf, "kurban@ornek.com", "dogruSifre12")
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("victim's legitimate POST /giris status = %d, want 302", loginResp.StatusCode)
	}
	code := extractCode(t, mustGet(t, victim, loginResp.Header.Get("Location")))
	if code == "" {
		t.Fatal("the victim's legitimate flow produced no code: the control broke the legitimate flow")
	}
	status, token := o.exchangeToken(t, code, victimVerifier)
	if status != http.StatusOK {
		t.Fatalf("victim's token exchange status = %d, body: %v", status, token)
	}
	if token["access_token"] == nil || token["access_token"] == "" {
		t.Error("no access_token in the victim's legitimate flow")
	}
}

// A cross-site request does NOT BREAK the victim's EXISTING bindings and
// cookie: the check is only "do not add a new binding", the cookie is not
// renewed or the set cleared. Two parallel legitimate flows must still be
// able to complete AFTER the cross-site request that came in between.
func TestAuthorize_CrossSiteKeepsExistingBindings(t *testing.T) {
	o := newTestServer(t)
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	victim := newBrowser(t)

	_, challenge1 := pkceS256(t)
	_, challenge2 := pkceS256(t)
	id1 := extractAuthRequestID(t, getWithFetchSite(t, victim, o.authorizeURL(challenge1, "S256"), fetchSiteNone))
	id2 := extractAuthRequestID(t, getWithFetchSite(t, victim, o.authorizeURL(challenge2, "S256"), fetchSiteNone))

	// A cross-site request comes in between.
	_, attackerChallenge := pkceS256(t)
	injectedID := extractAuthRequestID(t, getWithFetchSite(t, victim, o.authorizeURL(attackerChallenge, "S256"), fetchSiteCrossSite))
	if g := mustGet(t, victim, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(injectedID)); g.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /giris status for the injected id = %d, want 403", g.StatusCode)
	}

	// Both legitimate tabs must still be able to open their form and complete the login.
	for _, id := range []string{id1, id2} {
		csrf := o.openLoginForm(t, victim, id)
		resp := o.submitLogin(t, victim, id, csrf, "kurban@ornek.com", "dogruSifre12")
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("legitimate tab POST /giris status = %d, want 302", resp.StatusCode)
		}
		if code := extractCode(t, mustGet(t, victim, resp.Header.Get("Location"))); code == "" {
			t.Fatal("legitimate tab produced no code: the cross-site request broke the binding")
		}
	}
}

// If federation is NOT CONFIGURED AT ALL (setupTestServer passes empty flows)
// NO federation endpoint must be wired and the login page must work without
// buttons, UNBROKEN.
//
// WHY IT MATTERS: an endpoint that is wired with a button that is not shown,
// or a button that is shown with an endpoint that is not wired, leads the user
// into an error either way. The production wiring (buildMux) must enforce
// this rule itself.
func TestBuildMux_NoFederationMountsNoEndpoints(t *testing.T) {
	o := newTestServer(t)
	c := newBrowser(t)

	for _, path := range []string{
		"/federasyon/google/baslat?authRequestID=x",
		"/federasyon/apple/baslat?authRequestID=x",
		"/federasyon/google/geri?state=x&code=y",
		"/baglama?beklemeID=x",
	} {
		resp := mustGet(t, c, o.issuer+path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404 (endpoint must not be mounted)", path, resp.StatusCode)
		}
	}

	// The login page still works and carries NO federation button.
	_, challenge := pkceS256(t)
	id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256")))
	formResp := mustGet(t, c, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id))
	if formResp.StatusCode != http.StatusOK {
		t.Fatalf("login form status = %d, want 200", formResp.StatusCode)
	}
	bodyText := readBody(t, formResp)
	if !strings.Contains(bodyText, `name="sifre"`) {
		t.Errorf("login form is broken:\n%s", bodyText)
	}
	if strings.Contains(bodyText, "/federasyon/") {
		t.Errorf("a button appeared in a setup without federation:\n%s", bodyText)
	}
}

// ---- Federation wiring (through buildMux) ----

// fedButtonPattern takes the href of the federation button on the login page.
var fedButtonPattern = regexp.MustCompile(`href="(/federasyon/[^"]*)"`)

// It measures that the federation endpoints are REALLY wired in the
// PRODUCTION WIRING, i.e. inside buildMux, and that they are rate limited.
//
// WHY THIS TEST EXISTS: the "flows are set" branch of buildMux did not run in
// any test (the only caller passed nil as the federation parameters), so the
// package would stay green even if the Mount / MountLink calls were DELETED
// ENTIRELY. The concrete result: in a process where
// AUTH_FED_GOOGLE_CLIENT_ID is set the button is VISIBLE on the login page
// but, since the endpoint is not in the mux, the request falls to
// mux.Handle("/", provider) and the user gets a 404. Likewise, passing nil
// instead of the limiter to the two calls also stayed green, so the
// unauthenticated endpoints could have gone live without any limit.
//
// Every subtest calls newTestServerFederated in ITS OWN body: what has to be
// skipped when there is no Redis is that subtest, not the test above it
// (calling it at a higher level would cause an error that silently skips the
// whole measurement).
func TestBuildMux_FederationWiring(t *testing.T) {
	// A config that keeps the limits HIGH: the wiring subtests must not be caught
	// by the rate limit; the subtests that measure the rate limit set up their
	// own tight config.
	relaxed := RateLimitConfig{
		LoginIPPerMin:     50,
		LoginEmailPerHour: 50,
		SignupIPPerHour:   50,
	}

	t.Run("start and link endpoints are mounted on the mux", func(t *testing.T) {
		o := newTestServerFederated(t, relaxed)
		c := newBrowser(t)

		// NO authRequestID: in Start's check order (provider 404 -> empty id 400 ->
		// cross-site 403 -> session binding 403 -> reading the ID 400) this is the
		// first check after the provider is RESOLVED. So a 400 can come back only if
		// the handler RAN; if the endpoint were not wired the request would fall to
		// mux.Handle("/", provider).
		start := mustGet(t, c, o.issuer+federationStartPath(providerGoogle))
		if start.StatusCode == http.StatusNotFound {
			t.Errorf("%s is NOT MOUNTED on the mux: it returned 404", federationStartPath(providerGoogle))
		} else if start.StatusCode != http.StatusBadRequest {
			t.Errorf("start status = %d, want 400 (empty authRequestID)", start.StatusCode)
		}

		// NO pendingID: when the link GET handler finds no valid pending binding it
		// returns 403 (session reject). A 404 would mean the endpoint was never wired.
		binding := mustGet(t, c, o.issuer+pathLink)
		if binding.StatusCode == http.StatusNotFound {
			t.Errorf("%s is NOT MOUNTED on the mux: it returned 404", pathLink)
		} else if binding.StatusCode != http.StatusForbidden {
			t.Errorf("link status = %d, want 403 (no pending)", binding.StatusCode)
		}
	})

	t.Run("the button on the login page goes to the mounted endpoint's path", func(t *testing.T) {
		o := newTestServerFederated(t, relaxed)
		c := newBrowser(t)
		_, challenge := pkceS256(t)
		id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256")))

		page := mustGet(t, c, o.issuer+pathLogin+"?authRequestID="+url.QueryEscape(id))
		if page.StatusCode != http.StatusOK {
			t.Fatalf("login page status = %d", page.StatusCode)
		}
		// ALL buttons are checked: more than one provider may be configured in the
		// setup (see wireFederation) and the path of every button MUST be the path of
		// a wired endpoint.
		allMatches := fedButtonPattern.FindAllStringSubmatch(readBody(t, page), -1)
		if allMatches == nil {
			t.Fatal("no federation button on the login page")
		}
		seen := map[string]bool{}
		for _, matches := range allMatches {
			// The template escapes the "&" character inside the href; to separate the
			// path we drop the query part, the assertion is on the PATH.
			buttonPath, _, _ := strings.Cut(matches[1], "?")
			seen[buttonPath] = true
			// The button's REAL href must get an answer on the same mux too.
			resp := mustGet(t, c, o.issuer+html.UnescapeString(matches[1]))
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("button href returned 404: %q", matches[1])
			}
		}
		if !seen[federationStartPath(providerGoogle)] {
			t.Fatalf("Google button does not go to the mounted endpoint's path; paths seen: %v", seen)
		}
	})

	t.Run("start endpoint is rate limited", func(t *testing.T) {
		o := newTestServerFederated(t, RateLimitConfig{
			LoginIPPerMin: 1, LoginEmailPerHour: 50, SignupIPPerHour: 50,
		})
		c := newBrowser(t)
		path := o.issuer + federationStartPath(providerGoogle)

		if first := mustGet(t, c, path); first.StatusCode == http.StatusTooManyRequests {
			t.Fatal("first start got 429; the limit is set up wrong")
		}
		if second := mustGet(t, c, path); second.StatusCode != http.StatusTooManyRequests {
			t.Errorf("second start status = %d, want 429", second.StatusCode)
		}
	})

	t.Run("link endpoint is rate limited", func(t *testing.T) {
		o := newTestServerFederated(t, RateLimitConfig{
			LoginIPPerMin: 1, LoginEmailPerHour: 50, SignupIPPerHour: 50,
		})
		c := newBrowser(t)
		path := o.issuer + pathLink

		if first := mustGet(t, c, path); first.StatusCode == http.StatusTooManyRequests {
			t.Fatal("first link got 429; the limit is set up wrong")
		}
		if second := mustGet(t, c, path); second.StatusCode != http.StatusTooManyRequests {
			t.Errorf("second link status = %d, want 429", second.StatusCode)
		}
	})
}
