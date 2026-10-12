package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

const testWebAnahtari = "web-anahtari-en-az-otuz-iki-karakter-uzun"

// ipUygulamasi, kimliksiz bir uca LimitIP baglar.
func ipUygulamasi(rdb *redis.Client, ipBasina int, webAnahtari string, webMax int) *fiber.App {
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb).LimitIP("onizleme", ipBasina, time.Minute, webAnahtari, webMax),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	return app
}

// ipIstek, istegi verilen basliklarla atar; durumu ve hata kodunu doner.
func ipIstek(t *testing.T, app *fiber.App, basliklar map[string]string) (int, string) {
	t.Helper()
	istek := httptest.NewRequest("GET", "/uc", nil)
	for k, v := range basliklar {
		istek.Header.Set(k, v)
	}
	y, err := app.Test(istek, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	ham, _ := io.ReadAll(y.Body)
	var govde struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(ham, &govde)
	return y.StatusCode, govde.Error.Code
}

func TestLimitIP_RedisYoksaFailClosed(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	durum, _ := ipIstek(t, ipUygulamasi(rdb, 5, "", 0), nil)
	if durum != fiber.StatusServiceUnavailable {
		t.Fatalf("durum = %d, beklenen 503 (kimliksiz uc 401 vermemeli, Redis yoksa gecirmemeli)", durum)
	}
}

func TestCanliLimitIP_IPBasinaSayar(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	app := ipUygulamasi(rdb, 2, "", 0)
	a := map[string]string{"X-Real-Ip": "203.0.113.5"}

	for i := 1; i <= 2; i++ {
		if durum, _ := ipIstek(t, app, a); durum != fiber.StatusOK {
			t.Fatalf("istek %d: durum = %d, beklenen 200", i, durum)
		}
	}
	if durum, kod := ipIstek(t, app, a); durum != fiber.StatusTooManyRequests || kod != "RATE_LIMITED" {
		t.Fatalf("3. istek: durum = %d kod = %q, beklenen 429 RATE_LIMITED", durum, kod)
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "203.0.113.6"}); durum != fiber.StatusOK {
		t.Fatalf("baska IP etkilenmemeli: durum = %d", durum)
	}
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:ip:203.0.113.5").Int(); err != nil || n != 3 {
		t.Fatalf("IP anahtari = %d (hata %v), beklenen 3", n, err)
	}
	if ttl, err := rdb.TTL(ctx, "ratelimit:onizleme:ip:203.0.113.5").Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("TTL = %v (hata %v)", ttl, err)
	}
}

func TestCanliLimitIP_GecersizBaslikBaglantiAdresineDuser(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	app := ipUygulamasi(rdb, 5, "", 0)
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "1.2.3.4:80 kotu"}); durum != fiber.StatusOK {
		t.Fatalf("durum = %d", durum)
	}
	if durum, _ := ipIstek(t, app, nil); durum != fiber.StatusOK {
		t.Fatalf("durum = %d", durum)
	}
	// app.Test'in baglanti adresi 0.0.0.0'dir; iki istek de onda sayilir.
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:ip:0.0.0.0").Int(); err != nil || n != 2 {
		t.Fatalf("baglanti adresi anahtari = %d (hata %v), beklenen 2", n, err)
	}
	if anahtarlar, _ := rdb.Keys(ctx, "ratelimit:onizleme:ip:1.2.3.4*").Result(); len(anahtarlar) != 0 {
		t.Fatalf("gecersiz baslik anahtar olmamali: %v", anahtarlar)
	}
}

func TestCanliLimitIP_WebAnahtariOrtakSinir(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	app := ipUygulamasi(rdb, 1, testWebAnahtari, 3)

	// Dogru anahtar: IP'den bagimsiz, ortak "web" sayacinda 3 hak.
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.1"} {
		durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": ip, WebAnahtariBasligi: testWebAnahtari})
		if durum != fiber.StatusOK {
			t.Fatalf("web istegi %d: durum = %d, beklenen 200", i+1, durum)
		}
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "198.51.100.3", WebAnahtariBasligi: testWebAnahtari}); durum != fiber.StatusTooManyRequests {
		t.Fatalf("web siniri asilinca 429 beklenir: %d", durum)
	}
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:web").Int(); err != nil || n != 4 {
		t.Fatalf("web anahtari = %d (hata %v), beklenen 4", n, err)
	}
	if anahtarlar, _ := rdb.Keys(ctx, "ratelimit:onizleme:ip:*").Result(); len(anahtarlar) != 0 {
		t.Fatalf("dogru web anahtari IP sayacina dokunmamali: %v", anahtarlar)
	}

	// Yanlis anahtar: olagan IP siniri (1).
	yanlis := map[string]string{"X-Real-Ip": "198.51.100.9", WebAnahtariBasligi: testWebAnahtari + "x"}
	if durum, _ := ipIstek(t, app, yanlis); durum != fiber.StatusOK {
		t.Fatalf("yanlis anahtar ilk istek: %d", durum)
	}
	if durum, _ := ipIstek(t, app, yanlis); durum != fiber.StatusTooManyRequests {
		t.Fatalf("yanlis anahtar IP sinirina takilmali: %d", durum)
	}
}

func TestCanliLimitIP_BosYapilandirmaWebYolunuKapatir(t *testing.T) {
	rdb := canliRedis(t)
	app := ipUygulamasi(rdb, 1, "", 100)
	// Yapilandirma bosken bos baslik da, herhangi bir baslik da IP sinirindadir.
	for _, anahtar := range []string{"", "herhangi"} {
		b := map[string]string{"X-Real-Ip": "192.0.2.10", WebAnahtariBasligi: anahtar}
		_, _ = ipIstek(t, app, b)
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "192.0.2.10", WebAnahtariBasligi: ""}); durum != fiber.StatusTooManyRequests {
		t.Fatalf("durum = %d, beklenen 429", durum)
	}
	if n, _ := rdb.Exists(context.Background(), "ratelimit:onizleme:web").Result(); n != 0 {
		t.Fatal("web anahtari bosken web sayaci olusmamali")
	}
}

func TestCanliLimitKod_OzelKodla429(t *testing.T) {
	rdb := canliRedis(t)
	app := fiber.New()
	app.Get("/uc", func(c *fiber.Ctx) error {
		c.Locals("userId", "kullanici-yorum")
		return c.Next()
	}, NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", "Biraz yavasla"),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })

	if durum, _ := ipIstek(t, app, nil); durum != fiber.StatusOK {
		t.Fatalf("ilk istek: %d", durum)
	}
	if durum, kod := ipIstek(t, app, nil); durum != fiber.StatusTooManyRequests || kod != "yavasla" {
		t.Fatalf("durum = %d kod = %q, beklenen 429 yavasla", durum, kod)
	}
	if n, err := rdb.Get(context.Background(), "ratelimit:yorum:kullanici-yorum").Int(); err != nil || n != 2 {
		t.Fatalf("kullanici anahtari = %d (hata %v)", n, err)
	}
}

func TestLimitKod_KimliksizIstek401(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", "x"),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	if durum, _ := ipIstek(t, app, nil); durum != fiber.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", durum)
	}
}

func TestIPAnahtari_IPv6Ust64VeIPv4Esleme(t *testing.T) {
	durumlar := map[string]string{
		"203.0.113.5":                    "203.0.113.5",
		"::ffff:203.0.113.5":             "203.0.113.5",
		"2001:db8:1:2:aaaa:bbbb:cccc:dd": "2001:db8:1:2::",
		"2001:db8:1:2::1":                "2001:db8:1:2::",
		"2001:db8:1:3::1":                "2001:db8:1:3::",
		"gecersiz":                       "gecersiz",
	}
	for ham, beklenen := range durumlar {
		if got := ipAnahtari(ham); got != beklenen {
			t.Errorf("ipAnahtari(%q) = %q, beklenen %q", ham, got, beklenen)
		}
	}
}

// Ayni /64'teki iki IPv6 adresi ayni sayaci paylasir; komsu /64 ayridir.
func TestCanliLimitIP_IPv6Ayni64OrtakSayac(t *testing.T) {
	rdb := canliRedis(t)
	app := ipUygulamasi(rdb, 2, "", 0)
	for i, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2:ffff::9"} {
		if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": ip}); durum != fiber.StatusOK {
			t.Fatalf("istek %d: durum = %d", i+1, durum)
		}
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "2001:db8:1:2::abcd"}); durum != fiber.StatusTooManyRequests {
		t.Fatalf("ayni /64'te 3. istek 429 olmali: %d", durum)
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "2001:db8:1:3::1"}); durum != fiber.StatusOK {
		t.Fatalf("komsu /64 etkilenmemeli: %d", durum)
	}
	if n, err := rdb.Get(context.Background(), "ratelimit:onizleme:ip:2001:db8:1:2::").Int(); err != nil || n != 3 {
		t.Fatalf("/64 anahtari = %d (hata %v), beklenen 3", n, err)
	}
}

// IPv4-eslemeli IPv6 adresi IPv4 bicimiyle ayni sayactadir.
func TestCanliLimitIP_IPv4EslemeliAyniSayac(t *testing.T) {
	rdb := canliRedis(t)
	app := ipUygulamasi(rdb, 5, "", 0)
	for _, ip := range []string{"203.0.113.7", "::ffff:203.0.113.7"} {
		if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": ip}); durum != fiber.StatusOK {
			t.Fatalf("%s: durum = %d", ip, durum)
		}
	}
	if n, err := rdb.Get(context.Background(), "ratelimit:onizleme:ip:203.0.113.7").Int(); err != nil || n != 2 {
		t.Fatalf("IPv4 anahtari = %d (hata %v), beklenen 2", n, err)
	}
}
