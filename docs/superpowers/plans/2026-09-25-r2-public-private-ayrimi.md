# R2 Public/Private Bucket Ayrımı Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Export dosyaları CDN'den herkese açık public bucket'tan, kullanıcının çalışma dosyaları (vokal, master, stem) ise 1 saatlik presigned URL ile private bucket'tan sunulsun.

**Architecture:** Hedef bucket, nesne anahtarının önekinden belirlenir (`exports/` → public, diğer hepsi → private). Kural iki serviste de aynı: Go'da `BucketFor`, Python'da `bucket_for`. audio-service dosyaları artık URL'den HTTP ile değil, S3 API ile anahtara göre indirir; anahtarı Go üretir ve kabloda gönderir (`KeyFromURL`). İstemci API'si değişmez.

**Tech Stack:** Go 1.22 (Fiber, viper, aws-sdk-go-v2 s3 v1.51.0), Python 3.11 (FastAPI, boto3 1.34.0), stdlib `testing` + pytest.

**Spec:** `docs/superpowers/specs/2026-09-18-r2-public-private-ayrimi-design.md`

## Global Constraints

- Önek kuralı, iki serviste de birebir aynı: `exports/` → public bucket; diğer bütün önekler (ve bilinmeyen önekler) → private bucket.
- Presigned URL ömrü: 1 saat. Yapılandırma anahtarı `R2_PRESIGN_TTL`, varsayılan `1h`.
- Env değişkenleri: `R2_PUBLIC_BUCKET` (`makeasinger-public`), `R2_PRIVATE_BUCKET` (`makeasinger-private`), `R2_PUBLIC_URL` (`https://makesinger-cdn.celalettindemir.dev`), `R2_PRESIGN_TTL`. Eski `R2_BUCKET_NAME` tamamen kaldırılır.
- Servisler arası aktarım presigned URL kullanmaz; S3 API ile anahtara göre okur.
- İstemciye dönen API alan adları değişmez (`fileUrl`, `stemUrls`, `masterFileUrl`). Yalnız Go↔Python iç kablosu anahtara geçer.
- Go tarafında yeni testler `internal/` altında birim testi olarak yazılır. `e2e/` paketi şu an kırmızı (Groq anahtarı süresi dolmuş, 401 `expired_api_key`); bu plan onu düzeltmez ve ona dokunmaz.
- Go testleri Redis gerektirmez; yeni birim testleri ağa çıkmaz.
- Commit mesajları Türkçe, mevcut depo üslubuyla (`feat:`, `test:`, `refactor:`).

---

### Task 1: Yapılandırma — iki bucket ve TTL

**Files:**
- Modify: `core-service/internal/config/config.go:75-81` (R2Config), `:133-137` (BindEnv), `:208-214` (struct doldurma)
- Modify: `core-service/config.yaml:29-34`
- Test: `core-service/internal/config/config_test.go` (yeni)

**Interfaces:**
- Consumes: yok (ilk görev)
- Produces: `config.R2Config{AccountID, AccessKeyID, SecretAccessKey, PublicBucket, PrivateBucket, PublicURL, PresignTTL time.Duration}`. `BucketName` alanı SİLİNİR.

- [ ] **Step 1: Failing test yaz**

`core-service/internal/config/config_test.go`:

```go
package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_R2IkiBucket(t *testing.T) {
	os.Setenv("R2_PUBLIC_BUCKET", "makeasinger-public")
	os.Setenv("R2_PRIVATE_BUCKET", "makeasinger-private")
	os.Setenv("R2_PRESIGN_TTL", "1h")
	defer func() {
		os.Unsetenv("R2_PUBLIC_BUCKET")
		os.Unsetenv("R2_PRIVATE_BUCKET")
		os.Unsetenv("R2_PRESIGN_TTL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.R2.PublicBucket != "makeasinger-public" {
		t.Errorf("PublicBucket = %q, beklenen makeasinger-public", cfg.R2.PublicBucket)
	}
	if cfg.R2.PrivateBucket != "makeasinger-private" {
		t.Errorf("PrivateBucket = %q, beklenen makeasinger-private", cfg.R2.PrivateBucket)
	}
	if cfg.R2.PresignTTL != time.Hour {
		t.Errorf("PresignTTL = %v, beklenen 1h", cfg.R2.PresignTTL)
	}
}

func TestLoad_PresignTTLVarsayilani(t *testing.T) {
	os.Unsetenv("R2_PRESIGN_TTL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.R2.PresignTTL != time.Hour {
		t.Errorf("varsayilan PresignTTL = %v, beklenen 1h", cfg.R2.PresignTTL)
	}
}
```

- [ ] **Step 2: Testi çalıştır, başarısız olduğunu gör**

Run: `cd core-service && go test ./internal/config/ -run TestLoad_R2 -v`
Expected: FAIL — derleme hatası, `cfg.R2.PublicBucket` alanı yok.

- [ ] **Step 3: R2Config'i genişlet**

`internal/config/config.go:75-81` yerine:

```go
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	PublicBucket    string
	PrivateBucket   string
	PublicURL       string
	PresignTTL      time.Duration
}
```

Dosyanın import bloğunda `"time"` yoksa ekle.

- [ ] **Step 4: BindEnv ve varsayılanları güncelle**

`internal/config/config.go:133-137` içindeki iki satırı değiştir — `r2.bucket_name` SİLİNİR:

```go
	_ = viper.BindEnv("r2.account_id", "R2_ACCOUNT_ID")
	_ = viper.BindEnv("r2.access_key_id", "R2_ACCESS_KEY_ID")
	_ = viper.BindEnv("r2.secret_access_key", "R2_SECRET_ACCESS_KEY")
	_ = viper.BindEnv("r2.public_bucket", "R2_PUBLIC_BUCKET")
	_ = viper.BindEnv("r2.private_bucket", "R2_PRIVATE_BUCKET")
	_ = viper.BindEnv("r2.public_url", "R2_PUBLIC_URL")
	_ = viper.BindEnv("r2.presign_ttl", "R2_PRESIGN_TTL")
```

Varsayılanlar bloğuna (`:149-175`) ekle:

```go
	viper.SetDefault("r2.presign_ttl", "1h")
```

- [ ] **Step 5: Struct doldurmayı güncelle**

`internal/config/config.go:208-214` yerine:

```go
		R2: R2Config{
			AccountID:       viper.GetString("r2.account_id"),
			AccessKeyID:     viper.GetString("r2.access_key_id"),
			SecretAccessKey: viper.GetString("r2.secret_access_key"),
			PublicBucket:    viper.GetString("r2.public_bucket"),
			PrivateBucket:   viper.GetString("r2.private_bucket"),
			PublicURL:       viper.GetString("r2.public_url"),
			PresignTTL:      viper.GetDuration("r2.presign_ttl"),
		},
```

- [ ] **Step 6: config.yaml'ı güncelle**

`core-service/config.yaml:29-34` yerine:

```yaml
r2:
  account_id: "" # Set via R2_ACCOUNT_ID env var
  access_key_id: "" # Set via R2_ACCESS_KEY_ID env var
  secret_access_key: "" # Set via R2_SECRET_ACCESS_KEY env var
  public_bucket: "makeasinger-public"
  private_bucket: "makeasinger-private"
  public_url: "" # Set via R2_PUBLIC_URL env var (orn. https://makesinger-cdn.celalettindemir.dev)
  presign_ttl: "1h" # private nesneler icin presigned URL omru
```

- [ ] **Step 7: Testleri çalıştır, geçtiğini gör**

Run: `cd core-service && go test ./internal/config/ -v`
Expected: PASS (2 test)

- [ ] **Step 8: Commit**

```bash
git add core-service/internal/config/config.go core-service/internal/config/config_test.go core-service/config.yaml
git commit -m "feat(config): R2 icin iki bucket ve presign TTL"
```

---

### Task 2: Go — önek kuralı ve URL→anahtar çözümü

**Files:**
- Create: `core-service/internal/client/bucket.go`
- Test: `core-service/internal/client/bucket_test.go`

**Interfaces:**
- Consumes: Task 1'den `config.R2Config`
- Produces:
  - `func BucketFor(key string, cfg *config.R2Config) string` — anahtarın gideceği bucket adı
  - `func IsPublicKey(key string) bool` — `exports/` öneki mi
  - `func KeyFromURL(rawURL string, cfg *config.R2Config) (string, error)` — URL'den nesne anahtarı

- [ ] **Step 1: Failing test yaz**

`core-service/internal/client/bucket_test.go`:

```go
package client

import (
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func testCfg() *config.R2Config {
	return &config.R2Config{
		PublicBucket:  "makeasinger-public",
		PrivateBucket: "makeasinger-private",
		PublicURL:     "https://makesinger-cdn.celalettindemir.dev",
	}
}

func TestBucketFor(t *testing.T) {
	cfg := testCfg()
	durumlar := []struct {
		anahtar  string
		beklenen string
	}{
		{"exports/abc.mp3", "makeasinger-public"},
		{"exports/abc.wav", "makeasinger-public"},
		{"exports/abc.zip", "makeasinger-public"},
		{"vocals/p1/s1/t1.wav", "makeasinger-private"},
		{"masters/p1/m1.wav", "makeasinger-private"},
		{"stems/p1/s1.wav", "makeasinger-private"},
		{"bilinmeyen/x.bin", "makeasinger-private"},
		{"", "makeasinger-private"},
		{"exportsabc.mp3", "makeasinger-private"},
	}
	for _, d := range durumlar {
		if got := BucketFor(d.anahtar, cfg); got != d.beklenen {
			t.Errorf("BucketFor(%q) = %q, beklenen %q", d.anahtar, got, d.beklenen)
		}
	}
}

func TestKeyFromURL(t *testing.T) {
	cfg := testCfg()
	durumlar := []struct {
		ad       string
		url      string
		beklenen string
	}{
		{"cdn adresi", "https://makesinger-cdn.celalettindemir.dev/exports/a.mp3", "exports/a.mp3"},
		{"r2 endpoint", "https://makeasinger-private.r2.cloudflarestorage.com/vocals/p/s/t.wav", "vocals/p/s/t.wav"},
		{"presigned", "https://makeasinger-private.r2.cloudflarestorage.com/masters/p/m.wav?X-Amz-Signature=abc&X-Amz-Expires=3600", "masters/p/m.wav"},
	}
	for _, d := range durumlar {
		got, err := KeyFromURL(d.url, cfg)
		if err != nil {
			t.Errorf("%s: beklenmeyen hata: %v", d.ad, err)
			continue
		}
		if got != d.beklenen {
			t.Errorf("%s: KeyFromURL = %q, beklenen %q", d.ad, got, d.beklenen)
		}
	}
}

func TestKeyFromURL_TaninmayanKonak(t *testing.T) {
	cfg := testCfg()
	if _, err := KeyFromURL("https://kotu.example.com/vocals/p/s/t.wav", cfg); err == nil {
		t.Error("taninmayan konak icin hata bekleniyordu, nil geldi")
	}
}

func TestKeyFromURL_BosVeBozuk(t *testing.T) {
	cfg := testCfg()
	for _, u := range []string{"", "://bozuk", "https://makesinger-cdn.celalettindemir.dev/"} {
		if _, err := KeyFromURL(u, cfg); err == nil {
			t.Errorf("%q icin hata bekleniyordu", u)
		}
	}
}
```

- [ ] **Step 2: Testi çalıştır, başarısız olduğunu gör**

Run: `cd core-service && go test ./internal/client/ -run 'TestBucketFor|TestKeyFromURL' -v`
Expected: FAIL — `undefined: BucketFor`, `undefined: KeyFromURL`

- [ ] **Step 3: Uygulamayı yaz**

`core-service/internal/client/bucket.go`:

```go
package client

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// publicOnek: yalnizca bu onekteki nesneler public bucket'a gider. Liste
// buyurse burasi tek degisim noktasi (ayni kural Python'da bucket_for).
const publicOnek = "exports/"

// IsPublicKey, anahtarin public bucket'a ait olup olmadigini soyler.
func IsPublicKey(key string) bool {
	return strings.HasPrefix(key, publicOnek)
}

// BucketFor, anahtarin yazilacagi/okunacagi bucket adini dondurur.
// Bilinmeyen onek PRIVATE'a duser: yanlis tarafa dusen bir dosya sizinti
// degil, yalnizca erisilemezlik uretsin.
func BucketFor(key string, cfg *config.R2Config) string {
	if IsPublicKey(key) {
		return cfg.PublicBucket
	}
	return cfg.PrivateBucket
}

// KeyFromURL, istemciden gelen bir URL'den nesne anahtarini cikarir.
// Kabul edilen bicimler: <R2_PUBLIC_URL>/<key>, https://<bucket>.r2.
// cloudflarestorage.com/<key> ve bunlarin presigned (sorgu dizeli) hali.
// Taninmayan konak icin hata doner; tahmin edilmez.
func KeyFromURL(rawURL string, cfg *config.R2Config) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("bos URL")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("URL cozulemedi: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL'de konak yok: %s", rawURL)
	}

	tanidik := false
	if cfg.PublicURL != "" {
		if pu, err := url.Parse(cfg.PublicURL); err == nil && pu.Host == u.Host {
			tanidik = true
		}
	}
	for _, b := range []string{cfg.PublicBucket, cfg.PrivateBucket} {
		if b != "" && u.Host == fmt.Sprintf("%s.r2.cloudflarestorage.com", b) {
			tanidik = true
		}
	}
	if !tanidik {
		return "", fmt.Errorf("taninmayan konak: %s", u.Host)
	}

	key := strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return "", fmt.Errorf("URL'de nesne anahtari yok: %s", rawURL)
	}
	return key, nil
}
```

- [ ] **Step 4: Testleri çalıştır, geçtiğini gör**

Run: `cd core-service && go test ./internal/client/ -v`
Expected: PASS (4 test)

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/client/bucket.go core-service/internal/client/bucket_test.go
git commit -m "feat(storage): onek kurali (BucketFor) ve URL'den anahtar cozumu"
```

---

### Task 3: Go — R2Client iki bucket ile çalışsın

**Files:**
- Modify: `core-service/internal/client/r2_client.go` (tamamı)
- Modify: `core-service/cmd/server/main.go:92-102`
- Test: `core-service/internal/client/r2_client_test.go` (yeni)

**Interfaces:**
- Consumes: Task 1 `config.R2Config`, Task 2 `BucketFor`, `IsPublicKey`
- Produces:
  - `StorageClient` arayüzü: `Upload(ctx, key, body, contentType) (string, error)`, `Delete(ctx, key) error`, `GetSignedURL(ctx, key, expiry) (string, error)`, `URLFor(ctx, key) (string, time.Time, error)`
  - `URLFor` public anahtarda kalıcı CDN adresi ve sıfır `time.Time` döndürür; private anahtarda presigned URL ve son kullanma anı döndürür.
  - `GetPublicURL(key string) string` SİLİNİR; yerini `URLFor` alır.

- [ ] **Step 1: Failing test yaz**

`core-service/internal/client/r2_client_test.go`:

```go
package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

func testR2Cfg() *config.R2Config {
	return &config.R2Config{
		AccountID:       "hesap123",
		AccessKeyID:     "anahtar",
		SecretAccessKey: "gizli",
		PublicBucket:    "makeasinger-public",
		PrivateBucket:   "makeasinger-private",
		PublicURL:       "https://makesinger-cdn.celalettindemir.dev",
		PresignTTL:      time.Hour,
	}
}

func TestURLFor_PublicAnahtarKaliciCDNAdresi(t *testing.T) {
	c, err := NewR2Client(testR2Cfg())
	if err != nil {
		t.Fatalf("NewR2Client: %v", err)
	}

	url, expires, err := c.URLFor(context.Background(), "exports/abc.mp3")
	if err != nil {
		t.Fatalf("URLFor: %v", err)
	}
	if url != "https://makesinger-cdn.celalettindemir.dev/exports/abc.mp3" {
		t.Errorf("public URL = %q", url)
	}
	if !expires.IsZero() {
		t.Errorf("public anahtar icin expires sifir olmali, geldi: %v", expires)
	}
}

func TestURLFor_PrivateAnahtarPresignedVeBirSaat(t *testing.T) {
	c, err := NewR2Client(testR2Cfg())
	if err != nil {
		t.Fatalf("NewR2Client: %v", err)
	}

	once := time.Now()
	url, expires, err := c.URLFor(context.Background(), "vocals/p1/s1/t1.wav")
	if err != nil {
		t.Fatalf("URLFor: %v", err)
	}
	if !strings.Contains(url, "X-Amz-Signature") {
		t.Errorf("presigned URL bekleniyordu: %q", url)
	}
	if !strings.Contains(url, "makeasinger-private") {
		t.Errorf("private bucket bekleniyordu: %q", url)
	}
	fark := expires.Sub(once)
	if fark < 59*time.Minute || fark > 61*time.Minute {
		t.Errorf("expires ~1 saat olmali, fark: %v", fark)
	}
}

func TestNewR2Client_EksikYapilandirma(t *testing.T) {
	cfg := testR2Cfg()
	cfg.AccessKeyID = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("eksik yapilandirma icin hata bekleniyordu")
	}
}

func TestNewR2Client_BucketAdlariZorunlu(t *testing.T) {
	cfg := testR2Cfg()
	cfg.PrivateBucket = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("private bucket adi bos iken hata bekleniyordu")
	}
}
```

Not: import bloğunda `"context"` var; `t.Context()` Go 1.24 gerektirdiği ve `go.mod` `go 1.22` dediği için bilerek kullanılmadı.

- [ ] **Step 2: Testi çalıştır, başarısız olduğunu gör**

Run: `cd core-service && go test ./internal/client/ -run TestURLFor -v`
Expected: FAIL — `c.URLFor undefined`

- [ ] **Step 3: R2Client'i iki bucket'a geçir**

`internal/client/r2_client.go` içinde arayüzü ve struct'ı değiştir:

```go
// StorageClient defines the interface for object storage operations
type StorageClient interface {
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, error)
	Delete(ctx context.Context, key string) error
	GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	// URLFor, anahtarin onegine gore dogru adresi uretir: public anahtarda
	// kalici CDN adresi (expires sifir), private anahtarda presigned URL ve
	// son kullanma ani.
	URLFor(ctx context.Context, key string) (string, time.Time, error)
}

// R2Client implements StorageClient for Cloudflare R2
type R2Client struct {
	s3Client   *s3.Client
	presigner  *s3.PresignClient
	cfg        *config.R2Config
}
```

- [ ] **Step 4: Constructor'ı güncelle**

`NewR2Client` içindeki doğrulamayı ve dönüşü değiştir:

```go
func NewR2Client(cfg *config.R2Config) (*R2Client, error) {
	if cfg.AccountID == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("R2 configuration incomplete")
	}
	if cfg.PublicBucket == "" || cfg.PrivateBucket == "" {
		return nil, fmt.Errorf("R2 bucket adlari eksik: public=%q private=%q", cfg.PublicBucket, cfg.PrivateBucket)
	}
	// ... endpoint/resolver/awsCfg blogu AYNEN KALIR ...

	s3Client := s3.NewFromConfig(awsCfg)
	presigner := s3.NewPresignClient(s3Client)

	return &R2Client{
		s3Client:  s3Client,
		presigner: presigner,
		cfg:       cfg,
	}, nil
}
```

- [ ] **Step 5: Upload, Delete, GetSignedURL ve URLFor'u yaz**

`Upload`, `Delete`, `GetSignedURL` gövdelerinde `c.bucketName` yerine `BucketFor` kullan; `GetPublicURL`'ü `URLFor` ile değiştir:

```go
// Upload uploads a file to R2 and returns its URL (public keys: CDN address,
// private keys: presigned URL).
func (c *R2Client) Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(BucketFor(key, c.cfg)),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}

	if _, err := c.s3Client.PutObject(ctx, input); err != nil {
		return "", fmt.Errorf("failed to upload to R2: %w", err)
	}

	url, _, err := c.URLFor(ctx, key)
	return url, err
}

// Delete removes a file from R2
func (c *R2Client) Delete(ctx context.Context, key string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(BucketFor(key, c.cfg)),
		Key:    aws.String(key),
	}

	if _, err := c.s3Client.DeleteObject(ctx, input); err != nil {
		return fmt.Errorf("failed to delete from R2: %w", err)
	}
	return nil
}

// GetSignedURL generates a presigned URL for temporary access
func (c *R2Client) GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(BucketFor(key, c.cfg)),
		Key:    aws.String(key),
	}

	presignedReq, err := c.presigner.PresignGetObject(ctx, input, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}
	return presignedReq.URL, nil
}

// URLFor returns the address a client should use for this key.
func (c *R2Client) URLFor(ctx context.Context, key string) (string, time.Time, error) {
	if IsPublicKey(key) {
		if c.cfg.PublicURL != "" {
			return fmt.Sprintf("%s/%s", c.cfg.PublicURL, key), time.Time{}, nil
		}
		return fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s", c.cfg.PublicBucket, key), time.Time{}, nil
	}

	url, err := c.GetSignedURL(ctx, key, c.cfg.PresignTTL)
	if err != nil {
		return "", time.Time{}, err
	}
	return url, time.Now().Add(c.cfg.PresignTTL), nil
}

// IsConfigured returns true if the client has valid configuration
func (c *R2Client) IsConfigured() bool {
	return c.s3Client != nil && c.cfg != nil && c.cfg.PrivateBucket != ""
}
```

- [ ] **Step 6: Testleri çalıştır, geçtiğini gör**

Run: `cd core-service && go test ./internal/client/ -v`
Expected: PASS (8 test: Task 2'nin 4'ü + bu görevin 4'ü)

- [ ] **Step 7: Derlemeyi kontrol et ve kalan çağrı yerlerini düzelt**

Run: `cd core-service && go build ./...`
Expected: `internal/worker/render_worker.go:204` `GetPublicURL` çağrısı yüzünden FAIL.

`internal/worker/render_worker.go:203-204` yerine:

```go
		key := fmt.Sprintf("stems/%s/%s.wav", projectID, stemID)
		fileURL, _, err := w.r2Client.URLFor(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("stem URL uretilemedi: %w", err)
		}
```

Not: `fileURL`'ün atandığı satırın üstündeki değişken adlarını (`projectID`, `stemID`, `ctx`) çevredeki koddan aynen al; bu blok `uploadStems` içinde.

- [ ] **Step 8: Derleme ve testler**

Run: `cd core-service && go build ./... && go vet ./... && go test ./internal/...`
Expected: derleme OK, vet temiz, testler PASS

- [ ] **Step 9: Commit**

```bash
git add core-service/internal/client/r2_client.go core-service/internal/client/r2_client_test.go core-service/internal/worker/render_worker.go
git commit -m "feat(storage): R2Client iki bucket ile calissin, URLFor eklendi"
```

---

### Task 4: Go — kablo anahtara geçsin

**Files:**
- Modify: `core-service/internal/client/audio_client.go:39-90` (istek modelleri)
- Modify: `core-service/internal/worker/master_worker.go:83-96`
- Modify: `core-service/internal/service/export_service.go:47,107,140,146-169`
- Test: `core-service/internal/client/audio_client_test.go` (yeni)

**Interfaces:**
- Consumes: Task 2 `KeyFromURL`
- Produces: Kablo modelleri `MasterRequest{StemKeys []string, MixSettings, Profile, VocalTakes []VocalTakeInput{Key, Volume, Pan}, OutputKey}`, `EncodeRequest{InputKey, Format, ...}`, `ZipFileEntry{Key, Filename}`

- [ ] **Step 1: Failing test yaz**

`core-service/internal/client/audio_client_test.go`:

```go
package client

import (
	"encoding/json"
	"testing"
)

func TestMasterRequest_AnahtarAlanlariJSON(t *testing.T) {
	req := MasterRequest{
		StemKeys:   []string{"stems/p1/s1.wav"},
		Profile:    "clean",
		VocalTakes: []VocalTakeInput{{Key: "vocals/p1/s1/t1.wav", Volume: 1.0}},
		OutputKey:  "masters/p1/m1.wav",
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, var_ := cikti["stem_keys"]; !var_ {
		t.Errorf("stem_keys alani yok: %s", b)
	}
	if _, yok := cikti["stem_urls"]; yok {
		t.Errorf("stem_urls alani hala duruyor: %s", b)
	}

	takes := cikti["vocal_takes"].([]interface{})
	ilk := takes[0].(map[string]interface{})
	if _, var_ := ilk["key"]; !var_ {
		t.Errorf("vocal_takes[].key alani yok: %s", b)
	}
}

func TestEncodeRequest_InputKeyJSON(t *testing.T) {
	req := EncodeRequest{InputKey: "masters/p1/m1.wav", Format: "mp3", OutputKey: "exports/e1.mp3"}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cikti["input_key"] != "masters/p1/m1.wav" {
		t.Errorf("input_key = %v", cikti["input_key"])
	}
	if _, yok := cikti["input_url"]; yok {
		t.Errorf("input_url alani hala duruyor: %s", b)
	}
}

func TestZipFileEntry_KeyJSON(t *testing.T) {
	b, err := json.Marshal(ZipFileEntry{Key: "stems/p1/s1.wav", Filename: "s1.wav"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cikti["key"] != "stems/p1/s1.wav" {
		t.Errorf("key = %v", cikti["key"])
	}
}
```

- [ ] **Step 2: Testi çalıştır, başarısız olduğunu gör**

Run: `cd core-service && go test ./internal/client/ -run 'TestMasterRequest|TestEncodeRequest|TestZipFileEntry' -v`
Expected: FAIL — `unknown field StemKeys in struct literal`

- [ ] **Step 3: Kablo modellerini değiştir**

`internal/client/audio_client.go:39-90` içindeki dört struct:

```go
type VocalTakeInput struct {
	Key    string  `json:"key"`
	Volume float64 `json:"volume"`
	Pan    float64 `json:"pan,omitempty"`
}

// MasterRequest represents the request for mastering
type MasterRequest struct {
	StemKeys    []string         `json:"stem_keys"`
	MixSettings []MixChannel     `json:"mix_settings"`
	Profile     string           `json:"profile"`
	VocalTakes  []VocalTakeInput `json:"vocal_takes,omitempty"`
	OutputKey   string           `json:"output_key"`
}

// EncodeRequest represents the request for encoding
type EncodeRequest struct {
	InputKey   string            `json:"input_key"`
	Format     string            `json:"format"`
	Quality    int               `json:"quality,omitempty"`
	SampleRate int               `json:"sample_rate,omitempty"`
	BitDepth   int               `json:"bit_depth,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	OutputKey  string            `json:"output_key"`
}

// ZipFileEntry represents a file to include in the ZIP
type ZipFileEntry struct {
	Key      string `json:"key"`
	Filename string `json:"filename"`
}
```

- [ ] **Step 4: Testleri çalıştır, geçtiğini gör**

Run: `cd core-service && go test ./internal/client/ -v`
Expected: PASS (11 test)

- [ ] **Step 5: Derlemeyi çalıştır, kırılan çağrı yerlerini gör**

Run: `cd core-service && go build ./...`
Expected: FAIL — `master_worker.go` ve `export_service.go` eski alan adlarını kullanıyor.

- [ ] **Step 6: master_worker.go'yu anahtara geçir**

`internal/worker/master_worker.go` içinde `MasterRequest` kurulan yeri (~`:83-96`) şöyle yap. İstemciden gelen her URL `KeyFromURL` ile anahtara çevrilir:

```go
	stemKeys := make([]string, 0, len(payload.StemURLs))
	for _, u := range payload.StemURLs {
		k, err := client.KeyFromURL(u, w.r2Cfg)
		if err != nil {
			return fmt.Errorf("stem URL cozulemedi: %w", err)
		}
		stemKeys = append(stemKeys, k)
	}

	vocalTakes := make([]client.VocalTakeInput, 0, len(payload.VocalTakes))
	for _, vt := range payload.VocalTakes {
		k, err := client.KeyFromURL(vt.FileURL, w.r2Cfg)
		if err != nil {
			return fmt.Errorf("vokal URL cozulemedi: %w", err)
		}
		vocalTakes = append(vocalTakes, client.VocalTakeInput{Key: k, Volume: vt.Volume, Pan: vt.Pan})
	}

	outputKey := fmt.Sprintf("masters/%s/%s.wav", payload.ProjectID, uuid.New().String())

	req := client.MasterRequest{
		StemKeys:    stemKeys,
		MixSettings: mixSettings,
		Profile:     payload.Profile,
		VocalTakes:  vocalTakes,
		OutputKey:   outputKey,
	}
```

`MasterWorker` struct'ına `r2Cfg *config.R2Config` alanı ekle ve `NewMasterWorker` imzasına parametre olarak geçir; `mixSettings` değişkenini çevredeki mevcut koddan aynen kullan.

- [ ] **Step 7: export_service.go'yu anahtara geçir**

`internal/service/export_service.go` içinde `EncodeRequest` kurulan iki yerde (`:47` ve `:107` civarı) `InputURL: req.MasterFileURL` yerine:

```go
	inputKey, err := client.KeyFromURL(req.MasterFileURL, s.r2Cfg)
	if err != nil {
		return nil, fmt.Errorf("master URL cozulemedi: %w", err)
	}
	// ... encodeReq icinde:
	InputKey: inputKey,
```

`:146-169` arasındaki `ZipFileEntry` listesinde her `URL:` alanını anahtara çevir:

```go
	key, err := client.KeyFromURL(u, s.r2Cfg)
	if err != nil {
		return nil, fmt.Errorf("zip girdisi cozulemedi: %w", err)
	}
	files = append(files, client.ZipFileEntry{Key: key, Filename: filename})
```

`ExportService` struct'ına `r2Cfg *config.R2Config` alanı ekle ve `NewExportService` imzasına parametre olarak geçir.

- [ ] **Step 8: main.go'daki constructor çağrılarını güncelle**

`cmd/server/main.go:120` ve `:313`:

```go
	exportService := service.NewExportService(r2Client, audioClient, &cfg.R2)
	// ...
	masterWorker := worker.NewMasterWorker(redisClient, audioClient, r2Client, masterService, hub, &cfg.R2)
```

`startWorkerServer` imzasına `&cfg.R2` taşınması gerekiyorsa `cfg` zaten parametre olarak var; `&cfg.R2` doğrudan kullanılabilir.

- [ ] **Step 9: Derleme, vet ve testler**

Run: `cd core-service && go build ./... && go vet ./... && go test ./internal/...`
Expected: hepsi temiz.

Not: `e2e/` paketi bu plandan ÖNCE de kırmızıydı (Groq 401). `go test ./e2e/...` çalıştırma; `./internal/...` yeterli. Ancak `go build ./...` e2e derlemesini de kapsar — e2e içinde `NewExportService(nil, nil)` ve `NewUploadService(nil)` çağrıları var, yeni imzaya göre `NewExportService(nil, nil, &config.R2Config{})` şeklinde güncellenmeli (derleme için zorunlu).

- [ ] **Step 10: Commit**

```bash
git add core-service/internal/client/audio_client.go core-service/internal/client/audio_client_test.go core-service/internal/worker/master_worker.go core-service/internal/service/export_service.go core-service/cmd/server/main.go core-service/e2e/testhelper_test.go
git commit -m "feat(storage): Go->Python kablosu URL yerine nesne anahtari tasisin"
```

---

### Task 5: Go — API yanıtlarında expiresAt

**Files:**
- Modify: `core-service/internal/model/upload.go:5-13`
- Modify: `core-service/internal/model/render.go:94-101`
- Modify: `core-service/internal/service/upload_service.go:33-58`
- Modify: `core-service/internal/service/export_service.go:85,128,185,198,211,230`
- Test: `core-service/internal/model/model_test.go` (yeni)

**Interfaces:**
- Consumes: Task 3 `URLFor`
- Produces: Bütün yanıt modellerinde `ExpiresAt *time.Time \`json:"expiresAt,omitempty"\``. Kalıcı (public) linklerde alan `nil` olur ve JSON'da HİÇ görünmez. `time.Time{}` bırakmak istemciye `0001-01-01T00:00:00Z` gönderirdi; bu, kalıcı bir linki süresi dolmuş gibi gösterir.
- Produces: `func ExpiresPtr(t time.Time) *time.Time` (`internal/model/expires.go`) — sıfır zamanı `nil`'e çevirir.

- [ ] **Step 1: Failing test yaz**

`core-service/internal/model/model_test.go`:

```go
package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExpiresPtr_SifirZamanNil(t *testing.T) {
	if ExpiresPtr(time.Time{}) != nil {
		t.Error("sifir zaman icin nil bekleniyordu")
	}
	son := time.Now().Add(time.Hour)
	p := ExpiresPtr(son)
	if p == nil || !p.Equal(son) {
		t.Errorf("ExpiresPtr degeri kaybetti: %v", p)
	}
}

func TestUploadVocalResponse_SureliLinkteExpiresAtVar(t *testing.T) {
	son := time.Now().Add(time.Hour)
	b, err := json.Marshal(UploadVocalResponse{ID: "t1", FileURL: "https://x/y", ExpiresAt: ExpiresPtr(son)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, varmi := cikti["expiresAt"]; !varmi {
		t.Errorf("expiresAt alani yok: %s", b)
	}
}

func TestExportResponse_KaliciLinkteExpiresAtHicYok(t *testing.T) {
	b, err := json.Marshal(ExportMP3Response{FileURL: "https://cdn/exports/a.mp3", ExpiresAt: ExpiresPtr(time.Time{})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, varmi := cikti["expiresAt"]; varmi {
		t.Errorf("kalici link icin expiresAt hic olmamali: %s", b)
	}
}

func TestStemResult_ExpiresAtAlani(t *testing.T) {
	b, err := json.Marshal(StemResult{ID: "s1", FileURL: "https://x/y", ExpiresAt: ExpiresPtr(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, varmi := cikti["expiresAt"]; !varmi {
		t.Errorf("expiresAt alani yok: %s", b)
	}
}
```

- [ ] **Step 2: Testi çalıştır, başarısız olduğunu gör**

Run: `cd core-service && go test ./internal/model/ -v`
Expected: FAIL — `unknown field ExpiresAt`

- [ ] **Step 3: ExpiresPtr yardımcısını ve model alanlarını yaz**

`internal/model/expires.go` (yeni):

```go
package model

import "time"

// ExpiresPtr, sifir zamani nil'e cevirir. Kalici (public) linklerde
// expiresAt alani JSON'da HIC gorunmesin diye: `0001-01-01T00:00:00Z`
// gondermek kalici bir linki suresi dolmus gibi gosterirdi.
func ExpiresPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
```

`internal/model/upload.go:5-13`:

```go
// UploadVocalResponse represents the response for vocal upload
type UploadVocalResponse struct {
	ID         string    `json:"id"`
	FileURL    string    `json:"fileUrl"`
	Duration   float64   `json:"duration"`
	SampleRate int       `json:"sampleRate"`
	Channels   int       `json:"channels"`
	CreatedAt  time.Time `json:"createdAt"`
	// ExpiresAt: fileUrl presigned ve SURELI. Istemci bu adresi onbellege
	// alip sonra kirik link gostermesin.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
```

`internal/model/render.go:94-101`:

```go
// StemResult represents a single stem in the render result
type StemResult struct {
	ID           string     `json:"id"`
	Instrument   Instrument `json:"instrument"`
	FileURL      string     `json:"fileUrl"`
	Duration     float64    `json:"duration"`
	WaveformData []float64  `json:"waveformData"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
}
```

`render.go` import bloğunda `"time"` yoksa ekle.

Mevcut beş modelde `ExpiresAt` alanı `time.Time` olarak duruyor; hepsini işaretçiye çevir:
- `internal/model/master.go:29-33` `MasterPreviewResponse`
- `internal/model/master.go:68-75` `MasterResultResponse`
- `internal/model/export.go:23-29` `ExportMP3Response`
- `internal/model/export.go:40-47` `ExportWAVResponse`
- `internal/model/export.go:61-66` `ExportStemsResponse`

Her birinde alan şu hale gelir:

```go
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
```

- [ ] **Step 4: Testleri çalıştır, geçtiğini gör**

Run: `cd core-service && go test ./internal/model/ -v`
Expected: PASS (2 test)

- [ ] **Step 5: upload_service.go'yu URLFor'a geçir**

`internal/service/upload_service.go:45-58` içinde `Upload` sonrası URL üretimini değiştir:

```go
	if _, err := s.r2Client.Upload(ctx, key, file, "audio/wav"); err != nil {
		return nil, fmt.Errorf("vokal yuklenemedi: %w", err)
	}

	fileURL, expiresAt, err := s.r2Client.URLFor(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("vokal URL uretilemedi: %w", err)
	}

	return &model.UploadVocalResponse{
		ID:        takeID,
		FileURL:   fileURL,
		ExpiresAt: model.ExpiresPtr(expiresAt),
		CreatedAt: time.Now(),
	}, nil
```

Diğer alanların (`Duration`, `SampleRate`, `Channels`) mevcut doldurma biçimini aynen koru.

- [ ] **Step 6: export_service.go'daki 24 saati kaldır**

`export_service.go` içinde `ExpiresAt: time.Now().Add(24 * time.Hour)` geçen HER satırı (`:85,128,185,198,211,230`) şununla değiştir — export'lar public ve kalıcı, alan JSON'da hiç görünmez:

```go
		ExpiresAt: nil,
```

- [ ] **Step 7: audio-service'ten dönen adresleri URLFor'dan geçir**

audio-service yanıtındaki `output_url` doğrudan istemciye VERİLMEZ: o adres imzasızdır ve private nesnede çalışmaz. Dönen anahtar zaten bilindiği için adres yeniden üretilir.

`internal/service/export_service.go` içinde encode/zip yanıtı işlenen her yerde (`:85` ve `:128` civarı MP3/WAV, `:185` civarı stems), `FileURL: resp.OutputURL` yerine:

```go
	fileURL, expiresAt, err := s.r2Client.URLFor(ctx, outputKey)
	if err != nil {
		return nil, fmt.Errorf("export URL uretilemedi: %w", err)
	}
	// ... yanit icinde:
	FileURL:   fileURL,
	ExpiresAt: model.ExpiresPtr(expiresAt),
```

`outputKey` değişkeni o fonksiyonda zaten var (`exports/%s.mp3` biçiminde kurulan değer).

`internal/worker/master_worker.go` içinde master sonucu yazılırken (`MasterResultResponse` kurulan yer, `:100` civarı) aynı düzeltme — `outputKey` Step 6'da kurulan `masters/...` değeridir:

```go
	fileURL, expiresAt, err := w.r2Client.URLFor(ctx, outputKey)
	if err != nil {
		return fmt.Errorf("master URL uretilemedi: %w", err)
	}
	// ... sonuc icinde:
	FileURL:   fileURL,
	ExpiresAt: model.ExpiresPtr(expiresAt),
```

`internal/worker/render_worker.go:203-204` Task 3'te `URLFor`'a geçirilmişti; oradaki `StemResult` kurulumuna da `ExpiresAt: model.ExpiresPtr(expiresAt)` ekle (ikinci dönüş değerini artık `_` ile atma).

- [ ] **Step 8: Derleme, vet ve testler**

Run: `cd core-service && go build ./... && go vet ./... && go test ./internal/...`
Expected: hepsi temiz.

- [ ] **Step 9: Commit**

```bash
git add core-service/internal/model core-service/internal/service core-service/internal/worker
git commit -m "feat(api): private dosya yanitlarina expiresAt, export'larda kalici URL"
```

---

### Task 6: Python — test altyapısı ve önek kuralı

**Files:**
- Create: `audio-service/requirements-dev.txt`
- Create: `audio-service/tests/__init__.py`, `audio-service/tests/test_storage.py`
- Modify: `audio-service/services/storage.py`

**Interfaces:**
- Consumes: yok (Go tarafından bağımsız)
- Produces:
  - `services/storage.py`: `PUBLIC_ONEK = "exports/"`, `bucket_for(key: str, public_bucket: str, private_bucket: str) -> str`
  - `StorageService(account_id, access_key_id, secret_access_key, public_bucket, private_bucket, public_url="", s3_client=None)`
  - `StorageService.upload(key, data, content_type) -> str`, `upload_file(key, path, content_type) -> str`
  - `StorageService.url_for(key) -> str`
  - `StorageService.download_key_to_file(key: str, path: str) -> None` — S3 ile indirir
  - `StorageService.delete(key) -> None`

- [ ] **Step 1: Test bağımlılıklarını ekle**

`audio-service/requirements-dev.txt` (yeni):

```
-r requirements.txt
pytest==8.0.0
pytest-asyncio==0.23.4
```

Kurulum: `cd audio-service && python3 -m pip install -r requirements-dev.txt`

Not: `moto` BILEREK eklenmedi; testler sahte bir S3 istemcisi enjekte eder (`s3_client=` parametresi), agir bir bagimlilik getirmeye gerek yok.

- [ ] **Step 2: Failing test yaz**

`audio-service/tests/__init__.py`: boş dosya.

`audio-service/tests/test_storage.py`:

```python
"""StorageService testleri. Ag erisimi yok: sahte S3 istemcisi enjekte edilir."""

import pytest

from services.storage import StorageService, bucket_for


class SahteS3:
    """boto3 s3 istemcisinin testte kullanilan uclari."""

    def __init__(self):
        self.yuklenenler = []
        self.indirilenler = []
        self.silinenler = []

    def upload_fileobj(self, data, bucket, key, ExtraArgs=None):
        self.yuklenenler.append((bucket, key, ExtraArgs))

    def download_file(self, bucket, key, path):
        self.indirilenler.append((bucket, key, path))
        with open(path, "wb") as f:
            f.write(b"ses")

    def delete_object(self, Bucket, Key):
        self.silinenler.append((Bucket, Key))


@pytest.fixture
def servis():
    return StorageService(
        account_id="hesap",
        access_key_id="anahtar",
        secret_access_key="gizli",
        public_bucket="makeasinger-public",
        private_bucket="makeasinger-private",
        public_url="https://makesinger-cdn.celalettindemir.dev",
        s3_client=SahteS3(),
    )


@pytest.mark.parametrize(
    "anahtar,beklenen",
    [
        ("exports/a.mp3", "makeasinger-public"),
        ("exports/a.zip", "makeasinger-public"),
        ("vocals/p/s/t.wav", "makeasinger-private"),
        ("masters/p/m.wav", "makeasinger-private"),
        ("stems/p/s.wav", "makeasinger-private"),
        ("bilinmeyen/x.bin", "makeasinger-private"),
        ("", "makeasinger-private"),
        ("exportsa.mp3", "makeasinger-private"),
    ],
)
def test_bucket_for(anahtar, beklenen):
    assert bucket_for(anahtar, "makeasinger-public", "makeasinger-private") == beklenen


def test_upload_dogru_bucket_secer(servis):
    servis.upload("exports/a.mp3", b"veri", "audio/mpeg")
    servis.upload("vocals/p/s/t.wav", b"veri", "audio/wav")

    bucketlar = [y[0] for y in servis.s3_client.yuklenenler]
    assert bucketlar == ["makeasinger-public", "makeasinger-private"]


def test_url_for_public_cdn_adresi(servis):
    assert servis.url_for("exports/a.mp3") == "https://makesinger-cdn.celalettindemir.dev/exports/a.mp3"


def test_url_for_private_cdn_adresi_vermez(servis):
    url = servis.url_for("vocals/p/s/t.wav")
    assert "makesinger-cdn" not in url
    assert "makeasinger-private" in url


def test_download_key_to_file_s3_kullanir(servis, tmp_path):
    hedef = tmp_path / "in.wav"
    servis.download_key_to_file("masters/p/m.wav", str(hedef))

    assert servis.s3_client.indirilenler == [("makeasinger-private", "masters/p/m.wav", str(hedef))]
    assert hedef.read_bytes() == b"ses"


def test_delete_dogru_bucket(servis):
    servis.delete("stems/p/s.wav")
    assert servis.s3_client.silinenler == [("makeasinger-private", "stems/p/s.wav")]
```

- [ ] **Step 3: Testi çalıştır, başarısız olduğunu gör**

Run: `cd audio-service && python3 -m pytest tests/ -v`
Expected: FAIL — `ImportError: cannot import name 'bucket_for'`

- [ ] **Step 4: storage.py'yi yaz**

`audio-service/services/storage.py` tamamı:

```python
"""Storage service for R2/S3 operations."""

import io
from typing import BinaryIO

import boto3

# Yalnizca bu onekteki nesneler public bucket'a gider. Ayni kural Go'da
# internal/client/bucket.go icindeki publicOnek sabiti; ikisi birlikte
# degistirilir.
PUBLIC_ONEK = "exports/"


def bucket_for(key: str, public_bucket: str, private_bucket: str) -> str:
    """Anahtarin yazilacagi/okunacagi bucket adi.

    Bilinmeyen onek PRIVATE'a duser: yanlis tarafa dusen bir dosya sizinti
    degil, yalnizca erisilemezlik uretsin.
    """
    if key.startswith(PUBLIC_ONEK):
        return public_bucket
    return private_bucket


class StorageService:
    """Handles file storage operations with Cloudflare R2."""

    def __init__(
        self,
        account_id: str,
        access_key_id: str,
        secret_access_key: str,
        public_bucket: str,
        private_bucket: str,
        public_url: str = "",
        s3_client=None,
    ):
        self.public_bucket = public_bucket
        self.private_bucket = private_bucket
        self.public_url = public_url

        if s3_client is not None:
            # Testler sahte istemci enjekte eder; ag erisimi olmaz.
            self.s3_client = s3_client
            return

        endpoint_url = f"https://{account_id}.r2.cloudflarestorage.com"

        self.s3_client = boto3.client(
            "s3",
            endpoint_url=endpoint_url,
            aws_access_key_id=access_key_id,
            aws_secret_access_key=secret_access_key,
            region_name="auto",
        )

    def _bucket(self, key: str) -> str:
        return bucket_for(key, self.public_bucket, self.private_bucket)

    def download_key_to_file(self, key: str, path: str) -> None:
        """Nesneyi S3 API ile indirir.

        Servisler arasi aktarim presigned URL KULLANMAZ: private nesneler
        icin acik bir adres uretmek gereksiz risk, ayrica yavas.
        """
        self.s3_client.download_file(self._bucket(key), key, path)

    def upload(self, key: str, data: bytes | BinaryIO, content_type: str) -> str:
        """Upload data to R2 and return its URL."""
        if isinstance(data, bytes):
            data = io.BytesIO(data)

        self.s3_client.upload_fileobj(
            data,
            self._bucket(key),
            key,
            ExtraArgs={"ContentType": content_type},
        )

        return self.url_for(key)

    def upload_file(self, key: str, path: str, content_type: str) -> str:
        """Upload a file from local path to R2."""
        with open(path, "rb") as f:
            return self.upload(key, f, content_type)

    def url_for(self, key: str) -> str:
        """Anahtarin adresi. Public anahtarda CDN, private anahtarda R2 ucu.

        Private nesneler icin istemciye giden presigned URL'yi core-service
        uretir (URLFor); burada uretilen adres yalnizca Go'ya donen
        output_url alanidir ve Go onu anahtara cevirip yeniden imzalar.
        """
        bucket = self._bucket(key)
        if bucket == self.public_bucket and self.public_url:
            return f"{self.public_url}/{key}"
        return f"https://{bucket}.r2.cloudflarestorage.com/{key}"

    def delete(self, key: str) -> None:
        """Delete a file from R2."""
        self.s3_client.delete_object(Bucket=self._bucket(key), Key=key)
```

Not: `download` ve `download_to_file` (httpx ile URL indirme) SILINDI; artik hicbir cagiran yok (Task 7 onlari da gecirir). `httpx` importu da kalkti.

- [ ] **Step 5: Testleri çalıştır, geçtiğini gör**

Run: `cd audio-service && python3 -m pytest tests/ -v`
Expected: PASS (13 test)

- [ ] **Step 6: Commit**

```bash
git add audio-service/requirements-dev.txt audio-service/tests audio-service/services/storage.py
git commit -m "feat(audio): iki bucket destegi, S3 ile indirme ve ilk testler"
```

---

### Task 7: Python — çağrı yerleri ve istek modelleri anahtara geçsin

**Files:**
- Modify: `audio-service/main.py:31-36` (env), `:48-92` (modeller), `:109-126` (lifespan)
- Modify: `audio-service/services/master.py:74,163,99-100`
- Modify: `audio-service/services/encoder.py:37,62-63`
- Modify: `audio-service/services/archiver.py:44,54-55`
- Test: `audio-service/tests/test_islemler.py` (yeni)

**Interfaces:**
- Consumes: Task 6 `StorageService.download_key_to_file`, Task 4 kablo alan adları (`stem_keys`, `key`, `input_key`)
- Produces: FastAPI modelleri `MasterRequest{stem_keys, mix_settings, profile, vocal_takes[{key, volume, pan}], output_key}`, `EncodeRequest{input_key, ...}`, `ZipFileEntry{key, filename}`

- [ ] **Step 1: Failing test yaz**

`audio-service/tests/test_islemler.py`:

```python
"""Islem servisleri anahtarla mi calisiyor? Ag ve ffmpeg yok: sahteler kullanilir."""

import pytest

from main import EncodeRequest, MasterRequest, ZipFileEntry
from services.encoder import EncoderService


class SahteStorage:
    def __init__(self):
        self.indirilen_anahtarlar = []
        self.yuklenenler = []

    def download_key_to_file(self, key, path):
        self.indirilen_anahtarlar.append(key)
        with open(path, "wb") as f:
            f.write(b"ses")

    def upload_file(self, key, path, content_type):
        self.yuklenenler.append(key)
        return f"https://makeasinger-private.r2.cloudflarestorage.com/{key}"


def test_master_request_anahtar_alanlari():
    req = MasterRequest(
        stem_keys=["stems/p/s.wav"],
        mix_settings=[],
        vocal_takes=[{"key": "vocals/p/s/t.wav", "volume": 1.0}],
        output_key="masters/p/m.wav",
    )
    assert req.stem_keys == ["stems/p/s.wav"]
    assert req.vocal_takes[0].key == "vocals/p/s/t.wav"


def test_master_request_eski_alan_reddedilir():
    with pytest.raises(Exception):
        MasterRequest(stem_urls=["https://x/stems/p/s.wav"], mix_settings=[], output_key="masters/p/m.wav")


def test_encode_request_input_key():
    req = EncodeRequest(input_key="masters/p/m.wav", format="mp3", output_key="exports/e.mp3")
    assert req.input_key == "masters/p/m.wav"


def test_zip_file_entry_key():
    e = ZipFileEntry(key="stems/p/s.wav", filename="s.wav")
    assert e.key == "stems/p/s.wav"


@pytest.mark.asyncio
async def test_encoder_anahtarla_indirir(monkeypatch, tmp_path):
    storage = SahteStorage()
    servis = EncoderService(storage)

    # ffmpeg cagrilmasin: donusturme adimi sahteye alinir.
    def sahte_donustur(girdi, cikti, *a, **kw):
        with open(cikti, "wb") as f:
            f.write(b"cikti")

    monkeypatch.setattr(servis, "_transcode", sahte_donustur, raising=False)

    await servis.encode(input_key="masters/p/m.wav", output_key="exports/e.mp3", format="mp3")

    assert storage.indirilen_anahtarlar == ["masters/p/m.wav"]
    assert storage.yuklenenler == ["exports/e.mp3"]
```

Not: `EncoderService.encode` imzası bu testte `input_key=`/`output_key=` bekler; Step 4'te imza buna göre değiştirilir. `_transcode` adı mevcut koddaki ffmpeg çağrısını saran yardımcının adıdır — kodda farklı bir ad varsa testteki `monkeypatch.setattr` hedefini o ada göre düzelt.

- [ ] **Step 2: Testi çalıştır, başarısız olduğunu gör**

Run: `cd audio-service && python3 -m pytest tests/test_islemler.py -v`
Expected: FAIL — `MasterRequest` hâlâ `stem_urls` alanını taşıyor.

- [ ] **Step 3: main.py modellerini ve env'i güncelle**

`audio-service/main.py:31-36`:

```python
# Configuration from environment
R2_ACCOUNT_ID = os.getenv("R2_ACCOUNT_ID", "")
R2_ACCESS_KEY_ID = os.getenv("R2_ACCESS_KEY_ID", "")
R2_SECRET_ACCESS_KEY = os.getenv("R2_SECRET_ACCESS_KEY", "")
R2_PUBLIC_BUCKET = os.getenv("R2_PUBLIC_BUCKET", "makeasinger-public")
R2_PRIVATE_BUCKET = os.getenv("R2_PRIVATE_BUCKET", "makeasinger-private")
R2_PUBLIC_URL = os.getenv("R2_PUBLIC_URL", "")
```

`:48-92` arasındaki üç modeli değiştir:

```python
class VocalTakeInput(BaseModel):
    key: str
    volume: float = 1.0
    pan: float = 0.0


class MasterRequest(BaseModel):
    model_config = {"extra": "forbid"}

    stem_keys: list[str]
    mix_settings: list[MixChannel]
    profile: str = "clean"  # clean, warm, loud
    vocal_takes: list[VocalTakeInput] = []
    output_key: str


class EncodeRequest(BaseModel):
    input_key: str
    format: str  # mp3, wav
    quality: int = 320  # for mp3
    sample_rate: int = 48000
    bit_depth: int = 24  # for wav
    metadata: dict[str, str] = {}
    output_key: str


class ZipFileEntry(BaseModel):
    key: str
    filename: str
```

`model_config = {"extra": "forbid"}` BILINCLI: eski `stem_urls` alaniyla gelen bir istek sessizce bos stem listesiyle calismasin, acikca reddedilsin.

`:109-126` lifespan içindeki constructor:

```python
        storage_service = StorageService(
            account_id=R2_ACCOUNT_ID,
            access_key_id=R2_ACCESS_KEY_ID,
            secret_access_key=R2_SECRET_ACCESS_KEY,
            public_bucket=R2_PUBLIC_BUCKET,
            private_bucket=R2_PRIVATE_BUCKET,
            public_url=R2_PUBLIC_URL,
        )
```

Endpoint gövdelerinde `req.stem_urls` → `req.stem_keys`, `req.input_url` → `req.input_key`, `f.url` → `f.key` olarak güncelle.

- [ ] **Step 4: Servisleri anahtara geçir**

`services/encoder.py:37` civarı — `encode` imzası ve indirme:

```python
    async def encode(self, input_key: str, output_key: str, format: str, **kw) -> str:
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        self.storage.download_key_to_file(input_key, input_path)
```

`services/master.py:74` (stem'ler) ve `:163` (vokaller):

```python
            self.storage.download_key_to_file(stem_key, stem_path)
            # ...
            self.storage.download_key_to_file(take.key, vocal_path)
```

`master.py:74` bugün `if self.storage` koruması OLMADAN çağrılıyor; `None` gelirse `AttributeError` verir. Fonksiyonun başına açık bir kontrol ekle:

```python
        if not self.storage:
            raise RuntimeError("Storage service not configured")
```

`services/archiver.py:44`:

```python
            self.storage.download_key_to_file(entry.key, temp_path)
```

Her üç dosyada `await` KALKAR: `download_key_to_file` senkron bir boto3 çağrısıdır. Çağıran fonksiyonlar `async` kalabilir.

- [ ] **Step 5: Testleri çalıştır, geçtiğini gör**

Run: `cd audio-service && python3 -m pytest tests/ -v`
Expected: PASS (18 test)

- [ ] **Step 6: Lint**

Run: `cd audio-service && python3 -m ruff check . && python3 -m ruff format --check .`
Expected: temiz. (ruff kurulu değilse: `python3 -m pip install ruff`)

- [ ] **Step 7: Commit**

```bash
git add audio-service/main.py audio-service/services audio-service/tests
git commit -m "feat(audio): islem servisleri URL yerine nesne anahtariyla calissin"
```

---

### Task 8: Sevkiyat — env, imajlar ve CI

**Files:**
- Modify: `docker-compose.yml:31-35`, `:72-76`
- Modify: `docker-stack.yml:19-20`, `:45-49`, `:144-148`
- Modify: `core-service/.env.example:26-27`
- Modify: `.github/workflows/core-service.yml`, `.github/workflows/audio-service.yml`
- Modify: `CLAUDE.md` (test suite notu)
- Modify (ayrı depo): `k3s-gitops/apps/makesinger/makesinger-backend.yaml:37-60,120-128`

**Interfaces:**
- Consumes: Task 1-7'nin tamamı
- Produces: çalışan sevkiyat yapılandırması

- [ ] **Step 1: CI'a test adımı ekle**

`.github/workflows/core-service.yml` içinde `go vet ./...` adımından sonra:

```yaml
      - name: Unit testler
        working-directory: core-service
        run: go test ./internal/...
```

Not: `./...` DEGIL `./internal/...` — `e2e/` paketi canli Redis ve gecerli Groq anahtari istiyor, CI'da kirmizi olur.

`.github/workflows/audio-service.yml` içinde ruff adımlarından sonra:

```yaml
      - name: Testler
        working-directory: audio-service
        run: |
          python -m pip install -r requirements-dev.txt
          python -m pytest tests/ -v
```

- [ ] **Step 2: Compose ve stack env'lerini güncelle**

`docker-compose.yml:31-35` (core) ve `:72-76` (audio) içindeki `R2_BUCKET_NAME` satırını şu ikisiyle değiştir:

```yaml
      - R2_PUBLIC_BUCKET=${R2_PUBLIC_BUCKET:-makeasinger-public}
      - R2_PRIVATE_BUCKET=${R2_PRIVATE_BUCKET:-makeasinger-private}
```

`docker-stack.yml:45-49` ve `:144-148` içinde aynı değişiklik (stack biçimiyle `R2_PUBLIC_BUCKET: ${R2_PUBLIC_BUCKET:-makeasinger-public}`), `:19-20` yorum satırını da güncelle.

`core-service/.env.example:26-27`:

```
R2_PUBLIC_BUCKET=makeasinger-public
R2_PRIVATE_BUCKET=makeasinger-private
R2_PUBLIC_URL=https://makesinger-cdn.celalettindemir.dev
R2_PRESIGN_TTL=1h
```

- [ ] **Step 3: CLAUDE.md'deki yanlış notu düzelt**

`CLAUDE.md` içinde "There is no test suite — no `_test.go` files exist." cümlesini şununla değiştir:

```markdown
Testler: `cd core-service && go test ./internal/...` (birim testler, ag gerekmez).
`./e2e/...` canli Redis ve gecerli Groq/R2 anahtarlari ister; CI onu calistirmaz.
audio-service: `cd audio-service && python3 -m pytest tests/ -v`.
```

- [ ] **Step 4: Testleri ve derlemeyi son kez çalıştır**

Run:
```bash
cd core-service && go build ./... && go vet ./... && go test ./internal/...
cd ../audio-service && python3 -m pytest tests/ -v
```
Expected: hepsi yeşil.

- [ ] **Step 5: Commit ve push**

```bash
git add docker-compose.yml docker-stack.yml core-service/.env.example .github/workflows CLAUDE.md
git commit -m "chore: R2 iki bucket icin env, CI test adimi ve CLAUDE.md duzeltmesi"
git push origin main
```

- [ ] **Step 6: İmajların üretilmesini bekle**

GitHub Actions'ta `Core Service CI` ve `Audio Service CI` işlerinin yeşil bittiğini ve yeni imaj digest'lerini üretmesini bekle.

Run: `gh run list -R celalettindemir/make-singer-backend -L 5`

- [ ] **Step 7: k3s-gitops manifestini güncelle**

`k3s-gitops/apps/makesinger/makesinger-backend.yaml` içinde her iki container için:
- `R2_BUCKET_NAME` satırını SİL
- Şunları ekle:

```yaml
            - name: R2_PUBLIC_BUCKET
              value: "makeasinger-public"
            - name: R2_PRIVATE_BUCKET
              value: "makeasinger-private"
```
- core-service container'ına ayrıca:

```yaml
            - name: R2_PRESIGN_TTL
              value: "1h"
```
- İki `image:` satırındaki digest'i yeni üretilen imajlarla değiştir.

**ÖN KOŞUL:** `makesinger-cdn.celalettindemir.dev` custom domain'i `makeasinger-public` bucket'ına bağlı olmalı; değilse export linkleri 404 döner. Ayrıca R2 token'ı her iki bucket'a yetkili olmalı.

- [ ] **Step 8: Sevkiyatı doğrula**

```bash
kubectl -n makesinger get pods
kubectl -n makesinger logs deploy/makesinger-api --tail=20
kubectl -n makesinger exec deploy/makesinger-api -- wget -qO- -T 5 http://127.0.0.1:8000/health
```
Expected: pod'lar `1/1 Running`, health `"r2":true`, loglarda R2 hatası yok.

- [ ] **Step 9: Commit (k3s-gitops)**

```bash
cd /Users/cos/Documents/k3s-gitops
git add apps/makesinger/makesinger-backend.yaml
git commit -m "deploy(makesinger): R2 public/private bucket ayrimi"
git push origin main
```

---

## Plan Dışı Bırakılanlar

Bu plan sırasında görülen, ayrı ele alınması gereken üç şey:

1. **Groq anahtarının süresi dolmuş.** `e2e/lyrics_real_test.go` 401 `expired_api_key` alıyor. Kümedeki secret aynı `.env`'den geldiyse canlıda şarkı sözü üretimi de çalışmıyordur.
2. **Tipli nil hatası.** `main.go:93` `r2Client` tipli `*client.R2Client` nil olarak arayüze kutulanıyor; servislerdeki `s.r2Client == nil` kontrolü bu durumda FALSE döner ve gerçek bir çağrıda nil-pointer panigi olur. Bugün patlamıyor çünkü `e2e` testleri literal `nil` geçiyor.
3. **`DeleteVocal` bozuk.** `upload_service.go:68` anahtarı `vocals/*/%s.wav` olarak kuruyor; `*` S3'te joker değil, düz karakter. Bu silme hiçbir zaman çalışmamış.
