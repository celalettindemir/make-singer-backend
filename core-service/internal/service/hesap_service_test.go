package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/hesapisaret"
	"github.com/makeasinger/api/internal/kimlik"
	"github.com/redis/go-redis/v9"
)

type sahteKimlik struct {
	silinen []string
	hata    error
}

func (k *sahteKimlik) HesapSil(ctx context.Context, userID string) error {
	k.silinen = append(k.silinen, userID)
	return k.hata
}

type sahteSilici struct {
	onekler []string
	hata    error
}

func (s *sahteSilici) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	s.onekler = append(s.onekler, prefix)
	return 1, s.hata
}

type sahteKuyruk struct {
	gorevler   []*asynq.Task
	secenekler [][]asynq.Option
}

func (q *sahteKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	q.gorevler = append(q.gorevler, t)
	q.secenekler = append(q.secenekler, opts)
	return &asynq.TaskInfo{}, nil
}

// ulasilamazRedis: isaret yazimi basarisiz olur; Sil bunu loglayip
// devam etmeli. Isaretin gercekten yazildigi canli testte olculur.
func ulasilamazRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
}

const hesapID = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

func TestHesapSil_HerSeyiSilerVeIkinciTemizligiKurar(t *testing.T) {
	k, s, q := &sahteKimlik{}, &sahteSilici{}, &sahteKuyruk{}
	svc := NewHesapService(k, s, ulasilamazRedis(), q, nil)
	if err := svc.Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if !slices.Equal(k.silinen, []string{hesapID}) {
		t.Fatalf("kimlik silinen = %v", k.silinen)
	}
	if len(s.onekler) != 5 {
		t.Fatalf("5 onek bekleniyordu, gelen %v", s.onekler)
	}
	if len(q.gorevler) != 1 || q.gorevler[0].Type() != TaskTypeHesapTemizle {
		t.Fatalf("ikinci temizlik gorevi kurulmadi: %v", q.gorevler)
	}
	var p struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(q.gorevler[0].Payload(), &p); err != nil || p.UserID != hesapID {
		t.Fatalf("gorev yuku = %s", q.gorevler[0].Payload())
	}
}

func TestHesapSil_KimlikYoksaErrHesapYok(t *testing.T) {
	k, s, q := &sahteKimlik{hata: kimlik.ErrKullaniciYok}, &sahteSilici{}, &sahteKuyruk{}
	err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID)
	if !errors.Is(err, ErrHesapYok) {
		t.Fatalf("ErrHesapYok bekleniyordu, gelen %v", err)
	}
	if len(s.onekler) != 0 || len(q.gorevler) != 0 {
		t.Fatal("kimlik silinmeden R2'ye dokunulmamali")
	}
}

func TestHesapSil_KimlikHatasindaR2yeDokunulmaz(t *testing.T) {
	k, s, q := &sahteKimlik{hata: errors.New("pg kapali")}, &sahteSilici{}, &sahteKuyruk{}
	err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID)
	if err == nil || errors.Is(err, ErrHesapYok) {
		t.Fatalf("genel hata bekleniyordu, gelen %v", err)
	}
	if len(s.onekler) != 0 || len(q.gorevler) != 0 {
		t.Fatal("kimlik silinmeden R2'ye dokunulmamali")
	}
}

func TestHesapSil_R2HatasiIstegiBozmaz(t *testing.T) {
	k, s, q := &sahteKimlik{}, &sahteSilici{hata: errors.New("r2 kapali")}, &sahteKuyruk{}
	if err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("hesap silindikten sonra hata donmemeli: %v", err)
	}
	if len(q.gorevler) != 1 {
		t.Fatal("R2 hatasinda ikinci temizlik yine kurulmali")
	}
}

func TestHesapSil_GecersizIDHicbirSeySilmez(t *testing.T) {
	k, s, q := &sahteKimlik{}, &sahteSilici{}, &sahteKuyruk{}
	err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), "../x")
	if !errors.Is(err, ErrHesapYok) || len(k.silinen) != 0 {
		t.Fatalf("gecersiz id silinmemeli: err=%v silinen=%v", err, k.silinen)
	}
}

func TestHesapTemizle_R2YoksaHataYok(t *testing.T) {
	svc := NewHesapService(&sahteKimlik{}, nil, ulasilamazRedis(), &sahteKuyruk{}, nil)
	if err := svc.Temizle(context.Background(), hesapID); err != nil {
		t.Fatalf("R2 yokken temizlik no-op olmali: %v", err)
	}
}

// Isaret is kaydi omru (24 saat) boyunca yasar: kuyrukta bekleyen ya da
// kosan isler sahibin silindigini bu sure boyunca gorebilmeli.
func TestHesapSil_Isaret24SaatYasar(t *testing.T) {
	rdb := canliRedis(t)
	svc := NewHesapService(&sahteKimlik{}, &sahteSilici{}, rdb, &sahteKuyruk{}, nil)
	if err := svc.Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	ttl, err := rdb.TTL(context.Background(), hesapisaret.Anahtar(hesapID)).Result()
	if err != nil || ttl <= 23*time.Hour || ttl > 24*time.Hour {
		t.Fatalf("isaret TTL = %v (err %v), ~24 saat bekleniyordu", ttl, err)
	}
}

// Gecikmeli temizlik bir saat sonra kosar: en uzun is denemesinden
// (render varsayilan 30dk zaman asimi) fazlasiyla sonra.
func TestHesapSil_GecikmeliTemizlikBirSaatSonra(t *testing.T) {
	q := &sahteKuyruk{}
	if err := NewHesapService(&sahteKimlik{}, &sahteSilici{}, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(q.secenekler) != 1 {
		t.Fatalf("tek gorev bekleniyordu: %d", len(q.secenekler))
	}
	var gecikme time.Duration
	for _, o := range q.secenekler[0] {
		if o.Type() == asynq.ProcessInOpt {
			gecikme = o.Value().(time.Duration)
		}
	}
	if gecikme != time.Hour {
		t.Fatalf("gecikme = %v, 1 saat bekleniyordu", gecikme)
	}
}
