package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/clientip"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/redis/go-redis/v9"
)

const testWebKey = "web-anahtari-en-az-otuz-iki-karakter-uzun"

// ipApp attaches LimitIP to an unauthenticated endpoint.
func ipApp(rdb *redis.Client, perIP int, webKey string, webMax int) *fiber.App {
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb, nil).LimitIP("onizleme", perIP, time.Minute, webKey, webMax, 0),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	return app
}

// ipRequest sends the request with the given headers; returns the status and the error code.
func ipRequest(t *testing.T, app *fiber.App, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/uc", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body.Error.Code
}

func TestLimitIP_FailsClosedWithoutRedis(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	status, _ := ipRequest(t, ipApp(rdb, 5, "", 0), nil)
	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (an unauthenticated endpoint must not give 401 and must not pass when Redis is down)", status)
	}
}

func TestLiveLimitIP_CountsPerIP(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	app := ipApp(rdb, 2, "", 0)
	a := map[string]string{"X-Real-Ip": "203.0.113.5"}

	for i := 1; i <= 2; i++ {
		if status, _ := ipRequest(t, app, a); status != fiber.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, status)
		}
	}
	if status, code := ipRequest(t, app, a); status != fiber.StatusTooManyRequests || code != "RATE_LIMITED" {
		t.Fatalf("3rd request: status = %d code = %q, want 429 RATE_LIMITED", status, code)
	}
	if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": "203.0.113.6"}); status != fiber.StatusOK {
		t.Fatalf("another IP must not be affected: status = %d", status)
	}
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:ip:203.0.113.5").Int(); err != nil || n != 3 {
		t.Fatalf("IP key = %d (err %v), want 3", n, err)
	}
	if ttl, err := rdb.TTL(ctx, "ratelimit:onizleme:ip:203.0.113.5").Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("TTL = %v (err %v)", ttl, err)
	}
}

func TestLiveLimitIP_InvalidHeaderFallsBackToConnIP(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	app := ipApp(rdb, 5, "", 0)
	if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": "1.2.3.4:80 kotu"}); status != fiber.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if status, _ := ipRequest(t, app, nil); status != fiber.StatusOK {
		t.Fatalf("status = %d", status)
	}
	// app.Test's connection address is 0.0.0.0; both requests are counted on it.
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:ip:0.0.0.0").Int(); err != nil || n != 2 {
		t.Fatalf("connection address key = %d (err %v), want 2", n, err)
	}
	if keys, _ := rdb.Keys(ctx, "ratelimit:onizleme:ip:1.2.3.4*").Result(); len(keys) != 0 {
		t.Fatalf("an invalid header must not produce a key: %v", keys)
	}
}

func TestLiveLimitIP_WebKeySharedLimit(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	app := ipApp(rdb, 1, testWebKey, 3)

	// Correct key: independent of the IP, 3 allowances on the shared "web" counter.
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.1"} {
		status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": ip, WebKeyHeader: testWebKey})
		if status != fiber.StatusOK {
			t.Fatalf("web request %d: status = %d, want 200", i+1, status)
		}
	}
	if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": "198.51.100.3", WebKeyHeader: testWebKey}); status != fiber.StatusTooManyRequests {
		t.Fatalf("429 is expected once the web limit is exceeded: %d", status)
	}
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:web").Int(); err != nil || n != 4 {
		t.Fatalf("web key = %d (err %v), want 4", n, err)
	}
	if keys, _ := rdb.Keys(ctx, "ratelimit:onizleme:ip:*").Result(); len(keys) != 0 {
		t.Fatalf("a correct web key must not touch the IP counter: %v", keys)
	}

	// Wrong key: the normal IP limit (1).
	wrong := map[string]string{"X-Real-Ip": "198.51.100.9", WebKeyHeader: testWebKey + "x"}
	if status, _ := ipRequest(t, app, wrong); status != fiber.StatusOK {
		t.Fatalf("wrong key first request: %d", status)
	}
	if status, _ := ipRequest(t, app, wrong); status != fiber.StatusTooManyRequests {
		t.Fatalf("a wrong key must hit the IP limit: %d", status)
	}
}

func TestLiveLimitIP_EmptyConfigDisablesWebPath(t *testing.T) {
	rdb := liveRedis(t)
	app := ipApp(rdb, 1, "", 100)
	// With an empty configuration, both an empty header and any header fall under the IP limit.
	for _, key := range []string{"", "herhangi"} {
		b := map[string]string{"X-Real-Ip": "192.0.2.10", WebKeyHeader: key}
		_, _ = ipRequest(t, app, b)
	}
	if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": "192.0.2.10", WebKeyHeader: ""}); status != fiber.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", status)
	}
	if n, _ := rdb.Exists(context.Background(), "ratelimit:onizleme:web").Result(); n != 0 {
		t.Fatal("no web counter may be created when the web key is empty")
	}
}

func TestLiveLimitWithCode_429WithCustomCode(t *testing.T) {
	rdb := liveRedis(t)
	app := fiber.New()
	app.Get("/uc", func(c *fiber.Ctx) error {
		c.Locals("userId", "kullanici-yorum")
		return c.Next()
	}, NewRateLimiter(rdb, nil).LimitWithCode("yorum", 1, time.Minute, "yavasla", i18n.KeySlowDown),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })

	if status, _ := ipRequest(t, app, nil); status != fiber.StatusOK {
		t.Fatalf("first request: %d", status)
	}
	if status, code := ipRequest(t, app, nil); status != fiber.StatusTooManyRequests || code != "yavasla" {
		t.Fatalf("status = %d code = %q, want 429 yavasla", status, code)
	}
	if n, err := rdb.Get(context.Background(), "ratelimit:yorum:kullanici-yorum").Int(); err != nil || n != 2 {
		t.Fatalf("user key = %d (err %v)", n, err)
	}
}

func TestLimitWithCode_UnauthenticatedIs401(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb, nil).LimitWithCode("yorum", 1, time.Minute, "yavasla", i18n.KeySlowDown),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	if status, _ := ipRequest(t, app, nil); status != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestIPKey_IPv6Upper64AndIPv4Mapped(t *testing.T) {
	cases := map[string]string{
		"203.0.113.5":                    "203.0.113.5",
		"::ffff:203.0.113.5":             "203.0.113.5",
		"2001:db8:1:2:aaaa:bbbb:cccc:dd": "2001:db8:1:2::",
		"2001:db8:1:2::1":                "2001:db8:1:2::",
		"2001:db8:1:3::1":                "2001:db8:1:3::",
		"gecersiz":                       "gecersiz",
	}
	for raw, want := range cases {
		if got := ipKey(raw); got != want {
			t.Errorf("ipKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Two IPv6 addresses in the same /64 share the same counter; a neighbouring /64 is separate.
func TestLiveLimitIP_IPv6Same64SharesCounter(t *testing.T) {
	rdb := liveRedis(t)
	app := ipApp(rdb, 2, "", 0)
	for i, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2:ffff::9"} {
		if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": ip}); status != fiber.StatusOK {
			t.Fatalf("request %d: status = %d", i+1, status)
		}
	}
	if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": "2001:db8:1:2::abcd"}); status != fiber.StatusTooManyRequests {
		t.Fatalf("the 3rd request in the same /64 must be 429: %d", status)
	}
	if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": "2001:db8:1:3::1"}); status != fiber.StatusOK {
		t.Fatalf("a neighboring /64 must not be affected: %d", status)
	}
	if n, err := rdb.Get(context.Background(), "ratelimit:onizleme:ip:2001:db8:1:2::").Int(); err != nil || n != 3 {
		t.Fatalf("/64 key = %d (err %v), want 3", n, err)
	}
}

// An IPv4-mapped IPv6 address is on the same counter as its IPv4 form.
func TestLiveLimitIP_IPv4MappedSharesCounter(t *testing.T) {
	rdb := liveRedis(t)
	app := ipApp(rdb, 5, "", 0)
	for _, ip := range []string{"203.0.113.7", "::ffff:203.0.113.7"} {
		if status, _ := ipRequest(t, app, map[string]string{"X-Real-Ip": ip}); status != fiber.StatusOK {
			t.Fatalf("%s: status = %d", ip, status)
		}
	}
	if n, err := rdb.Get(context.Background(), "ratelimit:onizleme:ip:203.0.113.7").Int(); err != nil || n != 2 {
		t.Fatalf("IPv4 key = %d (err %v), want 2", n, err)
	}
}

func cloudflareApp(t *testing.T, rdb *redis.Client) *fiber.App {
	t.Helper()
	cf, err := clientip.ParseCIDRs(clientip.DefaultCloudflareRanges)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb, cf).LimitIP("cftest", 100, time.Minute, "", 0, 0),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	return app
}

func TestLiveLimitIP_CloudflareHeaderKeysOnVisitor(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	rdb.Del(ctx, "ratelimit:cftest:ip:198.51.100.77", "ratelimit:cftest:ip:162.159.113.105", "ratelimit:cftest:ip:203.0.113.9")
	t.Cleanup(func() {
		rdb.Del(ctx, "ratelimit:cftest:ip:198.51.100.77", "ratelimit:cftest:ip:162.159.113.105", "ratelimit:cftest:ip:203.0.113.9")
	})
	app := cloudflareApp(t, rdb)

	// Peer in a Cloudflare range: the visitor from CF-Connecting-IP is counted.
	ipRequest(t, app, map[string]string{"X-Real-Ip": "162.159.113.105", "CF-Connecting-IP": "198.51.100.77"})
	if n, _ := rdb.Get(ctx, "ratelimit:cftest:ip:198.51.100.77").Int(); n != 1 {
		t.Errorf("visitor counter = %d, want 1", n)
	}
	if rdb.Exists(ctx, "ratelimit:cftest:ip:162.159.113.105").Val() != 0 {
		t.Error("the Cloudflare edge address must not be counted")
	}

	// Peer outside Cloudflare: a forged CF-Connecting-IP is ignored.
	ipRequest(t, app, map[string]string{"X-Real-Ip": "203.0.113.9", "CF-Connecting-IP": "198.51.100.77"})
	if n, _ := rdb.Get(ctx, "ratelimit:cftest:ip:198.51.100.77").Int(); n != 1 {
		t.Errorf("forged header was honoured: visitor counter = %d, want 1", n)
	}
	if n, _ := rdb.Get(ctx, "ratelimit:cftest:ip:203.0.113.9").Int(); n != 1 {
		t.Errorf("peer counter = %d, want 1", n)
	}
}

// visitorApp attaches LimitIP with a per-visitor web limit.
func visitorApp(rdb *redis.Client, perIP int, webMax, visitorMax int) *fiber.App {
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb, nil).LimitIP("ziyaretci", perIP, time.Minute, testWebKey, webMax, visitorMax),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	return app
}

func TestLiveLimitIP_WebKeyWithVisitorHasOwnBucket(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	app := visitorApp(rdb, 1, 100, 2)
	a := map[string]string{WebKeyHeader: testWebKey, VisitorIPHeader: "203.0.113.50"}
	b := map[string]string{WebKeyHeader: testWebKey, VisitorIPHeader: "203.0.113.51"}
	for i := 0; i < 2; i++ {
		if status, _ := ipRequest(t, app, a); status != fiber.StatusOK {
			t.Fatalf("visitor A request %d: %d, want 200", i+1, status)
		}
	}
	if status, _ := ipRequest(t, app, a); status != fiber.StatusTooManyRequests {
		t.Fatalf("visitor A third request: %d, want 429", status)
	}
	if status, _ := ipRequest(t, app, b); status != fiber.StatusOK {
		t.Fatalf("another visitor must still pass: %d", status)
	}
	if n, err := rdb.Get(ctx, "ratelimit:ziyaretci:web:ip:203.0.113.50").Int(); err != nil || n != 3 {
		t.Fatalf("visitor bucket = %d (err %v), want 3", n, err)
	}
	// The visitor bucket is checked first, so the rejected request never reached the shared bucket.
	if n, err := rdb.Get(ctx, "ratelimit:ziyaretci:web").Int(); err != nil || n != 3 {
		t.Fatalf("shared bucket = %d (err %v), want 3 (2 from A + 1 from B)", n, err)
	}
}

func TestLiveLimitIP_WebKeySharedBucketStillApplies(t *testing.T) {
	rdb := liveRedis(t)
	app := visitorApp(rdb, 1, 2, 10)
	for i, ip := range []string{"203.0.113.60", "203.0.113.61"} {
		h := map[string]string{WebKeyHeader: testWebKey, VisitorIPHeader: ip}
		if status, _ := ipRequest(t, app, h); status != fiber.StatusOK {
			t.Fatalf("request %d: %d, want 200", i+1, status)
		}
	}
	h := map[string]string{WebKeyHeader: testWebKey, VisitorIPHeader: "203.0.113.62"}
	if status, _ := ipRequest(t, app, h); status != fiber.StatusTooManyRequests {
		t.Fatalf("shared bucket must also limit: %d", status)
	}
}

func TestLiveLimitIP_WebKeyWithoutVisitorUsesSharedOnly(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	app := visitorApp(rdb, 1, 3, 1)
	for _, h := range []map[string]string{
		{WebKeyHeader: testWebKey},
		{WebKeyHeader: testWebKey, VisitorIPHeader: "not-an-ip"},
		{WebKeyHeader: testWebKey, VisitorIPHeader: ""},
	} {
		if status, _ := ipRequest(t, app, h); status != fiber.StatusOK {
			t.Fatalf("headers %v: %d, want 200", h, status)
		}
	}
	if status, _ := ipRequest(t, app, map[string]string{WebKeyHeader: testWebKey}); status != fiber.StatusTooManyRequests {
		t.Fatalf("shared bucket must trip: %d", status)
	}
	if keys, _ := rdb.Keys(ctx, "ratelimit:ziyaretci:web:ip:*").Result(); len(keys) != 0 {
		t.Fatalf("no visitor bucket expected: %v", keys)
	}
}

func TestLiveLimitIP_VisitorBucketDisabledWhenZero(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	app := visitorApp(rdb, 1, 100, 0)
	h := map[string]string{WebKeyHeader: testWebKey, VisitorIPHeader: "203.0.113.70"}
	for i := 0; i < 5; i++ {
		if status, _ := ipRequest(t, app, h); status != fiber.StatusOK {
			t.Fatalf("request %d: %d, want 200", i+1, status)
		}
	}
	if keys, _ := rdb.Keys(ctx, "ratelimit:ziyaretci:web:ip:*").Result(); len(keys) != 0 {
		t.Fatalf("disabled visitor bucket must not be written: %v", keys)
	}
}

func TestLiveLimitIP_VisitorHeaderIgnoredWithoutKey(t *testing.T) {
	rdb := liveRedis(t)
	ctx := context.Background()
	app := visitorApp(rdb, 1, 100, 100)
	// Same connection IP, different forged visitor values: all share the per-IP bucket.
	first := map[string]string{"X-Real-Ip": "198.51.100.80", VisitorIPHeader: "203.0.113.80"}
	second := map[string]string{"X-Real-Ip": "198.51.100.80", VisitorIPHeader: "203.0.113.81"}
	if status, _ := ipRequest(t, app, first); status != fiber.StatusOK {
		t.Fatalf("first: %d", status)
	}
	if status, _ := ipRequest(t, app, second); status != fiber.StatusTooManyRequests {
		t.Fatalf("a forged visitor header must not escape the IP limit: %d", status)
	}
	wrong := map[string]string{"X-Real-Ip": "198.51.100.81", VisitorIPHeader: "203.0.113.82", WebKeyHeader: testWebKey + "x"}
	ipRequest(t, app, wrong)
	if keys, _ := rdb.Keys(ctx, "ratelimit:ziyaretci:web*").Result(); len(keys) != 0 {
		t.Fatalf("without a valid key no web bucket may be touched: %v", keys)
	}
}
