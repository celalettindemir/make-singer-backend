package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/clientip"
	"github.com/redis/go-redis/v9"
)

// WHY THIS FILE EXISTS
//
// The OP listener is a second net/http server and is COMPLETELY SEPARATE
// from the main API's Fiber middleware chain (and therefore from
// internal/middleware/ratelimit.go). So POST /giris and POST /kayit were
// endpoints that REQUIRED NO authentication and had NO rate limit AT ALL.
// Three impacts measured live during the review:
//
//  1. Unlimited password brute-force: none of 25 wrong attempts got a 429.
//  2. CPU DoS: with bcrypt cost 12, ~258 ms of CPU per request. Since the OP
//     and the main API run in the SAME binary, a few parallel connections
//     would exhaust the pod's CPU and also take down the /api/* endpoints.
//  3. Unlimited account creation: email verification and CAPTCHA are absent
//     in the MVP.
//
// This file closes all three with a Redis-backed fixed-window counter. The
// counter pattern is THE SAME as in internal/middleware/ratelimit.go
// (SET key 0 EX window NX + INCR, inside a SINGLE TxPipeline): the TTL is
// pinned at creation time, so the "permanent lock caused by a key left
// without a TTL" trap is avoided.

// rateLimitPrefix is this package's Redis key prefix. It is SEPARATE from the
// "ratelimit:" prefix on the API side: the counters there are tied to user
// identity, these are tied to an unauthenticated endpoint (IP / email
// digest), and the two key spaces must not mix.
const rateLimitPrefix = "kimlik:hizlimit"

// rateLimitRedisTimeout is the upper bound for counter operations. If Redis
// is slow the login endpoint must not hang; when it elapses FAIL-CLOSED
// applies (503).
const rateLimitRedisTimeout = 2 * time.Second

// msgRateLimited is the text returned in the 429 body. Which counter (IP or
// email) filled up is NOT SAID: the email counter filling up would confirm
// "this email is being tried".
const msgRateLimited = "Cok fazla deneme yapildi, lutfen biraz sonra tekrar deneyin."

// msgRateLimitUnavailable is the text returned when Redis cannot be reached.
//
// Rationale for FAIL-CLOSED: Redis is ALREADY mandatory for this flow --
// auth requests (RequestStore) and the cookie/session binding
// (SessionStore) are kept in Redis. With Redis down the login flow cannot
// be completed IN ANY WAY, so rejecting the request here introduces no new
// dependency; it only reports the existing failure with the right status
// code. Fail-open would hand unlimited brute-force and unlimited account
// creation to anyone who can take Redis down.
const msgRateLimitUnavailable = "Istek su an denetlenemiyor, lutfen biraz sonra tekrar deneyin."

// RateLimitConfig holds the rate-limit values on the OP listener. The values
// come from config (AuthConfig); only the windows are fixed here.
type RateLimitConfig struct {
	// LoginIPPerMin is the per-minute upper bound per IP for POST /giris.
	LoginIPPerMin int

	// LoginEmailPerHour is the per-hour upper bound per email for POST /giris.
	// The IP counter does not stop a distributed brute-force (a different IP
	// per request); this counter limits attempts aimed at a single account.
	LoginEmailPerHour int

	// SignupIPPerHour is the per-hour upper bound per IP for POST /kayit.
	SignupIPPerHour int

	// TrustedProxies is the number of TRUSTED reverse proxies a request passes
	// through before reaching us (Traefik => 1 in this setup). See clientIP.
	TrustedProxies int

	// Cloudflare lists the Cloudflare edge ranges. When the address chosen by
	// clientIP is inside them, CF-Connecting-IP is used as the client
	// address. Empty disables it.
	Cloudflare clientip.CIDRs
}

// RateLimiter is the Redis-backed fixed-window rate limit for the OP
// listener's login/signup endpoints.
type RateLimiter struct {
	rdb *redis.Client
	cfg RateLimitConfig

	// emailHMACKey is the HMAC key used to produce the email digest (in
	// production AUTH_CRYPTO_KEY). The email is NOT kept as PLAIN TEXT in the
	// Redis key: the email is PII, and someone who can read Redis would
	// otherwise obtain the list "which accounts were tried in the last hour"
	// in plain text. Plain SHA-256 is not enough either (a list of known emails
	// can be tried directly); a keyed HMAC makes the digests unlinkable for
	// someone who does not know the key.
	emailHMACKey []byte
}

// NewRateLimiter sets up the rate limit. It returns an error if rdb is nil
// or the config values are invalid: silently falling into "unlimited" mode
// is exactly the flaw we are trying to close.
func NewRateLimiter(rdb *redis.Client, cfg RateLimitConfig, emailKey []byte) (*RateLimiter, error) {
	if rdb == nil {
		return nil, errors.New("rate limit: no redis client")
	}
	if cfg.LoginIPPerMin <= 0 {
		return nil, fmt.Errorf("rate limit: login_ip_per_min must be positive, got: %d", cfg.LoginIPPerMin)
	}
	if cfg.LoginEmailPerHour <= 0 {
		return nil, fmt.Errorf("rate limit: login_email_per_hour must be positive, got: %d", cfg.LoginEmailPerHour)
	}
	if cfg.SignupIPPerHour <= 0 {
		return nil, fmt.Errorf("rate limit: signup_ip_per_hour must be positive, got: %d", cfg.SignupIPPerHour)
	}
	if cfg.TrustedProxies < 0 {
		return nil, fmt.Errorf("rate limit: trusted_proxy must not be negative, got: %d", cfg.TrustedProxies)
	}
	if len(emailKey) == 0 {
		return nil, errors.New("rate limit: email digest key is empty")
	}
	return &RateLimiter{rdb: rdb, cfg: cfg, emailHMACKey: emailKey}, nil
}

// counter defines a single rate-limit counter.
type counter struct {
	key    string
	limit  int
	window time.Duration
}

// WrapLogin wraps the POST /giris handler with the rate limit: first the IP
// counter, then the email counter. The order matters: the IP counter runs
// before the form body is read, so it rejects on the cheapest path without
// parsing the body at all.
func (h *RateLimiter) WrapLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allow(w, r, counter{
			key:    h.ipKey("giris", r),
			limit:  h.cfg.LoginIPPerMin,
			window: time.Minute,
		}) {
			return
		}
		// The form is parsed here; even if the handler below calls ParseForm again,
		// net/http makes the second call a no-op (r.PostForm is already filled), so
		// the body is not read twice.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "form okunamadi", http.StatusBadRequest)
			return
		}
		// emailCounterInput: LOWERCASING IS REQUIRED, see the function note.
		email := emailCounterInput(r.PostFormValue("eposta"))
		if email == "" {
			// A POST without an email cannot be a login anyway; the IP counter already
			// counted it, there is no need to pollute the email counter.
			next.ServeHTTP(w, r)
			return
		}
		if !h.allow(w, r, counter{
			key:    h.emailKey("giris", email),
			limit:  h.cfg.LoginEmailPerHour,
			window: time.Hour,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WrapPageGet wraps GET /giris and GET /kayit with a per-minute counter per
// IP. name is the endpoint name in the counter key ("giris" / "kayit").
//
// WHY A SEPARATE KEY FROM THE POST COUNTER: if GET and POST shared the same
// key, opening the form would consume the password-guessing budget and a
// legitimate user would lock out their own flow. The meaning of 429 would
// also get muddled.
//
// WHY THE SAME LIMIT VALUE (LoginIPPerMin): GET is a cheap endpoint
// (template rendering; no bcrypt, the user/token store is not touched), but
// it is unauthenticated and a cheaper surface than /authorize. Rendering
// login_ip_per_min pages per minute is far above manual use and clearly
// below automated scanning; the difference does not justify adding a
// separate configuration key.
func (h *RateLimiter) WrapPageGet(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allow(w, r, counter{
			key:    h.ipKey(name+"-get", r),
			limit:  h.cfg.LoginIPPerMin,
			window: time.Minute,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WrapLink wraps the POST /baglama handler with a per-minute counter per IP.
//
// WHY A SEPARATE COUNTER: /baglama is the SECOND password-guessing surface
// in this system (the first is POST /giris). If it shared the login counter,
// a federated user would consume their own login budget with link attempts;
// worse, an attacker could choose which endpoint to try from and use a
// single budget twice as efficiently. The limit value is THE SAME as login
// (LoginIPPerMin): both endpoints run bcrypt, so the cost and threat
// profile are the same; the difference does not justify adding a separate
// configuration key.
//
// The PER-ACCOUNT counter is NOT HERE but in the handler: which account is
// being tried is known only after the pending record is READ, and reading
// that record a second time in middleware would break the single-use read
// discipline. See AllowAccount and linkPost.
func (h *RateLimiter) WrapLink(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allow(w, r, counter{
			key:    h.ipKey("baglama", r),
			limit:  h.cfg.LoginIPPerMin,
			window: time.Minute,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AllowAccount limits password attempts aimed at a SINGLE account per hour
// and says whether the request may proceed; if it returned false the
// response HAS ALREADY BEEN written (429 or 503).
//
// WHY IT IS NEEDED: the per-IP counter does not stop a distributed attempt
// that makes every request from a different IP -- which is why POST /giris
// has a second counter per email (see WrapLogin). /baglama is open to the
// same threat and has one more path there: even though the pending record
// is single-use, a NEW record can be requested as many times as desired by
// repeating the federation round. So the record's uniqueness does not limit
// the number of attempts; this counter does.
//
// WHY NOT A WRAPPER: which account is being tried cannot be known without
// reading the pending record. Reading the record in middleware would mean
// either a second read or moving the single-use read.
//
// accountID (the user ID) is NOT WRITTEN as PLAIN TEXT into the Redis key:
// the SAME rationale and the SAME HMAC path as the email counter are used
// (see emailKey).
func (h *RateLimiter) AllowAccount(w http.ResponseWriter, r *http.Request, accountID string) bool {
	if accountID == "" {
		// An anonymous attempt does not name an account to count; the IP counter
		// already counted it.
		return true
	}
	return h.allow(w, r, counter{
		key:    h.emailKey("baglama-hesap", accountID),
		limit:  h.cfg.LoginEmailPerHour,
		window: time.Hour,
	})
}

// WrapFederationStart wraps the GET /federasyon/{saglayici}/baslat endpoint
// with a per-minute counter per IP.
//
// WHY A SEPARATE COUNTER: the endpoint is unauthenticated and every
// accepted request WRITES a state record to Redis; leaving it unlimited
// would be a cheap way to fill Redis. The counter is kept under a key
// SEPARATE from the LOGIN, SIGNUP and LINK endpoints: if federation attempts
// consumed the password-guessing budget, a legitimate user would lock out
// their own login flow.
//
// WHY NOT WrapPageGet: that wrapper appends "-get" to the end of the key and
// is for GETs in the "page rendering" class; this endpoint renders no page,
// it writes to Redis and returns a redirect. A separate name makes what the
// counter measures readable in the key too and follows the same pattern as
// WrapFederationCallback.
//
// The limit value is THE SAME as LoginIPPerMin: both are far above manual
// use and clearly below automated scanning; the difference does not
// justify adding a separate configuration key.
func (h *RateLimiter) WrapFederationStart(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allow(w, r, counter{
			key:    h.ipKey("federasyon-baslat", r),
			limit:  h.cfg.LoginIPPerMin,
			window: time.Minute,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WrapFederationCallback wraps the federation return endpoint with a
// per-minute counter per IP. The endpoint is unauthenticated and every
// request comes with a Redis read (state) and -- if the state is valid --
// triggers an HTTP call to upstream; leaving it unlimited would be a way of
// putting load on an external service on our behalf.
//
// The counter is kept under a key SEPARATE from the LOGIN and LINK
// endpoints: legitimate federation returns must not consume the
// password-guessing budget.
func (h *RateLimiter) WrapFederationCallback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allow(w, r, counter{
			key:    h.ipKey("federasyon-geri", r),
			limit:  h.cfg.LoginIPPerMin,
			window: time.Minute,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WrapSignup wraps the POST /kayit handler with a per-hour counter per IP.
// There is NO per-email counter here: at signup the email does not yet
// belong to an account, the counter would only measure "retrying the same
// email" and the 409 already reports that. What limits abuse is the RATE of
// account creation, i.e. the IP counter.
func (h *RateLimiter) WrapSignup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allow(w, r, counter{
			key:    h.ipKey("kayit", r),
			limit:  h.cfg.SignupIPPerHour,
			window: time.Hour,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allow increments the counter and says whether the request may proceed.
// If it returned false the response HAS ALREADY been written (429 or 503).
func (h *RateLimiter) allow(w http.ResponseWriter, r *http.Request, s counter) bool {
	ctx, cancel := context.WithTimeout(r.Context(), rateLimitRedisTimeout)
	defer cancel()

	count, err := h.incr(ctx, s)
	if err != nil {
		// FAIL-CLOSED. The error text is logged but the connection string/key
		// content is not written: the key carries the email digest and the error
		// text may contain the Redis address (which may carry a secret), so only a
		// fixed message is printed.
		log.Print("rate limit check failed, request rejected (fail-closed)")
		w.Header().Set("Retry-After", "5")
		http.Error(w, msgRateLimitUnavailable, http.StatusServiceUnavailable)
		return false
	}
	if count > int64(s.limit) {
		w.Header().Set("Retry-After", strconv.Itoa(h.retryAfterSeconds(s)))
		http.Error(w, msgRateLimited, http.StatusTooManyRequests)
		return false
	}
	return true
}

// incr creates and increments the fixed-window counter inside a SINGLE
// TxPipeline. The SET ... NX + INCR order creates the key only when it does
// not exist and pins the TTL at creation time.
func (h *RateLimiter) incr(ctx context.Context, s counter) (int64, error) {
	pipe := h.rdb.TxPipeline()
	pipe.SetArgs(ctx, s.key, 0, redis.SetArgs{Mode: "NX", TTL: s.window})
	incrCmd := pipe.Incr(ctx, s.key)
	// redis.Nil = SET NX was not applied (the key already existed); normal flow.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	return incrCmd.Result()
}

// retryAfterSeconds produces the Retry-After value. At least 1: if the TTL
// cannot be read or the key somehow ended up without a TTL, a negative/zero
// value must not be returned.
func (h *RateLimiter) retryAfterSeconds(s counter) int {
	wait := s.window
	ctx, cancel := context.WithTimeout(context.Background(), rateLimitRedisTimeout)
	defer cancel()
	if ttl, err := h.rdb.TTL(ctx, s.key).Result(); err == nil && ttl > 0 {
		wait = ttl
	}
	seconds := int(wait.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

func (h *RateLimiter) ipKey(name string, r *http.Request) string {
	return fmt.Sprintf("%s:%s:ip:%s", rateLimitPrefix, name, clientIP(r, h.cfg.TrustedProxies, h.cfg.Cloudflare))
}

// emailCounterInput produces the HMAC input of the rate-limit counter.
//
// WHY LOWERCASING: the account identity is CASE-INSENSITIVE in Postgres
// (migrations/001_kullanici.sql: UNIQUE (lower(email)) and ByEmail
// "WHERE lower(email) = lower($1)"). The counter key, however, used to be
// derived from normalizeEmail (TrimSpace only, see user_store.go), so
// "Kurban@x.com" meant the SAME account but a DIFFERENT key: a distributed
// brute-force could try without limit using letter-case variants, and the
// counter's only reason to exist was exactly this scenario. Measured live
// (limit 2/hour): the same spelling gave 401 401 429, while three
// letter-case variants all gave 401.
//
// The STORED email does NOT CHANGE: lowercasing is applied only to the
// counter key, the form the user typed is preserved for display (see the
// normalizeEmail note). The fake store already applies the same pattern
// (user_store_fake.go fakeEmailKey).
func emailCounterInput(email string) string {
	return strings.ToLower(normalizeEmail(email))
}

// emailKey produces a key from the HMAC-SHA256 digest of the email. The
// email is NOT written to Redis as PLAIN TEXT (see RateLimiter.emailHMACKey).
// The digest is truncated to 128 bits: the collision probability is
// negligible and the key stays short.
func (h *RateLimiter) emailKey(name, email string) string {
	mac := hmac.New(sha256.New, h.emailHMACKey)
	mac.Write([]byte(email))
	return fmt.Sprintf("%s:%s:eposta:%s", rateLimitPrefix, name, hex.EncodeToString(mac.Sum(nil)[:16]))
}

// clientIP resolves the "real" client address that the rate-limit counter
// will be tied to.
//
// WHY X-Forwarded-For IS NOT TRUSTED AS IS: the client can write the header
// too. Taking the left-most (first) value lets an attacker bypass the
// counter entirely by writing a different made-up IP on every request.
//
// THE CHOSEN WAY: trustedProxy is the number of TRUSTED reverse proxies a
// request passes through before reaching us (Traefik => 1 in this setup).
// Each trusted proxy APPENDS the address it SAW to X-Forwarded-For, so the
// trustedProxy-th element from the END of the list is the address seen by
// the innermost trusted proxy and cannot be forged by the client: whatever
// the attacker writes on the left, Traefik appends the address it saw at the
// end and we read that.
//
// If trustedProxy == 0 (a setup without a proxy) X-Forwarded-For is NEVER
// read, RemoteAddr is used directly.
//
// If the chosen element is not a valid IP we again fall back to RemoteAddr:
// a made-up/garbled value must not pollute the Redis key space.
//
// BEHIND CLOUDFLARE: the address chosen above is then the Cloudflare edge
// (Traefik reports its TCP peer), which many visitors share. So the chosen
// address is passed through clientip.Resolve: only if it is inside the
// configured Cloudflare ranges is the CF-Connecting-IP header used instead.
// Traefik does not strip that header, so for any other address it is
// forgeable and is ignored.
//
// LIMIT: for someone who can reach the pod DIRECTLY, BYPASSING Traefik,
// X-Forwarded-For is entirely under their own control. In this setup the OP
// listener (8001) is a cluster-internal service and is exposed to the
// outside only through Traefik; for an attacker with direct access
// RemoteAddr is their own address anyway, so this is not a regression.
func clientIP(r *http.Request, trustedProxy int, cloudflare clientip.CIDRs) string {
	return clientip.Resolve(peerIP(r, trustedProxy), r.Header.Get("CF-Connecting-IP"), cloudflare)
}

// peerIP is the address clientIP selects before the Cloudflare step; see the
// clientIP comment for the X-Forwarded-For rules.
func peerIP(r *http.Request, trustedProxy int) string {
	if trustedProxy > 0 {
		// ALL X-Forwarded-For LINES are joined. Header.Get reads only the FIRST
		// line; when a client sends two separate XFF lines (HTTP allows this and
		// net/http keeps both) counting from the right would be done on the
		// ATTACKER's line and the reviewer obtained "9.9.9.9" from clientIP this
		// way. Per RFC 7230 3.2.2, multiple lines of the same header are equivalent
		// to a SINGLE comma-joined list; the value the trusted proxy appended thus
		// still stays at the FAR RIGHT.
		if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
			parts := strings.Split(xff, ",")
			if i := len(parts) - trustedProxy; i >= 0 {
				candidate := strings.TrimSpace(parts[i])
				if net.ParseIP(candidate) != nil {
					return candidate
				}
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
