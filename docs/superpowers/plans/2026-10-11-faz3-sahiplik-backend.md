# Faz 3 (Backend) — Sahiplik ve Sertlestirme Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Kimligi dogrulanmis bir kullanicinin baskasinin isini okuyamamasi, iptal edememesi, dosyasina yazamamasi/silememesi ve WebSocket'ten baskasinin isini dinleyememesi.

**Architecture:** Is kayitlarina `OwnerID` yazilir; okuma/iptal uclari ortak `sahipliJobOku` ile yuklenir ve sahibi degilse `ErrJobNotFound` (404) doner. R2 anahtarlari `client.KullaniciAnahtari` ile `<tur>/<userId>/...` olarak kurulur; istemciden gelen her URL `client.SahipliAnahtarCoz` ile anahtara cevrilip onek denetiminden gecer. WebSocket'e, kimligi dogrulanmis `POST /api/ws/ticket` ile alinan 30 saniyelik tek kullanimlik biletle baglanilir; bilet upgrade'den ONCE `GETDEL` ile tuketilir.

**Tech Stack:** Go 1.25, Fiber v2, go-redis v9 (Redis 7, `GETDEL`), Asynq, `crypto/rand`.

**Spec:** `docs/superpowers/specs/2026-09-29-kendi-oidc-saglayicimiz-design.md` (bolumler: "Sahiplik", "WebSocket", "Auth'a bagli diger duzeltmeler", "Sevkiyat > Faz 3", "Test")

## Global Constraints

- Tum komutlar `core-service/` dizininde calisir. Birim testler: `go test ./internal/...`. CI bu komutu `redis:7-alpine` servisiyle kosar; canli Redis testleri yerelde Redis yoksa `t.Skip` ile atlanir.
- Canli Redis test veritabanlari: `internal/service` **DB 13**, `internal/handler` **DB 12**. (DB 14 middleware'in, DB 15 kimlik'in; dokunma.)
- Kod yorumlari ve test adlari Turkce, **aksan isareti yok** (c g i o s u). 
- `go vet ./...` ve `gofmt -l internal cmd e2e` temiz kalmali. `e2e/` CI'da kosmaz ama derlenmek zorunda.
- Sahibi olmayan kullaniciya is uclari **404** doner, 403 degil ("403 o isin var oldugunu dogrular").
- R2 anahtar duzeni: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3` (Suno; mock yolda `<instrument>.wav`), `exports/<userId>/<exportId>.{mp3,wav,zip}`, `previews/<userId>/<id>.mp3`. Ilk segment `exports/` kaldigi icin `bucketFor` kurali degismez.
- Eski duzendeki nesneler ve `OwnerID`'siz is kayitlari icin geriye uyumluluk YOK (spec "Goc": deneme verisi). `OwnerID`'siz kayit kimseye acilmaz.
- WebSocket bileti: Redis anahtari `wsbilet:<bilet>`, deger jobId, TTL **30 saniye**, 32 rastgele bayt `base64.RawURLEncoding` (43 karakter), tuketim `GETDEL`.
- Commit mesajlari `tip(alan): ozet` bicimi, sonunda `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch: `faz3-sahiplik`. Push YOK.

## Zaten tamam olan Faz 3 maddeleri (dokunulmaz)

- Rate limit fail-closed: `internal/middleware/ratelimit.go:25-31` userId bosken 401 donuyor.
- Log seviyesi: k3s-gitops `makesinger-backend.yaml` API container'inda `LOG_LEVEL: info`.
- Authorization maskeleme: `cmd/server/main.go` logger formatinda `${reqHeaders}` ve `${body}` hicbir seviyede yok; header hic loglanmiyor.

## Kararlar (spec'in acik biraktiklari)

- **Girdi anahtarlari da onek denetiminden gecer.** Spec yazma/silmeyi onekle kapatiyor; ama master ve export istekleri istemciden URL aliyor ve worker/audio-service o anahtari OKUYOR. Denetim olmazsa A kullanicisi B'nin `stems/` anahtarini master'a vererek okuyabilir. Bu yuzden `StartFinal` ve export uclari her girdi URL'sini `SahipliAnahtarCoz` ile cozer; yabanci anahtar 404.
- **Vokal silme deterministik anahtar ister.** `DELETE /api/upload/vocal/:takeId` artik `?projectId=&sectionId=` sorgu parametrelerini zorunlu ister (mobil icin kirici; mobil planinda).
- **Segment dogrulamasi.** Anahtara giren her istemci kimligi `^[A-Za-z0-9_-]{1,64}(\.[a-z0-9]{1,5})?$` ile dogrulanir: `/` ve `..` tasiyamaz.
- **Suno ucretinden once sahip denetimi.** `sunoIleUret`, `OwnerID`/`ProjectID` anahtar kurmaya uygun degilse Suno'yu hic cagirmadan `SkipRetry` ile duser.
- **Kullanilmayan `DeleteVocalByKey` silinir**: herhangi bir anahtari silebilen, cagirani olmayan bir yuzey.

## Dosya Yapisi

| Dosya | Sorumluluk |
|---|---|
| `internal/client/anahtar.go` (yeni) | `KullaniciAnahtari`, `AnahtarSahibiDogrula`, `SahipliAnahtarCoz`, hata sabitleri |
| `internal/client/anahtar_test.go` (yeni) | Saf tablo testleri |
| `internal/model/job.go` (degisir) | `Job.OwnerID`, `Job.SahibiMi`, payload `OwnerID` alanlari |
| `internal/model/job_test.go` (yeni) | `SahibiMi` testi |
| `internal/model/websocket.go` (degisir) | `WSTicketRequest`, `WSTicketResponse` |
| `internal/service/job_kayit.go` (yeni) | `ErrJobNotFound`, `jobOku`, `jobYaz`, `sahipliJobOku` |
| `internal/service/render_service.go`, `master_service.go` (degisir) | userID parametreleri, sahiplik |
| `internal/service/upload_service.go`, `export_service.go` (degisir) | Kullanici onekli anahtarlar, girdi denetimi |
| `internal/service/ws_ticket_service.go` (yeni) | Bilet uretme/tuketme |
| `internal/service/canli_redis_test.go` (yeni) | DB 13 yardimcisi |
| `internal/service/sahiplik_canli_test.go`, `anahtar_duzeni_test.go`, `ws_ticket_canli_test.go` (yeni) | Servis testleri |
| `internal/handler/istek_sahibi.go` (yeni) | `istekSahibi` yardimcisi |
| `internal/handler/render.go`, `master.go`, `upload.go`, `export.go` (degisir) | userID gecirme, hata eslemesi |
| `internal/handler/ws_ticket.go` (yeni) | `Issue` ucu ve `Gate` on-upgrade middleware'i |
| `internal/handler/canli_redis_test.go`, `sahiplik_canli_test.go`, `ws_ticket_canli_test.go` (yeni) | Handler testleri (DB 12) |
| `internal/worker/render_worker.go`, `master_worker.go` (degisir) | Anahtarlar `OwnerID` ile |
| `cmd/server/main.go` (degisir) | Bilet servisi/handler'i ve rotalar |
| `e2e/upload_test.go` (degisir) | Silme testine sorgu parametreleri |
| `CLAUDE.md`, `core-service/docs/API.md`, `core-service/docs/*` (swag) | Dokuman |

---

### Task 1: Kullanici onekli anahtar yardimcilari

**Files:**
- Create: `internal/client/anahtar.go`
- Test: `internal/client/anahtar_test.go`

**Interfaces:**
- Consumes: `KeyFromURL(rawURL string, cfg *config.R2Config) (string, error)` (`internal/client/bucket.go`)
- Produces:
  - `var ErrGecersizSegment error`, `var ErrYabanciAnahtar error`
  - `func KullaniciAnahtari(tur, userID string, parcalar ...string) (string, error)` — son parca uzantili dosya adidir (`"<id>.wav"`)
  - `func AnahtarSahibiDogrula(key, userID string) error`
  - `func SahipliAnahtarCoz(rawURL, userID string, cfg *config.R2Config) (string, error)`

- [ ] **Step 1: Write the failing test**

`internal/client/anahtar_test.go`:

```go
package client

import (
	"errors"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func TestKullaniciAnahtari_Duzen(t *testing.T) {
	got, err := KullaniciAnahtari("vocals", "kullanici-a", "p1", "s1", "t1.wav")
	if err != nil {
		t.Fatalf("KullaniciAnahtari: %v", err)
	}
	if got != "vocals/kullanici-a/p1/s1/t1.wav" {
		t.Errorf("anahtar = %q", got)
	}
}

func TestKullaniciAnahtari_GecersizSegmentReddedilir(t *testing.T) {
	durumlar := map[string][]string{
		"bos kullanici":   {"", "p1", "t.wav"},
		"egik cizgi":      {"kullanici-a", "p1/../b", "t.wav"},
		"iki nokta":       {"kullanici-a", "..", "t.wav"},
		"nokta ile basla": {"kullanici-a", ".gizli", "t.wav"},
		"bosluk":          {"kullanici-a", "p 1", "t.wav"},
		"cok uzun":        {"kullanici-a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "t.wav"},
	}
	for ad, d := range durumlar {
		t.Run(ad, func(t *testing.T) {
			_, err := KullaniciAnahtari("vocals", d[0], d[1:]...)
			if !errors.Is(err, ErrGecersizSegment) {
				t.Errorf("ErrGecersizSegment bekleniyordu, geldi: %v", err)
			}
		})
	}
}

func TestKullaniciAnahtari_BilinmeyenTurVeDosyasizHata(t *testing.T) {
	if _, err := KullaniciAnahtari("gizli", "kullanici-a", "t.wav"); err == nil {
		t.Error("bilinmeyen tur icin hata bekleniyordu")
	}
	if _, err := KullaniciAnahtari("vocals", "kullanici-a"); !errors.Is(err, ErrGecersizSegment) {
		t.Errorf("dosya adi yokken ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
}

func TestAnahtarSahibiDogrula(t *testing.T) {
	durumlar := []struct {
		ad, key, kullanici string
		gecerli            bool
	}{
		{"kendi stem'i", "stems/kullanici-a/p1/s1.mp3", "kullanici-a", true},
		{"kendi export'u", "exports/kullanici-a/e1.zip", "kullanici-a", true},
		{"baskasinin stem'i", "stems/kullanici-b/p1/s1.mp3", "kullanici-a", false},
		{"eski duzen", "stems/p1/s1.mp3", "p1", false},
		{"bilinmeyen tur", "gizli/kullanici-a/x.wav", "kullanici-a", false},
		{"kisa anahtar", "stems/kullanici-a", "kullanici-a", false},
		{"bos kullanici", "stems//p1/s1.mp3", "", false},
		{"iki nokta segmenti", "stems/kullanici-a/../kullanici-b/s1.mp3", "kullanici-a", false},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			err := AnahtarSahibiDogrula(d.key, d.kullanici)
			if d.gecerli && err != nil {
				t.Errorf("gecerli olmaliydi: %v", err)
			}
			if !d.gecerli && !errors.Is(err, ErrYabanciAnahtar) {
				t.Errorf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
			}
		})
	}
}

func TestSahipliAnahtarCoz(t *testing.T) {
	cfg := &config.R2Config{AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private"}
	kendi := UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg) + "?X-Amz-Signature=imza"

	key, err := SahipliAnahtarCoz(kendi, "kullanici-a", cfg)
	if err != nil || key != "stems/kullanici-a/p1/s1.mp3" {
		t.Fatalf("kendi anahtari cozulemedi: %q, %v", key, err)
	}
	if _, err := SahipliAnahtarCoz(kendi, "kullanici-b", cfg); !errors.Is(err, ErrYabanciAnahtar) {
		t.Errorf("baska kullanici icin ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if _, err := SahipliAnahtarCoz("https://kotu.example.com/stems/kullanici-a/p1/s1.mp3", "kullanici-a", cfg); err == nil || errors.Is(err, ErrYabanciAnahtar) {
		t.Errorf("taninmayan konak KeyFromURL hatasi vermeli, geldi: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client/ -run 'KullaniciAnahtari|AnahtarSahibi|SahipliAnahtar' -v`
Expected: FAIL — `undefined: KullaniciAnahtari`

- [ ] **Step 3: Write minimal implementation**

`internal/client/anahtar.go`:

```go
package client

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// ErrGecersizSegment: istemciden gelen bir kimlik anahtar segmenti olamaz.
// Mesaj degeri tasimaz; hata Redis is kaydina ve yanita dusebilir.
var ErrGecersizSegment = errors.New("gecersiz anahtar segmenti")

// ErrYabanciAnahtar: nesne, istegi yapan kullanicinin oneki disinda. Uclar
// bunu 404'e cevirir; 403 nesnenin var oldugunu dogrulardi.
var ErrYabanciAnahtar = errors.New("nesne bu kullaniciya ait degil")

// sahipliTurler: ilk segmenti bunlardan biri olan anahtarlarin ikinci
// segmenti her zaman sahibin userId'sidir.
var sahipliTurler = map[string]bool{
	"vocals":   true,
	"masters":  true,
	"stems":    true,
	"exports":  true,
	"previews": true,
}

// segmentDeseni: "/" ve ".." tasiyamaz, harf/rakamla baslar, tek bir
// uzanti noktasina izin verir.
var segmentDeseni = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}(\.[a-z0-9]{1,5})?$`)

// KullaniciAnahtari, "<tur>/<userId>/<parcalar...>" anahtarini kurar. Son
// parca uzantili dosya adidir. Sahiplik boylece yapisaldir: kimse kendi
// oneki disina yazamaz, kontrol edilecek ayri bir tablo yoktur.
func KullaniciAnahtari(tur, userID string, parcalar ...string) (string, error) {
	if !sahipliTurler[tur] {
		return "", fmt.Errorf("bilinmeyen anahtar turu: %s", tur)
	}
	if len(parcalar) == 0 {
		return "", fmt.Errorf("%w: dosya adi yok", ErrGecersizSegment)
	}
	hepsi := append([]string{userID}, parcalar...)
	for _, p := range hepsi {
		if !segmentDeseni.MatchString(p) {
			return "", ErrGecersizSegment
		}
	}
	return tur + "/" + strings.Join(hepsi, "/"), nil
}

// AnahtarSahibiDogrula, anahtarin userID'nin onekinde oldugunu dogrular.
// Eski (oneksiz) duzendeki anahtarlar da reddedilir.
func AnahtarSahibiDogrula(key, userID string) error {
	parcalar := strings.Split(key, "/")
	if userID == "" || len(parcalar) < 3 || !sahipliTurler[parcalar[0]] || parcalar[1] != userID {
		return ErrYabanciAnahtar
	}
	for _, p := range parcalar[1:] {
		if !segmentDeseni.MatchString(p) {
			return ErrYabanciAnahtar
		}
	}
	return nil
}

// SahipliAnahtarCoz, istemciden gelen URL'yi anahtara cevirir ve anahtarin
// istegi yapan kullaniciya ait oldugunu dogrular. Worker ve audio-service
// bu anahtari OKUDUGU icin denetimsiz bir URL baskasinin dosyasini okutur.
func SahipliAnahtarCoz(rawURL, userID string, cfg *config.R2Config) (string, error) {
	key, err := KeyFromURL(rawURL, cfg)
	if err != nil {
		return "", err
	}
	if err := AnahtarSahibiDogrula(key, userID); err != nil {
		return "", err
	}
	return key, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/client/ -v -run 'KullaniciAnahtari|AnahtarSahibi|SahipliAnahtar'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/client/anahtar.go internal/client/anahtar_test.go
git commit -m "feat(r2): kullanici onekli anahtar kurma ve sahiplik denetimi"
```

---

### Task 2: Is sahipligi (OwnerID + 404 kurali)

**Files:**
- Modify: `internal/model/job.go`
- Create: `internal/model/job_test.go`
- Create: `internal/service/job_kayit.go`
- Modify: `internal/service/render_service.go` (StartRender, GetStatus, GetResult, CancelRender, saveJob, getJob)
- Modify: `internal/service/master_service.go` (StartFinal, GetStatus, GetResult, saveJob, getJob)
- Create: `internal/handler/istek_sahibi.go`
- Modify: `internal/handler/render.go`, `internal/handler/master.go` (Final, Status, Result)
- Create: `internal/service/canli_redis_test.go`, `internal/service/sahiplik_canli_test.go`
- Create: `internal/handler/canli_redis_test.go`, `internal/handler/sahiplik_canli_test.go`

**Interfaces:**
- Produces:
  - `model.Job.OwnerID string` (`json:"ownerId"`), `func (j *Job) SahibiMi(userID string) bool`
  - `model.RenderJobPayload.OwnerID`, `model.MasterJobPayload.OwnerID` (`json:"ownerId"`)
  - `service.ErrJobNotFound` (mesaj `"job not found"`)
  - `func jobOku(ctx, rdb *redis.Client, jobID string) (*model.Job, error)`, `func jobYaz(ctx, rdb *redis.Client, job *model.Job) error`, `func sahipliJobOku(ctx, rdb *redis.Client, userID, jobID string) (*model.Job, error)` (paket ici; Task 4 kullanir)
  - `RenderService.StartRender(ctx, userID string, req)`, `GetStatus(ctx, userID, jobID)`, `GetResult(ctx, userID, jobID)`, `CancelRender(ctx, userID, jobID)`
  - `MasterService.StartFinal(ctx, userID string, req)`, `GetStatus(ctx, userID, jobID)`, `GetResult(ctx, userID, jobID)`
  - `func istekSahibi(c *fiber.Ctx) (string, bool)` (handler paketi ici; Task 3 ve 4 kullanir)
  - Test yardimcilari: service paketinde `canliRedis(t) *redis.Client` (DB 13), handler paketinde `canliRedis(t) *redis.Client` (DB 12)
- Worker'in kullandigi `UpdateJobProgress`, `CompleteJob`, `FailJob`, `BuildResult` imzalari DEGISMEZ.

- [ ] **Step 1: Write the failing model test**

`internal/model/job_test.go`:

```go
package model

import "testing"

func TestJob_SahibiMi(t *testing.T) {
	j := &Job{OwnerID: "kullanici-a"}
	if !j.SahibiMi("kullanici-a") {
		t.Error("sahibi kendi isini gormeli")
	}
	if j.SahibiMi("kullanici-b") {
		t.Error("baska kullanici isi gormemeli")
	}
	if j.SahibiMi("") {
		t.Error("bos kullanici hicbir isi gormemeli")
	}
	if (&Job{}).SahibiMi("") {
		t.Error("sahipsiz kayit bos kullaniciya da acilmamali")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/model/ -run SahibiMi -v`
Expected: FAIL — `j.SahibiMi undefined` / `unknown field OwnerID`

- [ ] **Step 3: Model alanlari**

`internal/model/job.go` — `Job` struct'inda `Type` satirindan sonra ekle:

```go
	OwnerID     string     `json:"ownerId"`
```

Struct'tan sonra ekle:

```go
// SahibiMi: is yalnizca onu baslatan kullaniciya aittir. OwnerID'siz kayit
// (Faz 3 oncesi) kimseye acilmaz; kayitlar 24 saatlik oldugu icin goc yok.
func (j *Job) SahibiMi(userID string) bool {
	return userID != "" && j.OwnerID == userID
}
```

`RenderJobPayload` ve `MasterJobPayload`'in ilk alani olarak ekle:

```go
	OwnerID     string        `json:"ownerId"`
```

(`RenderJobPayload`'da hizalama `OwnerID     string      `json:"ownerId"``; `gofmt` duzeltir.)

Run: `go test ./internal/model/ -v` → PASS

- [ ] **Step 4: Ortak is kaydi yardimcilari**

`internal/service/job_kayit.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// ErrJobNotFound: is yok YA DA istegi yapan kullaniciya ait degil. Iki
// durum bilerek ayirt edilmez; 403 isin var oldugunu dogrulardi.
var ErrJobNotFound = errors.New("job not found")

const jobTTL = 24 * time.Hour

func jobAnahtari(jobID string) string { return fmt.Sprintf("job:%s", jobID) }

func jobOku(ctx context.Context, rdb *redis.Client, jobID string) (*model.Job, error) {
	data, err := rdb.Get(ctx, jobAnahtari(jobID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	var job model.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

func jobYaz(ctx context.Context, rdb *redis.Client, job *model.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, jobAnahtari(job.ID), data, jobTTL).Err()
}

// sahipliJobOku, isi yalnizca sahibine dondurur; digerlerine ErrJobNotFound.
func sahipliJobOku(ctx context.Context, rdb *redis.Client, userID, jobID string) (*model.Job, error) {
	job, err := jobOku(ctx, rdb, jobID)
	if err != nil {
		return nil, err
	}
	if !job.SahibiMi(userID) {
		return nil, ErrJobNotFound
	}
	return job, nil
}
```

- [ ] **Step 5: Write the failing service tests**

`internal/service/canli_redis_test.go`:

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// canliRedisAdresi: CI redis:7-alpine servisini localhost:6379'da kosar.
const canliRedisAdresi = "localhost:6379"

// canliRedis, yerel Redis ister; yoksa test atlanir. DB 13 kullanilir
// (middleware 14, kimlik 15, handler 12).
func canliRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: canliRedisAdresi, DB: 13})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}
```

`internal/service/sahiplik_canli_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

func isKaydet(t *testing.T, rdb *redis.Client, job *model.Job) {
	t.Helper()
	if err := jobYaz(context.Background(), rdb, job); err != nil {
		t.Fatalf("jobYaz: %v", err)
	}
}

func TestRenderService_BaskasininIsi404(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	isKaydet(t, rdb, &model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued})

	if _, err := s.GetStatus(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("baska kullanici icin ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	if _, err := s.GetStatus(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Errorf("sahibi kendi isini okuyamadi: %v", err)
	}
}

func TestRenderService_BaskasiIptalEdemez(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	isKaydet(t, rdb, &model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued})

	if _, err := s.CancelRender(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("baska kullanici icin ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	job, err := jobOku(context.Background(), rdb, "is1")
	if err != nil {
		t.Fatalf("jobOku: %v", err)
	}
	if job.Status != model.JobStatusQueued {
		t.Errorf("baskasinin iptali isi degistirdi: %s", job.Status)
	}
}

func TestRenderService_BaskasininSonucu404(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, &sahteDepo{}, testR2Cfg())
	sonuc, _ := json.Marshal(model.RenderResultRecord{ID: "r1", Stems: []model.StemResultRecord{{ID: "s1", FileKey: "stems/kullanici-a/p1/s1.mp3"}}})
	isKaydet(t, rdb, &model.Job{ID: "is1", OwnerID: "kullanici-a", Status: model.JobStatusSucceeded, Result: sonuc})

	if _, err := s.GetResult(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestRenderService_SahipsizKayitKimseyeAcilmaz(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	isKaydet(t, rdb, &model.Job{ID: "eski", Status: model.JobStatusQueued})

	if _, err := s.GetStatus(context.Background(), "kullanici-a", "eski"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("OwnerID'siz kayit icin ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestMasterService_BaskasininIsi404(t *testing.T) {
	rdb := canliRedis(t)
	s := NewMasterService(rdb, nil, &sahteDepo{}, testR2Cfg())
	sonuc, _ := json.Marshal(model.MasterResultRecord{FileKey: "masters/kullanici-a/p1/m1.wav"})
	isKaydet(t, rdb, &model.Job{ID: "m1", Type: model.JobTypeMaster, OwnerID: "kullanici-a", Status: model.JobStatusSucceeded, Result: sonuc})

	if _, err := s.GetStatus(context.Background(), "kullanici-b", "m1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("status: ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	if _, err := s.GetResult(context.Background(), "kullanici-b", "m1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("result: ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	if _, err := s.GetResult(context.Background(), "kullanici-a", "m1"); err != nil {
		t.Errorf("sahibi sonucu okuyamadi: %v", err)
	}
}

// bekleyenGorevPayload, kuyruktaki tek gorevin is payload'ini cozer.
func bekleyenGorevPayload(t *testing.T, kuyruk string, hedef any) {
	t.Helper()
	insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer insp.Close()
	gorevler, err := insp.ListPendingTasks(kuyruk)
	if err != nil || len(gorevler) != 1 {
		t.Fatalf("bekleyen gorev = %d, hata %v", len(gorevler), err)
	}
	var zarf struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(gorevler[0].Payload, &zarf); err != nil {
		t.Fatalf("gorev zarfi: %v", err)
	}
	if err := json.Unmarshal(zarf.Payload, hedef); err != nil {
		t.Fatalf("gorev payload: %v", err)
	}
}

func TestRenderService_StartRenderSahibiYazar(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	resp, err := s.StartRender(context.Background(), "kullanici-a", &model.RenderStartRequest{ProjectID: uuid.New().String()})
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	job, err := jobOku(context.Background(), rdb, resp.JobID)
	if err != nil || job.OwnerID != "kullanici-a" {
		t.Fatalf("is kaydi sahibi = %q, hata %v", job.OwnerID, err)
	}
	var p model.RenderJobPayload
	bekleyenGorevPayload(t, "render", &p)
	if p.OwnerID != "kullanici-a" {
		t.Errorf("gorev payload sahibi = %q", p.OwnerID)
	}
}

func TestMasterService_StartFinalSahibiYazar(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	cfg := testR2Cfg()
	s := NewMasterService(rdb, ac, nil, cfg)

	req := &model.MasterFinalRequest{
		ProjectID: uuid.New().String(),
		StemURLs:  []string{client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg)},
	}
	resp, err := s.StartFinal(context.Background(), "kullanici-a", req)
	if err != nil {
		t.Fatalf("StartFinal: %v", err)
	}
	job, err := jobOku(context.Background(), rdb, resp.JobID)
	if err != nil || job.OwnerID != "kullanici-a" {
		t.Fatalf("is kaydi sahibi = %q, hata %v", job.OwnerID, err)
	}
	var p model.MasterJobPayload
	bekleyenGorevPayload(t, "master", &p)
	if p.OwnerID != "kullanici-a" {
		t.Errorf("gorev payload sahibi = %q", p.OwnerID)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/service/ -run 'BaskasininIsi|BaskasiIptal|BaskasininSonucu|SahipsizKayit|SahibiYazar' -v`
Expected: FAIL — derleme hatasi (`too many arguments in call to s.GetStatus` vb.)

- [ ] **Step 7: RenderService**

`internal/service/render_service.go`:

1. `StartRender` imzasi `func (s *RenderService) StartRender(ctx context.Context, userID string, req *model.RenderStartRequest) (*model.RenderStartResponse, error)`; `job` literaline `OwnerID: userID,`, `payload` literaline `OwnerID: userID,` ekle.
2. `GetStatus`, `GetResult`, `CancelRender` imzalarina `userID string` ekle (ctx'ten sonra, jobID'den once) ve ilk satirdaki `s.getJob(ctx, jobID)` cagrisini `sahipliJobOku(ctx, s.redis, userID, jobID)` yap.
3. `saveJob` ve `getJob` govdelerini su hale getir (worker'in kullandigi `UpdateJobProgress`/`CompleteJob`/`FailJob` sahiplik denetlemez, isi kendisi baslatmadi):

```go
func (s *RenderService) saveJob(ctx context.Context, job *model.Job) error {
	return jobYaz(ctx, s.redis, job)
}

func (s *RenderService) getJob(ctx context.Context, jobID string) (*model.Job, error) {
	return jobOku(ctx, s.redis, jobID)
}
```

- [ ] **Step 8: MasterService**

`internal/service/master_service.go`:

1. `StartFinal` imzasi `func (s *MasterService) StartFinal(ctx context.Context, userID string, req *model.MasterFinalRequest) (*model.MasterFinalResponse, error)`; `job` ve `payload` literallerine `OwnerID: userID,` ekle.
2. `GetStatus`, `GetResult` imzalarina `userID string` ekle; `s.getJob(ctx, jobID)` → `sahipliJobOku(ctx, s.redis, userID, jobID)`.
3. `saveJob`/`getJob` govdelerini Step 7'deki gibi `jobYaz`/`jobOku`'ya devret. Artik kullanilmayan importlari (`redis` hala `MasterService.redis` alaninda kullaniliyor; `errors` yoksa sorun yok) `go build` ile kontrol et.

- [ ] **Step 9: Handler yardimcisi ve handler'lar**

`internal/handler/istek_sahibi.go`:

```go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/middleware"
)

// istekSahibi, auth middleware'in yazdigi userId'yi okur. Bos gelmesi bir
// yapilandirma hatasidir; cagiran fail-closed 401 doner (sahipsiz is
// yaratmak ya da "bos sahip" ile is okumak yerine).
func istekSahibi(c *fiber.Ctx) (string, bool) {
	userID := middleware.GetUserID(c)
	return userID, userID != ""
}
```

`internal/handler/render.go`: `"errors"` importu ekle. `Start`, `Status`, `Result`, `Cancel`'in basina (Start'ta body parse'tan once):

```go
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
```

Servis cagrilarina `userID`'yi ctx'ten sonra gecir. `err.Error() == "job not found"` karsilastirmalarinin hepsini `errors.Is(err, service.ErrJobNotFound)` yap.

`internal/handler/master.go`: ayni degisiklikler `Final`, `Status`, `Result` icin (`Preview` Task 3'te).

- [ ] **Step 10: Handler canli testi**

`internal/handler/canli_redis_test.go`:

```go
package handler

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// canliRedis, yerel Redis ister; yoksa test atlanir. DB 12 kullanilir
// (service 13, middleware 14, kimlik 15).
func canliRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 12})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}
```

`internal/handler/sahiplik_canli_test.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// kullaniciOlarak, auth middleware'in yerine gecip userId yazar.
func kullaniciOlarak(userID string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("userId", userID)
		}
		return c.Next()
	}
}

func renderUygulamasi(t *testing.T, userID string) *fiber.App {
	t.Helper()
	rdb := canliRedis(t)
	ham, _ := json.Marshal(model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued, CreatedAt: time.Now()})
	if err := rdb.Set(context.Background(), "job:is1", ham, time.Hour).Err(); err != nil {
		t.Fatalf("is yazilamadi: %v", err)
	}
	h := NewRenderHandler(service.NewRenderService(rdb, nil, nil, &config.R2Config{}), validator.New())
	app := fiber.New()
	app.Get("/status/:jobId", kullaniciOlarak(userID), h.Status)
	app.Post("/cancel/:jobId", kullaniciOlarak(userID), h.Cancel)
	return app
}

func TestRenderHandler_BaskasininIsi404(t *testing.T) {
	app := renderUygulamasi(t, "kullanici-b")
	for _, istek := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/status/is1", nil),
		httptest.NewRequest(http.MethodPost, "/cancel/is1", nil),
	} {
		resp, err := app.Test(istek)
		if err != nil {
			t.Fatalf("istek: %v", err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, 404 bekleniyordu", istek.Method, istek.URL.Path, resp.StatusCode)
		}
	}
}

func TestRenderHandler_SahibiOkur(t *testing.T) {
	app := renderUygulamasi(t, "kullanici-a")
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/status/is1", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, 200 bekleniyordu", resp.StatusCode)
	}
}

func TestRenderHandler_KimliksizIstek401(t *testing.T) {
	app := renderUygulamasi(t, "")
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/status/is1", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, 401 bekleniyordu", resp.StatusCode)
	}
}
```

- [ ] **Step 11: Run all and verify**

Run: `go build ./... && go vet ./... && go test ./internal/...`
Expected: PASS (yerel Redis acikken canli testler kosar; `redis-cli ping` → PONG). Derleme hatasi veren baska cagiran varsa (ornegin `e2e/`) imzaya uydur.

- [ ] **Step 12: Commit**

```bash
gofmt -l internal cmd e2e   # bos olmali
git add internal/model internal/service internal/handler
git commit -m "feat(sahiplik): is kayitlarinda OwnerID, baskasinin isine 404"
```

---

### Task 3: Kullanici onekli R2 anahtar duzeni ve girdi denetimi

**Files:**
- Modify: `internal/service/upload_service.go`
- Modify: `internal/service/export_service.go`
- Modify: `internal/service/master_service.go` (Preview, StartFinal girdi denetimi)
- Modify: `internal/handler/upload.go`, `internal/handler/export.go`, `internal/handler/master.go`
- Modify: `internal/worker/render_worker.go`, `internal/worker/master_worker.go`
- Modify: `internal/service/sonuc_tazeleme_test.go`, `internal/service/master_service_test.go`, `internal/worker/render_worker_test.go`, `internal/worker/mock_adres_test.go`
- Create: `internal/service/anahtar_duzeni_test.go`
- Modify: `e2e/upload_test.go`

**Interfaces:**
- Consumes: Task 1 (`KullaniciAnahtari`, `SahipliAnahtarCoz`, `ErrGecersizSegment`, `ErrYabanciAnahtar`); Task 2 (`istekSahibi`, payload `OwnerID`, `StartFinal(ctx, userID, req)`)
- Produces:
  - `UploadService.UploadVocal(ctx, userID, projectID, sectionID, takeName string, file io.Reader, fileSize int64)`
  - `UploadService.DeleteVocal(ctx, userID, projectID, sectionID, takeID string) error`
  - `ExportService.ExportMP3/ExportWAV/ExportStems(ctx, userID string, req)`
  - `MasterService.Preview(ctx, userID string, req)`
  - `DELETE /api/upload/vocal/:takeId?projectId=&sectionId=` (ikisi de zorunlu)

- [ ] **Step 1: Write the failing service tests**

`internal/service/anahtar_duzeni_test.go`:

```go
package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// sahteSes, audio-service cagrilarini kaydeder.
type sahteSes struct {
	encode *client.EncodeRequest
	zip    *client.ZipRequest
}

func (s *sahteSes) Master(context.Context, *client.MasterRequest) (*client.MasterResponse, error) {
	return &client.MasterResponse{}, nil
}
func (s *sahteSes) Encode(_ context.Context, r *client.EncodeRequest) (*client.EncodeResponse, error) {
	s.encode = r
	return &client.EncodeResponse{}, nil
}
func (s *sahteSes) CreateZip(_ context.Context, r *client.ZipRequest) (*client.ZipResponse, error) {
	s.zip = r
	return &client.ZipResponse{}, nil
}
func (s *sahteSes) HealthCheck(context.Context) error { return nil }

func TestUploadService_AnahtarKullaniciOnekli(t *testing.T) {
	depo := &sahteDepo{}
	s := NewUploadService(depo, testR2Cfg())

	resp, err := s.UploadVocal(context.Background(), "kullanici-a", "p1", "s1", "take1", nil, 0)
	if err != nil {
		t.Fatalf("UploadVocal: %v", err)
	}
	beklenen := "vocals/kullanici-a/p1/s1/" + resp.ID + ".wav"
	if len(depo.yuklenen) != 1 || depo.yuklenen[0] != beklenen {
		t.Errorf("yuklenen = %v, beklenen %q", depo.yuklenen, beklenen)
	}
}

func TestUploadService_GecersizSegmentYuklenmez(t *testing.T) {
	depo := &sahteDepo{}
	s := NewUploadService(depo, testR2Cfg())

	_, err := s.UploadVocal(context.Background(), "kullanici-a", "../kullanici-b", "s1", "take1", nil, 0)
	if !errors.Is(err, client.ErrGecersizSegment) {
		t.Fatalf("ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
	if len(depo.yuklenen) != 0 {
		t.Errorf("gecersiz istekte yukleme yapildi: %v", depo.yuklenen)
	}
}

// silenDepo, Delete'e giden anahtari kaydeder.
type silenDepo struct {
	sahteDepo
	silinen []string
}

func (d *silenDepo) Delete(_ context.Context, key string) error {
	d.silinen = append(d.silinen, key)
	return nil
}

func TestUploadService_SilmeDeterministikAnahtar(t *testing.T) {
	depo := &silenDepo{}
	s := NewUploadService(depo, testR2Cfg())

	if err := s.DeleteVocal(context.Background(), "kullanici-a", "p1", "s1", "t1"); err != nil {
		t.Fatalf("DeleteVocal: %v", err)
	}
	if len(depo.silinen) != 1 || depo.silinen[0] != "vocals/kullanici-a/p1/s1/t1.wav" {
		t.Errorf("silinen = %v", depo.silinen)
	}
	if err := s.DeleteVocal(context.Background(), "kullanici-a", "p1", "s1", "*"); !errors.Is(err, client.ErrGecersizSegment) {
		t.Errorf("glob takeId icin ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
}

func TestExportService_BaskasininMasteriReddedilir(t *testing.T) {
	cfg := testR2Cfg()
	ses := &sahteSes{}
	s := NewExportService(&sahteDepo{}, ses, cfg)

	yabanci := client.UnsignedURL("masters/kullanici-b/p1/m1.wav", cfg)
	_, err := s.ExportMP3(context.Background(), "kullanici-a", &model.ExportMP3Request{MasterFileURL: yabanci})
	if !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if ses.encode != nil {
		t.Error("yabanci girdiyle audio-service cagrildi")
	}
}

func TestExportService_CiktiKullaniciOnekli(t *testing.T) {
	cfg := testR2Cfg()
	ses := &sahteSes{}
	s := NewExportService(&sahteDepo{}, ses, cfg)

	kendi := client.UnsignedURL("masters/kullanici-a/p1/m1.wav", cfg)
	if _, err := s.ExportWAV(context.Background(), "kullanici-a", &model.ExportWAVRequest{MasterFileURL: kendi}); err != nil {
		t.Fatalf("ExportWAV: %v", err)
	}
	if ses.encode.InputKey != "masters/kullanici-a/p1/m1.wav" || !strings.HasPrefix(ses.encode.OutputKey, "exports/kullanici-a/") {
		t.Errorf("encode istegi = %+v", ses.encode)
	}
}

func TestExportService_ZipteYabanciStemReddedilir(t *testing.T) {
	cfg := testR2Cfg()
	ses := &sahteSes{}
	s := NewExportService(&sahteDepo{}, ses, cfg)

	req := &model.ExportStemsRequest{StemURLs: []string{
		client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg),
		client.UnsignedURL("stems/kullanici-b/p1/s2.mp3", cfg),
	}}
	if _, err := s.ExportStems(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if ses.zip != nil {
		t.Error("yabanci stem ile zip olusturuldu")
	}
}

func TestMasterService_StartFinalYabanciStemReddedilir(t *testing.T) {
	cfg := testR2Cfg()
	s := NewMasterService(nil, nil, nil, cfg)

	req := &model.MasterFinalRequest{StemURLs: []string{client.UnsignedURL("stems/kullanici-b/p1/s1.mp3", cfg)}}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Errorf("stem: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}

	req = &model.MasterFinalRequest{
		StemURLs:   []string{client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg)},
		VocalTakes: []model.VocalTake{{FileURL: client.UnsignedURL("vocals/kullanici-b/p1/s1/t1.wav", cfg)}},
	}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Errorf("vokal: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
}
```

Not: `TestMasterService_StartFinalYabanciStemReddedilir` redis'i nil verir; denetim is kaydi yazilmadan ONCE yapildigi icin nil'e hic dokunulmaz. Denetim sonraya kayarsa test panik ile kirilir — bu bilerek boyle.

Mevcut testleri yeni imzalara uydur:
- `internal/service/sonuc_tazeleme_test.go`: iki `s.UploadVocal(context.Background(), "p1", "s1", "take1", nil, 0)` cagrisi → `s.UploadVocal(context.Background(), "kullanici-a", "p1", "s1", "take1", nil, 0)`. `TestUploadService_MockAdresi_KeyFromURLIleCozulur`'da `beklenenAnahtar` → `fmt.Sprintf("vocals/kullanici-a/p1/s1/%s.wav", resp.ID)`.
- `internal/service/master_service_test.go`: `s.Preview(context.Background(), &model.MasterPreviewRequest{})` → `s.Preview(context.Background(), "kullanici-a", &model.MasterPreviewRequest{})`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/service/ -v -run 'AnahtarKullanici|GecersizSegment|SilmeDeterministik|Baskasinin|CiktiKullanici|Zipte|StartFinalYabanci'`
Expected: FAIL — derleme hatasi (`too many arguments`)

- [ ] **Step 3: UploadService**

`internal/service/upload_service.go` — `FileUploader` arayuzu, `UploadVocal`, `DeleteVocal`, `uploadMock`; `DeleteVocalByKey` SILINIR:

```go
// FileUploader defines the interface for file upload operations
type FileUploader interface {
	UploadVocal(ctx context.Context, userID, projectID, sectionID, takeName string, file io.Reader, fileSize int64) (*model.UploadVocalResponse, error)
	DeleteVocal(ctx context.Context, userID, projectID, sectionID, takeID string) error
}
```

```go
// UploadVocal uploads a vocal recording to R2 storage
func (s *UploadService) UploadVocal(ctx context.Context, userID, projectID, sectionID, takeName string, file io.Reader, fileSize int64) (*model.UploadVocalResponse, error) {
	takeID := uuid.New().String()

	// Anahtar kullaniciyla oneklenir; istemci kimlikleri segment
	// dogrulamasindan gecer. Mock yol da ayni dogrulamayi yapar.
	key, err := client.KullaniciAnahtari("vocals", userID, projectID, sectionID, takeID+".wav")
	if err != nil {
		return nil, err
	}

	if s.r2Client == nil {
		return s.uploadMock(takeID, key)
	}
	// ... (Upload cagrisi ve yanit degismez)
}

// DeleteVocal, vokali deterministik anahtariyla siler. Eskiden anahtar
// "vocals/*/<id>.wav" literal glob'uydu ve hicbir nesneyle eslesmiyordu.
func (s *UploadService) DeleteVocal(ctx context.Context, userID, projectID, sectionID, takeID string) error {
	key, err := client.KullaniciAnahtari("vocals", userID, projectID, sectionID, takeID+".wav")
	if err != nil {
		return err
	}
	if s.r2Client == nil {
		return nil // Mock: no-op
	}
	return s.r2Client.Delete(ctx, key)
}
```

`uploadMock` imzasi `func (s *UploadService) uploadMock(takeID, key string) (*model.UploadVocalResponse, error)`; govdedeki `key := fmt.Sprintf(...)` satiri silinir. `fmt` artik kullanilmiyorsa importtan cikar.

- [ ] **Step 4: ExportService**

`internal/service/export_service.go`:

1. `FileExporter` arayuzu ve uc metodun imzasina `userID string` ekle (ctx'ten sonra).
2. `ExportMP3`: mock dalindan sonra
```go
	exportID := uuid.New().String()
	outputKey, err := client.KullaniciAnahtari("exports", userID, exportID+".mp3")
	if err != nil {
		return nil, err
	}
```
ve `client.KeyFromURL(req.MasterFileURL, s.r2Cfg)` → `client.SahipliAnahtarCoz(req.MasterFileURL, userID, s.r2Cfg)`. (Hata sarmasi `%w` ile kaldigi icin `errors.Is` calisir.)
3. `ExportWAV`: ayni, uzanti `.wav`.
4. `ExportStems`: `outputKey` `.zip` ile ayni sekilde; uc `client.KeyFromURL(url, s.r2Cfg)` / `client.KeyFromURL(req.MasterURL, s.r2Cfg)` cagrisi → `client.SahipliAnahtarCoz(..., userID, s.r2Cfg)`.
5. Mock'lar: `exportMP3Mock(quality)` → `exportMP3Mock(userID string, quality int)`, govdede
```go
	key, err := client.KullaniciAnahtari("exports", userID, uuid.New().String()+".mp3")
	if err != nil {
		return nil, err
	}
	return &model.ExportMP3Response{FileURL: client.UnsignedURL(key, s.r2Cfg), ...
```
`exportWAVMock(userID, bitDepth, sampleRate)` ve `exportStemsMock(userID, req)` ayni kalipla. Cagri yerlerini guncelle.

- [ ] **Step 5: MasterService Preview ve StartFinal girdi denetimi**

`internal/service/master_service.go`:

`Preview` imzasi `func (s *MasterService) Preview(ctx context.Context, userID string, req *model.MasterPreviewRequest) (*model.MasterPreviewResponse, error)`; govde:

```go
	previewID := uuid.New().String()
	key, err := client.KullaniciAnahtari("previews", userID, previewID+".mp3")
	if err != nil {
		return nil, err
	}

	// Mock adres bile ayni kurallarla uretilir: istemci bunu geri
	// gonderdiginde KeyFromURL cozebilsin.
	fileURL := client.UnsignedURL(key, s.r2Cfg)
```

`StartFinal`'in EN BASINA (jobID uretmeden once):

```go
	// Girdi denetimi is kaydindan ONCE: worker bu anahtarlari okuyacak,
	// yabanci bir anahtar baskasinin dosyasini master'a sokardi.
	for _, u := range req.StemURLs {
		if _, err := client.SahipliAnahtarCoz(u, userID, s.r2Cfg); err != nil {
			return nil, fmt.Errorf("stem URL: %w", err)
		}
	}
	for _, take := range req.VocalTakes {
		if _, err := client.SahipliAnahtarCoz(take.FileURL, userID, s.r2Cfg); err != nil {
			return nil, fmt.Errorf("vokal URL: %w", err)
		}
	}
```

- [ ] **Step 6: Handler'lar**

`internal/handler/upload.go` — `"errors"` ve `"github.com/makeasinger/api/internal/client"` importlari. `Vocal`'in basina `istekSahibi` blogu (Task 2'deki 401 kalibi). Servis cagrisi:

```go
	result, err := h.service.UploadVocal(c.Context(), userID, projectID, sectionID, takeName, f, file.Size)
	if err != nil {
		if errors.Is(err, client.ErrGecersizSegment) {
			return response.ValidationError(c, "Invalid projectId or sectionId", nil)
		}
		return response.ServiceError(c, err.Error())
	}
```

`DeleteVocal`: swagger yorumuna iki satir ekle:

```go
// @Param        projectId query string true "Project ID"
// @Param        sectionId query string true "Section ID"
```

govde:

```go
func (h *UploadHandler) DeleteVocal(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	takeID := c.Params("takeId")
	projectID := c.Query("projectId")
	sectionID := c.Query("sectionId")
	if takeID == "" || projectID == "" || sectionID == "" {
		return response.ValidationError(c, "takeId, projectId and sectionId are required", nil)
	}

	if err := h.service.DeleteVocal(c.Context(), userID, projectID, sectionID, takeID); err != nil {
		if errors.Is(err, client.ErrGecersizSegment) {
			return response.ValidationError(c, "Invalid takeId, projectId or sectionId", nil)
		}
		return response.ServiceError(c, err.Error())
	}

	return response.NoContent(c)
}
```

`internal/handler/export.go` — uc ucun her birinde `istekSahibi` blogu, servis cagrisina `userID`, ve hata eslemesi:

```go
	if err != nil {
		if errors.Is(err, client.ErrYabanciAnahtar) {
			return response.NotFound(c, "File not found")
		}
		return response.ServiceError(c, err.Error())
	}
```

`internal/handler/master.go` — `Preview`'a `istekSahibi` + `userID`; `Final`'in hata eslemesine ayni `ErrYabanciAnahtar → NotFound("File not found")` dali. Swagger'da `Final`, `MP3`, `WAV`, `Stems` icin `// @Failure      404 {object} response.ErrorResponse` satiri ekle.

- [ ] **Step 7: Render worker**

`internal/worker/render_worker.go`:

1. `sunoIleUret`'in basinda, `w.r2Client == nil` kontrolunden hemen sonra:

```go
	// Ucretli cagridan ONCE: sahip/proje anahtar kurmaya uygun degilse
	// stem'ler yuklenemez ve 62 kredi bosa gider.
	if _, err := client.KullaniciAnahtari("stems", payload.OwnerID, payload.ProjectID, "x.mp3"); err != nil {
		return nil, fmt.Errorf("is sahibi/proje anahtara uygun degil, Suno cagrilmadi: %w: %w", err, asynq.SkipRetry)
	}
```

2. `w.uploadStems(ctx, payload.ProjectID, stems, sure)` → `w.uploadStems(ctx, payload.OwnerID, payload.ProjectID, stems, sure)`; `uploadStems` imzasi `(ctx context.Context, ownerID, projectID string, stems []client.Stem, duration float64)`, anahtar:

```go
		key, err := client.KullaniciAnahtari("stems", ownerID, projectID, stemID+".mp3")
		if err != nil {
			return nil, err
		}
```

3. `generateMockResult(payload)` imzasi `(*model.RenderResultRecord, error)` olur; dongudeki anahtar:

```go
		key, err := client.KullaniciAnahtari("stems", payload.OwnerID, payload.ProjectID, string(instrument)+".wav")
		if err != nil {
			return nil, err
		}
```

ve `FileKey: key`; sonda `return &model.RenderResultRecord{...}, nil`. `processWithMock`'ta:

```go
	result, err := w.generateMockResult(payload)
	if err != nil {
		w.failJob(ctx, jobID, "Gecersiz is sahibi veya proje")
		return fmt.Errorf("mock sonuc: %w: %w", err, asynq.SkipRetry)
	}
```

- [ ] **Step 8: Master worker**

`internal/worker/master_worker.go`:

1. `processWithAudioService` stem dongusunde `client.KeyFromURL(u, w.r2Cfg)` → `client.SahipliAnahtarCoz(u, payload.OwnerID, w.r2Cfg)`.
2. `buildVocalTakes`'te `client.KeyFromURL(take.FileURL, w.r2Cfg)` → `client.SahipliAnahtarCoz(take.FileURL, payload.OwnerID, w.r2Cfg)`.
3. `outputKey`:

```go
	outputKey, err := client.KullaniciAnahtari("masters", payload.OwnerID, payload.ProjectID, uuid.New().String()+".wav")
	if err != nil {
		w.failJob(ctx, jobID, "Gecersiz is sahibi veya proje")
		return fmt.Errorf("master anahtari: %w: %w", err, asynq.SkipRetry)
	}
```

(`asynq` zaten import edili; degilse ekle.)

4. `generateMockRecord` imzasi `(*model.MasterResultRecord, error)`:

```go
func (w *MasterWorker) generateMockRecord(payload *model.MasterJobPayload) (*model.MasterResultRecord, error) {
	key, err := client.KullaniciAnahtari("masters", payload.OwnerID, payload.ProjectID, uuid.New().String()+".wav")
	if err != nil {
		return nil, err
	}
	return &model.MasterResultRecord{
		FileKey:  key,
		Duration: 180.5,
		Profile:  payload.Profile,
		PeakDb:   -0.3,
		LUFS:     -14,
	}, nil
}
```

`processWithMock`'ta `record := w.generateMockRecord(payload)` →

```go
	record, err := w.generateMockRecord(payload)
	if err != nil {
		w.failJob(ctx, jobID, "Gecersiz is sahibi veya proje")
		return fmt.Errorf("mock kayit: %w: %w", err, asynq.SkipRetry)
	}
```

(sonraki `fileURL, expiresAt, err := ...` satiri `err` zaten tanimli oldugu icin `fileURL, expiresAt, err := ` derlenir; cunku yeni degiskenler var.)

- [ ] **Step 9: Worker testleri**

`internal/worker/render_worker_test.go`:
- `testPayload()` → `&model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", Brief: ...}`
- `TestSunoIleUret_MutluYol` icindeki `strings.HasPrefix(y.key, "stems/p1/")` → `strings.HasPrefix(y.key, "stems/kullanici-a/p1/")`
- Yeni test ekle:

```go
func TestSunoIleUret_SahipsizIsteSunoCagrilmaz(t *testing.T) {
	s := &sahteSuno{}
	p := testPayload()
	p.OwnerID = ""
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if s.uretimSayisi != 0 {
		t.Errorf("sahipsiz iste Suno %d kez cagrildi", s.uretimSayisi)
	}
}
```

`internal/worker/mock_adres_test.go`:
- `w.generateMockRecord(&model.MasterJobPayload{ProjectID: "p1"})` → `record, err := w.generateMockRecord(&model.MasterJobPayload{OwnerID: "kullanici-a", ProjectID: "p1"})` + `if err != nil { t.Fatalf("generateMockRecord: %v", err) }`; onek kontrolu `"masters/kullanici-a/p1/"`.
- `payload := &model.RenderJobPayload{ProjectID: "p1"}` → `OwnerID: "kullanici-a"` ekle; `record := w.generateMockResult(payload)` → `record, err := ...` + Fatalf; onek kontrolu `"stems/kullanici-a/p1/"`.

- [ ] **Step 10: e2e silme testi**

`e2e/upload_test.go` `TestDeleteVocal_Success`:

```go
	path := fmt.Sprintf("/api/upload/vocal/%s?projectId=%s&sectionId=s1", takeID, uuid.New().String())
```

- [ ] **Step 11: Run all and verify**

Run: `go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd e2e`
Expected: PASS, gofmt ciktisi bos. Ayrica `grep -rn 'fmt.Sprintf("\(vocals\|masters\|stems\|exports\|previews\)/' internal` bos donmeli (anahtarlar yalnizca `KullaniciAnahtari` ile kurulur; `export_service.go`'daki zip ici dosya adlari `"stems/stem_%d.wav"` bir R2 anahtari degil, istisnadir).

- [ ] **Step 12: Commit**

```bash
git add internal e2e
git commit -m "feat(r2): kullanici onekli anahtarlar, girdi URL'lerinde sahip denetimi, deterministik vokal silme"
```

---

### Task 4: WebSocket bileti

**Files:**
- Modify: `internal/model/websocket.go`
- Create: `internal/service/ws_ticket_service.go`
- Create: `internal/service/ws_ticket_canli_test.go`
- Create: `internal/handler/ws_ticket.go`
- Create: `internal/handler/ws_ticket_canli_test.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: Task 2 (`sahipliJobOku`, `ErrJobNotFound`, `jobYaz`, `istekSahibi`, `canliRedis`, `kullaniciOlarak`)
- Produces:
  - `model.WSTicketRequest{JobID string}` (`json:"jobId" validate:"required,uuid"`), `model.WSTicketResponse{Ticket string; ExpiresAt time.Time}`
  - `service.ErrBiletGecersiz`, `service.NewWSTicketService(*redis.Client) *WSTicketService`, `Issue(ctx, userID, jobID string) (*model.WSTicketResponse, error)`, `Redeem(ctx, bilet, jobID string) error`
  - `handler.NewWSTicketHandler(*service.WSTicketService, *validator.Validate) *WSTicketHandler`, `Issue`, `Gate`
  - Rotalar: `POST /api/ws/ticket`, `GET /ws/jobs/:jobId?ticket=<bilet>`

- [ ] **Step 1: Model**

`internal/model/websocket.go` sonuna (`time` importu ekle):

```go
// WSTicketRequest, bir isin WebSocket yayinina baglanmak icin bilet ister.
type WSTicketRequest struct {
	JobID string `json:"jobId" validate:"required,uuid"`
}

// WSTicketResponse: bilet tek kullanimliktir ve ExpiresAt'e kadar gecerlidir.
// Istemci onu GET /ws/jobs/{jobId}?ticket=<bilet> ile kullanir.
type WSTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
}
```

- [ ] **Step 2: Write the failing service test**

`internal/service/ws_ticket_canli_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/model"
)

func biletServisi(t *testing.T) *WSTicketService {
	t.Helper()
	rdb := canliRedis(t)
	isKaydet(t, rdb, &model.Job{ID: "is1", OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	isKaydet(t, rdb, &model.Job{ID: "is2", OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	return NewWSTicketService(rdb)
}

func TestWSBilet_TekKullanimlik(t *testing.T) {
	s := biletServisi(t)
	b, err := s.Issue(context.Background(), "kullanici-a", "is1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(b.Ticket) != 43 || time.Until(b.ExpiresAt) > 31*time.Second {
		t.Errorf("bilet = %q, son %v", b.Ticket, b.ExpiresAt)
	}
	if err := s.Redeem(context.Background(), b.Ticket, "is1"); err != nil {
		t.Fatalf("ilk kullanim reddedildi: %v", err)
	}
	if err := s.Redeem(context.Background(), b.Ticket, "is1"); !errors.Is(err, ErrBiletGecersiz) {
		t.Errorf("ikinci kullanimda ErrBiletGecersiz bekleniyordu, geldi: %v", err)
	}
}

func TestWSBilet_BaskasininIsineVerilmez(t *testing.T) {
	s := biletServisi(t)
	if _, err := s.Issue(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestWSBilet_BaskaIseKullanilamaz(t *testing.T) {
	s := biletServisi(t)
	b, err := s.Issue(context.Background(), "kullanici-a", "is1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Redeem(context.Background(), b.Ticket, "is2"); !errors.Is(err, ErrBiletGecersiz) {
		t.Errorf("is1 bileti is2'de ErrBiletGecersiz vermeli, geldi: %v", err)
	}
}

func TestWSBilet_BicimsizBiletRedisiSormadanReddedilir(t *testing.T) {
	s := NewWSTicketService(nil) // nil Redis: dokunulursa panik
	for _, b := range []string{"", "kisa", "wsbilet:*", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"} {
		if err := s.Redeem(context.Background(), b, "is1"); !errors.Is(err, ErrBiletGecersiz) {
			t.Errorf("%q icin ErrBiletGecersiz bekleniyordu, geldi: %v", b, err)
		}
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/service/ -run WSBilet -v`
Expected: FAIL — `undefined: WSTicketService`

- [ ] **Step 4: Service**

`internal/service/ws_ticket_service.go`:

```go
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// wsBiletTTL: bilet alma ile baglanma arasi icin yeterli, sizarsa kisa.
const wsBiletTTL = 30 * time.Second

// ErrBiletGecersiz: bilet yok, suresi dolmus, kullanilmis ya da baska bir
// ise ait. Durumlar bilerek ayirt edilmez.
var ErrBiletGecersiz = errors.New("websocket bileti gecersiz")

// biletDeseni: 32 bayt base64url (dolgusuz) = 43 karakter. Bicimsiz girdi
// Redis'e hic gitmez.
var biletDeseni = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// WSTicketService, WebSocket baglantisi icin tek kullanimlik bilet uretir.
// React Native'in WebSocket'i iOS'ta ozel baslik gonderemiyor; access
// token'i sorgu dizesine koymak onu loglara tasirdi. Bilet 30 saniyelik ve
// tek kullanimlik oldugu icin sorgu dizesinde durabilir.
type WSTicketService struct {
	redis *redis.Client
}

func NewWSTicketService(rdb *redis.Client) *WSTicketService {
	return &WSTicketService{redis: rdb}
}

func biletAnahtari(bilet string) string { return "wsbilet:" + bilet }

// Issue, isin sahibine bilet verir. Sahiplik burada denetlenir; bilet o
// ise baglidir.
func (s *WSTicketService) Issue(ctx context.Context, userID, jobID string) (*model.WSTicketResponse, error) {
	if _, err := sahipliJobOku(ctx, s.redis, userID, jobID); err != nil {
		return nil, err
	}
	ham := make([]byte, 32)
	if _, err := rand.Read(ham); err != nil {
		return nil, fmt.Errorf("bilet uretilemedi: %w", err)
	}
	bilet := base64.RawURLEncoding.EncodeToString(ham)
	if err := s.redis.Set(ctx, biletAnahtari(bilet), jobID, wsBiletTTL).Err(); err != nil {
		return nil, fmt.Errorf("bilet kaydedilemedi: %w", err)
	}
	return &model.WSTicketResponse{Ticket: bilet, ExpiresAt: time.Now().Add(wsBiletTTL)}, nil
}

// Redeem, bileti tuketir. GETDEL atomiktir: ayni bilet iki baglantida
// kullanilamaz. Yanlis ise sunulan bilet de tuketilir.
func (s *WSTicketService) Redeem(ctx context.Context, bilet, jobID string) error {
	if !biletDeseni.MatchString(bilet) {
		return ErrBiletGecersiz
	}
	isID, err := s.redis.GetDel(ctx, biletAnahtari(bilet)).Result()
	if errors.Is(err, redis.Nil) {
		return ErrBiletGecersiz
	}
	if err != nil {
		return fmt.Errorf("bilet okunamadi: %w", err)
	}
	if isID != jobID {
		return ErrBiletGecersiz
	}
	return nil
}
```

Run: `go test ./internal/service/ -run WSBilet -v` → PASS

- [ ] **Step 5: Write the failing handler test**

`internal/handler/ws_ticket_canli_test.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

const biletIsID = "0b6f3c1e-8a52-4d0e-9a51-6d3f8f2b7c11"

func biletUygulamasi(t *testing.T, userID string) *fiber.App {
	t.Helper()
	rdb := canliRedis(t)
	ham, _ := json.Marshal(model.Job{ID: biletIsID, OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	if err := rdb.Set(context.Background(), "job:"+biletIsID, ham, time.Hour).Err(); err != nil {
		t.Fatalf("is yazilamadi: %v", err)
	}
	h := NewWSTicketHandler(service.NewWSTicketService(rdb), validator.New())
	app := fiber.New()
	app.Post("/api/ws/ticket", kullaniciOlarak(userID), h.Issue)
	// Gercek rotada Gate'ten sonra websocket.New gelir; burada upgrade
	// olmadan Gate'in karari olculur.
	app.Get("/ws/jobs/:jobId", h.Gate, func(c *fiber.Ctx) error { return c.SendString("gecti") })
	return app
}

func biletAl(t *testing.T, app *fiber.App) (*http.Response, model.WSTicketResponse) {
	t.Helper()
	istek := httptest.NewRequest(http.MethodPost, "/api/ws/ticket", strings.NewReader(`{"jobId":"`+biletIsID+`"}`))
	istek.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(istek)
	if err != nil {
		t.Fatalf("bilet istegi: %v", err)
	}
	var b model.WSTicketResponse
	govde, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(govde, &b)
	return resp, b
}

func TestWSTicketHandler_SahibiBiletleBaglanir(t *testing.T) {
	app := biletUygulamasi(t, "kullanici-a")
	resp, b := biletAl(t, app)
	if resp.StatusCode != http.StatusOK || b.Ticket == "" {
		t.Fatalf("bilet = %d %+v", resp.StatusCode, b)
	}

	yol := "/ws/jobs/" + biletIsID + "?ticket=" + b.Ticket
	ilk, _ := app.Test(httptest.NewRequest(http.MethodGet, yol, nil))
	if ilk.StatusCode != http.StatusOK {
		t.Errorf("ilk baglanti = %d, 200 bekleniyordu", ilk.StatusCode)
	}
	ikinci, _ := app.Test(httptest.NewRequest(http.MethodGet, yol, nil))
	if ikinci.StatusCode != http.StatusUnauthorized {
		t.Errorf("ikinci baglanti = %d, 401 bekleniyordu", ikinci.StatusCode)
	}
}

func TestWSTicketHandler_BaskasinaBilet404(t *testing.T) {
	app := biletUygulamasi(t, "kullanici-b")
	resp, _ := biletAl(t, app)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, 404 bekleniyordu", resp.StatusCode)
	}
}

func TestWSTicketHandler_BiletsizBaglanti401(t *testing.T) {
	app := biletUygulamasi(t, "kullanici-a")
	resp, _ := app.Test(httptest.NewRequest(http.MethodGet, "/ws/jobs/"+biletIsID, nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, 401 bekleniyordu", resp.StatusCode)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/handler/ -run WSTicketHandler -v`
Expected: FAIL — `undefined: NewWSTicketHandler`

- [ ] **Step 7: Handler**

`internal/handler/ws_ticket.go`:

```go
package handler

import (
	"errors"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type WSTicketHandler struct {
	service   *service.WSTicketService
	validator *validator.Validate
}

func NewWSTicketHandler(svc *service.WSTicketService, v *validator.Validate) *WSTicketHandler {
	return &WSTicketHandler{service: svc, validator: v}
}

// Issue handles POST /api/ws/ticket
// @Summary      Get WebSocket ticket
// @Description  Issue a single-use, 30-second ticket for GET /ws/jobs/{jobId}?ticket=...
// @Tags         WebSocket
// @Accept       json
// @Produce      json
// @Param        request body model.WSTicketRequest true "Ticket request"
// @Success      200 {object} model.WSTicketResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/ws/ticket [post]
func (h *WSTicketHandler) Issue(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var req model.WSTicketRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.Issue(c.Context(), userID, req.JobID)
	if err != nil {
		if errors.Is(err, service.ErrJobNotFound) {
			return response.NotFound(c, "Job not found")
		}
		return response.ServiceError(c, "Bilet uretilemedi")
	}
	return response.OK(c, result)
}

// Gate, WebSocket upgrade'inden ONCE bileti tuketir. Upgrade sonrasi
// reddetmek, HTTP durum kodu yerine kapanis cercevesi demek olurdu.
func (h *WSTicketHandler) Gate(c *fiber.Ctx) error {
	err := h.service.Redeem(c.Context(), c.Query("ticket"), c.Params("jobId"))
	if errors.Is(err, service.ErrBiletGecersiz) {
		return response.Unauthorized(c, "Gecersiz WebSocket bileti")
	}
	if err != nil {
		return response.ServiceError(c, "Bilet denetlenemedi")
	}
	return c.Next()
}
```

- [ ] **Step 8: main.go baglantisi**

`cmd/server/main.go`:
- Servisler blogunun sonuna: `wsTicketService := service.NewWSTicketService(redisClient)`
- Handler'lar blogunun sonuna: `wsTicketHandler := handler.NewWSTicketHandler(wsTicketService, validate)`
- Upload rotalarindan sonra:
```go
	// WebSocket bileti: kimligi dogrulanmis istekle alinir.
	api.Post("/ws/ticket", wsTicketHandler.Issue)
```
- WebSocket rotasi:
```go
	// Bilet upgrade'den once tuketilir; bilet yoksa ya da baska isinse 401.
	app.Get("/ws/jobs/:jobId", wsTicketHandler.Gate, websocket.New(func(c *websocket.Conn) {
		jobID := c.Params("jobId")
		hub.HandleConnection(c, jobID)
	}))
```

- [ ] **Step 9: Run all and verify**

Run: `go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd e2e`
Expected: PASS, gofmt bos.

- [ ] **Step 10: Commit**

```bash
git add internal/model/websocket.go internal/service/ws_ticket_service.go internal/service/ws_ticket_canli_test.go internal/handler/ws_ticket.go internal/handler/ws_ticket_canli_test.go cmd/server/main.go
git commit -m "feat(ws): tek kullanimlik WebSocket bileti, baskasinin isi dinlenemez"
```

---

### Task 5: Dokuman ve Swagger

**Files:**
- Modify: `CLAUDE.md` (repo koku, `make-singer-backend/CLAUDE.md`)
- Modify: `core-service/docs/API.md`
- Regenerate: `core-service/docs/docs.go`, `swagger.json`, `swagger.yaml`

- [ ] **Step 1: CLAUDE.md**

`### Job lifecycle` paragrafinin sonuna ekle:

```markdown
Each job record carries `ownerId` (the `sub` of the user who started it). `status`, `result` and `cancel` load the job through `sahipliJobOku` (`internal/service/job_kayit.go`) and answer **404** — not 403 — when the caller is not the owner; records without `ownerId` are visible to nobody.
```

`### Auth` bolumunun sonuna ekle:

```markdown
**Ownership (Phase 3):** R2 keys are prefixed with the owner: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3`, `exports/<userId>/<exportId>.<ext>`, `previews/<userId>/<id>.mp3`. Keys are only built with `client.KullaniciAnahtari`, which rejects any client-supplied segment containing `/` or `..`. Every URL a client sends back (master stems/vocals, export inputs) is resolved with `client.SahipliAnahtarCoz`; a key outside the caller's prefix answers 404. `DELETE /api/upload/vocal/:takeId` requires `?projectId=&sectionId=`.

**WebSocket:** `GET /ws/jobs/:jobId` requires `?ticket=` from `POST /api/ws/ticket` (body `{"jobId"}`, owner only). Tickets live 30 s in Redis (`wsbilet:<ticket>`) and are consumed with `GETDEL` before the upgrade.
```

`### Key layers` icindeki `- **websocket/** — ... via \`GET /ws/jobs/:jobId\`.` satirini `... via \`GET /ws/jobs/:jobId?ticket=\` (ticket from \`POST /api/ws/ticket\`).` yap.

- [ ] **Step 2: API.md**

`core-service/docs/API.md`'de WebSocket ve upload silme bolumlerini bul (`grep -n "ws/jobs\|upload/vocal" core-service/docs/API.md`) ve Step 1'deki iki kurali ayni dilde (dosyanin dili neyse) ekle: bilet akisi (istek/yanit ornegi `{"jobId":"..."}` → `{"ticket":"...","expiresAt":"..."}`) ve silmenin zorunlu sorgu parametreleri. Is uclarinin baskasinin isine 404 dondugunu ekle.

- [ ] **Step 3: Swagger**

Run: `cd core-service && swag init -g cmd/server/main.go`
Expected: `docs/docs.go`, `swagger.json`, `swagger.yaml` guncellenir; `grep -c "ws/ticket" docs/swagger.json` ≥ 1.

- [ ] **Step 4: Son dogrulama**

Run: `cd core-service && go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd e2e`
Expected: PASS, gofmt bos.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md core-service/docs
git commit -m "docs: Faz 3 sahiplik, anahtar duzeni ve WebSocket bileti"
```

---

## Mobil icin kirici degisiklikler (ayri plan: `make-singer-mobile`)

Bu plan tek basina yayina alinabilir: API Faz 4'e kadar disa kapali, bugun backend'i kullanan istemci yok. Mobil plani su sozlesmeyi tuketir:

1. WebSocket: once `POST /api/ws/ticket` `{"jobId"}` → `{"ticket","expiresAt"}`, sonra `ws(s)://<api>/ws/jobs/<jobId>?ticket=<ticket>`. Her (yeniden) baglanti yeni bilet ister.
2. `DELETE /api/upload/vocal/<takeId>?projectId=<p>&sectionId=<s>`.
3. Is ve dosya uclari baskasinin kaynagina 404 doner; istemcinin geri gonderdigi URL'ler kendi aldigi URL'ler olmali (bugun zaten oyle).
