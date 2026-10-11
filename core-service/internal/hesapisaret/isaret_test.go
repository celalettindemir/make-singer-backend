package hesapisaret

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// canliRedis, yerel Redis ister; yoksa test atlanir. DB 11 kullanilir
// (handler 12, service 13, middleware 14, kimlik 15).
func canliRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 11})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

func TestAnahtar_BicimDegismez(t *testing.T) {
	if got := Anahtar("u1"); got != "kimlik:silindi:u1" {
		t.Fatalf("anahtar = %q", got)
	}
}

func TestOmur_IsKaydiOmruneEsit(t *testing.T) {
	if Omur != 24*time.Hour {
		t.Fatalf("isaret omru = %v, 24 saat bekleniyordu", Omur)
	}
}

func TestRedisDenetci_IsaretiOkur(t *testing.T) {
	rdb := canliRedis(t)
	d := NewRedisDenetci(rdb)
	if err := rdb.Set(context.Background(), Anahtar("u1"), "1", time.Minute).Err(); err != nil {
		t.Fatalf("isaret yazilamadi: %v", err)
	}
	if s, err := d.Silinmis(context.Background(), "u1"); err != nil || !s {
		t.Fatalf("u1 silinmis gorunmeli: %v, %v", s, err)
	}
	if s, err := d.Silinmis(context.Background(), "u2"); err != nil || s {
		t.Fatalf("u2 silinmis gorunmemeli: %v, %v", s, err)
	}
}

func TestRedisDenetci_RedisYoksaHata(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	if _, err := NewRedisDenetci(rdb).Silinmis(context.Background(), "u1"); err == nil {
		t.Fatal("Redis yokken hata bekleniyordu")
	}
}
