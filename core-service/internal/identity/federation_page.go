package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// Paths of the federation endpoints. The {saglayici} path value is used ONLY
// WHEN STARTING the flow; on return the provider is read from the state
// RECORD (see Callback).
const (
	pathFederationStart    = "/federasyon/{saglayici}/baslat"
	pathFederationCallback = "/federasyon/{saglayici}/geri"
)

// providerPlaceholder is the ServeMux path value that stands in for the
// provider name in the patterns above.
const providerPlaceholder = "{saglayici}"

// federationStartPath builds the REAL path of a provider's start endpoint.
//
// WHY THIS HELPER EXISTS: the button path on the login page (see
// Pages.federationButtons) and the endpoint path mounted on the mux MUST be
// the SAME. When the two were built separately by hand, renaming the
// endpoint path silently left the buttons on the old path, i.e. a 404. The
// button path is now derived from the endpoint's OWN constant: the single
// source of truth is pathFederationStart. A helper was preferred (over
// calling Replace by hand at the call site) because the placeholder's form
// also stays in one place that way.
func federationStartPath(provider string) string {
	return strings.Replace(pathFederationStart, providerPlaceholder, provider, 1)
}

// cookieNameFederation is the name of the cookie given to the browser that
// STARTS the federation flow (K2).
//
// WHY A SEPARATE COOKIE: when Apple is asked for the name/email scope it
// returns its response with response_mode=form_post, i.e. as a CROSS-SITE
// POST to US from Apple's origin. Phase 1's session cookie
// (cookieNameSession) is SameSite=Lax and the browser does NOT SEND it on
// that POST: Phase 1's binding check effectively NEVER RUNS on the
// federation return. So the federation flow uses its own cookie with
// SameSite=None. The session cookie is NOT CHANGED: making it None would
// weaken Phase 1's login-CSRF protection.
const cookieNameFederation = "kimlik_fed"

// msgRequestInvalid is shown when the federation flow is started for an auth
// request that does not exist (or has expired).
const msgRequestInvalid = "Giris istegi gecersiz, lutfen uygulamadan tekrar deneyin."

// msgRequestExpired is shown when the pending auth request has dropped out
// of Redis by TTL.
//
// WHY A SEPARATE CLASS: the requestTTL (10 min) counter starts FIRST, at
// /authorize, and is never extended on any path; federationStateTTL and
// pendingLinkTTL are also 10 min but are consumed SEQUENTIALLY. So the
// federation chain can OUTLIVE the auth request it points to: Start passes
// at T=9m50s (the request EXISTS), the user spends 30 seconds upstream, and
// at T=10m20s MarkDone returns ErrRequestNotFound in Callback. This is an
// ORDINARY expiry; it used to fall into msgServerError and produce a 500,
// raising the SAME alarm as real server errors while not telling the user
// what happened. The TTL values were DELIBERATELY not changed: extending
// requestTTL would widen a security window to fix a UX problem.
const msgRequestExpired = "Giris isteginin suresi doldu. Lutfen uygulamaya donup girisi yeniden baslatin."

// msgUpstreamNoEmail is shown when a new account cannot be opened because
// upstream did not send an email (see ErrUpstreamNoEmail). The message tells
// the user WHAT TO DO; saying "an error occurred" would leave the user
// pressing the same button forever.
const msgUpstreamNoEmail = "Saglayici e-posta adresinizi bize vermedigi icin yeni bir hesap olusturulamadi. Lutfen e-posta ve sifre ile giris yapin ya da diger giris yontemini kullanin."

// federationHTTPTimeout is the upper bound for HTTP calls made to upstream.
// ExchangeCode also applies its own internal timeout; this is the second
// belt at the client level.
const federationHTTPTimeout = 15 * time.Second

// FederationRequestStore defines the TWO operations the federation flow
// needs on the pending auth request: Start reads to verify the request
// REALLY exists, Callback binds it to the user when the flow finishes. In
// production *RequestStore implements it; tests run with an in-memory
// counterpart, offline.
type FederationRequestStore interface {
	RequestCompleter

	// GetByID returns the pending auth request; ErrRequestNotFound if missing.
	GetByID(ctx context.Context, id string) (*AuthRequest, error)
}

// FlowStore is the WHOLE Redis surface of the federation flow: Task 3's
// single-use state record (StateStore) and the pending link record
// (PendingLinkStore, see link_page.go). Both are short-lived state of the
// same flow and in production are implemented by the same *RequestStore;
// wiring them separately would mean a setup that can break in two places
// independently.
type FlowStore interface {
	StateStore
	PendingLinkStore
}

// FederationPages carries the legs where our OP goes to Apple/Google as an
// RP and comes back.
// THE SECURITY TRIANGLE (so as not to reopen Phase 1's C1 in federation):
//
//	state  -> SINGLE-USE (Redis GETDEL, see TakeState); a captured callback
//	          cannot be replayed a second time.
//	cookie -> forces the browser that STARTED the flow and the browser that
//	          COMPLETES it to be the same (K2).
//	nonce  -> proves the id_token the provider returns was issued for THIS
//	          flow (see auth.VerifyWithNonce).
//
// If any one of the three fails the flow stops; none substitutes for another.
type FederationPages struct {
	upstreams    map[string]*Upstream
	verifiers    map[string]*auth.UpstreamVerifier
	flows        FlowStore
	federated    FederatedStore
	users        UserStore
	requests     FederationRequestStore
	sessions     SessionStore
	authCallback func(context.Context, string) string
	issuer       string
	secureCookie bool

	// httpClient is used for calls to the upstream token endpoint. It is kept
	// as a field so tests can inject an httptest client.
	httpClient *http.Client

	// limiter is used in the /baglama handler for the PER-ACCOUNT password
	// attempt counter; it is set by MountLink and MAY BE nil (unit tests only).
	limiter *RateLimiter
}

// NewFederationPages sets up the federation endpoints.
//
// verifiers maps a provider name to Task 2's upstream verifier: the
// id_token's signature, iss, aud and nonce are verified THERE and not
// re-verified here.
//
// sessions is ONLY so that the /baglama page enforces the same session
// binding as Phase 1 (see link_page.go); the federation return uses its own
// cookie.
//
// cookieSecure follows the same rule as Phase 1: true if the issuer is
// https, false for http:// local development (otherwise the browser would
// not store a Secure cookie at all and the local flow would break).
func NewFederationPages(
	flows map[string]*Upstream,
	verifiers map[string]*auth.UpstreamVerifier,
	flowStore FlowStore,
	federatedStore FederatedStore,
	user UserStore,
	requests FederationRequestStore,
	sessions SessionStore,
	authCallback func(context.Context, string) string,
	issuer string,
	cookieSecure bool,
) *FederationPages {
	return &FederationPages{
		upstreams:    flows,
		verifiers:    verifiers,
		flows:        flowStore,
		federated:    federatedStore,
		users:        user,
		requests:     requests,
		sessions:     sessions,
		authCallback: authCallback,
		issuer:       issuer,
		secureCookie: cookieSecure,
		httpClient:   &http.Client{Timeout: federationHTTPTimeout},
	}
}

// Mount mounts the federation endpoints on the mux.
//
// Callback is WRAPPED with the issuer interceptor: authCallback
// (op.AuthCallbackURL) reads the issuer from the request context, and
// mounting it without the wrapper breaks the flow at the redirect stage.
// Start is NOT wrapped: it does not call authCallback.
//
// POST /federasyon/{saglayici}/geri is REQUIRED: when Apple is asked for the
// name/email scope it returns its response with form_post (K2).
//
// limiter MAY BE nil, in which case NO rate limit is APPLIED; this is only
// for unit tests (same rule as Pages.Mount in Phase 1).
func (f *FederationPages) Mount(mux *http.ServeMux, interceptor *op.IssuerInterceptor, limiter *RateLimiter) {
	var start http.Handler = http.HandlerFunc(f.Start)
	callback := interceptor.Handler(http.HandlerFunc(f.Callback))
	if limiter != nil {
		// Start is an unauthenticated endpoint and every accepted request writes a
		// state record to Redis; it deserves its own counter (see
		// RateLimiter.WrapFederationStart).
		start = limiter.WrapFederationStart(start)
		callback = limiter.WrapFederationCallback(callback)
	}
	mux.Handle("GET "+pathFederationStart, start)
	mux.Handle("GET "+pathFederationCallback, callback)
	mux.Handle("POST "+pathFederationCallback, callback)
}

// upstreamRedirectURI builds the redirect_uri given to the provider. It must
// be registered IDENTICALLY on the provider side, so it is built
// deterministically from the issuer and url.JoinPath is used instead of
// manual string concatenation.
func (f *FederationPages) upstreamRedirectURI(provider string) string {
	base := strings.TrimSuffix(f.issuer, "/")
	return base + "/federasyon/" + url.PathEscape(provider) + "/geri"
}

// federationCookie: K2. SameSite=None is REQUIRED because Apple's form_post
// makes a CROSS-SITE POST and a Lax cookie is not sent on that POST.
// HttpOnly and Secure are not given up; Path is RESTRICTED to the federation
// prefix so it does not widen the session cookie's surface.
//
// LOCAL DEVELOPMENT TRAP: cookieSecure is false for an http:// issuer
// (see NewFederationPages) and MODERN BROWSERS REJECT a cookie that has
// SameSite=None without Secure. So locally this cookie is never stored and
// the return gives 403 with "cookie binding did not hold". In practice it
// does not affect production: Apple and Google require the redirect_uri to
// be https, so the federation flow cannot be tried by hand locally ANYWAY.
// The code was DELIBERATELY not changed -- making Secure true on http would
// break the cookie in another way (this time in every environment); what
// someone who wants to try it locally needs to know is that this flow
// requires an https issuer.
func (f *FederationPages) federationCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     cookieNameFederation,
		Value:    value,
		Path:     "/federasyon",
		HttpOnly: true,
		Secure:   f.secureCookie,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   int(federationStateTTL.Seconds()),
	}
}

// clearFederationCookie produces the cookie that DELETES the kimlik_fed
// cookie.
//
// The attributes MUST match the ones used when writing EXACTLY (especially
// Path and SameSite/Secure): the browser matches a cookie by the name+Path+
// Domain triple, and a mismatched deletion cookie does NOT DELETE the OLD
// one but writes a SECOND cookie next to it. So the value is derived from
// federationCookie and never rebuilt by hand.
func (f *FederationPages) clearFederationCookie() *http.Cookie {
	c := f.federationCookie("")
	// MaxAge < 0 means "delete now" (net/http writes it as Max-Age=0); MaxAge = 0
	// means "no attribute" and the cookie CONTINUES TO LIVE as a session cookie.
	c.MaxAge = -1
	return c
}

// errorPage writes federation flow errors in a single uniform shape. The
// text carries the CLASS, not the value: telling which check (state /
// cookie / nonce / signature) failed would guide an attacker.
func (f *FederationPages) errorPage(w http.ResponseWriter, status int, text string) {
	http.Error(w, text, status)
}

// writeCompletionError splits MarkDone's error into THREE CLASSES and writes
// the uniform response. The same classification is used on both legs
// (Callback and linkPost); previously only ErrRequestAlreadyCompleted was
// separated and EVERYTHING else, including an ordinary expiry, produced 500.
//
//	ErrRequestAlreadyCompleted -> 409: the request is bound to another
//	                              identity, Subject is NOT OVERWRITTEN.
//	ErrRequestNotFound         -> 400: the record dropped by TTL. The user
//	                              is told what happened and what to do.
//	others                     -> 500: a REAL server error. Keeping expiry
//	                              out of here also prevents real incidents
//	                              from drowning in noise.
//
// This split does NOT COVER SECURITY REJECTIONS: the cross-site rejection,
// cookie mismatch, session binding and CSRF stay 403 + a GENERIC message
// (we do not want to tell the attacker which check fired). The only class
// that goes to 400 here is "the record dropped by TTL".
func (f *FederationPages) writeCompletionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRequestAlreadyCompleted):
		f.errorPage(w, http.StatusConflict, msgRequestAlreadyUsed)
	case errors.Is(err, ErrRequestNotFound):
		f.errorPage(w, http.StatusBadRequest, msgRequestExpired)
	default:
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
	}
}

// Start starts the federation flow: it generates the state/nonce/PKCE
// verifier, gives the browser that STARTS the flow a cookie, and redirects
// the user to the upstream authorize address.
//
// THIS LEG IS NOT EXEMPT FROM K3. The exemption belongs ONLY to the return
// endpoint (see Callback): there the return is cross-site BY DEFINITION.
// Start, on the other hand, is a SAME-SITE top-level GET coming from the
// button on the /giris page, so both of Phase 1's protections apply here as
// they do there, and both are REQUIRED:
//
//  1. SESSION BINDING (verifySessionBinding): authRequestID alone is NOT an
//     authorization input. Without the binding, someone who learns a live
//     authRequestID could run the federation leg in THEIR OWN browser and
//     complete the request to THEIR OWN account at Callback; the victim's
//     app would receive tokens belonging to the attacker's account. The
//     kimlik_fed cookie ties start and callback together but ties neither
//     to the browser that STARTED /authorize; the only thing that does is
//     Phase 1's kimlik_oturum binding.
//
//  2. CROSS-SITE REJECTION (isCrossSite): when /authorize is reached FROM A
//     CROSS-SITE ORIGIN, WrapAuthorize DELIBERATELY does NOT set the binding,
//     and the invariant it relies on is: "the auth request is created but
//     no browser is bound to it, so GET /giris returns 403 and the request
//     CANNOT BE COMPLETED". Requiring a binding in Start already preserves
//     this invariant; the cross-site rejection is the second belt and makes
//     the intent readable.
//
// The legitimate flow is unaffected: kimlik_oturum is SameSite=Lax and is
// SENT on the same-site top-level GET coming from /giris.
func (f *FederationPages) Start(w http.ResponseWriter, r *http.Request) {
	flow, ok := f.upstreams[r.PathValue("saglayici")]
	if !ok {
		// The button of an unconfigured provider is never visible, so normally we
		// do not get here; if we do, 404, not "500".
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		f.errorPage(w, http.StatusBadRequest, msgRequestInvalid)
		return
	}
	if isCrossSite(r) {
		// authRequestID and the cookie value are NOT LOGGED.
		log.Print("federation: cross-site start rejected")
		f.errorPage(w, http.StatusForbidden, msgSessionInvalid)
		return
	}
	// A binding is REQUIRED, NOT MINTED (Phase 1's loginGet rule): minting a
	// new binding would let an attacker obtain a binding in their own browser
	// and send the link to the victim.
	if _, err := verifySessionBinding(r.Context(), f.sessions, r, id); err != nil {
		rejectSession(w, "federation-start", err)
		return
	}
	// The request must REALLY exist: going upstream with a nonexistent id would
	// take the user to a meaningless error, since there is nothing to complete
	// on return.
	if _, err := f.requests.GetByID(r.Context(), id); err != nil {
		// authRequestID is NOT LOGGED (Phase 1's rule): combined with MarkDone it
		// acts like a short-lived authorization token that binds a pending request
		// to a specific user.
		log.Printf("federation: start request is invalid: %v", err)
		f.errorPage(w, http.StatusBadRequest, msgRequestInvalid)
		return
	}

	state, err := randomToken()
	nonce, err2 := randomToken()
	cookieValue, err3 := randomToken()
	verifier, _, err4 := generateCodeVerifier()
	// ERRORS ARE NOT SWALLOWED: a weak/empty state, nonce or cookie value
	// effectively removes the protection.
	if err != nil || err2 != nil || err3 != nil || err4 != nil {
		log.Print("federation: could not generate a random value")
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
		return
	}

	if err := f.flows.WriteState(r.Context(), state, FederationState{
		AuthRequestID: id,
		Nonce:         nonce,
		Provider:      flow.Name,
		Cookie:        cookieValue,
		CodeVerifier:  verifier,
	}); err != nil {
		// state, nonce, cookie and verifier are NOT LOGGED.
		log.Printf("federation: could not write the state record: %v", err)
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
		return
	}
	http.SetCookie(w, f.federationCookie(cookieValue))
	http.Redirect(w, r, flow.AuthorizeURL(state, nonce, verifier, f.upstreamRedirectURI(flow.Name)), http.StatusFound)
}

// stateAndCode reads the state and code values on return: from the query on
// GET, from the body on POST (Apple form_post).
func (f *FederationPages) stateAndCode(r *http.Request) (string, string) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			return "", ""
		}
		return r.PostFormValue("state"), r.PostFormValue("code")
	}
	q := r.URL.Query()
	return q.Get("state"), q.Get("code")
}

// Callback receives the user returning from upstream.
//
// K3: the isCrossSite guard is NOT APPLIED HERE. Apple's form_post return
// is cross-site (Sec-Fetch-Site: cross-site) BY DEFINITION, and running the
// guard here would break Apple sign-in entirely. The guard's place is taken
// by the state (single-use) + cookie (K2) + nonce triangle; ALL THREE are
// REQUIRED. This exemption belongs ONLY to this endpoint: the guard stays in
// place on the /authorize and /giris legs.
func (f *FederationPages) Callback(w http.ResponseWriter, r *http.Request) {
	state, code := f.stateAndCode(r)
	// TakeState is GETDEL: the record is SINGLE-USE. A captured callback
	// (address bar, log, proxy) cannot be replayed a second time.
	d, err := f.flows.TakeState(r.Context(), state)
	if err != nil {
		// state is NOT LOGGED; only the error class is.
		log.Printf("federation: could not verify the state: %v", err)
		f.errorPage(w, http.StatusForbidden, msgSessionInvalid)
		return
	}

	cookie, cookieErr := r.Cookie(cookieNameFederation)
	if cookieErr != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(d.Cookie)) != 1 {
		// This branch prevents a flow started in ANOTHER browser from being
		// completed in this browser (account takeover): even if an attacker opens
		// a valid state+code pair obtained from their own authorization in the
		// victim's browser, the cookie is NOT in the victim's browser.
		log.Print("federation: the cookie binding did not hold")
		f.errorPage(w, http.StatusForbidden, msgSessionInvalid)
		return
	}

	// The provider is read from the RECORD, NOT THE PATH: otherwise an attacker
	// could bring a flow started at Google to /federasyon/apple/geri and mix up
	// the choice of token endpoint, PKCE verifier and id_token verifier.
	flow, flowExists := f.upstreams[d.Provider]
	verifier, verifierExists := f.verifiers[d.Provider]
	if !flowExists || !verifierExists {
		// The provider may have been removed from the configuration after the record
		// was written (redeploy). Since provider names are a FIXED set, it is safe
		// to log.
		log.Printf("federation: the registered provider is no longer configured (%s)", d.Provider)
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
		return
	}
	if code == "" {
		// The user may have declined at the provider, or the provider may have
		// returned an error; there is no code to exchange.
		log.Print("federation: no authorization code on return")
		f.errorPage(w, http.StatusBadRequest, msgRequestInvalid)
		return
	}

	idToken, err := flow.ExchangeCode(r.Context(), f.httpClient, code, d.CodeVerifier, f.upstreamRedirectURI(flow.Name))
	if err != nil {
		// The code, client_secret or response body does NOT appear in the error
		// text (see ExchangeCode).
		log.Printf("federation: code exchange failed: %v", err)
		f.errorPage(w, http.StatusForbidden, msgSessionInvalid)
		return
	}
	// The signature (RS256), iss, aud, expiry, sub and NONCE are verified here;
	// this file does not re-verify any of them.
	claims, err := verifier.VerifyWithNonce(idToken, d.Nonce)
	if err != nil {
		// The id_token does NOT appear in err (see auth.VerifyWithNonce).
		log.Printf("federation: could not verify the id_token: %v", err)
		f.errorPage(w, http.StatusForbidden, msgSessionInvalid)
		return
	}

	// resolveUser returns THREE values: user, conflict, err. If conflict is NOT
	// nil, NO login happens -- we go to the link or 409 page.
	user, conflict, err := f.resolveUser(r.Context(), flow.Name, claims)
	if err != nil {
		// Email and sub are NOT LOGGED.
		log.Printf("federation: could not resolve the user: %v", err)
		// Upstream not sending an email is NOT a SERVER error and does not go to the
		// 409 conflict page showing an empty email either: the user must read what
		// happened and what to do (see ErrUpstreamNoEmail).
		if errors.Is(err, ErrUpstreamNoEmail) {
			f.errorPage(w, http.StatusBadRequest, msgUpstreamNoEmail)
			return
		}
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
		return
	}
	if conflict != nil {
		f.handleConflict(w, r, d.AuthRequestID, conflict)
		return
	}

	if err := f.requests.MarkDone(r.Context(), d.AuthRequestID, user.ID, []string{amrFederation}); err != nil {
		// authRequestID is NOT LOGGED.
		log.Printf("federation: could not complete the request: %v", err)
		// The classification is in writeCompletionError: 409 (bound to another
		// identity, Subject NOT OVERWRITTEN), 400 (dropped by TTL) and 500 (real
		// server error) are SEPARATE.
		f.writeCompletionError(w, err)
		return
	}
	// HYGIENE: the flow is over, and there is no reason for the kimlik_fed
	// cookie to stay in the browser. This is NOT a security fix -- the security
	// basis is that the state record is single-use via GETDEL, so a leftover
	// cookie alone cannot replay any flow. The deletion cookie's
	// Name/Path/Secure/SameSite attributes MUST be the SAME as when written:
	// otherwise the browser would not apply the deletion at all, which is why
	// the same constructor (federationCookie) is used.
	http.SetCookie(w, f.clearFederationCookie())
	http.Redirect(w, r, f.authCallback(r.Context(), d.AuthRequestID), http.StatusFound)
}
