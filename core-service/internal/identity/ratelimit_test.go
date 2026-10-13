package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// ---- clientIP: resolving the REAL client address (no network needed) ----

// Trusting X-Forwarded-For blindly lets an attacker forge the header and
// bypass the rate limit. The chosen approach: count from the RIGHT by the
// number of trusted proxies (each trusted proxy APPENDS the address it saw).
func TestClientIP_ResolvesByTrustedProxyCount(t *testing.T) {
	statuses := []struct {
		name string
		xff  string
		// xffLines is for MULTIPLE LINES of the SAME header; it is not used together
		// with xff.
		xffLines       []string
		remoteAddr     string
		trustedProxies int
		want           string
	}{
		{
			name: "proxy yok: XFF HIC okunmaz",
			// Without a proxy the header is entirely under the client's control.
			xff:            "1.2.3.4",
			remoteAddr:     "203.0.113.9:51234",
			trustedProxies: 0,
			want:           "203.0.113.9",
		},
		{
			name:           "tek proxy (Traefik): son eleman alinir",
			xff:            "198.51.100.7",
			remoteAddr:     "10.0.0.1:40000",
			trustedProxies: 1,
			want:           "198.51.100.7",
		},
		{
			name: "tek proxy + UYDURMA on ekler: yine son eleman alinir",
			// Whatever the attacker writes on the left, Traefik appends the address it
			// saw at the END; the limit cannot be bypassed.
			xff:            "1.1.1.1, 2.2.2.2, 198.51.100.7",
			remoteAddr:     "10.0.0.1:40000",
			trustedProxies: 1,
			want:           "198.51.100.7",
		},
		{
			name:           "iki proxy: sondan ikinci eleman alinir",
			xff:            "1.1.1.1, 198.51.100.7, 10.0.0.5",
			remoteAddr:     "10.0.0.1:40000",
			trustedProxies: 2,
			want:           "198.51.100.7",
		},
		{
			name: "beklenenden kisa XFF: RemoteAddr'a dusulur",
			// If the chain is shorter than expected, the header is not coming from our
			// proxies; a forged value is not trusted.
			xff:            "198.51.100.7",
			remoteAddr:     "10.0.0.1:40000",
			trustedProxies: 3,
			want:           "10.0.0.1",
		},
		{
			name:           "secilen eleman gecerli IP degil: RemoteAddr'a dusulur",
			xff:            "1.1.1.1, bu-bir-ip-degil",
			remoteAddr:     "10.0.0.1:40000",
			trustedProxies: 1,
			want:           "10.0.0.1",
		},
		{
			name: "IKI AYRI XFF SATIRI: satirlar birlestirilir, en sagdaki alinir",
			// Header.Get reads only the FIRST line; an attacker can add their own line
			// and make the right-to-left count happen on their own value, and the
			// reviewer got "9.9.9.9" this way. RFC 7230 3.2.2: multiple lines are
			// equivalent to ONE comma-joined list.
			xffLines:       []string{"9.9.9.9", "1.1.1.1, 198.51.100.7"},
			remoteAddr:     "10.0.0.1:40000",
			trustedProxies: 1,
			want:           "198.51.100.7",
		},
		{
			name: "IKI AYRI XFF SATIRI + proxy yok: yine RemoteAddr",
			// While trustedProxies == 0 the header is NEVER read.
			xffLines:       []string{"9.9.9.9", "1.1.1.1"},
			remoteAddr:     "203.0.113.9:51234",
			trustedProxies: 0,
			want:           "203.0.113.9",
		},
		{
			name:           "baslik yok: RemoteAddr",
			xff:            "",
			remoteAddr:     "192.0.2.33:1234",
			trustedProxies: 1,
			want:           "192.0.2.33",
		},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, pathLogin, nil)
			r.RemoteAddr = d.remoteAddr
			if d.xff != "" {
				r.Header.Set("X-Forwarded-For", d.xff)
			}
			for _, line := range d.xffLines {
				r.Header.Add("X-Forwarded-For", line)
			}
			if got := clientIP(r, d.trustedProxies); got != d.want {
				t.Errorf("istemciIP = %q, beklenen %q", got, d.want)
			}
		})
	}
}

// ---- Setup: silently falling into "no limit" mode is forbidden ----

func TestNewRateLimiter_RejectsInvalidConfig(t *testing.T) {
	valid := RateLimitConfig{LoginIPPerMin: 1, LoginEmailPerHour: 1, SignupIPPerHour: 1}
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	t.Cleanup(func() { _ = rdb.Close() })

	broken := func(mutate func(*RateLimitConfig)) RateLimitConfig {
		a := valid
		mutate(&a)
		return a
	}
	statuses := []struct {
		name string
		rdb  *redis.Client
		cfg  RateLimitConfig
		key  []byte
	}{
		{"redis yok", nil, valid, []byte("anahtar")},
		{"giris ip siniri 0", rdb, broken(func(a *RateLimitConfig) { a.LoginIPPerMin = 0 }), []byte("anahtar")},
		{"giris eposta siniri 0", rdb, broken(func(a *RateLimitConfig) { a.LoginEmailPerHour = 0 }), []byte("anahtar")},
		{"kayit ip siniri 0", rdb, broken(func(a *RateLimitConfig) { a.SignupIPPerHour = 0 }), []byte("anahtar")},
		{"guvenilen proxy negatif", rdb, broken(func(a *RateLimitConfig) { a.TrustedProxies = -1 }), []byte("anahtar")},
		{"eposta ozet anahtari bos", rdb, valid, nil},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			if _, err := NewRateLimiter(d.rdb, d.cfg, d.key); err == nil {
				t.Error("gecersiz ayarla hiz limiti kuruldu")
			}
		})
	}
}

// ---- FAIL-CLOSED: if Redis is unreachable the request does NOT pass ----

// Rationale: Redis is mandatory for this flow anyway (auth requests + cookie
// binding). Fail-open would give unlimited brute force to anyone who can
// take Redis down. This test needs NO network: a connection to 127.0.0.1:1
// is refused instantly on localhost.
func TestRateLimiter_FailsClosedWithoutRedis(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter, err := NewRateLimiter(rdb, RateLimitConfig{
		LoginIPPerMin: 100, LoginEmailPerHour: 100, SignupIPPerHour: 100,
	}, []byte(testCryptoKey))
	if err != nil {
		t.Fatalf("NewHizLimit: %v", err)
	}

	wasCalled := false
	h := limiter.WrapLogin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { wasCalled = true }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, loginPostRequest("kurban@ornek.com", "198.51.100.7"))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("durum = %d, beklenen 503 (fail-closed)", w.Code)
	}
	if wasCalled {
		t.Error("Redis yokken istek handler'a gecti (fail-open)")
	}
}

// ---- Counter behavior with live Redis ----

func newTestRateLimiter(t *testing.T, cfg RateLimitConfig) (*RateLimiter, *redis.Client) {
	t.Helper()
	rdb := testRedis(t) // Skipped here if there is no Redis
	limiter, err := NewRateLimiter(rdb, cfg, []byte(testCryptoKey))
	if err != nil {
		t.Fatalf("NewHizLimit: %v", err)
	}
	return limiter, rdb
}

// loginPostRequest builds a form request that looks like POST /giris.
func loginPostRequest(email, ip string) *http.Request {
	form := url.Values{"eposta": {email}, "sifre": {"herhangiBirSifre"}, "authRequestID": {"istek-1"}}
	r := httptest.NewRequest(http.MethodPost, pathLogin, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = ip + ":40000"
	return r
}

// countPassed calls the wrapped handler n times and returns how many of them
// REACHED the handler and the last status code.
func countPassed(t *testing.T, h http.Handler, n int, req func(i int) *http.Request) (int, int) {
	t.Helper()
	passed := 0
	lastStatus := 0
	for i := 0; i < n; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req(i))
		lastStatus = w.Code
		if w.Code == http.StatusOK {
			passed++
		}
	}
	return passed, lastStatus
}

func allowHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// POST /giris: per-minute limit per IP. A request ABOVE the limit must get 429;
// before, NONE of 25 wrong-password attempts got 429.
func TestRateLimiter_LoginPerIP(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 3, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
	})
	h := limiter.WrapLogin(allowHandler())

	// A DIFFERENT email on every request: only the IP counter should be measured.
	passed, lastStatus := countPassed(t, h, 5, func(i int) *http.Request {
		return loginPostRequest(string(rune('a'+i))+"@ornek.com", "198.51.100.7")
	})
	if passed != 3 {
		t.Errorf("gecen istek = %d, beklenen 3", passed)
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", lastStatus)
	}
}

// Distributed brute force (a separate IP per request) bypasses the IP
// counter; the per-email counter limits attempts aimed at a single account.
func TestRateLimiter_LoginPerEmail(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 1000, LoginEmailPerHour: 3, SignupIPPerHour: 1000,
	})
	h := limiter.WrapLogin(allowHandler())

	// A DIFFERENT IP on every request: only the email counter should be measured.
	passed, lastStatus := countPassed(t, h, 5, func(i int) *http.Request {
		return loginPostRequest("kurban@ornek.com", "198.51.100."+string(rune('1'+i)))
	})
	if passed != 3 {
		t.Errorf("gecen istek = %d, beklenen 3", passed)
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", lastStatus)
	}
}

// Different emails must NOT share the SAME counter (otherwise one user
// would lock out another).
func TestRateLimiter_LoginEmailCountersIndependent(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 1000, LoginEmailPerHour: 2, SignupIPPerHour: 1000,
	})
	h := limiter.WrapLogin(allowHandler())

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, loginPostRequest("birinci@ornek.com", "198.51.100.7"))
		if w.Code != http.StatusOK {
			t.Fatalf("birinci hesap %d. istekte durum = %d", i+1, w.Code)
		}
	}
	// The first account's counter is full; the second account must NOT BE AFFECTED.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, loginPostRequest("ikinci@ornek.com", "198.51.100.7"))
	if w.Code != http.StatusOK {
		t.Errorf("ikinci hesap durumu = %d, beklenen 200 (sayaclar ayrismali)", w.Code)
	}
}

// POST /kayit: per-hour limit per IP. During the review 15 accounts could be
// created without authentication.
func TestRateLimiter_SignupPerIP(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 1000, LoginEmailPerHour: 1000, SignupIPPerHour: 2,
	})
	h := limiter.WrapSignup(allowHandler())

	passed, lastStatus := countPassed(t, h, 4, func(i int) *http.Request {
		r := loginPostRequest(string(rune('a'+i))+"@ornek.com", "198.51.100.7")
		r.URL.Path = pathSignup
		return r
	})
	if passed != 2 {
		t.Errorf("gecen istek = %d, beklenen 2", passed)
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", lastStatus)
	}
}

// The 429 response must carry Retry-After: the client has to know when to
// retry and the value must never be <= 0.
func TestRateLimiter_429HasRetryAfter(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 1, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
	})
	h := limiter.WrapLogin(allowHandler())
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, loginPostRequest("kurban@ornek.com", "198.51.100.7"))
		if i == 1 {
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("durum = %d, beklenen 429", w.Code)
			}
			if w.Header().Get("Retry-After") == "" {
				t.Error("429 yanitinda Retry-After yok")
			}
		}
	}
}

// Email is PII: it is NOT kept in plain text in the Redis key. This exact
// flaw showed up on this branch before.
func TestRateLimiter_NoPlaintextEmailInRedisKey(t *testing.T) {
	limiter, rdb := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 1000, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
	})
	const email = "gizli.kullanici@ornek.com"
	h := limiter.WrapLogin(allowHandler())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, loginPostRequest(email, "198.51.100.7"))
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}

	keyList, err := rdb.Keys(context.Background(), rateLimitPrefix+"*").Result()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keyList) == 0 {
		t.Fatal("hic hiz limiti anahtari yazilmamis")
	}
	for _, a := range keyList {
		if strings.Contains(strings.ToLower(a), "gizli") ||
			strings.Contains(strings.ToLower(a), "ornek.com") ||
			strings.Contains(strings.ToLower(a), email) {
			t.Errorf("Redis anahtari e-postayi duz metin tasiyor: %q", a)
		}
	}
}

// Is the rate limit really ACTIVE in the PRODUCTION WIRING: the request goes
// through the real mux (buildMux). What this test catches is that the limit is
// wired not only at unit level but in the OP listener too.
func TestBuildMux_LoginRateLimitActive(t *testing.T) {
	o := newTestServerWithLimits(t, RateLimitConfig{
		LoginIPPerMin: 2, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
	})
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")

	verifier, challenge := pkceS256(t)
	_ = verifier
	c := newBrowser(t)
	id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256")))

	// The form is opened ONCE and POSTed three times from the same form: a real
	// browser does not reload the form after a failed attempt either. Also, the
	// GET counter (separate key, see WrapPageGet) must not pollute this measurement.
	csrf := o.openLoginForm(t, c, id)

	// The first two WRONG-password attempts must give 401, the third 429.
	for i := 1; i <= 3; i++ {
		resp := o.submitLogin(t, c, id, csrf, "kurban@ornek.com", "yanlisSifre12")
		switch {
		case i < 3 && resp.StatusCode != http.StatusUnauthorized:
			t.Fatalf("%d. deneme durumu = %d, beklenen 401", i, resp.StatusCode)
		case i == 3 && resp.StatusCode != http.StatusTooManyRequests:
			t.Fatalf("%d. deneme durumu = %d, beklenen 429 (hiz limiti)", i, resp.StatusCode)
		}
	}
}

// I1a: the email counter must be CASE-INSENSITIVE. Account identity is
// case-insensitive in Postgres (UNIQUE (lower(email)), ByEmail lower(email) =
// lower($1)), so "Kurban@x.com" is the SAME account. If the counter key is
// not lowercased, a distributed brute force would try without limit with
// case variants, and the counter's only reason to exist is exactly this
// scenario. Live measurement (limit 2/hour): the same spelling gives 401 401
// 429, three case variants all give 401.
func TestRateLimiter_LoginEmailCaseInsensitive(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 1000, LoginEmailPerHour: 2, SignupIPPerHour: 1000,
	})
	h := limiter.WrapLogin(allowHandler())

	// A DIFFERENT IP on every request: only the email counter should be measured.
	// A DIFFERENT CASING on every request: all of them must land on the SAME counter.
	writes := []string{
		"kurban@ornek.com", "Kurban@ornek.com", "KURBAN@ORNEK.COM",
		"kUrBaN@OrNeK.cOm", "  Kurban@Ornek.Com  ",
	}
	passed, lastStatus := countPassed(t, h, len(writes), func(i int) *http.Request {
		return loginPostRequest(writes[i], "198.51.100."+strconv.Itoa(i+1))
	})
	if passed != 2 {
		t.Errorf("gecen istek = %d, beklenen 2 (harf varyantlari AYNI sayaca dusmeli)", passed)
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", lastStatus)
	}
}

// I1b: the rate limit cannot be bypassed with TWO LINES of the SAME header.
// The clientIP table above measures at unit level; this test measures that
// the counter REALLY lands on the same key.
func TestRateLimiter_MultipleXFFLinesCannotBypass(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 2, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
		TrustedProxies: 1,
	})
	h := limiter.WrapLogin(allowHandler())

	// On EVERY request the attacker writes a different FIRST line; the value the
	// trusted proxy appended is always the same and at the FAR RIGHT.
	passed, lastStatus := countPassed(t, h, 4, func(i int) *http.Request {
		r := loginPostRequest(string(rune('a'+i))+"@ornek.com", "10.0.0.1")
		r.Header.Add("X-Forwarded-For", "9.9.9."+strconv.Itoa(i+1))
		r.Header.Add("X-Forwarded-For", "198.51.100.7")
		return r
	})
	if passed != 2 {
		t.Errorf("gecen istek = %d, beklenen 2 (coklu XFF satiri sayaci atlatmamali)", passed)
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", lastStatus)
	}
}

// Minor: GET /giris and GET /kayit are also subject to the rate limit. Three
// surfaces that are unauthenticated and cheaper than /authorize (which
// requires a valid client_id + redirect_uri + S256).
func TestRateLimiter_PageGetLimited(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 2, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
	})
	for _, name := range []string{"giris", "kayit"} {
		t.Run(name, func(t *testing.T) {
			h := limiter.WrapPageGet(name, allowHandler())
			passed, lastStatus := countPassed(t, h, 4, func(int) *http.Request {
				r := httptest.NewRequest(http.MethodGet, pathLogin+"?authRequestID=istek-1", nil)
				r.RemoteAddr = "198.51.100.7:40000"
				return r
			})
			if passed != 2 {
				t.Errorf("gecen istek = %d, beklenen 2", passed)
			}
			if lastStatus != http.StatusTooManyRequests {
				t.Errorf("son durum = %d, beklenen 429", lastStatus)
			}
		})
	}
}

// The GET counter must be on a key SEPARATE from the POST counter: otherwise
// opening the form would consume the password-attempt budget and a legitimate
// user would lock their own flow.
func TestRateLimiter_PageGetCounterSeparateFromPost(t *testing.T) {
	limiter, _ := newTestRateLimiter(t, RateLimitConfig{
		LoginIPPerMin: 1, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
	})
	const ip = "198.51.100.7"

	// CONSUME the GET budget.
	getH := limiter.WrapPageGet("giris", allowHandler())
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, pathLogin+"?authRequestID=istek-1", nil)
		r.RemoteAddr = ip + ":40000"
		getH.ServeHTTP(w, r)
		if i == 1 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("GET butcesi dolmadi, durum = %d", w.Code)
		}
	}

	// POST must STILL PASS.
	w := httptest.NewRecorder()
	limiter.WrapLogin(allowHandler()).ServeHTTP(w, loginPostRequest("kurban@ornek.com", ip))
	if w.Code != http.StatusOK {
		t.Errorf("POST durumu = %d, beklenen 200 (GET sayaci POST butcesini tuketmemeli)", w.Code)
	}
}

// Is the rate limit also ACTIVE on the GET endpoints in the PRODUCTION WIRING.
func TestBuildMux_GetRateLimitActive(t *testing.T) {
	o := newTestServerWithLimits(t, RateLimitConfig{
		LoginIPPerMin: 2, LoginEmailPerHour: 1000, SignupIPPerHour: 1000,
	})
	o.createTestUser(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)
	c := newBrowser(t)
	id := extractAuthRequestID(t, mustGet(t, c, o.authorizeURL(challenge, "S256")))

	// The first two GETs pass (one is 200), the third is 429.
	address := o.issuer + pathLogin + "?authRequestID=" + url.QueryEscape(id)
	for i := 1; i <= 3; i++ {
		resp := mustGet(t, c, address)
		switch {
		case i < 3 && resp.StatusCode != http.StatusOK:
			t.Fatalf("%d. GET durumu = %d, beklenen 200", i, resp.StatusCode)
		case i == 3 && resp.StatusCode != http.StatusTooManyRequests:
			t.Fatalf("%d. GET durumu = %d, beklenen 429 (hiz limiti)", i, resp.StatusCode)
		}
	}
}
