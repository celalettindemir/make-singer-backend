package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// cookieNameSession is the name of the cookie given to the browser that
// STARTS the auth request. authRequestID alone cannot be an authorization
// input: it is possible to send the link to someone else, have them do the
// login, and collect the code in one's own browser (account takeover). The
// cookie binds the browser that started the auth request to the request.
//
// THE BINDING IS SET ONLY AT /authorize (see Pages.WrapAuthorize).
// Previously GET /giris and GET /kayit also MINTED bindings, which
// effectively removed the protection: an attacker could call
// GET /giris?authRequestID=<X> in their own browser and bind their own
// cookie to X, then send the same link to the victim, have the victim do
// the login, and collect the code in their own browser. Because the binding
// check is the question "is the id bound to my cookie equal to the id I
// want", it PASSED with the cookie the attacker minted themselves. Now the
// binding is given only to the browser that REALLY started the request; the
// GET forms REQUIRE an existing binding, they do not mint one.
//
// The binding is mandatory at FOUR points: GET /giris, GET /kayit,
// POST /giris + POST /kayit (plus CSRF) and /authorize/callback.
const cookieNameSession = "kimlik_oturum"

// sessionTokenBytes is the raw entropy of the session and CSRF values. 32
// bytes (256 bits) is more than enough to be unguessable.
const sessionTokenBytes = 32

// maxSessionBindings is the maximum number of authRequestIDs a SINGLE cookie
// can stay bound to at the same time.
//
// WHY A SET (many ids) IS NEEDED: there is a single cookie name with Path=/,
// so one cookie is held per browser. Since the binding is set at
// /authorize, if the user starts parallel flows in two tabs, the second
// /authorize would OVERWRITE the cookie and the first tab's POST would be
// 403. Keeping the binding as a "session -> {authRequestID}" set preserves
// parallel flows; each leg still verifies EXACT equality ("is this cookie
// bound to this id"), so a cookie has no access to ANOTHER browser's
// request.
//
// WHY NOT "FIRST BINDER WINS": in that patch the attacker becomes the first
// binder with a pre-binding and drops the victim's legitimate login to 403;
// a DoS results instead of a takeover.
//
// WHY 10: unbounded growth would be a bloat surface (calling /authorize
// repeatedly with the same cookie to grow a single Redis value without
// limit). The number of parallel login tabs in human use is single digits;
// 10 is comfortably above that and keeps the record small
// (10 * (~36 byte uuid + 43 character CSRF) < 1 KB). When the limit is
// reached the OLDEST entry drops (FIFO): only that cookie's own oldest tab
// is affected, other browsers are not.
const maxSessionBindings = 10

// Session binding errors. All return to the user with the SAME generic
// message; separating them would tell the attacker which condition did not hold.
var (
	// ErrSessionNotFound: there is no binding recorded for the cookie value
	// (never created, expired, or a made-up cookie).
	ErrSessionNotFound = errors.New("oturum baglamasi bulunamadi")

	// ErrSessionMismatch: the authRequestID bound to the cookie and the
	// authRequestID in the request are not exactly the same.
	ErrSessionMismatch = errors.New("oturum baglamasi istekle eslesmiyor")

	// ErrSessionCookieMissing: there is no session cookie in the request at all.
	ErrSessionCookieMissing = errors.New("oturum cerezi yok")

	// ErrCSRF: the form/request CSRF token does not match the token bound to the cookie.
	ErrCSRF = errors.New("csrf jetonu gecersiz")
)

// SessionEntry holds the binding for a single auth request: the request's
// id and the CSRF token given to that browser for that request. The CSRF
// token is stored on the SERVER next to the cookie; so token verification
// is effectively the question "did we issue this token for this cookie and
// this request".
type SessionEntry struct {
	AuthRequestID string
	CSRF          string
}

// SessionBinding holds which auth requests a browser cookie is bound to.
// The reason it is a set (one-to-many) is parallel flows; see
// maxSessionBindings for the rationale and the upper bound.
//
// The field MUST be exported (capitalized): the record is written to Redis
// as JSON and encoding/json silently skips lowercase fields.
type SessionBinding struct {
	Entries []SessionEntry `json:"Kayitlar"`
}

// CSRFFor returns the CSRF token belonging to the given authRequestID if it
// is BOUND to this cookie. The comparison is exact equality.
func (b SessionBinding) CSRFFor(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	for _, k := range b.Entries {
		if k.AuthRequestID == id {
			return k.CSRF, true
		}
	}
	return "", false
}

// Add adds an authRequestID (and its CSRF token) to the binding. If the same
// id already exists the CSRF token is RENEWED (the entry is not duplicated).
// If the set exceeds maxSessionBindings the OLDEST entry drops.
func (b *SessionBinding) Add(id, csrf string) {
	for i := range b.Entries {
		if b.Entries[i].AuthRequestID == id {
			b.Entries[i].CSRF = csrf
			return
		}
	}
	b.Entries = append(b.Entries, SessionEntry{AuthRequestID: id, CSRF: csrf})
	if excess := len(b.Entries) - maxSessionBindings; excess > 0 {
		b.Entries = b.Entries[excess:]
	}
}

// SessionStore holds the cookie -> SessionBinding mapping. In production
// RequestStore (Redis) implements it; tests run offline with FakeSessionStore.
type SessionStore interface {
	WriteSession(ctx context.Context, session string, binding SessionBinding, ttl time.Duration) error
	ReadSession(ctx context.Context, session string) (SessionBinding, error)
}

// randomToken generates a value of sessionTokenBytes bytes with crypto/rand
// and returns it in a URL-safe form. The error is NOT SWALLOWED: continuing
// with a weak/empty session value effectively removes the binding.
func randomToken() (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rasgele deger uretilemedi: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// csrfEqual compares CSRF values in constant time.
func csrfEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// sessionCookie writes the session value to the browser.
//
// secure (the Secure flag) is determined by the ISSUER: ON for an https
// issuer, OFF for http:// local development. If Secure is on over http the
// browser does not store the cookie at all and the local flow breaks.
//
// SameSite=Lax: since the flow always proceeds with a form POST from the
// same site, Lax is sufficient, and the cookie is still sent on the GET
// redirect coming from /authorize.
func sessionCookie(session string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieNameSession,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   int(requestTTL.Seconds()),
	}
}

// verifySessionBinding reads the cookie in the request, fetches the binding
// from the store and verifies that the expected authRequestID is EXACTLY
// bound to this cookie. The returned CSRF token is for the caller to compare
// with the form/request token.
func verifySessionBinding(
	ctx context.Context,
	store SessionStore,
	r *http.Request,
	wantID string,
) (string, error) {
	cookie, err := r.Cookie(cookieNameSession)
	if err != nil || cookie.Value == "" {
		return "", ErrSessionCookieMissing
	}
	binding, err := store.ReadSession(ctx, cookie.Value)
	if err != nil {
		return "", err
	}
	// Exact equality: the browser that started and the browser that completes
	// must be the same. The set may hold more than one id (parallel tabs) but
	// each id is found only in the cookie that STARTED it.
	csrf, bound := binding.CSRFFor(wantID)
	if !bound {
		return "", ErrSessionMismatch
	}
	return csrf, nil
}
