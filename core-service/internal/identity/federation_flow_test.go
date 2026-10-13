package identity

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// This file measures the federation flow END TO END, over a real net/http
// listener (buildMux) and the COOKIE JAR. The test infrastructure is in
// fake_upstream_test.go.
//
// WHY THIS FILE EXISTS: in Phase 1 the per-task reviews could not catch the
// mismatch between our own layers and the library's call order STRUCTURALLY;
// the two Criticals that went live were exactly there. Here no assertion
// rests on the call of a single layer, but on the OBSERVABLE result of the
// flow (the code produced, the token issued, the link written).
//
// Every attack test follows the SAME PATTERN: first it is asserted that the
// precondition was REALLY set up (positive control), then the rejection is
// measured. This pattern is mandatory: in Phase 1 two tests passed green
// without ever measuring the fix.

// ---------------------------------------------------------------------
// Common helpers
// ---------------------------------------------------------------------

// userinfo calls the /userinfo endpoint with the access token.
//
// WHY A SEPARATE SURFACE: the id_token and /userinfo fill the claims through
// TWO INDEPENDENT paths (id_token: Storage.SetUserinfoFromRequest,
// userinfo: Storage.SetUserinfoFromToken). One being right does not prove the
// other, so the happy path tests measure BOTH.
func (o *flowEnv) userinfo(t *testing.T, accessToken string) map[string]any {
	t.Helper()
	b := o.NewBrowser(t)
	resp := b.getWithHeaders(t, http.MethodGet, o.issuer+"/userinfo", map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("/userinfo status = %d, body: %s", resp.Status, resp.Body())
	}
	return decodeJSON(t, resp.Body())
}

// federationCode runs the federation flow from start to finish and returns
// the produced authorization code and the browser that ran the flow.
func (o *flowEnv) federationCode(t *testing.T, provider string) (string, *fedBrowser) {
	t.Helper()
	session := o.NewBrowser(t)
	requestID := session.StartAuthorize(t)
	last := session.RunFederationFlow(t, provider, requestID)
	code := last.LocationCode()
	if code == "" {
		t.Fatalf("%s flow did not produce a code; last status %d, location %q, body: %s",
			provider, last.Status, last.Location(), last.Body())
	}
	return code, session
}

// federationTokens runs the federation flow from start to finish and returns
// the response of the token endpoint. The common body of the happy path tests.
func (o *flowEnv) federationTokens(
	t *testing.T, provider string,
) (map[string]any, *fedBrowser) {
	t.Helper()
	code, session := o.federationCode(t, provider)
	status, token := o.exchangeToken(t, code, session.verifier)
	if status != http.StatusOK {
		t.Fatalf("token status = %d, body: %v", status, token)
	}
	return token, session
}

// accessTokenSub returns the sub claim of the access token (our user ID).
func accessTokenSub(t *testing.T, token map[string]any) string {
	t.Helper()
	access, _ := token["access_token"].(string)
	if access == "" {
		t.Fatal("no access_token")
	}
	_, claims := jwtClaims(t, access)
	sub, _ := claims["sub"].(string)
	if sub == "" {
		t.Fatal("no sub in the access token")
	}
	return sub
}

// upstreamCallbackURL takes a VALID (state, code) pair from the upstream but
// does NOT RUN OUR callback leg: the returned value is the FULL address of the
// callback endpoint, ready to be called. This is exactly the input of the
// attack tests -- the attacker gets a valid pair from their own authorization.
func upstreamCallbackURL(t *testing.T, b *fedBrowser, provider, requestID string) string {
	t.Helper()
	start := b.StartFederation(t, provider, requestID)
	if start.Status != http.StatusFound {
		t.Fatalf("start status = %d, want 302. body: %s", start.Status, start.Body())
	}
	if start.fedCookie() == nil {
		t.Fatal("start did not write the federation cookie; scenario could not be set up")
	}
	// The user approves in the upstream flow (cross-site top-level GET).
	upstream := b.do(t, http.MethodGet, start.Location(), nil, ctxCrossSiteGet)
	if upstream.Status != http.StatusFound {
		t.Fatalf("upstream /authorize status = %d, want 302", upstream.Status)
	}
	callback := upstream.Location()
	parsed, err := url.Parse(callback)
	if err != nil {
		t.Fatalf("could not parse callback address: %v", err)
	}
	if parsed.Query().Get("code") == "" || parsed.Query().Get("state") == "" {
		t.Fatalf("upstream did not return code+state: %q", callback)
	}
	return callback
}

// ---------------------------------------------------------------------
// Happy paths
// ---------------------------------------------------------------------

// End to end: /authorize -> /giris -> Google button ->
// /federasyon/google/baslat -> fake Google -> /federasyon/google/geri
// -> /authorize/callback -> code -> /oauth/token -> access + id + refresh
//
// It runs with a real net/http listener and a COOKIE JAR; the cookie
// behavior (Path, SameSite) is part of the flow and cannot be measured with
// hand-built requests.
func TestE2E_GoogleLogin(t *testing.T) {
	const email = "google-kullanici@ornek.com"
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         email,
		EmailVerified: true,
		Name:          "Google Kullanici",
		Sub:           "google-sub-1",
	})

	token, _ := env.federationTokens(t, providerGoogle)

	// 0) The request WE SEND to the upstream is part of the flow too: our own
	// PKCE pair and nonce are visible there.
	query := env.Upstream(t, providerGoogle).lastAuthorizeQuery()
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		t.Errorf("S256 PKCE was not sent upstream: %v", query)
	}
	if query.Get("nonce") == "" {
		t.Error("nonce was not sent upstream")
	}

	// 1) access_token: signed with RS256 and for OUR client.
	access, _ := token["access_token"].(string)
	if access == "" {
		t.Fatal("no access_token")
	}
	header, accessClaims := jwtClaims(t, access)
	if header["alg"] != "RS256" {
		t.Errorf("access token alg = %v, want RS256", header["alg"])
	}
	if !audContains(accessClaims["aud"], "makesinger-mobil") {
		t.Errorf("access token aud = %v, want makesinger-mobil", accessClaims["aud"])
	}

	// 2) amr = ["fed"]: the fact that the user was verified through federation
	// must be VISIBLE in the token. The token that carries amr is the id_token
	// (the brief says access token; the library puts it in the id_token, see
	// the report).
	idToken, _ := token["id_token"].(string)
	if idToken == "" {
		t.Fatal("no id_token")
	}
	_, idClaim := jwtClaims(t, idToken)
	amr := amrValues(idClaim["amr"])
	if len(amr) != 1 || amr[0] != amrFederation {
		t.Errorf("amr = %v, want [%s]", amr, amrFederation)
	}

	// 3) The id_token must CARRY the REQUIRED claim of OIDC Core 1.0 §2: sub
	// must be set and the SAME user as the access token's sub. Since the email
	// scope is requested, the email must be in the id_token as well. This
	// assertion is a separate surface: the library feeds the id_token's userinfo
	// from Storage.SetUserinfoFromRequest and the /userinfo endpoint from
	// Storage.SetUserinfoFromToken.
	wantSub := accessTokenSub(t, token)
	if idSub, _ := idClaim["sub"].(string); idSub != wantSub {
		t.Errorf("id_token sub = %v, want %q (the access token's sub)", idClaim["sub"], wantSub)
	}
	if idClaim["email"] != email {
		t.Errorf("id_token email = %v, want %q", idClaim["email"], email)
	}

	// 4) Federation resolved the RIGHT user: the upstream's email and our sub
	// are set in the userinfo.
	info := env.userinfo(t, access)
	if info["sub"] == "" || info["sub"] == nil {
		t.Error("no sub in userinfo")
	}
	if info["email"] != email {
		t.Errorf("userinfo email = %v, want %q", info["email"], email)
	}

	// 5) A refresh_token was issued and can be exchanged ONCE; the old token
	// becomes invalid with the returned one (family rotation).
	refresh, _ := token["refresh_token"].(string)
	if refresh == "" {
		t.Fatal("offline_access was requested but there is no refresh_token")
	}
	status, fresh := env.refresh(t, refresh)
	if status != http.StatusOK {
		t.Fatalf("refresh status = %d, body: %v", status, fresh)
	}
	rotatedRefresh, _ := fresh["refresh_token"].(string)
	if rotatedRefresh == "" || rotatedRefresh == refresh {
		t.Errorf("refresh was not rotated: same as the old one or empty (%q)", rotatedRefresh)
	}
	if repeatStatus, repeatBody := env.refresh(t, refresh); repeatStatus == http.StatusOK {
		t.Errorf("a consumed refresh_token was accepted again: %v", repeatBody)
	}
}

// The same flow, but the return is a CROSS-SITE POST.
// This test is the live proof of K2: it does NOT pass with a SameSite=Lax
// cookie, it passes with SameSite=None. The proof is not only written but
// MEASURED: the cookie jar applies the SameSite rule (the standard cookiejar
// ignores it, see cookieJar) and turning the cookie into Lax makes this test
// FAIL (mutation proof in the report).
func TestE2E_AppleLoginFormPost(t *testing.T) {
	const email = "apple-kullanici@ornek.com"
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         email,
		EmailVerified: true,
		Name:          "Apple Kullanici",
		Sub:           "apple-sub-1",
	})

	// PRECONDITION: the return must REALLY be a cross-site POST. If the start
	// address has no response_mode=form_post the test measures the same GET
	// return as Google and says NOTHING about K2.
	session := env.NewBrowser(t)
	requestID := session.StartAuthorize(t)
	start := session.StartFederation(t, providerApple, requestID)
	if start.Status != http.StatusFound {
		t.Fatalf("start status = %d, want 302", start.Status)
	}
	upstreamURL, err := url.Parse(start.Location())
	if err != nil {
		t.Fatalf("could not parse upstream address: %v", err)
	}
	if upstreamURL.Query().Get("response_mode") != "form_post" {
		t.Fatalf("Apple flow does not request form_post (response_mode=%q); the test does NOT MEASURE K2",
			upstreamURL.Query().Get("response_mode"))
	}
	// To be carried on a cross-site POST the federation cookie must be
	// SameSite=None and its surface must be limited to /federasyon.
	cookie := start.fedCookie()
	if cookie == nil {
		t.Fatal("start did not write the federation cookie")
	}
	if cookie.SameSite != http.SameSiteNoneMode {
		t.Errorf("federation cookie SameSite = %v, want None", cookie.SameSite)
	}
	if cookie.Path != "/federasyon" {
		t.Errorf("federation cookie Path = %q, want /federasyon", cookie.Path)
	}

	// The rest of the flow: the fake Apple POSTs to US with form_post.
	last := session.follow(t, start)
	code := last.LocationCode()
	if code == "" {
		t.Fatalf("form_post flow did not produce a code; last status %d, location %q, body: %s",
			last.Status, last.Location(), last.Body())
	}
	status, token := env.exchangeToken(t, code, session.verifier)
	if status != http.StatusOK {
		t.Fatalf("token status = %d, body: %v", status, token)
	}
	access, _ := token["access_token"].(string)
	if access == "" {
		t.Fatal("no access_token")
	}
	if info := env.userinfo(t, access); info["email"] != email {
		t.Errorf("userinfo email = %v, want %q", info["email"], email)
	}
}

// The second login must land on the SAME account: the (provider, sub) link is permanent.
func TestE2E_SecondLoginSameAccount(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "tekrar@ornek.com",
		EmailVerified: true,
		Name:          "Tekrar Giren",
		Sub:           "google-sub-sabit",
	})

	first, _ := env.federationTokens(t, providerGoogle)
	firstSub := accessTokenSub(t, first)
	// PRECONDITION: the first login must REALLY have written a link; otherwise
	// the second login being the "same account" proves nothing.
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Fatalf("match count after the first sign-in = %d, want 1", count)
	}

	second, _ := env.federationTokens(t, providerGoogle)
	if secondSub := accessTokenSub(t, second); secondSub != firstSub {
		t.Errorf("second sign-in landed on ANOTHER account: %q != %q", secondSub, firstSub)
	}
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Errorf("second sign-in wrote a new match: count = %d", count)
	}
}

// Apple does NOT SEND the email claim on the second login. This must not
// lead to losing the account -- the link is by sub.
func TestE2E_AppleSecondLoginWithoutEmail(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "apple-ilk@ornek.com",
		EmailVerified: true,
		Name:          "Apple Ilk",
		Sub:           "apple-sub-sabit",
	})

	first, _ := env.federationTokens(t, providerApple)
	firstSub := accessTokenSub(t, first)
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Fatalf("match count after the first sign-in = %d, want 1", count)
	}

	// PRECONDITION: Apple sends NO email AT ALL in the second round. This is the
	// ONLY thing that changes: other fields such as the Name stay RIGHT,
	// otherwise it becomes unclear which gap the test measures.
	apple := env.Upstream(t, providerApple)
	apple.configure(func(a *fakeOPConfig) { a.Email = "" })

	second, _ := env.federationTokens(t, providerApple)
	if secondSub := accessTokenSub(t, second); secondSub != firstSub {
		t.Errorf("second sign-in without email landed on ANOTHER account: %q != %q", secondSub, firstSub)
	}
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Errorf("second sign-in without email opened a new account: match count = %d", count)
	}
	// The email must not be lost: the address learned in the first round stays
	// on the account.
	access, _ := second["access_token"].(string)
	if info := env.userinfo(t, access); info["email"] != "apple-ilk@ornek.com" {
		t.Errorf("userinfo email = %v, want the first round's address", info["email"])
	}
	// OUR id_token CARRIES the email too. This is not a contradiction: the
	// source of the claim is NOT the upstream's response in this round but the
	// user record in our own database (Storage.fillUserinfo, through
	// users.ByID). The upstream never sending the email again does not impoverish
	// our own tokens.
	idToken, _ := second["id_token"].(string)
	if idToken == "" {
		t.Fatal("no id_token")
	}
	_, idClaim := jwtClaims(t, idToken)
	if idClaim["email"] != "apple-ilk@ornek.com" {
		t.Errorf("id_token email = %v, want the first round's address", idClaim["email"])
	}
	if idSub, _ := idClaim["sub"].(string); idSub != firstSub {
		t.Errorf("id_token sub = %v, want %q", idClaim["sub"], firstSub)
	}
}

// ---------------------------------------------------------------------
// Attacks
// ---------------------------------------------------------------------

// The attacker gets a valid state+code pair from their own Google
// authorization and opens the callback in the victim's browser. The victim's
// flow must NOT BE COMPLETED for the attacker's account.
//
// WHY THE VICTIM ALSO RUNS THEIR OWN FEDERATION LEG: if the victim only
// did /authorize, the victim would have NO kimlik_fed cookie AT ALL and the
// rejection would come from the "NO cookie" branch -- i.e. this test would
// measure the SAME mechanism as TestAttack_CallbackWithoutCookieRejected,
// and the cookie VALUE comparison (subtle.ConstantTimeCompare, see
// FederationPages.Callback) would never run. When the victim gets their own
// cookie the cookie EXISTS but its VALUE does not match the attacker's: what
// is measured is the binding itself.
func TestAttack_FlowFromOtherBrowserCannotComplete(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "saldirgan@ornek.com",
		EmailVerified: true,
		Sub:           "google-sub-saldirgan",
	})

	attacker := env.NewBrowser(t)
	victim := env.NewBrowser(t)
	attackerRequest := attacker.StartAuthorize(t)
	victimRequest := victim.StartAuthorize(t)

	// PRECONDITION 1: the attacker obtained a VALID state+code pair.
	callbackURL := upstreamCallbackURL(t, attacker, providerGoogle, attackerRequest)

	// PRECONDITION 2: the victim started THEIR OWN federation leg, i.e. there is
	// a kimlik_fed cookie in the victim's browser too.
	victimStart := victim.StartFederation(t, providerGoogle, victimRequest)
	if victimStart.Status != http.StatusFound {
		t.Fatalf("victim's start status = %d, want 302", victimStart.Status)
	}
	if victimStart.fedCookie() == nil {
		t.Fatal("victim's start did not write the federation cookie; scenario could not be set up")
	}

	// PRECONDITION 3: the VALUES of the two cookies are different. If they were
	// equal the rejection below would come from something other than the cookie
	// binding.
	victimCookie := victim.jar.cookieValue(cookieNameFederation)
	attackerCookie := attacker.jar.cookieValue(cookieNameFederation)
	if victimCookie == "" || attackerCookie == "" {
		t.Fatalf("could not read cookie values (victim %q, attacker %q)", victimCookie, attackerCookie)
	}
	if victimCookie == attackerCookie {
		t.Fatal("the federation cookies of the two browsers are the SAME; the binding test would be empty")
	}

	// The attacker has that address opened in the victim's browser: the victim
	// HAS a cookie but its VALUE does not match the one in the attacker's state
	// record, so it must be rejected.
	red := victim.do(t, http.MethodGet, callbackURL, nil, ctxCrossSiteGet)
	if red.Status != http.StatusForbidden {
		t.Errorf("callback status in another browser = %d, want 403. body: %s",
			red.Status, red.Body())
	}
	if env.RequestDone(t, attackerRequest) {
		t.Fatal("ACCOUNT TAKEOVER: the attacker's request was completed in the victim's browser")
	}
	if env.RequestDone(t, victimRequest) {
		t.Fatal("the victim's request was completed through the attacker's flow")
	}
	if _, err := victim.ExchangeCode(t, attackerRequest); err == nil {
		t.Fatal("code exchange succeeded in the victim's browser")
	}
	if count := env.FederatedLinkCount(t); count != 0 {
		t.Errorf("rejected flow wrote a match: count = %d", count)
	}

	// POSITIVE CONTROL: a second pair obtained the SAME way works in the
	// attacker's OWN browser. So what is measured is the cookie binding, not a
	// broken scenario setup.
	ownRequest := attacker.StartAuthorize(t)
	ownCallback := upstreamCallbackURL(t, attacker, providerGoogle, ownRequest)
	own := attacker.follow(t,
		attacker.do(t, http.MethodGet, ownCallback, nil, ctxCrossSiteGet))
	if own.LocationCode() == "" {
		t.Fatalf("flow did not complete in the attacker's own browser (status %d); scenario is invalid",
			own.Status)
	}
}

// Even if the attacker has captured the victim's state, they must not be able
// to complete without the cookie (K2).
func TestAttack_CallbackWithoutCookieRejected(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "cerezsiz@ornek.com",
		EmailVerified: true,
		Sub:           "google-sub-cerezsiz",
	})

	session := env.NewBrowser(t)
	requestID := session.StartAuthorize(t)
	// PRECONDITION: there is a valid state+code pair and the cookie was WRITTEN
	// (the upstream code helper verifies both).
	callbackURL := upstreamCallbackURL(t, session, providerGoogle, requestID)

	// The cookie is DELETED: this is the only thing that changes.
	session.jar.deleteCookie(cookieNameFederation)
	red := session.do(t, http.MethodGet, callbackURL, nil, ctxCrossSiteGet)
	if red.Status != http.StatusForbidden {
		t.Errorf("callback status without cookie = %d, want 403. body: %s", red.Status, red.Body())
	}
	if env.RequestDone(t, requestID) {
		t.Fatal("callback without cookie completed the request")
	}
	if count := env.FederatedLinkCount(t); count != 0 {
		t.Errorf("callback without cookie wrote a match: count = %d", count)
	}

	// POSITIVE CONTROL: a new round with the cookie IN PLACE completes.
	newRequest := session.StartAuthorize(t)
	newCallback := upstreamCallbackURL(t, session, providerGoogle, newRequest)
	last := session.follow(t, session.do(t, http.MethodGet, newCallback, nil, ctxCrossSiteGet))
	if last.LocationCode() == "" {
		t.Fatalf("flow with cookie did not complete (status %d); the test is measuring something OTHER than the cookie",
			last.Status)
	}
}

// The same callback cannot be replayed twice (state GETDEL).
func TestAttack_CallbackReplayRejected(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "tekrar-oynatma@ornek.com",
		EmailVerified: true,
		Sub:           "google-sub-tekrar",
	})

	session := env.NewBrowser(t)
	requestID := session.StartAuthorize(t)
	callbackURL := upstreamCallbackURL(t, session, providerGoogle, requestID)

	// PRECONDITION: the callback REALLY works ONCE.
	first := session.follow(t, session.do(t, http.MethodGet, callbackURL, nil, ctxCrossSiteGet))
	if first.LocationCode() == "" {
		t.Fatalf("first callback did not produce a code (status %d); the replay test would be empty", first.Status)
	}
	if !env.RequestDone(t, requestID) {
		t.Fatal("first callback did not complete the request; the replay test would be empty")
	}
	google := env.Upstream(t, providerGoogle)
	if count := google.tokenCalls(); count != 1 {
		t.Fatalf("upstream token calls in the first round = %d, want 1", count)
	}

	// The SAME address a second time: the state record was consumed by GETDEL.
	again := session.do(t, http.MethodGet, callbackURL, nil, ctxCrossSiteGet)
	if again.Status != http.StatusForbidden {
		t.Errorf("replayed callback status = %d, want 403. body: %s",
			again.Status, again.Body())
	}
	// The rejection must happen BEFORE going to the upstream: the state check
	// runs BEFORE the code exchange.
	if count := google.tokenCalls(); count != 1 {
		t.Errorf("replay made a new token call upstream: count = %d", count)
	}
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Errorf("replay changed the match count: %d, want 1", count)
	}
}

// If the upstream returns an id_token issued to ANOTHER application (wrong
// aud) there must be NO login.
func TestAttack_OtherAppTokenRejected(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "aud@ornek.com",
		EmailVerified: true,
		Sub:           "google-sub-aud",
	})
	google := env.Upstream(t, providerGoogle)

	// PRECONDITION: with the right aud the flow REALLY completes. Otherwise the
	// rejection below would come from something other than aud.
	env.federationTokens(t, providerGoogle)
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Fatalf("match count with correct aud = %d, want 1", count)
	}

	// THE ONLY CHANGE: aud now belongs to ANOTHER application.
	google.configure(func(a *fakeOPConfig) { a.WrongAudience = "baska-uygulama" })
	session := env.NewBrowser(t)
	requestID := session.StartAuthorize(t)
	red := session.RunFederationFlow(t, providerGoogle, requestID)
	if red.Status != http.StatusForbidden {
		t.Errorf("status with wrong aud = %d, want 403. body: %s", red.Status, red.Body())
	}
	if red.LocationCode() != "" {
		t.Fatal("a code was issued with an id_token with the wrong aud")
	}
	if env.RequestDone(t, requestID) {
		t.Fatal("the request was completed with an id_token with the wrong aud")
	}
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Errorf("flow with the wrong aud wrote a new match: count = %d", count)
	}
}

// An id_token with a different nonce cannot be injected.
func TestAttack_NonceInjectionRejected(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "nonce@ornek.com",
		EmailVerified: true,
		Sub:           "google-sub-nonce",
	})
	google := env.Upstream(t, providerGoogle)

	// PRECONDITION: the flow completes with the right nonce.
	env.federationTokens(t, providerGoogle)
	if count := env.FederatedLinkCount(t); count != 1 {
		t.Fatalf("match count with correct nonce = %d, want 1", count)
	}

	// THE ONLY CHANGE: the id_token's nonce does not belong to THIS flow. The
	// real scenario is the attacker injecting a valid id_token from their own
	// flow into the victim's flow.
	google.configure(func(a *fakeOPConfig) { a.WrongNonce = "saldirganin-nonce-degeri" })
	session := env.NewBrowser(t)
	requestID := session.StartAuthorize(t)
	red := session.RunFederationFlow(t, providerGoogle, requestID)
	if red.Status != http.StatusForbidden {
		t.Errorf("status with wrong nonce = %d, want 403. body: %s", red.Status, red.Body())
	}
	if env.RequestDone(t, requestID) {
		t.Fatal("the request was completed with an id_token with the wrong nonce")
	}
	if _, err := session.ExchangeCode(t, requestID); err == nil {
		t.Fatal("the code from the flow with the wrong nonce was exchanged for a token")
	}
}

// The spec's main attack, END TO END: the attacker opens an account with a
// password using the victim's address, the victim logs in with Google. The
// victim must NOT LAND ON THE ATTACKER's account; the link page must appear
// and ask for a password.
//
// This test is the PATTERN of the others mentioned in the comment: first it
// is asserted that the precondition was set up, then the rejection is
// measured.
func TestAttack_EmailBasedTakeoverBlocked(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "kurban@ornek.com",
		EmailVerified: true, // it must not link EVEN IF email_verified=true
		Sub:           "google-sub-kurban",
	})

	// 1) The attacker opens an account WITH A PASSWORD using the victim's address.
	const attackerPassword = "saldirganin-sifresi-123"
	env.SignUp(t, "kurban@ornek.com", "Saldirgan", attackerPassword)

	// POSITIVE CONTROL: was the account REALLY opened? If not, the rejection in
	// step 4 would measure nothing and the test would be EMPTY.
	if token := env.LoginWithPassword(t, "kurban@ornek.com", attackerPassword); token == "" {
		t.Fatal("precondition could not be set up: the attacker's account was not created")
	}

	// 2) The victim goes from the login page to Google.
	session := env.NewBrowser(t)
	requestID := session.StartAuthorize(t)
	resp := session.RunFederationFlow(t, providerGoogle, requestID)

	// 3) The link page must appear: a password MUST BE ASKED.
	body := resp.Body()
	if !strings.Contains(body, `name="beklemeID"`) {
		t.Fatalf("link form did not appear; status %d, body: %s", resp.Status, body)
	}
	if !strings.Contains(body, `type="password"`) {
		t.Error("link page does not ask for a password")
	}

	// 4) THERE MUST BE NO LOGIN: since the request was not completed no code is
	// produced and no access_token can be obtained.
	if session.RequestDone(t, requestID) {
		t.Fatal("federation completed the request without asking for a password (account takeover)")
	}
	if _, err := session.ExchangeCode(t, requestID); err == nil {
		t.Fatal("code exchange succeeded; that means a sign-in happened")
	}

	// 5) Since the victim does not know the attacker's password the flow stops
	// HERE: linking with a wrong password must be REJECTED and NO link written.
	red := session.SubmitLink(t, body, "kurbanin-kendi-sifresi-123")
	if red.Status != http.StatusUnauthorized {
		t.Errorf("link status with wrong password = %d, want 401", red.Status)
	}
	if env.FederatedLinkCount(t) != 0 {
		t.Error("federated match was written despite the wrong password")
	}
}

// PKCE: the federation path must NOT SKIP Phase 1's S256 requirement. One
// cannot come to /authorize without PKCE and press the federation button.
func TestAttack_FederationDoesNotBypassPKCE(t *testing.T) {
	env := setupFlowEnv(t, fakeOPConfig{
		Email:         "pkce@ornek.com",
		EmailVerified: true,
		Sub:           "google-sub-pkce",
	})

	// 1) /authorize without PKCE is REJECTED, i.e. an authRequestID for the
	// federation button to stand on is NEVER born. The federation leg cannot be
	// called with an id that has no binding (see Start).
	c := newBrowser(t)
	assertPKCERejected(t, mustGet(t, c, env.authorizeURLWithScope("", "", fedScopes)))

	// 2) POSITIVE CONTROL: the federation path works with the RIGHT verifier.
	// (Since the code is SINGLE-USE two separate rounds are needed: trying the
	// same code twice would measure "invalid code" on the second attempt and it
	// would be unclear what the test measures.)
	code, session := env.federationCode(t, providerGoogle)
	status, token := env.exchangeToken(t, code, session.verifier)
	if status != http.StatusOK {
		t.Fatalf("token status with correct verifier = %d, body: %v", status, token)
	}

	// 3) The code produced by the federation path stays BOUND to S256 too: it
	// cannot be exchanged for a token without the right verifier. This is the
	// measurable form of federation not being "a side door that bypasses
	// PKCE".
	wrongCode, _ := env.federationCode(t, providerGoogle)
	wrongVerifier, _ := pkceS256(t)
	if status, body := env.exchangeToken(t, wrongCode, wrongVerifier); status == http.StatusOK {
		t.Fatalf("federation code was exchanged for a token with the WRONG verifier: %v", body)
	}
}
