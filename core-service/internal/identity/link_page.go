package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// pathLink is the path of the account-link page. It is a step in the
// MIDDLE of the federation flow: the user was verified upstream but their
// email collides with an existing account that HAS A PASSWORD.
const pathLink = "/baglama"

// fieldPendingID is the form/query field name of the pending link record.
const fieldPendingID = "beklemeID"

// privateRelaySuffix is the suffix of Apple's "Hide My Email" addresses.
//
// These addresses are NOT trustworthy for LINKING under ANY CONDITION (spec
// prohibition): the address belongs not to the user but to the (app, Apple
// account) pair; if the user deletes their account and signs up again the
// SAME address comes back, and finding the address on another row does NOT
// PROVE it is the same person.
const privateRelaySuffix = "@privaterelay.appleid.com"

// pendingLinkTTL is the lifetime of the pending link record. Long enough for
// the user to remember and enter their password, short enough for a stolen
// record to lose its value.
const pendingLinkTTL = 10 * time.Minute

// PendingLink holds everything the /baglama page needs to complete a POST
// safely. It is kept in Redis, short-lived and SINGLE-USE.
//
// The fields MUST be exported (capitalized): the record is written as JSON
// and encoding/json silently skips lowercase fields.
type PendingLink struct {
	// AuthRequestID is the pending auth request that the flow will complete.
	AuthRequestID string
	// UserID is the EXISTING account whose password will be asked for. It does
	// NOT come from the form: otherwise the user could be steered into trying
	// another account's password.
	UserID string
	// Provider and Sub are the federated identity to link once the correct password is entered.
	Provider string `json:"Saglayici"`
	Sub      string
	// Email is the address SHOWN on the page (read-only).
	Email string `json:"Eposta"`
	// CSRF is the token specific to this record and this browser.
	CSRF string
}

// ErrPendingLinkNotFound means the expected link record does not exist (never
// created, expired, or ALREADY CONSUMED).
var ErrPendingLinkNotFound = errors.New("pending link not found")

// ErrUpstreamNoEmail is returned when a new federated account cannot be
// opened because the upstream verified id_token carried no email (see the
// rationale in resolveUser: at most ONE account can exist with an empty
// email, and the second would lock the user out PERMANENTLY).
var ErrUpstreamNoEmail = errors.New("upstream did not send an email")

// PendingLinkStore defines writing and reading the pending link record. In
// production *RequestStore implements it.
//
// The TWO read paths are INTENTIONAL:
//   - ReadPendingLink (GET): to RENDER the form. Rendering must NOT consume
//     the record, otherwise viewing the page would end the flow.
//   - TakePendingLink (GETDEL): for the POST. The record is consumed
//     ATOMICALLY, so only ONE link can be completed per record.
type PendingLinkStore interface {
	WritePendingLink(ctx context.Context, id string, b PendingLink, ttl time.Duration) error
	ReadPendingLink(ctx context.Context, id string) (PendingLink, error)
	TakePendingLink(ctx context.Context, id string) (PendingLink, error)
}

var _ PendingLinkStore = (*RequestStore)(nil)

func pendingLinkKey(id string) string { return "kimlik:baglama:" + id }

func (d *RequestStore) WritePendingLink(ctx context.Context, id string, b PendingLink, ttl time.Duration) error {
	// A zero/negative TTL means a PERSISTENT key in Redis: a record that must
	// be single-use would live forever.
	if ttl <= 0 {
		return fmt.Errorf("pending link ttl must be positive")
	}
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("could not serialize the pending link: %w", err)
	}
	if err := d.rdb.Set(ctx, pendingLinkKey(id), data, ttl).Err(); err != nil {
		return fmt.Errorf("could not write the pending link: %w", err)
	}
	return nil
}

// ReadPendingLink reads the record WITHOUT consuming it (for rendering the form).
func (d *RequestStore) ReadPendingLink(ctx context.Context, id string) (PendingLink, error) {
	return d.decodePendingLink(d.rdb.Get(ctx, pendingLinkKey(id)).Bytes())
}

// TakePendingLink reads the record and deletes it ATOMICALLY (GETDEL):
// SINGLE-USE. Otherwise, after the correct password was entered once, the
// same record could drive a second link (including completing another auth
// request). Same pattern as the code exchange (see RequestStore.GetByCode).
func (d *RequestStore) TakePendingLink(ctx context.Context, id string) (PendingLink, error) {
	return d.decodePendingLink(d.rdb.GetDel(ctx, pendingLinkKey(id)).Bytes())
}

func (d *RequestStore) decodePendingLink(data []byte, err error) (PendingLink, error) {
	if errors.Is(err, redis.Nil) {
		return PendingLink{}, ErrPendingLinkNotFound
	}
	if err != nil {
		return PendingLink{}, fmt.Errorf("could not read the pending link: %w", err)
	}
	var b PendingLink
	if err := json.Unmarshal(data, &b); err != nil {
		return PendingLink{}, fmt.Errorf("could not decode the pending link: %w", err)
	}
	return b, nil
}

// Conflict describes the situation where the federation flow stops WITHOUT
// logging in. It has two forms:
//
//	HasPassword = true  -> /baglama page: the existing account's password is
//	                       asked ONCE and, if correct, the identity is linked.
//	HasPassword = false -> 409 conflict page (K1): no password to ask, NO
//	                       link, NO new account.
type Conflict struct {
	// PendingID is the record key of the /baglama flow. It is set only when
	// HasPassword is true.
	PendingID string
	// Email is the conflicting address (shown to the user).
	Email string
	// ExistingProviders feeds the "which method to sign in with" part of the
	// 409 text. It MAY BE EMPTY; in that case no provider name is MADE UP.
	ExistingProviders []string
	HasPassword       bool

	// The following are NOT exported: they are carried only so that
	// handleConflict can write the pending record. They do NOT come from the form.
	userID   string
	provider string
	sub      string
	csrf     string
}

// resolveUser finds our user from a verified upstream id_token.
// THE ORDER IS THE SECURITY ITSELF:
//
//  1. (provider, sub) -- a linked account always comes first. The user's
//     email at the provider may change; looking up by email could take them
//     to ANOTHER account (or to no account at all).
//  2. Private relay address -> NO conflict check (spec prohibition).
//  3. No account with this email -> new account.
//  4. Account exists and HAS A PASSWORD -> link page (password once).
//  5. Account exists and has NO PASSWORD -> 409 (K1).
//
// In 4 and 5 NO AUTOMATIC LINKING happens, even if email_verified is true.
// The attack in the spec: since WE do not verify the email, an attacker can
// open a password account with the victim's address; if the victim signs in
// with Google and is linked automatically, they land in an account whose
// PASSWORD THE ATTACKER HAS.
func (f *FederationPages) resolveUser(
	ctx context.Context, provider string, claims *auth.Claims,
) (*User, *Conflict, error) {
	// 1) A linked identity always comes FIRST.
	user, err := f.federated.UserByIdentity(ctx, provider, claims.UserID)
	if err == nil {
		return user, nil, nil
	}
	if !errors.Is(err, ErrIdentityNotFound) {
		return nil, nil, fmt.Errorf("could not read federated identity: %w", err)
	}

	email := normalizeEmail(claims.Email)
	// 2) The conflict check is NEVER done for a private relay address.
	privateRelay := strings.HasSuffix(strings.ToLower(email), privateRelaySuffix)

	// No lookup is done for an EMPTY email either: searching for an account
	// with an empty address is meaningless and the uniqueness constraint
	// already prevents creating one.
	if !privateRelay && email != "" {
		existing, err := f.users.ByEmail(ctx, email)
		switch {
		// 4) Account exists and HAS A PASSWORD -> link page.
		case err == nil && existing.PasswordHash != "":
			c, err := f.buildConflict(existing, email, provider, claims)
			if err != nil {
				return nil, nil, err
			}
			return nil, c, nil
		// 5) Account exists and has NO PASSWORD -> 409 (K1). NO link, NO new
		//    account: there is no password to ask, and we cannot prove the
		//    account belongs to the same person.
		case err == nil:
			return nil, &Conflict{
				Email:             email,
				ExistingProviders: f.linkedProviders(ctx, existing.ID),
				HasPassword:       false,
			}, nil
		case !errors.Is(err, ErrUserNotFound):
			return nil, nil, fmt.Errorf("could not read the user: %w", err)
		}
	}

	// 3) No account (or a private relay address) -> new federated account.
	//
	// BUT if the email is EMPTY, NO account is opened. The users table has
	// "email NOT NULL" and "UNIQUE (lower(email))" (migrations/001_kullanici.sql),
	// so AT MOST ONE federated account without an email can exist. If upstream
	// omits the email once, the second such user fails with 23505 ->
	// ErrEmailInUse and lands on a 409 conflict page showing an EMPTY email;
	// that user can never sign up again, i.e. is locked out PERMANENTLY. The
	// probability is low, the door it opens is heavy.
	//
	// The fix is NOT to change the schema (making email nullable would affect a
	// running production table and is out of scope for this round) but to
	// reject EXPLICITLY instead of silently opening an account with an empty
	// email, and to tell the user what to do.
	if email == "" {
		return nil, nil, ErrUpstreamNoEmail
	}
	created, err := f.federated.CreateFederatedUser(
		ctx, provider, claims.UserID, email, claims.Name, bool(claims.EmailVerified))
	if errors.Is(err, ErrEmailInUse) {
		// The private relay address is already registered or a race happened in
		// between. In both cases there is NO LINK: we cannot prove that row
		// belongs to the same person.
		return nil, &Conflict{Email: email, HasPassword: false}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("could not create federated user: %w", err)
	}
	return created, nil, nil
}

// linkedProviders returns the provider names the user is linked to, to feed
// the 409 text. The error is NOT SWALLOWED but does NOT STOP THE FLOW: the
// list is for display only and an empty list falls back to the "another
// method" text (see providerText).
func (f *FederationPages) linkedProviders(ctx context.Context, userID string) []string {
	providers, err := f.federated.ProvidersByUserID(ctx, userID)
	if err != nil {
		log.Printf("federation: could not read the provider list: %v", err)
		return nil
	}
	return providers
}

// buildConflict produces the identifiers of the /baglama flow for a conflict
// with an account that HAS A PASSWORD. It does NOT WRITE the record to
// Redis: handleConflict carries authRequestID, so there is a single write point.
func (f *FederationPages) buildConflict(
	existing *User, email, provider string, claims *auth.Claims,
) (*Conflict, error) {
	pendingID, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("could not generate the pending id: %w", err)
	}
	csrf, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("could not generate csrf: %w", err)
	}
	return &Conflict{
		PendingID:   pendingID,
		Email:       email,
		HasPassword: true,
		userID:      existing.ID,
		provider:    provider,
		sub:         claims.UserID,
		csrf:        csrf,
	}, nil
}

// handleConflict turns the two forms of conflict into a response: for an
// account with a password it redirects to the /baglama page, for one
// without it writes the 409 conflict page. NO login happens in ANY branch.
func (f *FederationPages) handleConflict(
	w http.ResponseWriter, r *http.Request, authRequestID string, c *Conflict,
) {
	if !c.HasPassword {
		f.renderConflict(w, authRequestID, c.Email, c.ExistingProviders)
		return
	}
	pending := PendingLink{
		AuthRequestID: authRequestID,
		UserID:        c.userID,
		Provider:      c.provider,
		Sub:           c.sub,
		Email:         c.Email,
		CSRF:          c.csrf,
	}
	if err := f.flows.WritePendingLink(r.Context(), c.PendingID, pending, pendingLinkTTL); err != nil {
		// pendingID, csrf and email are NOT LOGGED.
		log.Printf("federation: could not write the pending link: %v", err)
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
		return
	}
	// The URL is built in Go with url.Values, NOT by string concatenation in the template.
	target := pathLink + "?" + url.Values{fieldPendingID: {c.PendingID}}.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}

// linkData is the data given to the baglama.html template.
type linkData struct {
	Email     string
	PendingID string
	CSRF      string
	Error     string
	LinkPath  string
}

// conflictData holds the fields cakisma.html expects. The Conflict struct is
// NOT passed directly: the template wants the text to SHOW the user, not the
// provider list.
type conflictData struct {
	Email        string
	ProviderText string
	// LoginURL is built in Go with url.Values; NO string concatenation in the template.
	LoginURL string
}

// providerLabel turns a provider name into the label shown to the user. An
// unknown name is NOT MADE UP, it is shown as is.
func providerLabel(name string) string {
	switch name {
	case providerGoogle:
		return "Google"
	case providerApple:
		return "Apple"
	default:
		return name
	}
}

// providerText turns the list into a single phrase to show the user. The
// list MAY BE EMPTY (for example if the private relay address is already
// registered); in that case no provider name is MADE UP.
func providerText(providers []string) string {
	switch len(providers) {
	case 0:
		return "baska bir yontem"
	case 1:
		return providerLabel(providers[0])
	default:
		labels := make([]string, 0, len(providers))
		for _, s := range providers {
			labels = append(labels, providerLabel(s))
		}
		return strings.Join(labels, " veya ")
	}
}

// renderConflict writes K1's 409 page: NO link, NO new account, only the
// "which method to sign in with" information.
func (f *FederationPages) renderConflict(w http.ResponseWriter, authRequestID, email string, providers []string) {
	data := conflictData{
		Email:        email,
		ProviderText: providerText(providers),
		LoginURL:     pathLogin + "?" + url.Values{"authRequestID": {authRequestID}}.Encode(),
	}
	w.WriteHeader(http.StatusConflict)
	if err := templates.ExecuteTemplate(w, "cakisma.html", data); err != nil {
		log.Printf("could not write the conflict template: %v", err)
	}
}

func (f *FederationPages) renderLink(w http.ResponseWriter, status int, b PendingLink, pendingID, errMsg string) {
	data := linkData{
		Email:     b.Email,
		PendingID: pendingID,
		CSRF:      b.CSRF,
		Error:     errMsg,
		LinkPath:  pathLink,
	}
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, "baglama.html", data); err != nil {
		log.Printf("could not write the link template: %v", err)
	}
}

// MountLink mounts the /baglama page on the mux.
//
// The POST is WRAPPED with the issuer interceptor (authCallback reads the
// issuer from the context). Rate limiting uses a SEPARATE counter from the
// LOGIN endpoint: /baglama is the SECOND password-guessing surface in this
// system, and opening the form and guessing a password must not consume the
// same budget.
//
// limiter MAY BE nil (only for unit tests).
func (f *FederationPages) MountLink(mux *http.ServeMux, interceptor *op.IssuerInterceptor, limiter *RateLimiter) {
	// limiter is also needed in the handler for the PER-ACCOUNT counter: that
	// counter's key is known only after the pending record is read (see
	// RateLimiter.AllowAccount).
	f.limiter = limiter
	var get http.Handler = http.HandlerFunc(f.linkGet)
	post := interceptor.Handler(http.HandlerFunc(f.linkPost))
	if limiter != nil {
		get = limiter.WrapPageGet("baglama", get)
		post = limiter.WrapLink(post)
	}
	mux.Handle("GET "+pathLink, get)
	mux.Handle("POST "+pathLink, post)
}

// verifyLinkBinding verifies TWO conditions together for a /baglama
// request: (1) a valid pending record exists, (2) the auth request the
// record is bound to is bound to THIS browser (Phase 1's session cookie; the
// federation cookie has Path=/federasyon so it is NEVER sent to /baglama).
//
// THE RECORD IS NOT CONSUMED HERE. Doing GETDEL BEFORE the checks would let
// a third party who learns a pendingID destroy the victim's pending link
// with a single COOKIELESS POST (the victim would have to redo the whole
// federation round). Consumption is done EXPLICITLY in linkPost, AFTER the
// password is verified.
func (f *FederationPages) verifyLinkBinding(
	r *http.Request, pendingID string,
) (PendingLink, error) {
	if pendingID == "" {
		return PendingLink{}, ErrPendingLinkNotFound
	}
	b, err := f.flows.ReadPendingLink(r.Context(), pendingID)
	if err != nil {
		return PendingLink{}, err
	}
	// Session binding: the browser that STARTED the flow and the browser that
	// COMPLETES the link must be the same.
	if _, err := verifySessionBinding(r.Context(), f.sessions, r, b.AuthRequestID); err != nil {
		return PendingLink{}, err
	}
	return b, nil
}

// linkGet shows the password form ONLY if a valid pending link exists.
// Phase 1's C1 lesson: RENDERING a form for a request without a binding IS
// the vulnerability. So the form is NOT MINTED here; an existing pending
// record is REQUIRED.
func (f *FederationPages) linkGet(w http.ResponseWriter, r *http.Request) {
	pendingID := r.URL.Query().Get(fieldPendingID)
	b, err := f.verifyLinkBinding(r, pendingID)
	if err != nil {
		rejectSession(w, "baglama-get", err)
		return
	}
	f.renderLink(w, http.StatusOK, b, pendingID, "")
}

// linkPost verifies the existing account's password ONCE and, if correct,
// links the federated identity to that account.
func (f *FederationPages) linkPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	pendingID := r.FormValue(fieldPendingID)
	// The record is read WITHOUT being consumed; consumption happens below,
	// AFTER the password is verified (see verifyLinkBinding).
	b, err := f.verifyLinkBinding(r, pendingID)
	if err != nil {
		rejectSession(w, "baglama", err)
		return
	}
	// The CSRF token is stored on the SERVER in the record: the verification is
	// effectively "did we issue this token for this pending record".
	if !csrfEqual(b.CSRF, r.FormValue(fieldCSRF)) {
		rejectSession(w, "baglama", ErrCSRF)
		return
	}

	existing, err := f.users.ByID(r.Context(), b.UserID)
	if err != nil {
		// Email and user id are NOT LOGGED.
		log.Printf("federation: could not read the account to link: %v", err)
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
		return
	}
	// An account without a password must NEVER reach this page (resolveUser
	// returns 409 in the K1 branch); if it does, 409 here as defense in depth.
	// PasswordMatches already returns false on an empty hash, but that path
	// would tell the user "you entered the wrong password" and the user would
	// try forever on a form they can NEVER get through.
	if existing.PasswordHash == "" {
		log.Print("federation: a link was attempted for an account without a password")
		f.renderConflict(w, b.AuthRequestID, b.Email, f.linkedProviders(r.Context(), existing.ID))
		return
	}
	// PER-ACCOUNT rate limit: BEFORE the password verification. The IP counter
	// does not stop a distributed attempt (a different IP per request); this
	// counter limits attempts aimed at a SINGLE account, INDEPENDENT of the
	// source and of the pending record used. Since new pending records can be
	// obtained as often as desired by repeating the federation round, the IP
	// counter alone is NOT sufficient.
	// limiter MAY BE nil (unit tests only); in production Start always sets up
	// a limiter and does NOT START AT ALL if it cannot.
	if f.limiter != nil && !f.limiter.AllowAccount(w, r, b.UserID) {
		return
	}
	if !existing.PasswordMatches(r.FormValue("sifre")) {
		// The record is NOT CONSUMED: a typo should not force the whole federation
		// round to be redone. What limits the attempts is the rate limits (per IP
		// + the per-account counter above).
		//
		// The password is NOT LOGGED and the error text does not leak the
		// account's existence: msgLoginFailed is the SAME single message as the
		// login page.
		f.renderLink(w, http.StatusUnauthorized, b, pendingID, msgLoginFailed)
		return
	}

	// Password CORRECT: only now is the record consumed ATOMICALLY (GETDEL).
	// Being SINGLE-USE prevents the same record from driving a second link
	// (including completing another auth request) after the correct password
	// was entered once.
	if _, err := f.flows.TakePendingLink(r.Context(), pendingID); err != nil {
		// Race: the record was consumed in the meantime. We stop WITHOUT linking.
		rejectSession(w, "baglama", err)
		return
	}

	if err := f.federated.Link(r.Context(), b.Provider, b.Sub, b.UserID); err != nil {
		log.Printf("federation: could not link the identity: %v", err)
		if errors.Is(err, ErrIdentityInUse) {
			// The identity was linked to another account in the meantime, or this
			// account is already linked to this provider: we stop explicitly instead of
			// silently overwriting.
			f.renderConflict(w, b.AuthRequestID, b.Email, f.linkedProviders(r.Context(), b.UserID))
			return
		}
		f.errorPage(w, http.StatusInternalServerError, msgServerError)
		return
	}
	if err := f.requests.MarkDone(r.Context(), b.AuthRequestID, b.UserID, []string{amrFederation}); err != nil {
		// authRequestID is NOT LOGGED.
		log.Printf("federation: could not complete the request: %v", err)
		// SAME classification as the callback leg: 409 / 400 (dropped by TTL) /
		// 500. Since pendingLinkTTL and requestTTL overlap, this leg too can
		// OUTLIVE the auth request.
		f.writeCompletionError(w, err)
		return
	}
	http.Redirect(w, r, f.authCallback(r.Context(), b.AuthRequestID), http.StatusFound)
}
