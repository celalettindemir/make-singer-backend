package middleware

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// canliRedis, yerel Redis ister. Yoksa test atlanir: birim testler agsiz
// gecmek zorunda (bkz. internal/kimlik/istek_test.go'daki ayni kalip).
// DB 14 kullanilir; kimlik testleri DB 15'te.
func canliRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 14})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

// canliUygulama, gercek Redis'e bagli bir rate limit'li uc kurar.
func canliUygulama(rdb *redis.Client, userID, onEk string, maxIstek int, pencere time.Duration) *fiber.App {
	rl := NewRateLimiter(rdb)
	app := fiber.New()
	app.Get("/uc", func(c *fiber.Ctx) error {
		c.Locals("userId", userID)
		return c.Next()
	}, rl.Limit(onEk, maxIstek, pencere), func(c *fiber.Ctx) error {
		return c.SendString("tamam")
	})
	return app
}

type canliYanit struct {
	durum      int
	kalan      string
	retryAfter string
}

func canliIstek(t *testing.T, app *fiber.App) canliYanit {
	t.Helper()
	y, err := app.Test(httptest.NewRequest("GET", "/uc", nil), -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	return canliYanit{
		durum:      y.StatusCode,
		kalan:      y.Header.Get("X-RateLimit-Remaining"),
		retryAfter: y.Header.Get("Retry-After"),
	}
}

// Mutlu yol: sayac 1'den baslar, X-RateLimit-Remaining azalir, kota
// asilinca 429 + pozitif Retry-After gelir. Bu davranis Go tarafindan
// degil Redis'in SET NX EX + INCR + MULTI/EXEC semantiginden geliyor;
// Go-tarafi bir test bunu yakalayamaz.
func TestCanliMutluYolVeSayac(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	const kullanici = "kullanici-mutlu"
	app := canliUygulama(rdb, kullanici, "mutlu", 3, time.Minute)
	anahtar := fmt.Sprintf("ratelimit:mutlu:%s", kullanici)

	for i, beklenenKalan := range []string{"2", "1", "0"} {
		y := canliIstek(t, app)
		if y.durum != fiber.StatusOK {
			t.Fatalf("istek %d: durum = %d, beklenen 200", i+1, y.durum)
		}
		if y.kalan != beklenenKalan {
			t.Errorf("istek %d: X-RateLimit-Remaining = %q, beklenen %q", i+1, y.kalan, beklenenKalan)
		}
		if i == 0 {
			// Sayac 1'den baslamali: SET NX 0 + INCR.
			if deger, err := rdb.Get(ctx, anahtar).Result(); err != nil || deger != "1" {
				t.Errorf("ilk istekten sonra sayac = %q (hata %v), beklenen \"1\"", deger, err)
			}
			// TTL olusturma aninda kenetlenmis olmali.
			if ttl, err := rdb.TTL(ctx, anahtar).Result(); err != nil || ttl <= 0 {
				t.Errorf("ilk istekten sonra TTL = %v (hata %v), beklenen > 0", ttl, err)
			}
		}
	}

	y := canliIstek(t, app)
	if y.durum != fiber.StatusTooManyRequests {
		t.Fatalf("4. istek: durum = %d, beklenen 429", y.durum)
	}
	saniye, err := strconv.Atoi(y.retryAfter)
	if err != nil || saniye < 1 {
		t.Errorf("Retry-After = %q (hata %v), beklenen >= 1", y.retryAfter, err)
	}
}

// Pencere SABIT (fixed window), kayan degil: TTL sonraki isteklerde
// YENILENMEZ. CLAUDE.md bir sure "sliding window" diyordu; olculen
// davranis budur.
func TestCanliPencereSabitTTLYenilenmez(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	const kullanici = "kullanici-pencere"
	app := canliUygulama(rdb, kullanici, "pencere", 100, 10*time.Second)
	anahtar := fmt.Sprintf("ratelimit:pencere:%s", kullanici)

	if y := canliIstek(t, app); y.durum != fiber.StatusOK {
		t.Fatalf("ilk istek: durum = %d, beklenen 200", y.durum)
	}
	ilkTTL, err := rdb.TTL(ctx, anahtar).Result()
	if err != nil {
		t.Fatalf("TTL okunamadi: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)

	if y := canliIstek(t, app); y.durum != fiber.StatusOK {
		t.Fatalf("ikinci istek: durum = %d, beklenen 200", y.durum)
	}
	ikinciTTL, err := rdb.TTL(ctx, anahtar).Result()
	if err != nil {
		t.Fatalf("TTL okunamadi: %v", err)
	}
	if ikinciTTL >= ilkTTL {
		t.Errorf("TTL yenilendi (%v -> %v); pencere sabit olmali", ilkTTL, ikinciTTL)
	}
}

// Pencere dolunca sayac sifirlanir ve kullanici yeniden gecebilir.
func TestCanliPencereDoluncaSayacSifirlanir(t *testing.T) {
	rdb := canliRedis(t)
	const kullanici = "kullanici-sifir"
	app := canliUygulama(rdb, kullanici, "sifir", 1, time.Second)

	if y := canliIstek(t, app); y.durum != fiber.StatusOK {
		t.Fatalf("ilk istek: durum = %d, beklenen 200", y.durum)
	}
	if y := canliIstek(t, app); y.durum != fiber.StatusTooManyRequests {
		t.Fatalf("ikinci istek: durum = %d, beklenen 429", y.durum)
	}

	time.Sleep(1200 * time.Millisecond)

	if y := canliIstek(t, app); y.durum != fiber.StatusOK {
		t.Errorf("pencere dolduktan sonra: durum = %d, beklenen 200", y.durum)
	}
}

// MIRAS ANAHTAR: eski kod (Incr basarili + Expire patladi) TTL'siz
// anahtar birakabiliyordu. Boyle bir anahtar sayaci asmis durumdaysa
// kullanici KALICI kilitli kalirdi. Yeni kod 429 dalinda TTL -1 gorunce
// pencereyi geri veriyor.
func TestCanliMirasTTLsizAnahtarIyilestirilir(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	const kullanici = "kullanici-miras"
	app := canliUygulama(rdb, kullanici, "miras", 3, 30*time.Second)
	anahtar := fmt.Sprintf("ratelimit:miras:%s", kullanici)

	// Elle TTL'siz ve kotayi asmis bir anahtar kur (eski kodun mirasi).
	if err := rdb.Set(ctx, anahtar, 99, 0).Err(); err != nil {
		t.Fatalf("miras anahtar kurulamadi: %v", err)
	}
	if ttl, err := rdb.TTL(ctx, anahtar).Result(); err != nil || ttl >= 0 {
		t.Fatalf("hazirlik: TTL = %v (hata %v), TTL'siz olmali (-1)", ttl, err)
	}

	y := canliIstek(t, app)
	if y.durum != fiber.StatusTooManyRequests {
		t.Fatalf("durum = %d, beklenen 429", y.durum)
	}
	saniye, err := strconv.Atoi(y.retryAfter)
	if err != nil || saniye < 1 {
		t.Errorf("Retry-After = %q (hata %v), beklenen >= 1", y.retryAfter, err)
	}
	// Iyilestirme: anahtar artik TTL tasimali, yani kilit kendiliginden
	// acilacak.
	ttl, err := rdb.TTL(ctx, anahtar).Result()
	if err != nil {
		t.Fatalf("TTL okunamadi: %v", err)
	}
	if ttl <= 0 {
		t.Errorf("istekten sonra TTL = %v, beklenen > 0 (miras anahtar iyilestirilmedi)", ttl)
	}
}
