package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

const testPassword = "cokgizlisifre"

// buildClaims imitates the claims of a verified upstream id_token. This is
// exactly the input of resolveUser: signature/nonce verification is done
// BEFORE it (see Callback).
func buildClaims(sub, email string, verified bool) *auth.Claims {
	return &auth.Claims{
		UserID:        sub,
		Email:         email,
		EmailVerified: auth.FlexibleBool(verified),
		Name:          "Test Kullanici",
	}
}

// bindSession imitates what the /authorize leg does: it binds the given
// authRequestID to a browser and returns that browser's session cookie. The
// /baglama page requires the SAME session binding as Phase 1's login page.
func (env *fedEnv) bindSession(t *testing.T, authReqID string) *http.Cookie {
	t.Helper()
	session, err := randomToken()
	if err != nil {
		t.Fatalf("could not generate session value: %v", err)
	}
	csrf, err := randomToken()
	if err != nil {
		t.Fatalf("could not generate csrf: %v", err)
	}
	var binding SessionBinding
	binding.Add(authReqID, csrf)
	if err := env.sessions.WriteSession(context.Background(), session, binding, time.Minute); err != nil {
		t.Fatalf("could not write session: %v", err)
	}
	return &http.Cookie{Name: cookieNameSession, Value: session}
}

func (env *fedEnv) linkGetRequest(pendingID string, cookie *http.Cookie) *httptest.ResponseRecorder {
	target := pathLink
	if pendingID != "" {
		target += "?" + url.Values{fieldPendingID: {pendingID}}.Encode()
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, r)
	return w
}

func (env *fedEnv) linkPostRequest(value url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, pathLink, strings.NewReader(value.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, r)
	return w
}

// setupPendingLink runs the REAL federation flow: with an account that has a
// PASSWORD and a conflicting email, it logs in with Google and the flow
// redirects to /baglama. Returns: the pending record's id, the CSRF token in
// the form, the browser's session cookie and the conflicting account.
func (env *fedEnv) setupPendingLink(t *testing.T) (string, string, *http.Cookie, *User) {
	t.Helper()
	env.google.configure(func(s *fakeProvider) { s.email = "cakisan@ornek.com" })
	existing, err := env.users.Create(context.Background(), "cakisan@ornek.com", "Mevcut", testPassword)
	if err != nil {
		t.Fatalf("could not create existing account: %v", err)
	}

	w := env.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect to /baglama), body = %q", w.Code, w.Body.String())
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("could not parse Location: %v", err)
	}
	if location.Path != pathLink {
		t.Fatalf("Location = %q, want %s", w.Header().Get("Location"), pathLink)
	}
	pendingID := location.Query().Get(fieldPendingID)
	if pendingID == "" {
		t.Fatal("no beklemeID in the redirect")
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Fatal("the request must NOT be completed while there is a conflict")
	}

	cookie := env.sessionCookie
	form := env.linkGetRequest(pendingID, cookie)
	if form.Code != http.StatusOK {
		t.Fatalf("could not open the link form: status = %d, body = %q", form.Code, form.Body.String())
	}
	matches := csrfPattern.FindStringSubmatch(form.Body.String())
	if matches == nil || matches[1] == "" {
		t.Fatalf("no CSRF token in the form: %q", form.Body.String())
	}
	if !strings.Contains(form.Body.String(), "cakisan@ornek.com") {
		t.Error("the form does not show the conflicting email")
	}
	return pendingID, matches[1], cookie, existing
}

// ---------------------------------------------------------------------
// Step 1: Resolution order tests
// ---------------------------------------------------------------------

//  1. A linked identity ALWAYS comes first: the user's email at Google may
//     have changed, and then a lookup by email would find ANOTHER account
//     (or none).
func TestResolveUser_LinkedIdentityBeforeEmail(t *testing.T) {
	env := newTestFederationPages(t)
	ctx := context.Background()

	a, err := env.users.Create(ctx, "a@ornek.com", "A", testPassword)
	if err != nil {
		t.Fatalf("could not create A: %v", err)
	}
	if err := env.federated.Link(ctx, providerGoogle, "sub-1", a.ID); err != nil {
		t.Fatalf("could not link A: %v", err)
	}
	b, err := env.users.Create(ctx, "yeni@ornek.com", "B", testPassword)
	if err != nil {
		t.Fatalf("could not create B: %v", err)
	}

	// id_token: sub=sub-1 (linked to A) but the email belongs to B.
	user, conflict, err := env.pages.resolveUser(ctx, providerGoogle, buildClaims("sub-1", "yeni@ornek.com", true))
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if conflict != nil {
		t.Fatalf("conflict returned: %+v (the linked identity must come BEFORE email)", conflict)
	}
	if user == nil || user.ID != a.ID {
		t.Fatalf("user = %v, want A (%s)", user, a.ID)
	}
	if user.ID == b.ID {
		t.Error("fell through to B via email")
	}
}

//  2. The Apple private relay address is NEVER used for linking.
//     This is the only case the spec explicitly forbids.
func TestResolveUser_PrivateRelayDoesNotTriggerLink(t *testing.T) {
	t.Run("address registered on an account WITH a password: NO linking", func(t *testing.T) {
		env := newTestFederationPages(t)
		ctx := context.Background()
		// The attacker may have created an account with a password using the
		// victim's relay address.
		if _, err := env.users.Create(ctx, "abc"+privateRelaySuffix, "Saldirgan", testPassword); err != nil {
			t.Fatalf("could not create account: %v", err)
		}

		user, conflict, err := env.pages.resolveUser(ctx, providerApple, buildClaims("yeni-sub", "abc"+privateRelaySuffix, true))
		if err != nil {
			t.Fatalf("resolveUser: %v", err)
		}
		if user != nil {
			t.Fatalf("signed in (%s): the hidden relay address must not be used for linking", user.ID)
		}
		if conflict == nil {
			t.Fatal("a conflict was expected")
		}
		if conflict.HasPassword {
			t.Error("HasPassword = true: the hidden relay address must NOT be taken to the link page")
		}
		if conflict.PendingID != "" {
			t.Error("no pending record must be opened for the hidden relay address")
		}
		if _, err := env.federated.UserByIdentity(ctx, providerApple, "yeni-sub"); err == nil {
			t.Error("identity was linked: NO linking on the hidden relay address")
		}
	})

	t.Run("address not registered: new account directly", func(t *testing.T) {
		env := newTestFederationPages(t)
		ctx := context.Background()
		user, conflict, err := env.pages.resolveUser(ctx, providerApple, buildClaims("yeni-sub", "def"+privateRelaySuffix, true))
		if err != nil {
			t.Fatalf("resolveUser: %v", err)
		}
		if conflict != nil {
			t.Fatalf("conflict returned: %+v", conflict)
		}
		if user == nil {
			t.Fatal("new account was not opened")
		}
		if user.PasswordHash != "" {
			t.Error("a federated account must not have a password")
		}
	})
}

// 4. Account with a password: the link page.
func TestResolveUser_EmailConflictWithPasswordLeadsToLink(t *testing.T) {
	env := newTestFederationPages(t)
	ctx := context.Background()
	existing, err := env.users.Create(ctx, "ortak@ornek.com", "Mevcut", testPassword)
	if err != nil {
		t.Fatalf("could not create account: %v", err)
	}

	user, conflict, err := env.pages.resolveUser(ctx, providerGoogle, buildClaims("yeni-sub", "ortak@ornek.com", true))
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if user != nil {
		t.Fatalf("signed in (%s): NO automatic linking on an account with a password", user.ID)
	}
	if conflict == nil {
		t.Fatal("a conflict was expected")
	}
	if !conflict.HasPassword {
		t.Error("HasPassword = false, want true")
	}
	if conflict.PendingID == "" {
		t.Error("PendingID is empty")
	}
	if conflict.Email != "ortak@ornek.com" {
		t.Errorf("Email = %q", conflict.Email)
	}
	if _, err := env.federated.UserByIdentity(ctx, providerGoogle, "yeni-sub"); err == nil {
		t.Error("identity was linked: NO linking without asking for the password")
	}
	if _, err := env.users.ByID(ctx, existing.ID); err != nil {
		t.Errorf("existing account was damaged: %v", err)
	}
}

// 5. K1: account without a password -> 409, NO link, NO new account.
func TestResolveUser_EmailConflictWithoutPasswordBlocked(t *testing.T) {
	env := newTestFederationPages(t)
	ctx := context.Background()
	// An account opened with Google that has NO PASSWORD.
	existing, err := env.federated.CreateFederatedUser(ctx, providerGoogle, "google-sub", "ortak@ornek.com", "Mevcut", true)
	if err != nil {
		t.Fatalf("could not create federated account: %v", err)
	}

	// It comes from Apple with the SAME email.
	user, conflict, err := env.pages.resolveUser(ctx, providerApple, buildClaims("apple-sub", "ortak@ornek.com", true))
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if user != nil {
		t.Fatalf("signed in (%s): K1 NO linking on an account without a password", user.ID)
	}
	if conflict == nil {
		t.Fatal("a conflict was expected")
	}
	if conflict.HasPassword {
		t.Error("HasPassword = true: there is no password to ask for on an account without one")
	}
	if !reflect.DeepEqual(conflict.ExistingProviders, []string{providerGoogle}) {
		t.Errorf("ExistingProviders = %v, want [google]", conflict.ExistingProviders)
	}
	if _, err := env.federated.UserByIdentity(ctx, providerApple, "apple-sub"); err == nil {
		t.Error("identity was linked: K1 FORBIDS linking")
	}
	// A NEW RECORD MUST NOT BE CREATED: the same email still belongs to the first account.
	same, err := env.users.ByEmail(ctx, "ortak@ornek.com")
	if err != nil {
		t.Fatalf("existing account disappeared: %v", err)
	}
	if same.ID != existing.ID {
		t.Errorf("email passed to account %q, want %q (a new account must NOT be created)", same.ID, existing.ID)
	}
}

// The spec's concrete attack: the attacker opens an account with a password
// using the victim's address; the victim logs in with Google. It must NOT BE
// LINKED AUTOMATICALLY.
func TestResolveUser_NoSilentLinkToPrecreatedAccount(t *testing.T) {
	env := newTestFederationPages(t)
	ctx := context.Background()
	// users: kurban@ornek.com, password = the ATTACKER's password.
	attackers, err := env.users.Create(ctx, "kurban@ornek.com", "Kurban", "saldirganin-sifresi")
	if err != nil {
		t.Fatalf("could not create account: %v", err)
	}

	// Google: it must not link EVEN IF the email is verified -- this is the
	// real point of the test. Since WE do not do the email verification,
	// email_verified does not prove that this account belongs to the victim.
	user, conflict, err := env.pages.resolveUser(ctx, providerGoogle, buildClaims("kurban-google-sub", "kurban@ornek.com", true))
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if user != nil {
		t.Fatalf("SIGNED IN (%s): automatically linked to an account whose password the attacker holds", user.ID)
	}
	if conflict == nil {
		t.Fatal("a conflict was expected")
	}
	if _, err := env.federated.UserByIdentity(ctx, providerGoogle, "kurban-google-sub"); err == nil {
		t.Errorf("identity was SILENTLY linked to account %s", attackers.ID)
	}
}

// ---------------------------------------------------------------------
// Step 2: Private relay + email uniqueness conflict
// ---------------------------------------------------------------------

// The private relay address goes straight to a new account in step 2, but
// because of UNIQUE (lower(email)) the INSERT returns ErrEmailInUse if the
// address already exists. Decision: 409 in this case too -- never a link.
func TestResolveUser_ExistingPrivateRelayReturns409(t *testing.T) {
	env := newTestFederationPages(t)
	ctx := context.Background()
	relay := "abc" + privateRelaySuffix
	// The address is already registered with ANOTHER sub (the user may have
	// deleted their account and signed up again; the Apple relay address stays
	// the same).
	if _, err := env.federated.CreateFederatedUser(ctx, providerApple, "eski-sub", relay, "Eski", true); err != nil {
		t.Fatalf("could not create first account: %v", err)
	}
	env.apple.configure(func(s *fakeProvider) {
		s.sub = "yeni-sub"
		s.email = relay
	})

	w := env.runFlow(t, providerApple, true)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<form") {
		t.Error("the 409 page has a password form: that means linking exists")
	}
	if _, err := env.federated.UserByIdentity(ctx, providerApple, "yeni-sub"); err == nil {
		t.Error("identity was linked: NO linking")
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Error("request was completed: there must be NO sign-in")
	}
}

// ---------------------------------------------------------------------
// Step 3: Link page tests
// ---------------------------------------------------------------------

// Phase 1's C1 lesson: if there is NO binding the form is NEVER RENDERED.
func TestLinkGet_WithoutBindingRejected(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, _, cookie, _ := env.setupPendingLink(t)

	statuses := []struct {
		name   string
		id     string
		cookie *http.Cookie
	}{
		{"no beklemeID", "", cookie},
		{"made-up beklemeID", "uydurma-bekleme-id", cookie},
		{"no session cookie", pendingID, nil},
		{"session bound to ANOTHER request", pendingID, env.bindSession(t, "baska-istek")},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			w := env.linkGetRequest(d.id, d.cookie)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
			if strings.Contains(w.Body.String(), "<form") {
				t.Errorf("FORM was rendered for a request without a binding: %q", w.Body.String())
			}
		})
	}
}

func TestLinkPost_WrongCSRFRejected(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, _, cookie, existing := env.setupPendingLink(t)

	w := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {"yanlis-csrf"},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if _, err := env.federated.UserByIdentity(context.Background(), providerGoogle, env.google.sub); err == nil {
		t.Error("identity was linked while CSRF was wrong")
	}
	if env.requests.subject(env.authReqID) == existing.ID {
		t.Error("signed in while CSRF was wrong")
	}
}

func TestLinkPost_CorrectPasswordLinksAndLogsIn(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, cookie, existing := env.setupPendingLink(t)

	w := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body = %q", w.Code, w.Body.String())
	}
	if location := w.Header().Get("Location"); location != env.expectedCallback() {
		t.Errorf("Location = %q, want %q", location, env.expectedCallback())
	}
	linked, err := env.federated.UserByIdentity(context.Background(), providerGoogle, env.google.sub)
	if err != nil {
		t.Fatalf("identity was not linked: %v", err)
	}
	if linked.ID != existing.ID {
		t.Errorf("identity linked to account %q, want %q", linked.ID, existing.ID)
	}
	if s := env.requests.subject(env.authReqID); s != existing.ID {
		t.Errorf("Subject = %q, want %q", s, existing.ID)
	}
	if got := env.requests.amrFor(env.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, want [fed]", got)
	}
}

func TestLinkPost_WrongPasswordDoesNotLink(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := env.setupPendingLink(t)

	w := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {"yanlis-sifre-degeri"},
	}, cookie)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), msgLoginFailed) {
		t.Errorf("body does not carry the error text: %q", w.Body.String())
	}
	if _, err := env.federated.UserByIdentity(context.Background(), providerGoogle, env.google.sub); err == nil {
		t.Error("identity was LINKED with the wrong password")
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Error("signed in with the wrong password")
	}
}

// The pending link must be SINGLE-USE: after the correct password is entered
// a second link cannot be made with the same PendingID.
func TestPendingLink_SingleUse(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := env.setupPendingLink(t)
	form := url.Values{fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {testPassword}}

	first := env.linkPostRequest(form, cookie)
	if first.Code != http.StatusFound {
		t.Fatalf("first POST status = %d, want 302, body = %q", first.Code, first.Body.String())
	}
	second := env.linkPostRequest(form, cookie)
	if second.Code != http.StatusForbidden {
		t.Errorf("second POST status = %d, want 403 (pending is single-use)", second.Code)
	}
	// The form must not open any more either.
	if w := env.linkGetRequest(pendingID, cookie); w.Code != http.StatusForbidden {
		t.Errorf("GET status for a consumed pending = %d, want 403", w.Code)
	}
}

// The attacker must not be able to complete SOMEONE ELSE's pending link in
// their own browser.
func TestLink_CannotCompleteInOtherBrowser(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, _, existing := env.setupPendingLink(t)
	// The attacker's browser: it has a session cookie bound to its own flow but
	// NOT bound to the VICTIM's request.
	attacker := env.bindSession(t, "saldirganin-istegi")

	w := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, attacker)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if _, err := env.federated.UserByIdentity(context.Background(), providerGoogle, env.google.sub); err == nil {
		t.Error("linking was COMPLETED in another browser")
	}
	if env.requests.subject(env.authReqID) == existing.ID {
		t.Error("signed in from another browser")
	}
}

// Password attempts must not be UNLIMITED: the link page is the SECOND
// surface for password guessing and a separate endpoint from Phase 1's login
// limit.
func TestLink_RateLimitApplied(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := env.setupPendingLink(t)

	// A separate mux: the same handler, but this time wrapped with the limiter's limit.
	limiter, err := NewRateLimiter(env.store.rdb, RateLimitConfig{
		LoginIPPerMin: 1, LoginEmailPerHour: 100, SignupIPPerHour: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("could not set up the rate limiter: %v", err)
	}
	mux := http.NewServeMux()
	interceptor := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	env.pages.MountLink(mux, interceptor, limiter)
	env.mux = mux

	form := url.Values{fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {"yanlis-sifre-degeri"}}
	first := env.linkPostRequest(form, cookie)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("first attempt got 429; the limit is set up wrong")
	}
	second := env.linkPostRequest(form, cookie)
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second attempt status = %d, want 429", second.Code)
	}

	// The counter must be SEPARATE from the login endpoint: the login budget
	// must not have been used up.
	ctx := context.Background()
	keyList, err := env.store.rdb.Keys(ctx, rateLimitPrefix+":*").Result()
	if err != nil && err != redis.Nil {
		t.Fatalf("could not read keys: %v", err)
	}
	var hasBinding bool
	for _, a := range keyList {
		if strings.Contains(a, ":baglama:") {
			hasBinding = true
		}
		if strings.Contains(a, ":giris:") {
			t.Errorf("a link attempt consumed the LOGIN counter: %q", a)
		}
	}
	if !hasBinding {
		t.Errorf("link counter was not created; keys: %v", keyList)
	}
}

// Password attempts must be limited PER ACCOUNT too: an attacker who gets
// past the IP counter by changing IPs can repeat the federation round to
// get a new pending record on every attempt. What limits the number of
// attempts aimed at a single account is the account counter.
func TestLink_AccountRateLimitApplied(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := env.setupPendingLink(t)

	// The IP limit is GENEROUS (100/min), the account limit is 1/hour: the ONLY
	// reason for rejection must be the account counter.
	limiter, err := NewRateLimiter(env.store.rdb, RateLimitConfig{
		LoginIPPerMin: 100, LoginEmailPerHour: 1, SignupIPPerHour: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("could not set up the rate limiter: %v", err)
	}
	mux := http.NewServeMux()
	interceptor := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	env.pages.Mount(mux, interceptor, limiter)
	env.pages.MountLink(mux, interceptor, limiter)
	env.mux = mux

	first := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {"yanlis-sifre-degeri"},
	}, cookie)
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt status = %d, want 401", first.Code)
	}

	// The attacker gets a NEW pending record (repeats the federation round):
	// the uniqueness of the record does not limit the number of attempts.
	secondFlow := env.runFlow(t, providerGoogle, false)
	if secondFlow.Code != http.StatusFound {
		t.Fatalf("second flow status = %d, want 302", secondFlow.Code)
	}
	location, err := url.Parse(secondFlow.Header().Get("Location"))
	if err != nil {
		t.Fatalf("could not parse Location: %v", err)
	}
	newID := location.Query().Get(fieldPendingID)
	if newID == "" || newID == pendingID {
		t.Fatalf("could not get the new pending record: %q", newID)
	}
	newForm := env.linkGetRequest(newID, cookie)
	if newForm.Code != http.StatusOK {
		t.Fatalf("could not open the new form: status = %d", newForm.Code)
	}
	matches := csrfPattern.FindStringSubmatch(newForm.Body.String())
	if matches == nil {
		t.Fatal("no CSRF token in the new form")
	}

	second := env.linkPostRequest(url.Values{
		fieldPendingID: {newID}, fieldCSRF: {matches[1]}, "sifre": {"yanlis-sifre-degeri"},
	}, cookie)
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second attempt status = %d, want 429 (account counter)", second.Code)
	}

	keyList, err := env.store.rdb.Keys(context.Background(), rateLimitPrefix+":*").Result()
	if err != nil {
		t.Fatalf("could not read keys: %v", err)
	}
	var hasAccount bool
	for _, a := range keyList {
		if strings.Contains(a, ":baglama-hesap:") {
			hasAccount = true
		}
	}
	if !hasAccount {
		t.Errorf("account counter was not created; keys: %v", keyList)
	}
	// The account identity must not appear in PLAIN TEXT in the Redis key.
	for _, a := range keyList {
		if strings.Contains(a, env.google.email) {
			t.Errorf("counter key carries the email in plain text: %q", a)
		}
	}
}

// The pending record must not be consumed BEFORE the validations: a third
// party who learns the pendingID must not be able to DESTROY the victim's
// pending link with a single cookieless POST (otherwise the victim would
// have to redo the whole federation round).
func TestPendingLink_UnboundPostCannotDestroy(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, cookie, existing := env.setupPendingLink(t)

	// The attacker: NO cookie, NO CSRF.
	attack := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID}, "sifre": {"herhangi"},
	}, nil)
	if attack.Code != http.StatusForbidden {
		t.Fatalf("attack status = %d, want 403", attack.Code)
	}

	// The victim must STILL be able to complete their own flow.
	w := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {testPassword},
	}, cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("the victim's pending was destroyed: status = %d, body = %q", w.Code, w.Body.String())
	}
	if s := env.requests.subject(env.authReqID); s != existing.ID {
		t.Errorf("Subject = %q, want %q", s, existing.ID)
	}
	if got := env.requests.amrFor(env.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, want [fed]", got)
	}
}

// An account without a password must NEVER come to the link page; if it
// does, 409.
func TestLink_NotOpenedForPasswordlessAccount(t *testing.T) {
	env := newTestFederationPages(t)
	ctx := context.Background()
	// resolveUser does not produce this record (the K1 branch returns 409); the
	// record is written BY HAND, so this is a defense-in-depth test.
	passwordless, err := env.federated.CreateFederatedUser(ctx, providerGoogle, "google-sub", "sifresiz@ornek.com", "Sifresiz", true)
	if err != nil {
		t.Fatalf("could not create federated account: %v", err)
	}
	pendingID, err := randomToken()
	if err != nil {
		t.Fatalf("could not generate id: %v", err)
	}
	csrf, err := randomToken()
	if err != nil {
		t.Fatalf("could not generate csrf: %v", err)
	}
	pending := PendingLink{
		AuthRequestID: env.authReqID,
		UserID:        passwordless.ID,
		Provider:      providerApple,
		Sub:           "apple-sub",
		Email:         "sifresiz@ornek.com",
		CSRF:          csrf,
	}
	if err := env.pages.flows.WritePendingLink(ctx, pendingID, pending, pendingLinkTTL); err != nil {
		t.Fatalf("could not write pending: %v", err)
	}
	cookie := env.sessionCookie

	w := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if _, err := env.federated.UserByIdentity(ctx, providerApple, "apple-sub"); err == nil {
		t.Error("linking was done on an account without a password")
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Error("signed in on an account without a password")
	}
}

// I3, link leg: pendingLinkTTL also overlaps with the request TTL, and since
// the request counter starts at the very beginning and is never extended on
// any path, the auth request may have expired even when the user enters the
// password CORRECTLY. This is an ordinary expiry and must not produce a 500:
// the user must read what happened and that they must restart from the app.
func TestLinkPost_ExpiredRequestReturns400(t *testing.T) {
	env := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := env.setupPendingLink(t)

	// The auth request expired with the TTL (since the TTL values are not
	// changed, the record is dropped by hand); the pending record and the session
	// binding are STILL valid, so the test really measures the EXPIRY path.
	env.requests.drop(env.authReqID)

	w := env.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code == http.StatusInternalServerError {
		t.Fatalf("an ordinary expiry produced a 500: body = %q", w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), msgRequestExpired) {
		t.Errorf("the user was not told what happened: body = %q", w.Body.String())
	}
	if env.requests.subject(env.authReqID) != "" {
		t.Error("a dropped request was COMPLETED")
	}
}
