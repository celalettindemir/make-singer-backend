# Faz 3b Backend: hesap silme ve URL yeniden imzalama Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mobil Faz 3 Bölüm B'nin beklediği iki ucu eklemek: `DELETE /api/account` (hesap ve tüm dosyaları silinir) ve `POST /api/files/sign` (kalıcı dosya adresleri çalmadan önce yeniden imzalanır).

**Architecture:** Hesap silme `kimlik.Sunucu.HesapSil` ile Postgres `users` satırını siler; `refresh_tokens` ve `federe_kimlikler` `ON DELETE CASCADE` ile gider. Ardından Redis'e `kimlik:silindi:<userId>` işareti (AccessTTL ömürlü) yazılır ve yeni bir middleware bu işaretli kullanıcının elindeki hâlâ geçerli access token'ı `/api/*`'de reddeder. R2'de beş önek (`<tur>/<userId>/`) `R2Client.DeletePrefix` ile temizlenir; silme anında koşan render/master işlerinin sonradan yazacağı dosyalar için AccessTTL+5 dk sonra bir Asynq görevi önekleri bir kez daha temizler. İmzalama ucu her URL'yi `client.SahipliAnahtarCoz` ile çözer, `client.ResolveURL` ile taze adres üretir.

**Tech Stack:** Go 1.25, Fiber v2, pgx v5, go-redis v9, Asynq, aws-sdk-go-v2 (S3/R2).

**Spec:** `docs/superpowers/specs/2026-09-29-kendi-oidc-saglayicimiz-design.md` ("Hesap silme" ve "Mobil" bölümleri). Mobil sözleşme: `make-singer-mobile/docs/superpowers/plans/2026-10-11-faz3-mobil.md` ("Bölüm B'nin beklediği backend sözleşmesi").

## Global Constraints

- Sözleşme (mobil buna göre yazıldı, değiştirilmez):
  - `DELETE /api/account` → `204`. Sonrasında aynı token'la gelen istek `401` alır.
  - `POST /api/files/sign` gövde `{"urls": string[]}` (1-50 adet) → `200 {"files": [{"url": string, "expiresAt"?: string}]}`, girdiyle aynı sırada. Yabancı anahtar `404`, tanınmayan konak / bozuk URL `400`. Public (`exports/`) anahtarlarda `expiresAt` yok.
- Silinen veri (spec): Postgres kullanıcı kaydı, federe eşleşmeler, refresh aileleri ve R2'de `vocals/<userId>/`, `masters/<userId>/`, `stems/<userId>/`, `exports/<userId>/`, `previews/<userId>/`.
- Kod yorumları Türkçe, aksansız (mevcut dosyalarla aynı).
- Commit mesajları Türkçe; her commit şu satırla biter: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`
- Her görev sonunda, `core-service` içinde: `go build ./...`, `go test ./internal/...` ve `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` temiz.
- `./e2e/...` ÇALIŞTIRILMAZ (gerçek Suno/R2/Groq'a gider, ücretli). Tag atılmaz, push/deploy yapılmaz.
- Canlı Redis/Postgres isteyen testler mevcut yardımcıları kullanır ve ortam yoksa `t.Skip` ile atlar (handler DB 12, service 13, middleware 14, kimlik 15).

---

### Task 1: Kimlik deposunda kullanıcı silme

**Files:**
- Modify: `core-service/internal/kimlik/kullanici_depo.go` (`UserStore` arayüzü, `PostgresUserStore.Sil`)
- Modify: `core-service/internal/kimlik/kullanici_depo_sahte.go` (`SahteUserStore.Sil`)
- Modify: `core-service/internal/kimlik/kullanici_depo_sozlesme_test.go` (üç yeni adım)
- Modify: `core-service/internal/kimlik/sunucu.go` (`Sunucu.kullanicilar` alanı, `HesapSil`)
- Test: `core-service/internal/kimlik/hesap_sil_test.go` (cascade, canlı Postgres)

**Interfaces:**
- Produces: `UserStore.Sil(ctx context.Context, id string) error` — yoksa `ErrKullaniciYok`. `func (s *Sunucu) HesapSil(ctx context.Context, userID string) error`.

- [ ] **Step 1: Sözleşme testine üç adım ekle (listenin SONUNA)**

`TestUserStoreSozlesmesi`'deki `adimlar` dizisinin sonuna. Dosyada "bulunamadı" beklentisinin mevcut adımlarda nasıl yazıldığına bak (`yokID` adımı) ve aynı biçimi kullan; aşağıdaki `sozlesmeSonuc{hata: sozlesmeHata(ErrKullaniciYok)}` o biçimdir:

```go
{
	ad: "Sil var olan kullaniciyi siler",
	cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
		k, err := d.ByEmail(ctx, temelEposta)
		if err != nil {
			return sonuc(nil, err)
		}
		return sozlesmeSonuc{hata: sozlesmeHata(d.Sil(ctx, k.ID))}
	},
	beklenen: sozlesmeSonuc{},
},
{
	ad: "ByEmail silinen kullaniciyi bulamaz",
	cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
		return sonuc(d.ByEmail(ctx, temelEposta))
	},
	beklenen: sozlesmeSonuc{hata: sozlesmeHata(ErrKullaniciYok)},
},
{
	ad: "Sil olmayan kullanici icin ErrKullaniciYok",
	cagri: func(ctx context.Context, d UserStore) sozlesmeSonuc {
		return sozlesmeSonuc{hata: sozlesmeHata(d.Sil(ctx, yokID))}
	},
	beklenen: sozlesmeSonuc{hata: sozlesmeHata(ErrKullaniciYok)},
},
```

- [ ] **Step 2: Testin derlenmediğini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run TestUserStoreSozlesmesi`
Expected: FAIL, `d.Sil undefined`.

- [ ] **Step 3: Arayüz ve iki uygulama**

`kullanici_depo.go`, `UserStore`:

```go
type UserStore interface {
	Create(ctx context.Context, email, name, password string) (*User, error)
	ByEmail(ctx context.Context, email string) (*User, error)
	ByID(ctx context.Context, id string) (*User, error)
	// Sil, kullaniciyi kalici olarak siler. refresh_tokens ve
	// federe_kimlikler FK uzerinden ON DELETE CASCADE ile birlikte gider.
	Sil(ctx context.Context, id string) error
}
```

`PostgresUserStore`:

```go
func (s *PostgresUserStore) Sil(ctx context.Context, id string) error {
	// uuid olmayan id Postgres'te tip hatasi verir; sozlesme geregi
	// "yok" ile ayni sonucu dondururuz (ByID ile tutarli).
	if _, err := uuid.Parse(id); err != nil {
		return ErrKullaniciYok
	}
	etiket, err := s.havuz.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("kullanici silinemedi: %w", err)
	}
	if etiket.RowsAffected() == 0 {
		return ErrKullaniciYok
	}
	return nil
}
```

(`ByID`'nin geçersiz uuid'de ne döndürdüğüne bak; farklıysa `Sil`'i ona hizala ve yorumu güncelle.)

`SahteUserStore` (dosyadaki mevcut alan adlarıyla; e-posta haritasının anahtarı dosyada nasıl normalize ediliyorsa aynısı):

```go
func (s *SahteUserStore) Sil(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, var_ := s.idyeGore[id]
	if !var_ {
		return ErrKullaniciYok
	}
	delete(s.idyeGore, id)
	for anahtar, aday := range s.epostayaGor {
		if aday.ID == k.ID {
			delete(s.epostayaGor, anahtar)
		}
	}
	return nil
}
```

(`var_` yerine golangci'nin kabul ettiği bir ad, örn. `bulundu`, kullan.)

- [ ] **Step 4: Sözleşme testi geçer**

Run: `go test ./internal/kimlik/ -run TestUserStoreSozlesmesi -v`
Expected: PASS (Postgres yoksa Postgres kolu SKIP, sahte kol PASS).

- [ ] **Step 5: Cascade testi (canlı Postgres)**

`hesap_sil_test.go`. Postgres bağlantısını `kullanici_depo_sozlesme_test.go`'nun Postgres kolunun kullandığı yardımcıyla al (aynı skip davranışı). Test: kullanıcı oluştur (`PostgresUserStore.Create`), `PostgresFedereDepo.Bagla(ctx, "google", "sub-"+uuid, k.ID)` ile federe eşleşme ekle, `PostgresTokenStore.RefreshOlustur` ile bir refresh kaydı ekle (`RefreshKayit` alanlarını `jeton_depo_pg_test.go`'daki kurulumdan kopyala), sonra `Sil` ve:

```go
var federe, refresh int
_ = havuz.QueryRow(ctx, `SELECT count(*) FROM federe_kimlikler WHERE user_id = $1`, k.ID).Scan(&federe)
_ = havuz.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, k.ID).Scan(&refresh)
if federe != 0 || refresh != 0 {
	t.Fatalf("cascade calismadi: federe=%d refresh=%d", federe, refresh)
}
```

Run: `go test ./internal/kimlik/ -run TestHesapSil -v` → PASS veya ortam yoksa SKIP.

- [ ] **Step 6: `Sunucu.HesapSil`**

`sunucu.go`: `Sunucu` struct'ına alan:

```go
	// kullanicilar, API tarafindaki hesap silme ucu icin tutulur
	// (bkz. HesapSil).
	kullanicilar UserStore
```

Struct'ın kurulduğu literal (`srv: srv, havuz: havuz, anahtar: anahtar, ...`, ~satır 215) içine `kullanicilar: kullaniciDepo,` ekle. Dosyanın sonuna:

```go
// HesapSil, kullanicinin kimlik kaydini siler. Federe eslesmeler ve
// refresh aileleri ON DELETE CASCADE ile gider; yeni access token
// uretilemez. Elde kalan access token'lar icin API tarafindaki
// silinmis kullanici engeli devreye girer.
func (s *Sunucu) HesapSil(ctx context.Context, userID string) error {
	return s.kullanicilar.Sil(ctx, userID)
}
```

- [ ] **Step 7: Doğrula ve commit**

Run: `go build ./... && go test ./internal/... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → temiz.

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): kullanici silme ve Sunucu.HesapSil

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: R2'de önek silme

**Files:**
- Modify: `core-service/internal/client/anahtar.go` (`KullaniciOnekleri`)
- Modify: `core-service/internal/client/r2_client.go` (`OnekSilici`, `R2Client.DeletePrefix`)
- Test: `core-service/internal/client/anahtar_test.go` (varsa ekle, yoksa oluştur), `core-service/internal/client/onek_sil_test.go`

**Interfaces:**
- Produces: `client.KullaniciOnekleri(userID string) ([]string, error)` → `["exports/<id>/", "masters/<id>/", "previews/<id>/", "stems/<id>/", "vocals/<id>/"]` (bu sırayla), geçersiz id'de `ErrGecersizSegment`. `type OnekSilici interface { DeletePrefix(ctx context.Context, prefix string) (int, error) }`; `*R2Client` bunu uygular.

- [ ] **Step 1: Failing test — `KullaniciOnekleri`**

```go
func TestKullaniciOnekleri(t *testing.T) {
	got, err := KullaniciOnekleri("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	want := []string{
		"exports/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"masters/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"previews/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"stems/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
		"vocals/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("onekler = %v, beklenen %v", got, want)
	}
}

func TestKullaniciOnekleri_GecersizID(t *testing.T) {
	for _, id := range []string{"", "..", "a/b", "x.wav", strings.Repeat("a", 65)} {
		if _, err := KullaniciOnekleri(id); !errors.Is(err, ErrGecersizSegment) {
			t.Errorf("%q icin ErrGecersizSegment bekleniyordu, gelen %v", id, err)
		}
	}
}
```

Run: `go test ./internal/client/ -run TestKullaniciOnekleri` → FAIL (tanımsız).

- [ ] **Step 2: Uygula**

`anahtar.go`:

```go
// KullaniciOnekleri, bir kullanicinin R2'deki tum sahipli oneklerini
// sabit sirayla dondurur. Hesap silme bu onekleri toptan temizler; bu
// yuzden id, KullaniciAnahtari ile ayni segment kuralindan gecer ve
// nokta iceremez (bir dosya adi degil, bir kimliktir). Bos ya da
// gecersiz id'de onek uretilmez: "vocals//" gibi bir onek baska
// kullanicilarin nesnelerini kapsardi.
func KullaniciOnekleri(userID string) ([]string, error) {
	if !segmentDeseni.MatchString(userID) || strings.Contains(userID, ".") {
		return nil, ErrGecersizSegment
	}
	turler := make([]string, 0, len(sahipliTurler))
	for tur := range sahipliTurler {
		turler = append(turler, tur)
	}
	slices.Sort(turler)
	onekler := make([]string, 0, len(turler))
	for _, tur := range turler {
		onekler = append(onekler, tur+"/"+userID+"/")
	}
	return onekler, nil
}
```

Run → PASS.

- [ ] **Step 3: Failing test — güvensiz önek ağa gitmeden reddedilir**

`onek_sil_test.go`:

```go
func TestDeletePrefix_GuvensizOnekReddedilir(t *testing.T) {
	c, err := NewR2Client(&config.R2Config{
		AccountID: "hesap", AccessKeyID: "a", SecretAccessKey: "s",
		PublicBucket: "pub", PrivateBucket: "priv", PublicURL: "https://cdn.ornek",
	})
	if err != nil {
		t.Fatalf("istemci kurulamadi: %v", err)
	}
	for _, onek := range []string{"", "vocals/", "vocals", "vocals/u1", "/u1/"} {
		if _, err := c.DeletePrefix(context.Background(), onek); err == nil {
			t.Errorf("%q kabul edilmemeliydi", onek)
		}
	}
}
```

(`NewR2Client` ağa çıkmaz; yalnızca yapılandırma kurar.)

Run → FAIL (tanımsız).

- [ ] **Step 4: `DeletePrefix`**

`r2_client.go`, `StorageClient` arayüzüne DOKUNMA (sahte depolar kırılır). Ayrı arayüz ve yöntem:

```go
// OnekSilici, bir onekin altindaki tum nesneleri siler. StorageClient'tan
// ayri tutulur: yalnizca hesap silme ihtiyac duyar ve mevcut sahte
// depolarin hepsini bu yontemi uygulamaya zorlamak gereksizdir.
type OnekSilici interface {
	DeletePrefix(ctx context.Context, prefix string) (int, error)
}

var _ OnekSilici = (*R2Client)(nil)

// DeletePrefix, "<tur>/<userId>/" biciminde bir onekin altindaki tum
// nesneleri sayfa sayfa listeleyip toplu siler ve silinen sayisini
// dondurur. Onek en az iki segment ve sonda "/" icermek ZORUNDA: "vocals/"
// gibi bir onek tum kullanicilarin kayitlarini silerdi.
func (c *R2Client) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	parcalar := strings.Split(prefix, "/")
	if !strings.HasSuffix(prefix, "/") || len(parcalar) != 3 || parcalar[0] == "" || parcalar[1] == "" {
		return 0, fmt.Errorf("guvensiz onek reddedildi: %q", prefix)
	}
	bucket := aws.String(BucketFor(prefix, c.cfg))
	sayfalayici := s3.NewListObjectsV2Paginator(c.s3Client, &s3.ListObjectsV2Input{
		Bucket: bucket,
		Prefix: aws.String(prefix),
	})
	silinen := 0
	for sayfalayici.HasMorePages() {
		sayfa, err := sayfalayici.NextPage(ctx)
		if err != nil {
			return silinen, fmt.Errorf("R2 listelenemedi: %w", err)
		}
		if len(sayfa.Contents) == 0 {
			continue
		}
		// ListObjectsV2 sayfasi en fazla 1000 nesne doner; DeleteObjects
		// siniri da 1000'dir, yani sayfa basina tek cagri yeter.
		nesneler := make([]types.ObjectIdentifier, 0, len(sayfa.Contents))
		for _, o := range sayfa.Contents {
			nesneler = append(nesneler, types.ObjectIdentifier{Key: o.Key})
		}
		cikti, err := c.s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: bucket,
			Delete: &types.Delete{Objects: nesneler, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return silinen, fmt.Errorf("R2 silinemedi: %w", err)
		}
		if len(cikti.Errors) > 0 {
			return silinen, fmt.Errorf("R2 %d nesneyi silemedi", len(cikti.Errors))
		}
		silinen += len(nesneler)
	}
	return silinen, nil
}
```

`types` = `github.com/aws/aws-sdk-go-v2/service/s3/types`. `go.mod`'daki s3 sürümünde `Delete.Quiet` `bool` (pointer değil) ise `Quiet: true` yaz.

Run: `go test ./internal/client/ -run "TestDeletePrefix|TestKullaniciOnekleri" -v` → PASS.

- [ ] **Step 5: Doğrula ve commit**

Run: `go build ./... && go test ./internal/... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → temiz.

```bash
git add core-service/internal/client
git commit -m "feat(client): kullanici onekleri ve R2 onek silme

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Silinmiş kullanıcı engeli

**Files:**
- Create: `core-service/internal/middleware/silinmis.go`
- Test: `core-service/internal/middleware/silinmis_test.go`

**Interfaces:**
- Produces: `middleware.SilinmisAnahtar(userID string) string` (`"kimlik:silindi:" + userID`); `middleware.SilinmisKullaniciEngeli(rdb *redis.Client) fiber.Handler`. Task 4 rotaya bağlar ve işareti yazar.

Neden: OP access token'ları 15 dk geçerli ve API yalnızca imzayı doğruluyor. Silinmiş hesabın elindeki token'la yeni render (Suno kredisi) başlatması ya da R2'ye yeni dosya yazması engellenmeli. Redis'e ulaşılamazsa rate limiter gibi fail-closed 503.

- [ ] **Step 1: Failing test**

`silinmis_test.go`. Canlı Redis için `ratelimit_canli_test.go`'daki yardımcıyı kullan (DB 14, ortam yoksa skip). Yardımcının adını oradan al; aşağıda `canliRedisMW(t)` diye geçiyor.

```go
func engelliUygulama(rdb *redis.Client, userID string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("userId", userID)
		}
		return c.Next()
	})
	app.Use(SilinmisKullaniciEngeli(rdb))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("gecti") })
	return app
}

func engelDurum(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	return resp.StatusCode
}

func TestSilinmisEngeli_RedisYoksa503(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond})
	if got := engelDurum(t, engelliUygulama(rdb, "u1")); got != http.StatusServiceUnavailable {
		t.Fatalf("durum = %d, beklenen 503", got)
	}
}

func TestSilinmisEngeli_UserIDBossa401(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if got := engelDurum(t, engelliUygulama(rdb, "")); got != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", got)
	}
}

func TestSilinmisEngeli_IsaretliKullanici401(t *testing.T) {
	rdb := canliRedisMW(t)
	if err := rdb.Set(context.Background(), SilinmisAnahtar("u1"), "1", time.Minute).Err(); err != nil {
		t.Fatalf("isaret yazilamadi: %v", err)
	}
	if got := engelDurum(t, engelliUygulama(rdb, "u1")); got != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", got)
	}
	if got := engelDurum(t, engelliUygulama(rdb, "u2")); got != http.StatusOK {
		t.Fatalf("isaretsiz kullanici durumu = %d, beklenen 200", got)
	}
}
```

Run: `go test ./internal/middleware/ -run TestSilinmisEngeli` → FAIL (tanımsız).

- [ ] **Step 2: Uygula**

```go
package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
	"github.com/redis/go-redis/v9"
)

// silinmisOnek altindaki anahtar, hesabi silinmis bir kullaniciyi
// isaretler. Omru access token omrune esittir: o sure dolunca elde
// kalan hicbir token zaten gecerli degildir.
const silinmisOnek = "kimlik:silindi:"

// SilinmisAnahtar, kullanicinin silinmis isaretinin Redis anahtaridir.
func SilinmisAnahtar(userID string) string { return silinmisOnek + userID }

// SilinmisKullaniciEngeli, hesabi silinmis kullanicinin elinde kalan
// (imzasi hala gecerli) access token'i reddeder. API token'i yalnizca
// imzayla dogruladigi icin bu engel olmadan silinen hesap 15 dakika
// boyunca render baslatip R2'ye dosya yazabilirdi. Redis'e
// ulasilamazsa rate limiter gibi fail-closed davranir.
func SilinmisKullaniciEngeli(rdb *redis.Client) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			return response.Unauthorized(c, "Kimlik dogrulanamadi")
		}
		n, err := rdb.Exists(c.Context(), SilinmisAnahtar(userID)).Result()
		if err != nil {
			c.Set("Retry-After", "5")
			return response.Error(c, fiber.StatusServiceUnavailable,
				response.CodeServiceError, "Hesap durumu denetlenemedi", nil)
		}
		if n > 0 {
			return response.Unauthorized(c, "Hesap silindi")
		}
		return c.Next()
	}
}
```

Run → PASS (canlı test ortam yoksa SKIP).

- [ ] **Step 3: Doğrula ve commit**

Run: `go build ./... && go test ./internal/... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → temiz.

```bash
git add core-service/internal/middleware/silinmis.go core-service/internal/middleware/silinmis_test.go
git commit -m "feat(middleware): silinmis hesabin elde kalan token'i reddedilir

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `DELETE /api/account`

**Files:**
- Create: `core-service/internal/service/hesap_service.go`, `core-service/internal/service/hesap_service_test.go`
- Create: `core-service/internal/worker/hesap_worker.go`, `core-service/internal/worker/hesap_worker_test.go`
- Create: `core-service/internal/handler/hesap.go`, `core-service/internal/handler/hesap_test.go`
- Modify: `core-service/cmd/server/main.go` (servis, handler, rota, `/api` grubuna engel, asynq kuyruğu ve görev)
- Modify: `core-service/docs/API.md`, `CLAUDE.md` (Auth bölümü)

**Interfaces:**
- Consumes: `kimlik.ErrKullaniciYok`, `(*kimlik.Sunucu).HesapSil` (Task 1); `client.KullaniciOnekleri`, `client.OnekSilici` (Task 2); `middleware.SilinmisAnahtar`, `middleware.SilinmisKullaniciEngeli` (Task 3).
- Produces: `service.TaskTypeHesapTemizle = "hesap:temizle"`; `service.ErrHesapYok`; `service.NewHesapService(kimlik KimlikSilici, silici client.OnekSilici, rdb *redis.Client, kuyruk Kuyruk, accessTTL time.Duration) *HesapService`; `(*HesapService).Sil(ctx, userID string) error`; `(*HesapService).Temizle(ctx, userID string) error`; `handler.NewHesapHandler(*service.HesapService) *HesapHandler` ve `(*HesapHandler).Sil`; `worker.NewHesapWorker(*service.HesapService) *HesapWorker` ve `ProcessTask`.

Sıra ve hata politikası (spec + sözleşme):
1. `userID` → `KullaniciOnekleri`; geçersizse `ErrHesapYok` (hiçbir şey silinmez).
2. `kimlik.HesapSil`. `ErrKullaniciYok` → `ErrHesapYok` (404). Başka hata → hata (500), R2'ye dokunulmaz.
3. Bundan sonra hesap YOKTUR ve kullanıcı yeniden deneyemez; sonraki adımların hataları isteği başarısız KILMAZ, loglanır: silinmiş işareti (TTL = AccessTTL), R2 önek temizliği, AccessTTL+5 dk sonra ikinci temizlik görevi (`bakim` kuyruğu, `MaxRetry(10)`). İkinci temizlik silme anında koşan render/master işlerinin sonradan yazdığı dosyaları da kapsar; işaret o süre içinde yeni iş başlatılmasını engeller.

- [ ] **Step 1: Failing test — servis**

`hesap_service_test.go` (package `service`):

```go
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

type sahteKuyruk struct{ gorevler []*asynq.Task }

func (q *sahteKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	q.gorevler = append(q.gorevler, t)
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
	svc := NewHesapService(k, s, ulasilamazRedis(), q, 15*time.Minute)
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
	err := NewHesapService(k, s, ulasilamazRedis(), q, time.Minute).Sil(context.Background(), hesapID)
	if !errors.Is(err, ErrHesapYok) {
		t.Fatalf("ErrHesapYok bekleniyordu, gelen %v", err)
	}
	if len(s.onekler) != 0 || len(q.gorevler) != 0 {
		t.Fatal("kimlik silinmeden R2'ye dokunulmamali")
	}
}

func TestHesapSil_KimlikHatasindaR2yeDokunulmaz(t *testing.T) {
	k, s, q := &sahteKimlik{hata: errors.New("pg kapali")}, &sahteSilici{}, &sahteKuyruk{}
	err := NewHesapService(k, s, ulasilamazRedis(), q, time.Minute).Sil(context.Background(), hesapID)
	if err == nil || errors.Is(err, ErrHesapYok) {
		t.Fatalf("genel hata bekleniyordu, gelen %v", err)
	}
	if len(s.onekler) != 0 || len(q.gorevler) != 0 {
		t.Fatal("kimlik silinmeden R2'ye dokunulmamali")
	}
}

func TestHesapSil_R2HatasiIstegiBozmaz(t *testing.T) {
	k, s, q := &sahteKimlik{}, &sahteSilici{hata: errors.New("r2 kapali")}, &sahteKuyruk{}
	if err := NewHesapService(k, s, ulasilamazRedis(), q, time.Minute).Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("hesap silindikten sonra hata donmemeli: %v", err)
	}
	if len(q.gorevler) != 1 {
		t.Fatal("R2 hatasinda ikinci temizlik yine kurulmali")
	}
}

func TestHesapSil_GecersizIDHicbirSeySilmez(t *testing.T) {
	k, s, q := &sahteKimlik{}, &sahteSilici{}, &sahteKuyruk{}
	err := NewHesapService(k, s, ulasilamazRedis(), q, time.Minute).Sil(context.Background(), "../x")
	if !errors.Is(err, ErrHesapYok) || len(k.silinen) != 0 {
		t.Fatalf("gecersiz id silinmemeli: err=%v silinen=%v", err, k.silinen)
	}
}

func TestHesapTemizle_R2YoksaHataYok(t *testing.T) {
	svc := NewHesapService(&sahteKimlik{}, nil, ulasilamazRedis(), &sahteKuyruk{}, time.Minute)
	if err := svc.Temizle(context.Background(), hesapID); err != nil {
		t.Fatalf("R2 yokken temizlik no-op olmali: %v", err)
	}
}

func TestHesapSil_IsaretAccessTTLIleYazilir(t *testing.T) {
	rdb := canliRedis(t) // service/canli_redis_test.go yardimcisi (DB 13)
	svc := NewHesapService(&sahteKimlik{}, &sahteSilici{}, rdb, &sahteKuyruk{}, 15*time.Minute)
	if err := svc.Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	ttl, err := rdb.TTL(context.Background(), middleware.SilinmisAnahtar(hesapID)).Result()
	if err != nil || ttl <= 14*time.Minute || ttl > 15*time.Minute {
		t.Fatalf("isaret TTL = %v (err %v), ~15dk bekleniyordu", ttl, err)
	}
}
```

(`canliRedis`'in service paketindeki adı farklıysa onu kullan.)

Run: `go test ./internal/service/ -run "TestHesap"` → FAIL (tanımsız).

- [ ] **Step 2: Servis**

`hesap_service.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/kimlik"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/redis/go-redis/v9"
)

// TaskTypeHesapTemizle, silinen hesabin R2 oneklerini gecikmeli olarak
// bir kez daha temizleyen gorevdir.
const TaskTypeHesapTemizle = "hesap:temizle"

// ErrHesapYok, silinecek hesap bulunamadi (ya da id gecersiz).
var ErrHesapYok = errors.New("hesap bulunamadi")

// ikinciTemizlikPayi, access token omrunun ustune eklenen gecikmedir:
// silme aninda kosan bir render/master isi bu sure icinde biter ve
// yazdigi dosyalar ikinci temizlikte gider.
const ikinciTemizlikPayi = 5 * time.Minute

// KimlikSilici, kimlik saglayicisinin hesap silme yetenegi
// (*kimlik.Sunucu uygular).
type KimlikSilici interface {
	HesapSil(ctx context.Context, userID string) error
}

// Kuyruk, *asynq.Client'in burada kullanilan yuzu.
type Kuyruk interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

type HesapService struct {
	kimlik    KimlikSilici
	silici    client.OnekSilici // nil: R2 yapilandirilmamis
	redis     *redis.Client
	kuyruk    Kuyruk
	accessTTL time.Duration
}

func NewHesapService(kimlikSilici KimlikSilici, silici client.OnekSilici, rdb *redis.Client, kuyruk Kuyruk, accessTTL time.Duration) *HesapService {
	return &HesapService{kimlik: kimlikSilici, silici: silici, redis: rdb, kuyruk: kuyruk, accessTTL: accessTTL}
}

// Sil, hesabi ve tum dosyalarini siler. Kimlik kaydi silindikten sonra
// kullanici istegi tekrarlayamaz (token'i artik reddedilir); bu yuzden
// sonraki adimlarin hatalari loglanir ama istegi basarisiz kilmaz.
// Eksik kalan R2 temizligini gecikmeli gorev tamamlar.
func (s *HesapService) Sil(ctx context.Context, userID string) error {
	if _, err := client.KullaniciOnekleri(userID); err != nil {
		return ErrHesapYok
	}
	if err := s.kimlik.HesapSil(ctx, userID); err != nil {
		if errors.Is(err, kimlik.ErrKullaniciYok) {
			return ErrHesapYok
		}
		return fmt.Errorf("hesap silinemedi: %w", err)
	}

	if err := s.redis.Set(ctx, middleware.SilinmisAnahtar(userID), "1", s.accessTTL).Err(); err != nil {
		log.Printf("Uyari: silinmis isareti yazilamadi (kullanici %s): %v", userID, err)
	}
	if err := s.Temizle(ctx, userID); err != nil {
		log.Printf("Uyari: hesap R2 temizligi eksik kaldi, gecikmeli gorev tamamlayacak (kullanici %s): %v", userID, err)
	}

	yuk, err := json.Marshal(map[string]string{"userId": userID})
	if err != nil {
		log.Printf("Uyari: temizlik gorevi hazirlanamadi (kullanici %s): %v", userID, err)
		return nil
	}
	if _, err := s.kuyruk.Enqueue(asynq.NewTask(TaskTypeHesapTemizle, yuk),
		asynq.Queue("bakim"),
		asynq.ProcessIn(s.accessTTL+ikinciTemizlikPayi),
		asynq.MaxRetry(10),
	); err != nil {
		log.Printf("Uyari: gecikmeli temizlik gorevi kurulamadi (kullanici %s): %v", userID, err)
	}
	return nil
}

// Temizle, kullanicinin tum R2 oneklerini siler. R2 yoksa bir sey
// yapmaz. Bir onek basarisiz olsa da digerleri denenir.
func (s *HesapService) Temizle(ctx context.Context, userID string) error {
	if s.silici == nil {
		return nil
	}
	onekler, err := client.KullaniciOnekleri(userID)
	if err != nil {
		return err
	}
	var hatalar []error
	for _, onek := range onekler {
		if _, err := s.silici.DeletePrefix(ctx, onek); err != nil {
			hatalar = append(hatalar, fmt.Errorf("%s: %w", onek, err))
		}
	}
	return errors.Join(hatalar...)
}
```

Import döngüsü çıkarsa (`service` → `middleware` ya da `service` → `kimlik`), `go build` hatası söyler; o durumda `SilinmisAnahtar`'ı döngüsüz küçük bir pakete (örn. `internal/hesapisaret`) taşı ve Task 3 dosyasını oraya yönlendir. Ledger'a not düş.

Run: `go test ./internal/service/ -run "TestHesap" -v` → PASS.

- [ ] **Step 3: Worker — test ve uygulama**

`hesap_worker_test.go` (package `worker`):

```go
type kayitSilici struct{ onekler []string }

func (k *kayitSilici) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	k.onekler = append(k.onekler, prefix)
	return 0, nil
}

type bosKimlik struct{}

func (bosKimlik) HesapSil(ctx context.Context, userID string) error { return nil }

func TestHesapWorker_OnekleriTemizler(t *testing.T) {
	s := &kayitSilici{}
	svc := service.NewHesapService(bosKimlik{}, s, nil, nil, time.Minute)
	gorev := asynq.NewTask(service.TaskTypeHesapTemizle, []byte(`{"userId":"6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"}`))
	if err := NewHesapWorker(svc).ProcessTask(context.Background(), gorev); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(s.onekler) != 5 {
		t.Fatalf("5 onek bekleniyordu: %v", s.onekler)
	}
}

func TestHesapWorker_BozukYukTekrarlanmaz(t *testing.T) {
	svc := service.NewHesapService(bosKimlik{}, &kayitSilici{}, nil, nil, time.Minute)
	err := NewHesapWorker(svc).ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeHesapTemizle, []byte("{")))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, gelen %v", err)
	}
}
```

`hesap_worker.go`:

```go
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/service"
)

// HesapWorker, silinen hesabin gecikmeli R2 temizligini yapar.
type HesapWorker struct {
	hesap *service.HesapService
}

func NewHesapWorker(hesap *service.HesapService) *HesapWorker {
	return &HesapWorker{hesap: hesap}
}

func (w *HesapWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var yuk struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(t.Payload(), &yuk); err != nil {
		// Bozuk yuk tekrar denemekle duzelmez.
		return fmt.Errorf("hesap temizlik yuku cozulemedi: %v: %w", err, asynq.SkipRetry)
	}
	return w.hesap.Temizle(ctx, yuk.UserID)
}
```

Run: `go test ./internal/worker/ -run TestHesapWorker -v` → PASS.

- [ ] **Step 4: Handler — test ve uygulama**

`hesap_test.go` (package `handler`; `kullaniciOlarak` `sahiplik_canli_test.go`'da tanımlı, aynı paket):

```go
type hSahteKimlik struct{ hata error }

func (k hSahteKimlik) HesapSil(ctx context.Context, userID string) error { return k.hata }

type hBosKuyruk struct{}

func (hBosKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{}, nil
}

func hesapUygulamasi(kimlikHata error, userID string) *fiber.App {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	svc := service.NewHesapService(hSahteKimlik{hata: kimlikHata}, nil, rdb, hBosKuyruk{}, time.Minute)
	app := fiber.New()
	app.Delete("/api/account", kullaniciOlarak(userID), NewHesapHandler(svc).Sil)
	return app
}

func hesapSilDurum(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodDelete, "/api/account", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	return resp.StatusCode
}

func TestHesapHandler_Sil204(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(nil, "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusNoContent {
		t.Fatalf("durum = %d, beklenen 204", got)
	}
}

func TestHesapHandler_Yok404(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(kimlik.ErrKullaniciYok, "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusNotFound {
		t.Fatalf("durum = %d, beklenen 404", got)
	}
}

func TestHesapHandler_KimliksizIstek401(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(nil, "")); got != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", got)
	}
}

func TestHesapHandler_KimlikHatasi500(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(errors.New("pg"), "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusInternalServerError {
		t.Fatalf("durum = %d, beklenen 500", got)
	}
}
```

`hesap.go`:

```go
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type HesapHandler struct {
	service *service.HesapService
}

func NewHesapHandler(svc *service.HesapService) *HesapHandler {
	return &HesapHandler{service: svc}
}

// Sil handles DELETE /api/account
// @Summary      Delete account
// @Description  Permanently delete the caller's account, linked sign-ins, refresh tokens and every stored file. The same token is rejected afterwards.
// @Tags         Account
// @Success      204
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/account [delete]
func (h *HesapHandler) Sil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	if err := h.service.Sil(c.Context(), userID); err != nil {
		if errors.Is(err, service.ErrHesapYok) {
			return response.NotFound(c, "Hesap bulunamadi")
		}
		return response.ServiceError(c, "Hesap silinemedi")
	}
	return c.SendStatus(fiber.StatusNoContent)
}
```

(`response.ServiceError`'un 500 döndürdüğünü `pkg/response`'tan doğrula.)

Run: `go test ./internal/handler/ -run TestHesapHandler -v` → PASS.

- [ ] **Step 5: main.go bağlantıları**

1. Servisler bloğunda (`wsTicketService := ...` sonrası):

```go
	// silici: arayuz degiskeni; r2Client nil iken dogrudan atamak tipli
	// nil tuzagini kurar (bkz. storage).
	var silici client.OnekSilici
	if r2Client != nil {
		silici = r2Client
	}
```

2. Hesap silme yalnızca kendi OP'umuz varken anlamlı. `kimlikSunucu` (tipi `*kimlik.Sunucu`) nil değilse:

```go
	var hesapHandler *handler.HesapHandler
	var hesapService *service.HesapService
	if kimlikSunucu != nil {
		hesapService = service.NewHesapService(kimlikSunucu, silici, redisClient, asynqClient, cfg.Auth.AccessTTL)
		hesapHandler = handler.NewHesapHandler(hesapService)
	}
```

3. `/api` grubu: `api := app.Group("/api", apiAuthMiddleware, middleware.SilinmisKullaniciEngeli(redisClient))`

4. Rota (`api.Post("/ws/ticket", ...)` yanına):

```go
	if hesapHandler != nil {
		api.Delete("/account", hesapHandler.Sil)
	}
```

5. Asynq: `Queues` haritasına `"bakim": 1`; mux'a:

```go
	if hesapService != nil {
		mux.HandleFunc(service.TaskTypeHesapTemizle, worker.NewHesapWorker(hesapService).ProcessTask)
	}
```

Run: `go build ./...` → temiz.

- [ ] **Step 6: Doküman**

- `core-service/docs/API.md`: "Account" başlığı altında `DELETE /api/account` → 204, 401/404/500; silinenlerin listesi (Global Constraints'teki gibi); sonrasında aynı token'ın 401 aldığı; R2 temizliğinin AccessTTL+5 dk sonra bir kez daha tekrarlandığı.
- `CLAUDE.md` "Auth" bölümüne bir paragraf: hesap silme sırası, `kimlik:silindi:<userId>` işareti ve `SilinmisKullaniciEngeli`, `hesap:temizle` görevi (`bakim` kuyruğu). "Job lifecycle" cümlesindeki kuyruk listesine `bakim` ekle.
- `swag` kuruluysa `cd core-service && swag init -g cmd/server/main.go`; değilse atla ve raporda belirt.

- [ ] **Step 7: Doğrula ve commit**

Run: `go build ./... && go test ./internal/... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → temiz.

```bash
git add core-service CLAUDE.md
git commit -m "feat: DELETE /api/account hesap ve tum dosyalari siler

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `POST /api/files/sign`

**Files:**
- Create: `core-service/internal/model/files.go`
- Create: `core-service/internal/service/file_service.go`, `core-service/internal/service/file_service_test.go`
- Create: `core-service/internal/handler/files.go`, `core-service/internal/handler/files_test.go`
- Modify: `core-service/cmd/server/main.go`, `core-service/docs/API.md`, `CLAUDE.md` (Ownership paragrafı)

**Interfaces:**
- Consumes: `client.SahipliAnahtarCoz`, `client.ResolveURL`, `client.ErrYabanciAnahtar`, `client.ErrGecersizURL`, `model.ExpiresPtr`.
- Produces: `model.FileSignRequest{URLs []string}`, `model.SignedFile{URL string; ExpiresAt *time.Time}`, `model.FileSignResponse{Files []model.SignedFile}`; `service.NewFileService(storage client.StorageClient, r2Cfg *config.R2Config) *FileService`, `(*FileService).Sign(ctx, userID string, urls []string) (*model.FileSignResponse, error)`; `handler.NewFileHandler(*service.FileService, *validator.Validate) *FileHandler`, `(*FileHandler).Sign`.

- [ ] **Step 1: Model**

`model/files.go`:

```go
package model

import "time"

// FileSignRequest, istemcinin elinde tuttugu (suresi gecmis olabilecek)
// dosya adreslerini tazelemek icin gonderdigi istektir.
type FileSignRequest struct {
	URLs []string `json:"urls" validate:"required,min=1,max=50,dive,required"`
}

// SignedFile, bir adresin taze hali. Public (exports/) anahtarlarda
// ExpiresAt yoktur.
type SignedFile struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// FileSignResponse, girdiyle AYNI sirada taze adresleri tasir.
type FileSignResponse struct {
	Files []SignedFile `json:"files"`
}
```

- [ ] **Step 2: Failing test — servis**

`file_service_test.go` (package `service`; `sahteDepo` ve `testR2Cfg` `sonuc_tazeleme_test.go`'da tanımlı):

```go
const imzaKullanici = "u1"

func ozelURL(key string) string {
	return "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/" + key + "?X-Amz-Signature=eski"
}

func TestFileSign_SirayiKoruyupTazeAdresUretir(t *testing.T) {
	depo := &sahteDepo{}
	svc := NewFileService(depo, testR2Cfg())
	girdi := []string{ozelURL("stems/u1/p1/a.mp3"), ozelURL("vocals/u1/p1/s1/t1.wav")}
	got, err := svc.Sign(context.Background(), imzaKullanici, girdi)
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(got.Files) != 2 {
		t.Fatalf("2 dosya bekleniyordu: %+v", got.Files)
	}
	for i, key := range []string{"stems/u1/p1/a.mp3", "vocals/u1/p1/s1/t1.wav"} {
		if !strings.Contains(got.Files[i].URL, key) || strings.Contains(got.Files[i].URL, "eski") {
			t.Errorf("dosya %d = %q, taze %s adresi bekleniyordu", i, got.Files[i].URL, key)
		}
		if got.Files[i].ExpiresAt == nil {
			t.Errorf("dosya %d icin expiresAt bekleniyordu", i)
		}
	}
}

func TestFileSign_YabanciAnahtarReddedilir(t *testing.T) {
	_, err := NewFileService(&sahteDepo{}, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{ozelURL("stems/u1/p1/a.mp3"), ozelURL("stems/u2/p1/b.mp3")})
	if !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, gelen %v", err)
	}
}

func TestFileSign_TaninmayanKonakReddedilir(t *testing.T) {
	_, err := NewFileService(&sahteDepo{}, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{"https://kotu.ornek/stems/u1/p1/a.mp3"})
	if !errors.Is(err, client.ErrGecersizURL) {
		t.Fatalf("ErrGecersizURL bekleniyordu, gelen %v", err)
	}
}

func TestFileSign_YabanciVarsaHicImzalanmaz(t *testing.T) {
	depo := &sahteDepo{}
	_, _ = NewFileService(depo, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{ozelURL("stems/u1/p1/a.mp3"), ozelURL("stems/u2/p1/b.mp3")})
	if depo.urlForCnt != 0 {
		t.Fatalf("yabanci anahtar varken imza uretilmemeli, URLFor %d kez cagrildi", depo.urlForCnt)
	}
}

func TestFileSign_PublicAnahtardaExpiresYok(t *testing.T) {
	// storage nil: ResolveURL imzasiz adres ve sifir zaman doner.
	got, err := NewFileService(nil, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{"https://makesinger-cdn.celalettindemir.dev/exports/u1/e1.mp3"})
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if got.Files[0].ExpiresAt != nil {
		t.Fatalf("public anahtarda expiresAt olmamali: %v", got.Files[0].ExpiresAt)
	}
}
```

Run: `go test ./internal/service/ -run TestFileSign` → FAIL (tanımsız).

- [ ] **Step 3: Servis**

`file_service.go`:

```go
package service

import (
	"context"
	"fmt"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// FileService, istemcinin sakladigi dosya adreslerini tazeler. Istemci
// adresi yalnizca nesne referansi olarak tutar; imza 1 saatte doldugu
// icin calmadan once buradan yeni adres alir.
type FileService struct {
	storage client.StorageClient
	r2Cfg   *config.R2Config
}

func NewFileService(storage client.StorageClient, r2Cfg *config.R2Config) *FileService {
	return &FileService{storage: storage, r2Cfg: r2Cfg}
}

// Sign, adresleri girdiyle ayni sirada tazeler. Once HEPSI cozulup
// sahiplik denetlenir: tek bir yabanci anahtar varsa hicbir imza
// uretilmez (kismi yanit, yabanci anahtarin varligini sizdirmaz).
func (s *FileService) Sign(ctx context.Context, userID string, urls []string) (*model.FileSignResponse, error) {
	anahtarlar := make([]string, len(urls))
	for i, u := range urls {
		key, err := client.SahipliAnahtarCoz(u, userID, s.r2Cfg)
		if err != nil {
			return nil, err
		}
		anahtarlar[i] = key
	}
	dosyalar := make([]model.SignedFile, len(anahtarlar))
	for i, key := range anahtarlar {
		adres, son, err := client.ResolveURL(ctx, s.storage, s.r2Cfg, key)
		if err != nil {
			return nil, fmt.Errorf("adres uretilemedi: %w", err)
		}
		dosyalar[i] = model.SignedFile{URL: adres, ExpiresAt: model.ExpiresPtr(son)}
	}
	return &model.FileSignResponse{Files: dosyalar}, nil
}
```

Run: `go test ./internal/service/ -run TestFileSign -v` → PASS. (`ozelURL` konağı `bilinenKonaklar`'da yoksa testteki konağı `sahteDepo.URLFor`'un ürettiği biçime göre düzelt; o biçim tanınan konaktır.)

- [ ] **Step 4: Handler — test ve uygulama**

`files_test.go` (package `handler`):

```go
func imzaUygulamasi(userID string) *fiber.App {
	cfg := &config.R2Config{
		AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
		PublicURL: "https://makesinger-cdn.celalettindemir.dev", PresignTTL: time.Hour,
	}
	app := fiber.New()
	app.Post("/api/files/sign", kullaniciOlarak(userID), NewFileHandler(service.NewFileService(nil, cfg), validator.New()).Sign)
	return app
}

func imzaDurum(t *testing.T, app *fiber.App, govde string) (int, string) {
	t.Helper()
	istek := httptest.NewRequest(http.MethodPost, "/api/files/sign", strings.NewReader(govde))
	istek.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(istek)
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestFileHandler_Imzalar(t *testing.T) {
	durum, govde := imzaDurum(t, imzaUygulamasi("u1"),
		`{"urls":["https://makesinger-cdn.celalettindemir.dev/exports/u1/e1.mp3"]}`)
	if durum != http.StatusOK || !strings.Contains(govde, `"files"`) {
		t.Fatalf("durum=%d govde=%s", durum, govde)
	}
}

func TestFileHandler_Yabanci404(t *testing.T) {
	durum, _ := imzaDurum(t, imzaUygulamasi("u1"),
		`{"urls":["https://makesinger-cdn.celalettindemir.dev/exports/u2/e1.mp3"]}`)
	if durum != http.StatusNotFound {
		t.Fatalf("durum=%d, beklenen 404", durum)
	}
}

func TestFileHandler_GecersizGirdi400(t *testing.T) {
	cok := make([]string, 51)
	for i := range cok {
		cok[i] = `"https://makesinger-cdn.celalettindemir.dev/exports/u1/e.mp3"`
	}
	for _, govde := range []string{
		`{"urls":[]}`,
		`{}`,
		`{"urls":["https://kotu.ornek/exports/u1/e.mp3"]}`,
		`{"urls":[` + strings.Join(cok, ",") + `]}`,
	} {
		if durum, _ := imzaDurum(t, imzaUygulamasi("u1"), govde); durum != http.StatusBadRequest {
			t.Errorf("%.40s... icin durum=%d, beklenen 400", govde, durum)
		}
	}
}

func TestFileHandler_Kimliksiz401(t *testing.T) {
	if durum, _ := imzaDurum(t, imzaUygulamasi(""), `{"urls":["x"]}`); durum != http.StatusUnauthorized {
		t.Fatalf("durum=%d, beklenen 401", durum)
	}
}
```

`files.go`:

```go
package handler

import (
	"errors"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type FileHandler struct {
	service   *service.FileService
	validator *validator.Validate
}

func NewFileHandler(svc *service.FileService, v *validator.Validate) *FileHandler {
	return &FileHandler{service: svc, validator: v}
}

// Sign handles POST /api/files/sign
// @Summary      Refresh file URLs
// @Description  Re-sign stored file URLs (1-50) owned by the caller. Response keeps the request order. Public export URLs have no expiresAt.
// @Tags         Files
// @Accept       json
// @Produce      json
// @Param        request body model.FileSignRequest true "URLs to refresh"
// @Success      200 {object} model.FileSignResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/files/sign [post]
func (h *FileHandler) Sign(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var req model.FileSignRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}
	result, err := h.service.Sign(c.Context(), userID, req.URLs)
	if err != nil {
		if errors.Is(err, client.ErrYabanciAnahtar) {
			return response.NotFound(c, "File not found")
		}
		if errors.Is(err, client.ErrGecersizURL) {
			return response.ValidationError(c, "Gecersiz dosya URL'si", nil)
		}
		return response.ServiceError(c, "Adres uretilemedi")
	}
	return response.OK(c, result)
}
```

Run: `go test ./internal/handler/ -run TestFileHandler -v` → PASS.

- [ ] **Step 5: main.go, doküman**

- Servisler: `fileService := service.NewFileService(storage, &cfg.R2)`; handler: `fileHandler := handler.NewFileHandler(fileService, validate)`; rota: `api.Post("/files/sign", fileHandler.Sign)`.
- `docs/API.md`: "Files" başlığı, sözleşme (Global Constraints'teki gibi), hata kodları.
- `CLAUDE.md` Ownership paragrafının sonuna: "`POST /api/files/sign` re-signs stored URLs (1-50, same order) after resolving each with `client.SahipliAnahtarCoz`; a single foreign key fails the whole request with 404 and nothing is signed."
- `swag` kuruluysa `swag init -g cmd/server/main.go`.

- [ ] **Step 6: Doğrula ve commit**

Run: `go build ./... && go test ./internal/... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → temiz.

```bash
git add core-service CLAUDE.md
git commit -m "feat: POST /api/files/sign kalici dosya adreslerini yeniden imzalar

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Spec kapsamı

| Gereksinim | Görev |
|---|---|
| Postgres kaydı, federe eşleşmeler, refresh aileleri silinir | Task 1 (cascade testli) |
| R2'de beş önek silinir | Task 2 + Task 4 (anında ve gecikmeli) |
| Sonrasında aynı token 401 | Task 3 + Task 4 (işaret) |
| `DELETE /api/account` → 204 | Task 4 |
| Presigned URL'ler yeniden çekilir (mobil Task 6'nın ucu) | Task 5 |

## Bilinen sınırlar

- Silme anında Redis'e ulaşılamazsa işaret yazılmaz; elde kalan token AccessTTL (15 dk) dolana kadar API'ye girebilir. Refresh imkânsız olduğu için süre sınırlıdır, yazılan dosyaları gecikmeli temizlik siler. Not: Redis yoksa `SilinmisKullaniciEngeli` zaten tüm `/api/*`'yi 503'e düşürür.
- Asynq görevi kurulamazsa ikinci temizlik olmaz; ilk temizlikten sonra yazılan dosyalar kalır. Log satırı `Uyari: gecikmeli temizlik gorevi kurulamadi` ile izlenir.
