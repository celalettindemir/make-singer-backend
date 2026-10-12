package identity

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/zitadel/oidc/v3/pkg/op"
)

// Paths of the login and signup pages. Both the client's LoginURL and the
// page handlers read from here.
const (
	pathLogin  = "/giris"
	pathSignup = "/kayit"
)

// fieldCSRF is the field name of the CSRF token in the form.
const fieldCSRF = "csrf"

// RequestCompleter binds the pending auth request to the user once login or
// signup succeeds. It is an interface so tests can measure the happy path
// OFFLINE; in production *RequestStore implements it.
type RequestCompleter interface {
	MarkDone(ctx context.Context, id string, userID string, amr []string) error
}

//go:embed sablonlar/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "sablonlar/*.html"))

// msgLoginFailed is shown for both "no such account" and "wrong password".
// Separating the two would turn the form into an email-counting tool (user
// enumeration).
const msgLoginFailed = "E-posta veya sifre hatali."

// msgServerError is shown for unexpected (5xx) situations; it carries no
// internal detail.
const msgServerError = "Bir hata olustu, lutfen tekrar deneyin."

// msgEmailInUse is shown when the email already exists at signup. Giving
// this information in the signup flow does not count as user enumeration:
// unlike the login flow, the user here is already trying to open a new
// account with the email they themselves supplied.
const msgEmailInUse = "Bu e-posta zaten kullaniliyor."

// msgSessionInvalid is returned when the cookie binding or the CSRF
// verification does not hold. A single generic message: separating which
// condition failed (no cookie / no binding / id mismatch / wrong csrf)
// would guide an attacker.
const msgSessionInvalid = "Oturum dogrulanamadi, lutfen giris sayfasini yeniden acin."

// msgRequestAlreadyUsed is returned when the pending auth request is
// ALREADY bound to another identity. Subject is not overwritten (see
// RequestStore.MarkDone); the user must restart the flow.
const msgRequestAlreadyUsed = "Bu giris istegi zaten kullanildi, lutfen uygulamadan tekrar deneyin."

// loginData is the data given to the giris.html template.
type loginData struct {
	AuthRequestID string
	CSRF          string
	Email         string
	Error         string
	LoginPath     string
	SignupPath    string

	// Providers are the UPSTREAM providers for which a button is shown on the
	// login page. If empty, no button appears.
	Providers []ProviderButton
}

// ProviderButton is a single upstream button on the login page.
type ProviderButton struct {
	Name  string // "google" | "apple"
	Label string // "Google ile giris yap" (UI text)
	Path  string // /federasyon/google/baslat?authRequestID=...
}

// signupData is the data given to the kayit.html template.
type signupData struct {
	AuthRequestID string
	CSRF          string
	Email         string
	Name          string
	Error         string
	LoginPath     string
	SignupPath    string
}

// Pages carries the hosted login/signup HTML forms and their return into
// the OIDC flow.
//
// SECURITY: authRequestID alone is NOT an authorization input. The cookie ->
// {authRequestID, CSRF} binding is set up ONLY at /authorize, for the
// browser that STARTED the request (see WrapAuthorize). GET /giris,
// GET /kayit, the POSTs and /authorize/callback REQUIRE this binding and
// NONE of them mints a new one. So the browser that starts the auth
// request, sees the form, completes the login and collects the code MUST be
// the SAME. Also, when /authorize is called FROM A CROSS-SITE ORIGIN, no
// binding is set at all (login-CSRF; see WrapAuthorize and isCrossSite).
type Pages struct {
	users        UserStore
	requests     RequestCompleter
	sessions     SessionStore
	authCallback func(context.Context, string) string
	secureCookie bool

	// callbackDelegate is the LIBRARY handler called once the cookie binding
	// is verified (op.AuthorizeCallbackHandler / provider). The library code is
	// not modified or copied; it is only put in front of.
	callbackDelegate http.Handler

	// federationProviders are the provider names that get a button on the login
	// page. It MAY BE LEFT EMPTY, in which case no button appears; this is the
	// correct behavior for a setup where federation is not configured.
	federationProviders []string
}

// NewPages builds Pages with a valid UserStore, RequestStore, SessionStore
// and the callback produced by op.AuthCallbackURL. cookieSecure is whether
// the Secure flag is put on the cookie: true if the issuer is https, false
// for http:// local development (otherwise the browser would not store the
// cookie at all and the local flow would break).
func NewPages(
	user UserStore,
	requests RequestCompleter,
	sessions SessionStore,
	authCallback func(context.Context, string) string,
	cookieSecure bool,
) *Pages {
	return &Pages{
		users:        user,
		requests:     requests,
		sessions:     sessions,
		authCallback: authCallback,
		secureCookie: cookieSecure,
	}
}

// SetCallbackDelegate wires the library handler to be called once the cookie
// binding is verified. A separate setter: the handler comes from the library
// provider and the provider is set up after NewPages.
func (s *Pages) SetCallbackDelegate(h http.Handler) { s.callbackDelegate = h }

// SetFederationProviders sets the upstream providers for which a button is
// shown on the login page. A separate setter: the list comes from
// configuration and its order is the order the caller gave (see
// buildMux; map iteration is random, so it is sorted there).
//
// The list MUST contain ONLY providers that are ACTUALLY configured: a
// button to an unwired endpoint shows the user a 404.
func (s *Pages) SetFederationProviders(names []string) { s.federationProviders = names }

// federationButtons builds the button list for the given auth request.
//
// The path is built HERE, with the url package; NO manual string
// concatenation is done in the template. If authRequestID entered unescaped,
// at best the link would break, at worst the "&" in it would inject a
// foreign parameter into the query string.
//
// The BODY of the path comes from federationStartPath, i.e. it is derived
// from the constant the endpoint is mounted on in the mux: the button and
// the endpoint share a single source of truth (see pathFederationStart).
func (s *Pages) federationButtons(id string) []ProviderButton {
	if len(s.federationProviders) == 0 {
		return nil
	}
	buttons := make([]ProviderButton, 0, len(s.federationProviders))
	for _, name := range s.federationProviders {
		redirect := url.URL{
			Path:     federationStartPath(name),
			RawQuery: url.Values{"authRequestID": {id}}.Encode(),
		}
		buttons = append(buttons, ProviderButton{
			Name:  name,
			Label: providerLabel(name) + " ile giris yap",
			Path:  redirect.String(),
		})
	}
	return buttons
}

// Mount mounts the pages on the mux. The POST handlers are WRAPPED with the
// issuer interceptor: the interceptor puts the issuer into the request
// context and authCallback reads it from there. Mounting without the
// wrapper breaks the flow at the redirect stage.
//
// limiter is the rate limit applied to the endpoints (see ratelimit.go). The
// GETs are WRAPPED too: now that the binding has moved to /authorize, the
// GET no longer writes to Redis, but the endpoint is still unauthenticated
// and a cheaper surface than /authorize (which requires a valid client_id +
// redirect_uri + S256); template rendering is not free either. GETs are
// counted under SEPARATE keys from POSTs: otherwise opening the form would
// consume the password-guessing budget and a legitimate user would lock out
// their own flow.
//
// nil MAY BE PASSED, in which case NO rate limit is APPLIED; this is only
// for unit tests, in production Start always sets up a limiter and does NOT
// START AT ALL if it cannot.
func (s *Pages) Mount(mux *http.ServeMux, interceptor *op.IssuerInterceptor, limiter *RateLimiter) {
	loginPost := interceptor.Handler(http.HandlerFunc(s.Login))
	signupPost := interceptor.Handler(http.HandlerFunc(s.Signup))
	// GETs are NOT wrapped with the issuer interceptor: form rendering does not
	// call authCallback, i.e. it does not need the issuer in the request context.
	var loginGet http.Handler = http.HandlerFunc(s.Login)
	var signupGet http.Handler = http.HandlerFunc(s.Signup)
	if limiter != nil {
		// The rate limit goes OUTSIDE the interceptor, not inside: a rejected
		// request returns without doing any extra work, including issuer resolution.
		loginPost = limiter.WrapLogin(loginPost)
		signupPost = limiter.WrapSignup(signupPost)
		loginGet = limiter.WrapPageGet("giris", loginGet)
		signupGet = limiter.WrapPageGet("kayit", signupGet)
	}
	mux.Handle("GET "+pathLogin, loginGet)
	mux.Handle("POST "+pathLogin, loginPost)
	mux.Handle("GET "+pathSignup, signupGet)
	mux.Handle("POST "+pathSignup, signupPost)
}

// BindSession binds the given authRequestID to the CALLING browser: if
// present it reuses the existing session cookie (for parallel flows, see
// maxSessionBindings), otherwise it generates a new session value; it
// generates a request-specific CSRF token, writes the binding to the store
// and puts the cookie on the response.
//
// Called ONLY from the /authorize leg (WrapAuthorize). GET /giris and
// GET /kayit do NOT CALL it: if they did, a binding could be minted for
// anyone, for any id (see the cookieNameSession note in session.go).
//
// The session value, CSRF token and authRequestID are NOT LOGGED.
func (s *Pages) BindSession(w http.ResponseWriter, r *http.Request, id string) error {
	if id == "" {
		return ErrSessionMismatch
	}
	var (
		session string
		binding SessionBinding
	)
	// An existing cookie is reused ONLY if it has a valid binding in the store;
	// overwriting a made-up or expired cookie value would revive a session value
	// the attacker can choose.
	if cookie, err := r.Cookie(cookieNameSession); err == nil && cookie.Value != "" {
		if existing, err := s.sessions.ReadSession(r.Context(), cookie.Value); err == nil {
			session = cookie.Value
			binding = existing
		}
	}
	if session == "" {
		newToken, err := randomToken()
		if err != nil {
			return err
		}
		session = newToken
	}
	csrf, err := randomToken()
	if err != nil {
		return err
	}
	binding.Add(id, csrf)
	if err := s.sessions.WriteSession(r.Context(), session, binding, requestTTL); err != nil {
		return err
	}
	// The cookie is rewritten on every path: required for a new session, and
	// for an existing session it refreshes MaxAge for the request TTL.
	http.SetCookie(w, sessionCookie(session, s.secureCookie))
	return nil
}

// WrapAuthorize wraps the /authorize handler and, AFTER the request
// verifications have PASSED (i.e. when the library redirects the user to the
// login page), sets the cookie binding for the browser that STARTED THE
// REQUEST.
//
// WHY HERE: /authorize is the only point at which the flow is STARTED BY
// THE BROWSER and it requires a valid client_id + redirect_uri + S256 PKCE
// (see pkceEnforcer). Setting the binding here makes it the only place where
// the equality "the browser that started the request" == "the browser that
// carries the cookie" can be established. The library code is NOT MODIFIED:
// we step in IMMEDIATELY BEFORE the response is written.
//
// If the redirect is NOT to the login page (an error 302, a direct
// response) NO binding is SET.
//
// FAIL-CLOSED: if the binding cannot be set (e.g. Redis is down) the
// library's redirect is NOT WRITTEN and 500 is returned. Otherwise the user
// would be sent to a form whose POST is certain to be 403.
//
// CROSS-SITE NAVIGATION (login-CSRF): since the session cookie is
// SameSite=Lax, it is CARRIED on top-level navigation. If an attacker
// redirects the victim to their own crafted
// /authorize?...&code_challenge=<ATTACKER'S>, the victim's EXISTING cookie
// is reused and the authRequestID born with the attacker's parameters enters
// the victim's binding set: the victim can open the form and completes the
// login, while the code_verifier of the produced code is held by the
// ATTACKER. If the code leaks from the victim's device (a malicious app
// claiming the same custom scheme, RFC 8252 section 8.1), the ONLY scenario
// PKCE protects against collapses. So NO binding is SET on a request coming
// from cross-site; see isCrossSite.
func (s *Pages) WrapAuthorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		interceptor := &bindingInterceptor{ResponseWriter: w}
		interceptor.bind = func(status int) error {
			if status != http.StatusFound && status != http.StatusSeeOther {
				return nil
			}
			id := loginRedirectID(w.Header().Get("Location"))
			if id == "" {
				return nil
			}
			if isCrossSite(r) {
				// ONLY THE NEW BINDING IS NOT ADDED. The victim's cookie, existing
				// bindings and parallel flows are left UNTOUCHED (the cookie is not
				// refreshed, the set is not cleared): otherwise this check would turn into
				// a DoS that drops the victim's open tabs. The library's redirect IS
				// WRITTEN; the auth request is created but no browser is bound to it, so
				// GET /giris returns 403 and the request CANNOT BE COMPLETED.
				//
				// authRequestID, the cookie value and the CSRF token are NOT LOGGED.
				log.Print("authorize: capraz-site navigasyon, oturum baglamasi kurulmadi")
				return nil
			}
			return s.BindSession(w, r, id)
		}
		next.ServeHTTP(interceptor, r)
	})
}

// Fetch-metadata header names (produced by the browser, CANNOT BE MODIFIED
// by the client's JavaScript; since the "Sec-" prefix is a forbidden header
// name, they cannot be overridden via fetch/XHR).
const (
	headerFetchSite = "Sec-Fetch-Site"
	headerFetchMode = "Sec-Fetch-Mode"
)

// Sec-Fetch-Site values. It is essential to separate ALL FOUR:
//
//   - "none"        -> a navigation the user started DIRECTLY: the address
//     bar, a bookmark AND a native app opening the system browser. THIS IS
//     OUR LEGITIMATE FLOW (today our only client is MobileClient /
//     ApplicationTypeNative); it is never rejected.
//   - "same-origin" -> navigation returning from our own pages
//     (login <-> signup cross links, returns after a form submit).
//   - "same-site"   -> same registered domain, different origin.
//   - "cross-site"  -> a request started by ANOTHER site: our only
//     target.
const (
	fetchSiteNone       = "none"
	fetchSiteSameOrigin = "same-origin"
	fetchSiteSameSite   = "same-site"
	fetchSiteCrossSite  = "cross-site"
)

// isCrossSite says whether the request was started by ANOTHER site.
//
// IF THE HEADER IS MISSING, FAIL-OPEN (returns false, i.e. the binding IS
// SET). Rationale: this check matters only in the "browser carrying the
// cookie" scenario, and every browser that carries a SameSite=Lax cookie
// also sends fetch-metadata (Chrome 76+, Firefox 90+, Safari 16.4+; an
// older browser that does not enforce SameSite has no Lax protection
// either, so failing closed would not close the exploit). On the other
// hand, failing closed would break login entirely for NON-BROWSER clients
// that never send the header (curl, local development tools, health
// checks) -- and no attacker "victim cookie" exists on those clients
// either. So the missing header breaks more than it gains.
//
// There is DELIBERATELY NO mode (Sec-Fetch-Mode) check: a cross-site
// subresource request (mode "no-cors"/"cors") ALREADY does not carry a Lax
// cookie, so not setting the binding on ALL cross-site requests without
// separating the mode is both simpler and narrower. The name is still kept
// in headerFetchMode so that the header remains readable.
//
// NOTE FOR THE FUTURE -- WEB RP: if a browser-based client (a web RP) is
// ever added, that client's redirect of the user to the OP LEGITIMATELY
// arrives as Sec-Fetch-Site: cross-site and this check would reject it.
// This function should be reviewed that day: for example the binding would
// have to be set and login-CSRF closed with a separate mechanism (a
// client-specific start token held on the OP side). Moving to
// SameSite=Strict IS NOT A SOLUTION: by itself it does not close the
// exploit and it drops the victim's legitimate flows.
func isCrossSite(r *http.Request) bool {
	return r.Header.Get(headerFetchSite) == fetchSiteCrossSite
}

// loginRedirectID returns the authRequestID if the redirect address produced
// by the library goes to OUR login page, otherwise an empty string. The path
// check is mandatory: the binding must not be set on error redirects
// returning to the client's redirect_uri.
func loginRedirectID(location string) string {
	if location == "" {
		return ""
	}
	u, err := url.Parse(location)
	if err != nil || u.Path != pathLogin {
		return ""
	}
	return u.Query().Get("authRequestID")
}

// bindingInterceptor calls bind BEFORE the wrapped handler's response is
// WRITTEN. The order of cookie and status code matters: Set-Cookie headers
// MUST be written BEFORE WriteHeader.
type bindingInterceptor struct {
	http.ResponseWriter

	// bind takes the response's status code and sets the binding. If it returns
	// an error, the wrapped handler's response is SUPPRESSED and 500 is written.
	bind func(status int) error

	wroteHeader bool
	suppressed  bool
}

func (ic *bindingInterceptor) WriteHeader(status int) {
	if ic.wroteHeader {
		return
	}
	ic.wroteHeader = true
	if err := ic.bind(status); err != nil {
		// authRequestID, the cookie value and the CSRF token are NOT LOGGED.
		log.Printf("authorize: oturum baglamasi kurulamadi: %v", err)
		ic.suppressed = true
		ic.ResponseWriter.Header().Del("Location")
		http.Error(ic.ResponseWriter, msgServerError, http.StatusInternalServerError)
		return
	}
	ic.ResponseWriter.WriteHeader(status)
}

func (ic *bindingInterceptor) Write(b []byte) (int, error) {
	if !ic.wroteHeader {
		ic.WriteHeader(http.StatusOK)
	}
	if ic.suppressed {
		// The body is swallowed but the writer sees no error: the 500 was ALREADY written.
		return len(b), nil
	}
	return ic.ResponseWriter.Write(b)
}

// enforceSession verifies that a POST REALLY belongs to the form shown in
// this browser: (1) a session cookie exists, (2) a record bound to the
// cookie exists, (3) the bound authRequestID is exactly the same as the form
// value, (4) the form CSRF token is the same as the token bound to the
// cookie. All four run BEFORE THE PASSWORD VERIFICATION; if one fails, no
// login attempt is made at all.
func (s *Pages) enforceSession(r *http.Request, id string) error {
	csrf, err := verifySessionBinding(r.Context(), s.sessions, r, id)
	if err != nil {
		return err
	}
	if !csrfEqual(csrf, r.FormValue(fieldCSRF)) {
		return ErrCSRF
	}
	return nil
}

// rejectSession writes a uniform response when the binding/CSRF verification
// does not hold. The REASON for the rejection is not leaked to the client;
// on the server side too the cookie value, CSRF token and authRequestID are
// not logged, only the error kind is.
func rejectSession(w http.ResponseWriter, where string, err error) {
	log.Printf("%s: oturum baglamasi dogrulanamadi: %v", where, err)
	http.Error(w, msgSessionInvalid, http.StatusForbidden)
}

// Login shows the form on GET, verifies credentials on POST and, if
// successful, returns to the OIDC flow.
func (s *Pages) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.loginPost(w, r)
		return
	}
	s.loginGet(w, r)
}

// loginGet shows the form ONLY if this browser started that auth request.
// It does NOT MINT a new binding: it REQUIRES an existing one. If there is
// no binding not even the form is shown -- otherwise an attacker could
// obtain a binding in their own browser and send the link to the victim.
func (s *Pages) loginGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	csrf, err := verifySessionBinding(r.Context(), s.sessions, r, id)
	if err != nil {
		rejectSession(w, "giris-get", err)
		return
	}
	s.renderLogin(w, http.StatusOK, id, csrf, "", "")
}

func (s *Pages) loginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	id := r.FormValue("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	// Cookie binding + CSRF: BEFORE the password verification. A request
	// rejected here NEVER touches the user store.
	if err := s.enforceSession(r, id); err != nil {
		rejectSession(w, "giris", err)
		return
	}
	csrf := r.FormValue(fieldCSRF)
	// normalizeEmail: signup and login MUST go through the SAME path. Previously
	// the signup side trimmed while the login side passed the raw form value; a
	// user who signed up with an email containing spaces could not log in with
	// the SAME input (see the normalizeEmail note in user_store.go).
	email := normalizeEmail(r.FormValue("eposta"))
	password := r.FormValue("sifre")

	user, err := s.users.ByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		// The email is PII and is not logged here; the error itself is already
		// enough for diagnosis.
		log.Printf("giris: kullanici okunamadi: %v", err)
	}
	if !user.PasswordMatches(password) {
		// We DELIBERATELY DO NOT DISTINGUISH whether the account exists: for the
		// SAME email the "no account" and "wrong password" cases return EXACTLY the
		// same body/status code (see
		// TestLogin_FailureDoesNotRevealUserExistence). Writing the entered email
		// back to the form does not count as enumeration: the attacker already sees
		// the value they typed, no different information leaks. html/template
		// escapes, see TestLogin_EchoedEmailEscaped. The password is not logged
		// here.
		s.renderLogin(w, http.StatusUnauthorized, id, csrf, email, msgLoginFailed)
		return
	}

	if err := s.requests.MarkDone(r.Context(), id, user.ID, []string{amrPassword}); err != nil {
		// authRequestID is not logged: combined with MarkDone it acts like an
		// authorization token, valid for 10 minutes, that binds a pending request
		// to a specific user.
		log.Printf("giris: istek tamamlanamadi: %v", err)
		if errors.Is(err, ErrRequestAlreadyCompleted) {
			// This request is bound to another identity; reject instead of overwriting.
			http.Error(w, msgRequestAlreadyUsed, http.StatusConflict)
			return
		}
		s.renderLogin(w, http.StatusInternalServerError, id, csrf, email, msgServerError)
		return
	}
	http.Redirect(w, r, s.authCallback(r.Context(), id), http.StatusFound)
}

func (s *Pages) renderLogin(w http.ResponseWriter, status int, id, csrf, email, errMsg string) {
	data := loginData{
		AuthRequestID: id,
		CSRF:          csrf,
		Email:         email,
		Error:         errMsg,
		LoginPath:     pathLogin,
		SignupPath:    pathSignup,
		Providers:     s.federationButtons(id),
	}
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, "giris.html", data); err != nil {
		log.Printf("giris sablonu yazilamadi: %v", err)
	}
}

// Signup shows the form on GET, creates a new user on POST and, if
// successful, returns to the OIDC flow.
func (s *Pages) Signup(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.signupPost(w, r)
		return
	}
	s.signupGet(w, r)
}

// signupGet is subject to the SAME rule as loginGet: it REQUIRES a binding,
// it does not mint one (see loginGet).
func (s *Pages) signupGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	csrf, err := verifySessionBinding(r.Context(), s.sessions, r, id)
	if err != nil {
		rejectSession(w, "kayit-get", err)
		return
	}
	s.renderSignup(w, http.StatusOK, id, csrf, "", "", "")
}

func (s *Pages) signupPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	id := r.FormValue("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	// Cookie binding + CSRF: BEFORE the user is CREATED.
	if err := s.enforceSession(r, id); err != nil {
		rejectSession(w, "kayit", err)
		return
	}
	csrf := r.FormValue(fieldCSRF)
	// The SAME normalization as login (see the note in loginPost).
	email := normalizeEmail(r.FormValue("eposta"))
	name := r.FormValue("ad")
	password := r.FormValue("sifre")

	user, err := s.users.Create(r.Context(), email, name, password)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailInUse):
			s.renderSignup(w, http.StatusConflict, id, csrf, email, name, msgEmailInUse)
		case errors.Is(err, ErrPasswordTooShort):
			s.renderSignup(w, http.StatusBadRequest, id, csrf, email, name, ErrPasswordTooShort.Error())
		default:
			// The email is PII and is not logged here; the error itself is already
			// enough for diagnosis.
			log.Printf("kayit: kullanici olusturulamadi: %v", err)
			s.renderSignup(w, http.StatusInternalServerError, id, csrf, email, name, msgServerError)
		}
		return
	}

	if err := s.requests.MarkDone(r.Context(), id, user.ID, []string{amrPassword}); err != nil {
		// authRequestID is not logged (see the same note in loginPost).
		log.Printf("kayit: istek tamamlanamadi: %v", err)
		if errors.Is(err, ErrRequestAlreadyCompleted) {
			// This request is already bound to another identity; Subject is NOT OVERWRITTEN.
			http.Error(w, msgRequestAlreadyUsed, http.StatusConflict)
			return
		}
		s.renderSignup(w, http.StatusInternalServerError, id, csrf, email, name, msgServerError)
		return
	}
	http.Redirect(w, r, s.authCallback(r.Context(), id), http.StatusFound)
}

func (s *Pages) renderSignup(w http.ResponseWriter, status int, id, csrf, email, name, errMsg string) {
	data := signupData{
		AuthRequestID: id,
		CSRF:          csrf,
		Email:         email,
		Name:          name,
		Error:         errMsg,
		LoginPath:     pathLogin,
		SignupPath:    pathSignup,
	}
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, "kayit.html", data); err != nil {
		log.Printf("kayit sablonu yazilamadi: %v", err)
	}
}

// Callback protects the /authorize/callback leg with the cookie binding.
//
// If this leg is left open, putting a CSRF token on the form is NOT ENOUGH:
// at the callback the library only looks at AuthRequestByID + Done()
// (pkg/op/auth_request.go, AuthorizeCallback), so the browser COLLECTING the
// code may be independent of the browser that DID the login. Here we verify
// the same cookie binding and delegate to the library's own handler only if
// the "id" parameter matches EXACTLY the authRequestID bound to the cookie.
//
// The CSRF token is NOT LOOKED FOR here: this leg is not a form POST but a
// GET that the /giris POST redirects to with a 302; if the token were put in
// the URL it would leak via Referer/browser history. What protects is the
// cookie binding, and on this leg the cookie binding alone is sufficient:
// CSRF is against the threat of "triggering an unwanted POST in the
// victim's browser", whereas here the threat is collecting the code in the
// attacker's browser, and the cookie closes that.
func (s *Pages) Callback(w http.ResponseWriter, r *http.Request) {
	if s.callbackDelegate == nil {
		log.Print("callback: delege baglanmamis")
		http.Error(w, msgServerError, http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	id := r.Form.Get("id")
	if id == "" {
		http.Error(w, "id eksik", http.StatusBadRequest)
		return
	}
	if _, err := verifySessionBinding(r.Context(), s.sessions, r, id); err != nil {
		rejectSession(w, "callback", err)
		return
	}
	s.callbackDelegate.ServeHTTP(w, r)
}
