package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/auth"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// ---------------------------------------------------------------------
// Helpers: fake upstream provider (NO NETWORK, our own RSA keys). The tests
// never reach out to Apple/Google.
// ---------------------------------------------------------------------

// fedKID derives a deterministic "kid" from an RSA key: the same key must
// carry the same kid both when published in the JWKS and when signing the
// token, otherwise the verifier cannot pick the right key.
func fedKID(key *rsa.PrivateKey) string {
	total := sha256.Sum256(key.N.Bytes())
	return base64.RawURLEncoding.EncodeToString(total[:8])
}

func fedRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("could not generate rsa key: %v", err)
	}
	return key
}

func fedJWK(key *rsa.PrivateKey) map[string]any {
	pub := key.PublicKey
	return map[string]any{
		"kty": "RSA",
		"kid": fedKID(key),
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// fakeProvider imitates the JWKS and token endpoints of an upstream provider
// (Apple/Google). Every field of the id_token the token endpoint will produce
// can be set from the test: the attack tests break exactly these fields
// (nonce, aud, signing key).
type fakeProvider struct {
	providerName string
	clientID     string
	server       *httptest.Server

	// publishedKey is the key PUBLISHED in the JWKS; signingKey is the key that
	// REALLY signs the token. If the two differ the signature cannot be
	// verified (the invalid signature test).
	publishedKey *rsa.PrivateKey

	mu            sync.Mutex
	signingKey    *rsa.PrivateKey
	sub           string
	email         string
	name          string
	emailVerified bool
	nonce         string
	forceAud      string
	forceIss      string
	tokenCount    int
	lastCode      string
	lastVerifier  string
	// issuedTokens is ALL the id_tokens the token endpoint returned: kept to
	// verify that none of them appears in any log.
	issuedTokens []string
}

func newFakeProvider(t *testing.T, name string) *fakeProvider {
	t.Helper()
	key := fedRSAKey(t)
	s := &fakeProvider{
		providerName:  name,
		clientID:      name + "-istemci",
		publishedKey:  key,
		signingKey:    key,
		sub:           name + "-sub-1",
		email:         name + "@ornek.com",
		name:          "Test Kullanici",
		emailVerified: true,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{fedJWK(s.publishedKey)}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.tokenCount++
		s.lastCode = r.PostFormValue("code")
		s.lastVerifier = r.PostFormValue("code_verifier")
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": s.idToken(t)})
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

// idToken produces the id_token the provider's token endpoint will return.
func (s *fakeProvider) idToken(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	iss := s.server.URL
	if s.forceIss != "" {
		iss = s.forceIss
	}
	aud := s.clientID
	if s.forceAud != "" {
		aud = s.forceAud
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":            iss,
		"aud":            aud,
		"sub":            s.sub,
		"email":          s.email,
		"email_verified": s.emailVerified,
		"name":           s.name,
		"nonce":          s.nonce,
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = fedKID(s.signingKey)
	signed, err := token.SignedString(s.signingKey)
	if err != nil {
		t.Fatalf("could not sign id_token: %v", err)
	}
	s.issuedTokens = append(s.issuedTokens, signed)
	return signed
}

func (s *fakeProvider) configure(f func(*fakeProvider)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// tokens returns a copy of the id_tokens the token endpoint returned.
func (s *fakeProvider) tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.issuedTokens...)
}

func (s *fakeProvider) counts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenCount
}

// fakeFederationRequests is the in-memory equivalent of the federation
// request store: it both reads the pending request (Start's existence check)
// and implements the completion contract (a second call does NOT overwrite
// Subject).
type fakeFederationRequests struct {
	mu       sync.Mutex
	requests map[string]*AuthRequest
	bound    map[string]string
	amrs     map[string][]string
}

func newFakeFederationRequests() *fakeFederationRequests {
	return &fakeFederationRequests{
		requests: map[string]*AuthRequest{},
		bound:    map[string]string{},
		amrs:     map[string][]string{},
	}
}

func (s *fakeFederationRequests) add(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[id] = &AuthRequest{ID: id, ClientID: "mobil"}
}

// drop REMOVES the request from the store: the equivalent of Redis dropping the
// key when requestTTL (10 min) expires in production. Since the TTL values
// are DELIBERATELY not changed, the tests measure expiry this way -- instead
// of waiting 10 minutes.
func (s *fakeFederationRequests) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.requests, id)
}

func (s *fakeFederationRequests) GetByID(_ context.Context, id string) (*AuthRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, exists := s.requests[id]
	if !exists {
		return nil, ErrRequestNotFound
	}
	clone := *req
	return &clone, nil
}

func (s *fakeFederationRequests) MarkDone(_ context.Context, id string, userID string, amr []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.requests[id]; !exists {
		return ErrRequestNotFound
	}
	if existing, exists := s.bound[id]; exists {
		if existing == userID {
			return nil
		}
		return ErrRequestAlreadyCompleted
	}
	s.bound[id] = userID
	s.amrs[id] = append([]string(nil), amr...)
	return nil
}

func (s *fakeFederationRequests) subject(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bound[id]
}

func (s *fakeFederationRequests) amrFor(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.amrs[id]
}

// ---------------------------------------------------------------------
// Test environment
// ---------------------------------------------------------------------

const fedTestIssuer = "https://op.ornek"

type fedEnv struct {
	pages     *FederationPages
	mux       *http.ServeMux
	store     *RequestStore
	users     *FakeUserStore
	federated *FakeFederatedStore
	sessions  *FakeSessionStore
	requests  *fakeFederationRequests
	google    *fakeProvider
	apple     *fakeProvider
	authReqID string

	// sessionCookie is the Phase 1 session cookie that binds authReqID to THIS
	// browser: the equivalent of the cookie the /authorize leg would produce.
	// Start REQUIRES it (see C1).
	sessionCookie *http.Cookie
}

// newTestFederationPages sets up the federation endpoints with REAL Redis
// (state and pending records), REAL id_token verification (our own RSA keys)
// and in-memory stores.
func newTestFederationPages(t *testing.T) *fedEnv {
	t.Helper()
	rdb := testRedis(t) // Skipped here if there is no Redis
	store := NewRequestStore(rdb)
	users := NewFakeUserStore()
	federated := NewFakeFederatedStore(users)
	sessionList := NewFakeSessionStore()
	reqLog := newFakeFederationRequests()

	google := newFakeProvider(t, providerGoogle)
	apple := newFakeProvider(t, providerApple)
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
			// REAL .p8 key is needed in the test too.
			AppleTeamID: "EKIP123", AppleKeyID: "ANAHTAR9", AppleKeyP8: appleP8,
		},
	}
	verifiers := map[string]*auth.UpstreamVerifier{}
	for name, s := range map[string]*fakeProvider{providerGoogle: google, providerApple: apple} {
		d, err := auth.NewUpstreamVerifier(context.Background(), s.server.URL+"/jwks", s.server.URL, s.clientID)
		if err != nil {
			t.Fatalf("could not set up the %s verifier: %v", name, err)
		}
		verifiers[name] = d
	}

	callback := func(_ context.Context, id string) string {
		return "/authorize/callback?" + url.Values{"id": {id}}.Encode()
	}
	// secure = true: the issuer is https, i.e. production behavior.
	f := NewFederationPages(flows, verifiers, store, federated, users, reqLog,
		sessionList, callback, fedTestIssuer, true)
	f.httpClient = &http.Client{Timeout: 5 * time.Second}

	mux := http.NewServeMux()
	interceptor := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	f.Mount(mux, interceptor, nil)
	f.MountLink(mux, interceptor, nil)

	env := &fedEnv{
		pages: f, mux: mux, store: store, users: users, federated: federated,
		sessions: sessionList, requests: reqLog, google: google, apple: apple,
		authReqID: "istek-1",
	}
	reqLog.add(env.authReqID)
	env.sessionCookie = env.bindSession(t, env.authReqID)
	return env
}

// peekState reads the state record in Redis WITHOUT CONSUMING it (so the test
// can learn the nonce without disturbing the flow). The production path is
// TakeState and it uses GETDEL.
func (env *fedEnv) peekState(t *testing.T, state string) FederationState {
	t.Helper()
	data, err := env.store.rdb.Get(context.Background(), stateKey(state)).Bytes()
	if err != nil {
		t.Fatalf("could not read state record: %v", err)
	}
	var d FederationState
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("could not decode state record: %v", err)
	}
	return d
}

// startRaw calls the start endpoint with the GIVEN session cookie and request.
// If crossSite=true it sends Sec-Fetch-Site: cross-site.
func (env *fedEnv) startRaw(provider, authReqID string, cookie *http.Cookie, crossSite bool) *httptest.ResponseRecorder {
	target := "/federasyon/" + provider + "/baslat?" + url.Values{"authRequestID": {authReqID}}.Encode()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if crossSite {
		r.Header.Set(headerFetchSite, fetchSiteCrossSite)
	}
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, r)
	return w
}

// stateCount returns how many federation state records are in Redis: the
// proof of the claim "a rejected start wrote NO record".
func (env *fedEnv) stateCount(t *testing.T) int {
	t.Helper()
	keyList, err := env.store.rdb.Keys(context.Background(), stateKey("*")).Result()
	if err != nil {
		t.Fatalf("could not read state keys: %v", err)
	}
	return len(keyList)
}

// hasFedCookie says whether the response has a kimlik_fed cookie.
func hasFedCookie(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieNameFederation {
			return true
		}
	}
	return false
}

// start calls GET /federasyon/<provider>/baslat and returns (state, fed
// cookie, response). The session cookie of the browser that STARTED the
// request is SENT: Start requires it (C1).
func (env *fedEnv) start(t *testing.T, provider, authReqID string) (string, *http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	w := env.startRaw(provider, authReqID, env.sessionCookie, false)
	if w.Code != http.StatusFound {
		return "", nil, w
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("could not parse Location: %v", err)
	}
	state := location.Query().Get("state")
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieNameFederation {
			cookie = c
		}
	}
	return state, cookie, w
}

// callback calls the callback return endpoint. If post=true it imitates
// Apple's form_post return (a CROSS-SITE POST).
func (env *fedEnv) callback(provider, state, code string, cookie *http.Cookie, post bool) *httptest.ResponseRecorder {
	path := "/federasyon/" + provider + "/geri"
	value := url.Values{"state": {state}, "code": {code}}
	var r *http.Request
	if post {
		r = httptest.NewRequest(http.MethodPost, path, strings.NewReader(value.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// Apple's form_post return is cross-site (K3).
		r.Header.Set(headerFetchSite, fetchSiteCrossSite)
	} else {
		r = httptest.NewRequest(http.MethodGet, path+"?"+value.Encode(), nil)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, r)
	return w
}

// runFlow runs the whole happy path: start -> (give the nonce to the fake
// provider) -> callback. The returned response is the response of the last step.
func (env *fedEnv) runFlow(t *testing.T, provider string, post bool) *httptest.ResponseRecorder {
	t.Helper()
	state, cookie, w := env.start(t, provider, env.authReqID)
	if state == "" {
		t.Fatalf("start failed: status = %d", w.Code)
	}
	d := env.peekState(t, state)
	env.provider(provider).configure(func(s *fakeProvider) { s.nonce = d.Nonce })
	return env.callback(provider, state, "kod-1", cookie, post)
}

func (env *fedEnv) provider(name string) *fakeProvider {
	if name == providerApple {
		return env.apple
	}
	return env.google
}

// ---------------------------------------------------------------------
// Step 1: Start tests
// ---------------------------------------------------------------------

// Start must bind the request to the browser that STARTED it: so that the
// federation side does not reopen Phase 1's C1 (account takeover) hole.
func TestFederationStart_SetsCookieAndWritesState(t *testing.T) {
	env := newTestFederationPages(t)
	state, cookie, w := env.start(t, providerGoogle, env.authReqID)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if state == "" {
		t.Fatal("no state in Location")
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("federation cookie was not set")
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("could not parse Location: %v", err)
	}
	if !strings.HasPrefix(w.Header().Get("Location"), env.google.server.URL+"/authorize") {
		t.Errorf("Location is not the upstream authorize address")
	}
	q := location.Query()
	if q.Get("client_id") != env.google.clientID {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("nonce") == "" {
		t.Error("nonce was not sent")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	wantCallback := fedTestIssuer + "/federasyon/" + providerGoogle + "/geri"
	if q.Get("redirect_uri") != wantCallback {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), wantCallback)
	}

	d := env.peekState(t, state)
	if d.Cookie != cookie.Value {
		t.Error("the cookie value in the state record differs from the cookie in the response")
	}
	if d.AuthRequestID != env.authReqID {
		t.Errorf("AuthRequestID = %q", d.AuthRequestID)
	}
	if d.Provider != providerGoogle {
		t.Errorf("Saglayici = %q", d.Provider)
	}
	if d.Nonce != q.Get("nonce") {
		t.Error("the nonce in the record differs from the nonce in the authorize URL")
	}
	if d.CodeVerifier == "" {
		t.Error("no code verifier in the record")
	}
	// The verifier must not appear in the URL in PLAIN TEXT; only its digest goes.
	if strings.Contains(w.Header().Get("Location"), d.CodeVerifier) {
		t.Error("the code verifier was put into the authorize URL in plain text")
	}
}

// K2: Apple's form_post return is CROSS-SITE and a SameSite=Lax cookie is NOT
// SENT on that POST. That is why the federation cookie must be None -- but
// HttpOnly and Secure CANNOT be given up.
func TestFederationCookie_SameSiteNone(t *testing.T) {
	env := newTestFederationPages(t)
	_, cookie, _ := env.start(t, providerApple, env.authReqID)
	if cookie == nil {
		t.Fatal("federation cookie was not set")
	}
	if cookie.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None (K2: Apple form_post is a cross-site POST)", cookie.SameSite)
	}
	if !cookie.HttpOnly {
		t.Error("HttpOnly is off")
	}
	if !cookie.Secure {
		t.Error("Secure is off (issuer is https)")
	}
	if cookie.Path != "/federasyon" {
		t.Errorf("Path = %q, want /federasyon (must NOT overwrite the session cookie)", cookie.Path)
	}
}

// The session cookie must NOT CHANGE: turning it into SameSite=None would
// weaken Phase 1's login-CSRF protection.
func TestFederationStart_LeavesSessionCookie(t *testing.T) {
	env := newTestFederationPages(t)
	_, _, w := env.start(t, providerGoogle, env.authReqID)
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieNameSession {
			t.Fatalf("start touched the session cookie: %+v", c)
		}
	}
}

func TestFederationStart_UnknownProvider404(t *testing.T) {
	env := newTestFederationPages(t)
	_, _, w := env.start(t, "facebook", env.authReqID)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestFederationStart_InvalidAuthRequestIDRejected(t *testing.T) {
	env := newTestFederationPages(t)
	// A session binding IS SET UP for the "nonexistent id": otherwise the test
	// stops at 403 and never measures the "does the request exist" check.
	missingCookie := env.bindSession(t, "yok-boyle-bir-istek")
	for _, d := range []struct {
		name   string
		id     string
		cookie *http.Cookie
	}{
		{"empty id", "", env.sessionCookie},
		{"nonexistent id", "yok-boyle-bir-istek", missingCookie},
	} {
		t.Run(d.name, func(t *testing.T) {
			w := env.startRaw(providerGoogle, d.id, d.cookie, false)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if hasFedCookie(w) {
				t.Error("an invalid request must not set the federation cookie")
			}
			if !strings.Contains(w.Body.String(), msgRequestInvalid) {
				t.Errorf("body = %q", w.Body.String())
			}
		})
	}
}

// C1: authRequestID ALONE is NOT an authorization input. Start REQUIRES the
// session binding of the browser that STARTED the request at /authorize.
// Otherwise someone who learns a live authRequestID could run the
// federation leg in THEIR OWN browser and make Callback complete the request
// for THEIR OWN account (Phase 1's C1 account takeover, through a new door).
func TestFederationStart_SessionBindingRequired(t *testing.T) {
	for _, d := range []struct {
		name   string
		cookie func(env *fedEnv, t *testing.T) *http.Cookie
	}{
		{"no session cookie", func(*fedEnv, *testing.T) *http.Cookie { return nil }},
		{"made-up session cookie", func(*fedEnv, *testing.T) *http.Cookie {
			return &http.Cookie{Name: cookieNameSession, Value: "saldirganin-uydurdugu-deger"}
		}},
		{"session bound to ANOTHER request", func(env *fedEnv, t *testing.T) *http.Cookie {
			return env.bindSession(t, "saldirganin-kendi-istegi")
		}},
	} {
		t.Run(d.name, func(t *testing.T) {
			env := newTestFederationPages(t)
			previousRecord := env.stateCount(t)

			w := env.startRaw(providerGoogle, env.authReqID, d.cookie(env, t), false)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
			if hasFedCookie(w) {
				t.Error("a request without a binding must NOT get the federation cookie")
			}
			if after := env.stateCount(t); after != previousRecord {
				t.Errorf("state record was written (%d -> %d); a request without a binding must produce NO record",
					previousRecord, after)
			}
			if w.Header().Get("Location") != "" {
				t.Errorf("redirected upstream: %q", w.Header().Get("Location"))
			}
		})
	}
}

// C1 (b): K3's exemption belongs ONLY to the return endpoint. Start cannot be
// called from cross-site; WrapAuthorize's decision NOT to set up a binding in
// its cross-site branch only works if ALL completion paths look for a
// binding.
func TestFederationStart_CrossSiteRejected(t *testing.T) {
	env := newTestFederationPages(t)
	previousRecord := env.stateCount(t)

	// The session cookie is VALID: the ONLY reason for the rejection must be
	// cross-site.
	w := env.startRaw(providerGoogle, env.authReqID, env.sessionCookie, true)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if hasFedCookie(w) {
		t.Error("a cross-site request must NOT get the federation cookie")
	}
	if after := env.stateCount(t); after != previousRecord {
		t.Errorf("state record was written (%d -> %d)", previousRecord, after)
	}
}

// The invariant of C1 (b): when /authorize is reached from cross-site,
// WrapAuthorize sets up NO binding and relies on the assumption "the auth
// request is created but, since no browser is bound to it, the request
// CANNOT BE COMPLETED". The federation leg must NOT BREAK that assumption:
// a request without a binding cannot be completed via federation either.
func TestFederationStart_UnboundRequestCannotComplete(t *testing.T) {
	env := newTestFederationPages(t)
	// The state WrapAuthorize leaves behind: the request EXISTS, the binding
	// does NOT.
	unbound := "baglamasiz-istek"
	env.requests.add(unbound)

	w := env.startRaw(providerGoogle, unbound, nil, false)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	// Nothing that could be completed was produced: no state, no cookie.
	if hasFedCookie(w) {
		t.Error("federation cookie was set")
	}
	if n := env.stateCount(t); n != 0 {
		t.Errorf("state record count = %d, want 0", n)
	}
	// And the request was NOT BOUND to any user.
	if s := env.requests.subject(unbound); s != "" {
		t.Errorf("Subject = %q, want empty (MarkDone must NOT be reached)", s)
	}
}

// Since the button of an unconfigured provider never shows, we should not
// arrive here; if we do, 404, not "500".
func TestFederationStart_UnconfiguredProvider404(t *testing.T) {
	env := newTestFederationPages(t)
	// Imitate a setup where Apple is not configured.
	delete(env.pages.upstreams, providerApple)
	_, _, w := env.start(t, providerApple, env.authReqID)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------
// Step 2: Callback tests
// ---------------------------------------------------------------------

// expectedCallback is the redirect address expected on the happy path.
func (env *fedEnv) expectedCallback() string {
	return "/authorize/callback?" + url.Values{"id": {env.authReqID}}.Encode()
}

// Happy path: known (provider, sub) -> login as that user.
func TestFederationCallback_KnownIdentityLogsIn(t *testing.T) {
	env := newTestFederationPages(t)
	existing, err := env.users.Create(context.Background(), "bagli@ornek.com", "Bagli", "cokgizlisifre")
	if err != nil {
		t.Fatalf("could not create user: %v", err)
	}
	if err := env.federated.Link(context.Background(), providerGoogle, env.google.sub, existing.ID); err != nil {
		t.Fatalf("could not link identity: %v", err)
	}

	w := env.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body = %q", w.Code, w.Body.String())
	}
	if location := w.Header().Get("Location"); location != env.expectedCallback() {
		t.Errorf("Location = %q, want %q", location, env.expectedCallback())
	}
	if s := env.requests.subject(env.authReqID); s != existing.ID {
		t.Errorf("Subject = %q, want %q", s, existing.ID)
	}
	if got := env.requests.amrFor(env.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, want [fed]", got)
	}
	if env.google.counts() != 1 {
		t.Errorf("token endpoint called %d times, want 1", env.google.counts())
	}
}

// New identity -> the account is created and the user logs in.
func TestFederationCallback_NewIdentityCreatesAccount(t *testing.T) {
	env := newTestFederationPages(t)
	env.google.configure(func(s *fakeProvider) { s.email = "yeni@ornek.com" })

	w := env.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body = %q", w.Code, w.Body.String())
	}
	fresh, err := env.users.ByEmail(context.Background(), "yeni@ornek.com")
	if err != nil {
		t.Fatalf("could not find the new user: %v", err)
	}
	if fresh.PasswordHash != "" {
		t.Error("a federated account must not have a password")
	}
	linked, err := env.federated.UserByIdentity(context.Background(), providerGoogle, env.google.sub)
	if err != nil {
		t.Fatalf("federated identity was not written: %v", err)
	}
	if linked.ID != fresh.ID {
		t.Errorf("identity is linked to user %q, want %q", linked.ID, fresh.ID)
	}
	if s := env.requests.subject(env.authReqID); s != fresh.ID {
		t.Errorf("Subject = %q, want %q", s, fresh.ID)
	}
	if got := env.requests.amrFor(env.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, want [fed]", got)
	}
}

// Apple's form_post return is a CROSS-SITE POST (K2/K3): the POST must be
// accepted AND Sec-Fetch-Site: cross-site must NOT be REJECTED.
func TestFederationCallback_CrossSitePostAccepted(t *testing.T) {
	env := newTestFederationPages(t)
	w := env.runFlow(t, providerApple, true)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body = %q", w.Code, w.Body.String())
	}
	if env.requests.subject(env.authReqID) == "" {
		t.Error("request was not completed")
	}
}

// --- ATTACK TESTS ---

// state missing or unknown: the flow must stop. This is the first attempt of
// an attacker who tries to guess the callback and complete the victim's request.
func TestFederationCallback_UnknownStateRejected(t *testing.T) {
	env := newTestFederationPages(t)
	for _, d := range []struct {
		name  string
		state string
	}{
		{"empty state", ""},
		{"made-up state", "uydurma-state-degeri"},
	} {
		t.Run(d.name, func(t *testing.T) {
			w := env.callback(providerGoogle, d.state, "kod-1", &http.Cookie{Name: cookieNameFederation, Value: "herhangi"}, false)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
			if env.requests.subject(env.authReqID) != "" {
				t.Error("request must NOT be completed")
			}
			if env.google.counts() != 0 {
				t.Error("token exchange must NOT happen at all")
			}
		})
	}
}

// state is SINGLE-USE: the same callback cannot be replayed twice.
func TestFederationCallback_StateReplayRejected(t *testing.T) {
	env := newTestFederationPages(t)
	env.google.configure(func(s *fakeProvider) { s.email = "tekrar@ornek.com" })

	state, cookie, w := env.start(t, providerGoogle, env.authReqID)
	if state == "" {
		t.Fatalf("start failed: status = %d", w.Code)
	}
	d := env.peekState(t, state)
	env.google.configure(func(s *fakeProvider) { s.nonce = d.Nonce })

	first := env.callback(providerGoogle, state, "kod-1", cookie, false)
	if first.Code != http.StatusFound {
		t.Fatalf("first return status = %d, want 302, body = %q", first.Code, first.Body.String())
	}
	second := env.callback(providerGoogle, state, "kod-1", cookie, false)
	if second.Code != http.StatusForbidden {
		t.Errorf("second return status = %d, want 403 (state is single-use)", second.Code)
	}
	if env.google.counts() != 1 {
		t.Errorf("token endpoint called %d times, want 1 (a replay must NOT reach the token exchange)", env.google.counts())
	}
}

// Proof of K2: if the state is RIGHT but the cookie is MISSING/WRONG it must
// be rejected. Otherwise an attacker could open a valid state+code pair from
// their own authorization in the victim's browser and make the victim's flow
// complete for the attacker's own account.
func TestFederationCallback_CookieMismatchRejected(t *testing.T) {
	for _, d := range []struct {
		name       string
		makeCookie func(correct *http.Cookie) *http.Cookie
	}{
		{"no cookie", func(*http.Cookie) *http.Cookie { return nil }},
		{"wrong cookie", func(*http.Cookie) *http.Cookie {
			return &http.Cookie{Name: cookieNameFederation, Value: "saldirganin-cerezi"}
		}},
		{"empty cookie", func(*http.Cookie) *http.Cookie {
			return &http.Cookie{Name: cookieNameFederation, Value: ""}
		}},
	} {
		t.Run(d.name, func(t *testing.T) {
			env := newTestFederationPages(t)
			// 1) The attacker gets a valid state in their own browser.
			state, validCookie, w := env.start(t, providerGoogle, env.authReqID)
			if state == "" {
				t.Fatalf("start failed: status = %d", w.Code)
			}
			status := env.peekState(t, state)
			env.google.configure(func(s *fakeProvider) { s.nonce = status.Nonce })

			// 2) It arrives with the same state from the victim's browser (cookie missing/wrong).
			resp := env.callback(providerGoogle, state, "kod-1", d.makeCookie(validCookie), false)
			if resp.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", resp.Code)
			}
			if env.requests.subject(env.authReqID) != "" {
				t.Error("request must NOT be completed (Subject must stay empty)")
			}
			if env.google.counts() != 0 {
				t.Error("token exchange must NOT happen at all")
			}
		})
	}
}

// nonce: the proof that Task 2's VerifyWithNonce really gets called in the
// flow. If the upstream returns an id_token with ANOTHER nonce the flow must stop.
func TestFederationCallback_NonceMismatchRejected(t *testing.T) {
	env := newTestFederationPages(t)
	state, cookie, w := env.start(t, providerGoogle, env.authReqID)
	if state == "" {
		t.Fatalf("start failed: status = %d", w.Code)
	}
	// The provider signs with ANOTHER nonce: the scenario where an attacker
	// injects a valid (signature/iss/aud correct) id_token obtained from their
	// own authorization into the victim's flow.
	env.google.configure(func(s *fakeProvider) { s.nonce = "saldirganin-nonce-degeri" })

	resp := env.callback(providerGoogle, state, "kod-1", cookie, false)
	if resp.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.Code)
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Error("request must NOT be completed")
	}
	if _, err := env.users.ByEmail(context.Background(), env.google.email); err == nil {
		t.Error("no account must be CREATED when the nonce does not match")
	}
}

// An id_token with an invalid signature, or signed with another key, must be rejected.
func TestFederationCallback_InvalidSignatureRejected(t *testing.T) {
	env := newTestFederationPages(t)
	// Sign with ANOTHER key that is not published in the JWKS.
	env.google.configure(func(s *fakeProvider) { s.signingKey = fedRSAKey(t) })

	w := env.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Error("request must NOT be completed")
	}
}

// If the upstream returns an id_token whose aud was issued to ANOTHER client
// it must be rejected (login with another app's token).
func TestFederationCallback_WrongAudienceRejected(t *testing.T) {
	env := newTestFederationPages(t)
	env.google.configure(func(s *fakeProvider) { s.forceAud = "baska-uygulama" })

	w := env.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Error("request must NOT be completed")
	}
}

// If the authRequestID has already been completed for ANOTHER user it must be
// rejected through the ErrRequestAlreadyCompleted path (Phase 1's rule also
// applies to federation).
func TestFederationCallback_CompletedRequestOtherUserRejected(t *testing.T) {
	env := newTestFederationPages(t)
	if err := env.requests.MarkDone(context.Background(), env.authReqID, "baska-kullanici", []string{amrFederation}); err != nil {
		t.Fatalf("pre-completion: %v", err)
	}
	w := env.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), msgRequestAlreadyUsed) {
		t.Errorf("body = %q", w.Body.String())
	}
	if s := env.requests.subject(env.authReqID); s != "baska-kullanici" {
		t.Errorf("Subject was overwritten: %q", s)
	}
}

// The provider is read from the state RECORD, not from the path. Otherwise an
// attacker could bring a flow started at Google to /federasyon/apple/geri and
// confuse the verifier selection.
func TestFederationCallback_ProviderReadFromState(t *testing.T) {
	env := newTestFederationPages(t)
	env.google.configure(func(s *fakeProvider) { s.email = "kayittan@ornek.com" })

	state, cookie, w := env.start(t, providerGoogle, env.authReqID)
	if state == "" {
		t.Fatalf("start failed: status = %d", w.Code)
	}
	d := env.peekState(t, state)
	env.google.configure(func(s *fakeProvider) { s.nonce = d.Nonce })

	// A flow started at Google comes back through the APPLE path.
	resp := env.callback(providerApple, state, "kod-1", cookie, false)
	if resp.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (the flow must run with the GOOGLE in the record), body = %q",
			resp.Code, resp.Body.String())
	}
	if env.google.counts() != 1 {
		t.Errorf("google token endpoint called %d times, want 1", env.google.counts())
	}
	if env.apple.counts() != 0 {
		t.Errorf("apple token endpoint called %d times, want 0 (the path value must not be used)", env.apple.counts())
	}
	linked, err := env.federated.UserByIdentity(context.Background(), providerGoogle, env.google.sub)
	if err != nil {
		t.Fatalf("identity was not written as GOOGLE: %v", err)
	}
	if linked.Email != "kayittan@ornek.com" {
		t.Errorf("email = %q", linked.Email)
	}
}

// The callback return endpoint is unauthenticated and every request triggers a
// Redis read (and, with a valid state, an HTTP call to the upstream); the
// limit must be kept in a counter SEPARATE from the LOGIN and LINK endpoints.
func TestFederationCallback_RateLimitApplied(t *testing.T) {
	env := newTestFederationPages(t)
	limiter, err := NewRateLimiter(env.store.rdb, RateLimitConfig{
		LoginIPPerMin: 1, LoginEmailPerHour: 100, SignupIPPerHour: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("could not set up the rate limiter: %v", err)
	}
	mux := http.NewServeMux()
	interceptor := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	env.pages.Mount(mux, interceptor, limiter)
	env.mux = mux

	first := env.callback(providerGoogle, "uydurma-state", "kod-1", nil, false)
	if first.Code == http.StatusTooManyRequests {
		t.Fatal("first attempt got 429; the limit is set up wrong")
	}
	second := env.callback(providerGoogle, "uydurma-state", "kod-1", nil, false)
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second attempt status = %d, want 429", second.Code)
	}
	keyList, err := env.store.rdb.Keys(context.Background(), rateLimitPrefix+":*").Result()
	if err != nil {
		t.Fatalf("could not read keys: %v", err)
	}
	var hasCallback bool
	for _, a := range keyList {
		if strings.Contains(a, ":federasyon-geri:") {
			hasCallback = true
		}
		if strings.Contains(a, ":giris:") || strings.Contains(a, ":baglama:") {
			t.Errorf("federation return consumed another endpoint's counter: %q", a)
		}
	}
	if !hasCallback {
		t.Errorf("federasyon-geri counter was not created; keys: %v", keyList)
	}
}

// /federasyon/{provider}/baslat must be counted per IP and must be on a key
// SEPARATE from the login/signup/link/callback counters: otherwise federation
// attempts would use up the password attempt budget and a legitimate user
// would lock their own login flow.
func TestFederationStart_RateLimitCountedSeparately(t *testing.T) {
	env := newTestFederationPages(t)
	limiter, err := NewRateLimiter(env.store.rdb, RateLimitConfig{
		LoginIPPerMin: 1, LoginEmailPerHour: 100, SignupIPPerHour: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("could not set up the rate limiter: %v", err)
	}
	mux := http.NewServeMux()
	interceptor := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	env.pages.Mount(mux, interceptor, limiter)
	env.mux = mux

	first := env.startRaw(providerGoogle, env.authReqID, env.sessionCookie, false)
	if first.Code == http.StatusTooManyRequests {
		t.Fatal("first start got 429; the limit is set up wrong")
	}
	second := env.startRaw(providerGoogle, env.authReqID, env.sessionCookie, false)
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second start status = %d, want 429", second.Code)
	}

	keyList, err := env.store.rdb.Keys(context.Background(), rateLimitPrefix+":*").Result()
	if err != nil {
		t.Fatalf("could not read keys: %v", err)
	}
	var hasStart bool
	for _, a := range keyList {
		if strings.Contains(a, ":federasyon-baslat:") {
			hasStart = true
		}
		// The counters of the other endpoints must NOT have been consumed. Including
		// ":federasyon-geri:": start and callback are separate budgets.
		for _, foreign := range []string{":giris:", ":kayit:", ":baglama:", ":federasyon-geri:"} {
			if strings.Contains(a, foreign) {
				t.Errorf("federation start consumed another endpoint's counter: %q", a)
			}
		}
	}
	if !hasStart {
		t.Errorf("federasyon-baslat counter was not created; keys: %v", keyList)
	}
}

// NO log line may contain state, code, id_token, cookie or email. In Phase 1
// this was a Critical.
func TestFederationCallback_LogsDoNotLeak(t *testing.T) {
	env := newTestFederationPages(t)
	env.google.configure(func(s *fakeProvider) { s.email = "sizmasin@ornek.com" })

	var buf strings.Builder
	oldOutput := log.Writer()
	oldFlag := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlag)
	})

	// 1) Happy path.
	state, cookie, w := env.start(t, providerGoogle, env.authReqID)
	if state == "" {
		t.Fatalf("start failed: status = %d", w.Code)
	}
	d := env.peekState(t, state)
	env.google.configure(func(s *fakeProvider) { s.nonce = d.Nonce })
	env.callback(providerGoogle, state, "gizli-kod-degeri", cookie, false)

	// 2) Faulty paths: unknown state, cookie mismatch, nonce mismatch, replay.
	env.callback(providerGoogle, "uydurma-state", "gizli-kod-degeri", cookie, false)
	env.callback(providerGoogle, state, "gizli-kod-degeri", nil, false)
	state2, cookie2, _ := env.start(t, providerGoogle, env.authReqID)
	env.google.configure(func(s *fakeProvider) { s.nonce = "yanlis-nonce" })
	env.callback(providerGoogle, state2, "gizli-kod-degeri", cookie2, false)

	outputs := buf.String()
	forbiddenMap := map[string]string{
		"state":      state,
		"state-2":    state2,
		"code":       "gizli-kod-degeri",
		"cookie":     cookie.Value,
		"cookie-2":   cookie2.Value,
		"nonce":      d.Nonce,
		"email":      "sizmasin@ornek.com",
		"request id": env.authReqID,
		"verifier":   d.CodeVerifier,
	}
	for name, value := range forbiddenMap {
		if value == "" {
			continue
		}
		if strings.Contains(outputs, value) {
			t.Errorf("log leaked the %s value; logs: %q", name, outputs)
		}
	}
	// id_token: it was produced both on the happy path and on the path where the
	// verification FAILED; none of it (neither all nor a part) may enter a log.
	tokenList := env.google.tokens()
	if len(tokenList) == 0 {
		t.Fatal("no id_token was produced; the test does NOT MEASURE id_token leakage")
	}
	for i, token := range tokenList {
		if strings.Contains(outputs, token) {
			t.Errorf("log leaked the id_token %d; logs: %q", i, outputs)
		}
		// The signature part is searched for too: a trimmed token is a leak as well.
		for _, part := range strings.Split(token, ".") {
			if len(part) > 16 && strings.Contains(outputs, part) {
				t.Errorf("log leaked part of the id_token %d; logs: %q", i, outputs)
			}
		}
	}
}

// ---------------------------------------------------------------------
// I3: expiry must be told apart from a REAL server error
// ---------------------------------------------------------------------

// The requestTTL (10 min) counter starts at the very beginning, at
// /authorize, and is never extended on any path; federationStateTTL is also
// 10 min but is consumed IN SEQUENCE. So the federation chain can OUTLIVE the
// auth request it points to: Start passes at T=9m50s (the request EXISTS),
// the user spends 30 seconds at Google, and at T=10m20s the request NO LONGER
// EXISTS in Callback. This is an ordinary expiry and it used to produce a
// 500, raising the SAME alarm as real server errors and not telling the user
// what happened.
func TestFederationCallback_ExpiredRequestReturns400(t *testing.T) {
	env := newTestFederationPages(t)
	state, cookie, w := env.start(t, providerGoogle, env.authReqID)
	if state == "" {
		t.Fatalf("start failed: status = %d", w.Code)
	}
	d := env.peekState(t, state)
	env.google.configure(func(s *fakeProvider) { s.nonce = d.Nonce })

	// T=10m20s: the request dropped from Redis by TTL. Since the TTL values are
	// not changed, the record is dropped BY HAND.
	env.requests.drop(env.authReqID)

	resp := env.callback(providerGoogle, state, "kod-1", cookie, false)
	if resp.Code == http.StatusInternalServerError {
		t.Fatalf("an ordinary expiry produced a 500: body = %q", resp.Body.String())
	}
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %q", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), msgRequestExpired) {
		t.Errorf("the user was not told what happened: body = %q", resp.Body.String())
	}
}

// It pins all THREE ends of the classification. The last row matters in
// particular: a real server error must STAY 500, otherwise the "make
// everything 400" mutation would pass the others.
func TestWriteCompletionError_Classification(t *testing.T) {
	f := &FederationPages{}
	statuses := []struct {
		name string
		err  error
		code int
		text string
	}{
		{"already completed", ErrRequestAlreadyCompleted, http.StatusConflict, msgRequestAlreadyUsed},
		{"dropped by TTL", ErrRequestNotFound, http.StatusBadRequest, msgRequestExpired},
		{"real server error", errors.New("redis connection dropped"), http.StatusInternalServerError, msgServerError},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			f.writeCompletionError(w, d.err)
			if w.Code != d.code {
				t.Errorf("status = %d, want %d", w.Code, d.code)
			}
			if !strings.Contains(w.Body.String(), d.text) {
				t.Errorf("body = %q, want %q", w.Body.String(), d.text)
			}
		})
	}
}

// ---------------------------------------------------------------------
// M-b: if the upstream gives no email NO account is created
// ---------------------------------------------------------------------

// The users table has "email NOT NULL" + "UNIQUE (lower(email))", so AT MOST
// ONE federated account without an email can exist; the second one hits a
// 409 showing an EMPTY email via ErrEmailInUse and that user can NEVER sign
// up again. That is why creating an account with an empty email is not
// silently accepted but EXPLICITLY rejected.
//
// It is run with TWO SEPARATE subs: both must get the same explanatory
// response. In the old behavior the first would open an account and the
// SECOND would be locked out permanently.
func TestFederationCallback_NoUpstreamEmailNoAccount(t *testing.T) {
	for _, sub := range []string{"epostasiz-1", "epostasiz-2"} {
		t.Run(sub, func(t *testing.T) {
			env := newTestFederationPages(t)
			env.google.configure(func(s *fakeProvider) {
				s.email = ""
				s.sub = sub
			})

			w := env.runFlow(t, providerGoogle, false)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %q", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), msgUpstreamNoEmail) {
				t.Errorf("the user was not told what to do: body = %q", w.Body.String())
			}
			// No account must have been CREATED and no login must have HAPPENED.
			if _, err := env.federated.UserByIdentity(context.Background(), providerGoogle, sub); !errors.Is(err, ErrIdentityNotFound) {
				t.Errorf("an account was OPENED with an empty email (error = %v)", err)
			}
			if env.requests.subject(env.authReqID) != "" {
				t.Error("sign-in with an empty email SUCCEEDED")
			}
		})
	}
}

// ---------------------------------------------------------------------
// M-c: the kimlik_fed cookie is cleared on return (HYGIENE)
// ---------------------------------------------------------------------

// This is NOT a security fix: the security basis is that the state record is
// single-use through GETDEL, so a leftover cookie alone cannot replay any
// flow. Still, there is no reason for the cookie to stay in the browser for
// 10 more minutes after the flow ends.
func TestFederationCallback_SuccessClearsFederationCookie(t *testing.T) {
	env := newTestFederationPages(t)
	w := env.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("flow failed: status = %d, body = %q", w.Code, w.Body.String())
	}

	var deletion *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieNameFederation {
			deletion = c
		}
	}
	if deletion == nil {
		t.Fatal("no kimlik_fed deletion cookie on a successful return")
	}
	if deletion.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative (MaxAge=0 makes the cookie a SESSION cookie)", deletion.MaxAge)
	}
	if deletion.Value != "" {
		t.Errorf("the deletion cookie still carries a value: %q", deletion.Value)
	}
	// The attributes MUST BE THE SAME as at write time, otherwise the browser
	// writes a SECOND cookie next to the old one instead of deleting it.
	writing := env.pages.federationCookie("deger")
	if deletion.Path != writing.Path {
		t.Errorf("Path = %q, want %q", deletion.Path, writing.Path)
	}
	if deletion.Secure != writing.Secure {
		t.Errorf("Secure = %v, want %v", deletion.Secure, writing.Secure)
	}
	if deletion.HttpOnly != writing.HttpOnly {
		t.Errorf("HttpOnly = %v, want %v", deletion.HttpOnly, writing.HttpOnly)
	}
	if deletion.SameSite != writing.SameSite {
		t.Errorf("SameSite = %v, want %v", deletion.SameSite, writing.SameSite)
	}
}
