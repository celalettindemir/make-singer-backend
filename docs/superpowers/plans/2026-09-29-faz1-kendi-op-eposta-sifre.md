# Faz 1: Kendi OpenID Provider'imiz (e-posta + sifre) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** core-service icinde, `zitadel/oidc` kutuphanesiyle calisan, e-posta+sifre ile giris yapilabilen bir OpenID Provider ayaga kaldirmak; ucundan ucuna: mobil tarayici sekmesinde `/authorize`'a gider, giris yapar, `/token`'dan id_token + access_token + refresh_token alir, `/api/*` bu access token'la calisir.

**Architecture:** OP, core-service'in ayni binary'sinde ikinci bir `net/http` dinleyicisinde (8001) kosar; Fiber/fasthttp ile `op` paketi arasinda adaptor kurulmaz. Kalici veri (kullanici, sifre hash'i, refresh token) Postgres'te, kisa omurlu veri (authorization code, access token kaydi) Redis'te durur. Imzalama anahtari ve AES crypto anahtari sops'lu Kubernetes Secret'tan gelir.

**Tech Stack:** Go 1.25, `github.com/zitadel/oidc/v3 v3.51.8`, `github.com/jackc/pgx/v5`, `golang.org/x/crypto/bcrypt`, Fiber v2 (mevcut API), Redis, Postgres (CNPG `pg-kiracilar`), `html/template`.

**Spec:** `docs/superpowers/specs/2026-09-29-kendi-oidc-saglayicimiz-design.md`

## Global Constraints

- Go surumu **1.25** olur: `core-service/go.mod`, `core-service/Dockerfile:2`, `.github/workflows/core-service.yml:33` ve `:53`. Sebep: `zitadel/oidc v3.51.8` kendi `go.mod`'unda `go 1.25.0` istiyor. Bu yukseltme olculdu — `go build ./...`, `go vet ./internal/...` ve `go test ./internal/...` temiz geciyor.
- Kutuphane surumu **kesin pinlenir**: `github.com/zitadel/oidc/v3 v3.51.8`. `@latest` kullanilmaz.
- `op.Storage` uygulamasi **kendi arayuzumuzun arkasina** konur (`internal/kimlik/depo.go`), boylece kutuphane degisirse tek katmanin isi olur.
- Akis: Authorization Code + **PKCE S256 zorunlu**. Implicit yok. Istemci **public client**, secret yok.
- Access token: **JWT**, 15 dakika. id_token: 1 saat. refresh token: opak, 60 gun, **her kullanimda doner**.
- Kullanici hesabi ve refresh token **Postgres'te**; authorization code ve access token kaydi **Redis'te**. Redis'e kalici veri YAZILMAZ — `makesinger-redis.yaml:20-38` volume'suz ve `allkeys-lru`, yani rastgele anahtar siler.
- Sifre hash'i **bcrypt**, cost 12.
- E-posta tekilligi **buyuk/kucuk harf duyarsiz** (`lower(email)` uzerinde unique index).
- MVP'de YOK: e-posta dogrulama, sifre sifirlama, MFA, consent ekrani, Apple/Google (Faz 2).
- Kod kimlikleri Ingilizce, yorumlar ve test verisi Turkce — mevcut kod tabaninin uslubu (bkz. `internal/client/bucket.go`, `internal/client/bucket_test.go`).
- Testler `go test ./internal/...` ile agsiz gecmeli. Postgres gerektiren testler `internal/kimlik` altinda **sahte depo** ile yazilir; gercek Postgres yalnizca `e2e/` icinde kullanilir ve CI onu calistirmaz.
- Hicbir sir kaynak koda yazilmaz. Yeni sirlar `k3s-gitops/apps/makesinger/*.decrypted.yaml` icine duz metin yazilir; pre-commit hook sifreler (bkz. `k3s-gitops/README.md` "Secret duzenleme").

---

## Dosya yapisi

Yeni paket: `core-service/internal/kimlik/` — kimlik ve OP ile ilgili her sey burada.

| Dosya | Sorumluluk |
|---|---|
| `internal/kimlik/kullanici.go` | `User` tipi, bcrypt ile hash/dogrula |
| `internal/kimlik/kullanici_depo.go` | `UserStore` arayuzu + Postgres uygulamasi |
| `internal/kimlik/kullanici_depo_sahte.go` | Testler icin bellek ici `UserStore` |
| `internal/kimlik/veritabani.go` | pgx pool kurulumu + gomulu migration calistirici |
| `internal/kimlik/migrations/001_kullanici.sql` | `users`, `refresh_tokens`, `schema_migrations` |
| `internal/kimlik/anahtar.go` | RSA imzalama anahtari: PEM okuma, `op.SigningKey` + `op.Key` |
| `internal/kimlik/istemci.go` | `op.Client` uygulamasi (mobil public client) |
| `internal/kimlik/istek.go` | `op.AuthRequest` uygulamasi + Redis'te saklama |
| `internal/kimlik/jeton_depo.go` | access token (Redis) ve refresh token (Postgres) kayitlari |
| `internal/kimlik/depo.go` | `op.Storage`'in tamami; yukaridakileri birlestirir |
| `internal/kimlik/sunucu.go` | `op.Provider` kurulumu + ikinci dinleyici (`Start`) |
| `internal/kimlik/sayfa.go` | Giris ve kayit sayfalari (handler + `html/template`) |
| `internal/kimlik/sablonlar/*.html` | `giris.html`, `kayit.html`, `hata.html` |
| `internal/config/config.go` | `AuthConfig` eklenir, `ZitadelConfig` **bu fazda kalir** (Faz 5'te silinir) |
| `internal/middleware/auth.go` | Kendi access token'imizi dogrulayan mod eklenir |
| `cmd/server/main.go` | `kimlik.Start` cagrisi; auth middleware secimi |

`main.go` bugun 347 satir ve zaten yogun. OP kurulumunun tamami `kimlik.Start` icinde durur; `main.go`'ya yalnizca tek cagri girer.

### Referans uygulama

`zitadel/oidc` modulu **tam calisan bir ornek sunucu** tasiyor. Mekanik `op.Storage` metotlari icin birebir referans budur ve okunmasi zorunludur:

```
$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/example/server/storage/storage.go   (938 satir, Storage'in tamami)
$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/example/server/storage/client.go    (235 satir, op.Client)
$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/example/server/storage/oidc.go      (235 satir, claim/scope esleme)
$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/example/server/exampleop/op.go      (166 satir, provider kurulumu)
$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/example/server/exampleop/login.go   (77 satir, giris sayfasi)
```

Ornek bellek ici calisir ve uretime uygun **degildir**: refresh rotasyonu, aile iptali ve kalici depolama bizim isimizdir. Ornegi imzalari ogrenmek icin okuyun, davranisi kopyalamayin.

### Arayuz sozlesmeleri (gorev sinirlarini gecenler)

```go
// internal/kimlik/kullanici.go
type User struct {
    ID            string    // uuid
    Email         string
    EmailVerified bool
    Name          string
    PasswordHash  string    // bos: yalnizca federe giris (Faz 2)
    CreatedAt     time.Time
}

// internal/kimlik/kullanici_depo.go
type UserStore interface {
    Create(ctx context.Context, email, name, password string) (*User, error)
    ByEmail(ctx context.Context, email string) (*User, error)
    ByID(ctx context.Context, id string) (*User, error)
}

var ErrKullaniciYok = errors.New("kullanici bulunamadi")
var ErrEpostaKullanimda = errors.New("eposta kullanimda")

// internal/kimlik/jeton_depo.go
type TokenStore interface {
    AccessKaydet(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error
    AccessOku(ctx context.Context, id string) (*AccessKayit, error)
    RefreshOlustur(ctx context.Context, k *RefreshKayit) (token string, err error)
    RefreshDondur(ctx context.Context, sunulan string, yeni *RefreshKayit) (token string, err error)
    RefreshOku(ctx context.Context, sunulan string) (*RefreshKayit, error)
    AileIptal(ctx context.Context, familyID string) error
    KullaniciIptal(ctx context.Context, userID, clientID string) error
}

var ErrJetonYok = errors.New("jeton bulunamadi")
var ErrJetonTekrar = errors.New("jeton yeniden kullanildi")
```

---

## Task 1: Go 1.25 yukseltmesi ve kutuphane bagimliligi

**Files:**
- Modify: `core-service/go.mod` (satir 3: `go 1.22`)
- Modify: `core-service/Dockerfile:2`
- Modify: `.github/workflows/core-service.yml:33`, `:53`

**Interfaces:**
- Consumes: yok
- Produces: `github.com/zitadel/oidc/v3 v3.51.8` ve `github.com/jackc/pgx/v5` kullanilabilir durumda; Go 1.25 her yerde.

- [ ] **Step 1: Mevcut durumu kaydet (yesil taban)**

```bash
cd core-service && go test ./internal/... 2>&1 | tail -12
```
Beklenen: `internal/client`, `internal/config`, `internal/model`, `internal/service`, `internal/worker` → `ok`. Bu cikti taban; Task 1 sonunda ayni olmali.

- [ ] **Step 2: go.mod'daki Go surumunu yukselt**

`core-service/go.mod` satir 3'u `go 1.22` yerine:

```
go 1.25.0
```

- [ ] **Step 3: Bagimliliklari ekle**

```bash
cd core-service
go get github.com/zitadel/oidc/v3@v3.51.8
go get github.com/jackc/pgx/v5
go mod tidy
```

`go.mod`'da `github.com/zitadel/oidc/v3 v3.51.8` satirinin **tam bu surumle** durdugunu dogrulayin.

- [ ] **Step 4: Derleme ve testlerin hala temiz oldugunu dogrula**

```bash
cd core-service && go build ./... && go vet ./internal/... && go test ./internal/... 2>&1 | tail -12
```
Beklenen: derleme hatasiz, vet ciktisi bos, testler Step 1'deki ile ayni. `go mod tidy` atlanirsa `missing go.sum entry` hatalari gelir; hata buysa tidy'i tekrar calistirin.

- [ ] **Step 5: Dockerfile'i yukselt**

`core-service/Dockerfile` satir 2:

```dockerfile
FROM golang:1.25-alpine AS builder
```

- [ ] **Step 6: CI'da iki yerdeki Go surumunu yukselt**

`.github/workflows/core-service.yml` icindeki **her iki** `go-version: "1.22"` satirini `go-version: "1.25"` yapin (satir 33 lint isi, satir 53 test isi). Tek birini degistirmek CI'yi yaniltici sekilde yesil/kirmizi birakir.

- [ ] **Step 7: Imajin gercekten derlendigini dogrula**

```bash
cd core-service && docker build -t makesinger-core-deneme . 2>&1 | tail -5
```
Beklenen: `naming to docker.io/library/makesinger-core-deneme` ile biten basarili cikti.

- [ ] **Step 8: Commit**

```bash
git add core-service/go.mod core-service/go.sum core-service/Dockerfile .github/workflows/core-service.yml
git commit -m "chore: Go 1.25'e cik ve zitadel/oidc v3.51.8 ekle"
```

---

## Task 2: Postgres baglantisi ve gomulu migration calistirici

**Files:**
- Create: `core-service/internal/kimlik/veritabani.go`
- Create: `core-service/internal/kimlik/migrations/001_kullanici.sql`
- Create: `core-service/internal/kimlik/veritabani_test.go`
- Modify: `core-service/internal/config/config.go`

**Interfaces:**
- Consumes: Task 1'in `pgx/v5` bagimliligi
- Produces: `kimlik.Baglan(ctx, dsn string) (*pgxpool.Pool, error)`; `kimlik.Migrate(ctx, pool *pgxpool.Pool) error`; `config.AuthConfig`

- [ ] **Step 1: Migration SQL'ini yaz**

`core-service/internal/kimlik/migrations/001_kullanici.sql`:

```sql
CREATE TABLE IF NOT EXISTS users (
    id             uuid PRIMARY KEY,
    email          text NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    name           text NOT NULL DEFAULT '',
    password_hash  text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- E-posta tekilligi buyuk/kucuk harf duyarsiz olmali: "Ali@x.com" ile
-- "ali@x.com" ayni hesaptir. Kolona degil, lower(email) ifadesine index.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (lower(email));

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         uuid PRIMARY KEY,
    family_id  uuid NOT NULL,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id  text NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    scopes     text[] NOT NULL DEFAULT '{}',
    audience   text[] NOT NULL DEFAULT '{}',
    amr        text[] NOT NULL DEFAULT '{}',
    auth_time  timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Calinti jeton tespiti: bir aile toptan iptal edilecegi icin aileye gore
-- arama sicak yoldur.
CREATE INDEX IF NOT EXISTS refresh_tokens_family_idx ON refresh_tokens (family_id);
```

Jetonun kendisi degil `sha256` ozeti saklanir; veritabani sizsa bile jetonlar kullanilamaz.

- [ ] **Step 2: Migration calisticiyi yazan testi yaz**

`core-service/internal/kimlik/veritabani_test.go`:

```go
package kimlik

import (
	"strings"
	"testing"
)

// Migration dosyalari gomulu olmali: imaj icinde SQL dosyasi ayrica
// tasinmaz. Bu test dosyanin gercekten binary'ye girdigini dogrular.
func TestMigrationlarGomulu(t *testing.T) {
	girdiler, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("migrations dizini okunamadi: %v", err)
	}
	if len(girdiler) == 0 {
		t.Fatal("migrations dizini bos, go:embed calismamis")
	}
	icerik, err := migrationFS.ReadFile("migrations/001_kullanici.sql")
	if err != nil {
		t.Fatalf("001_kullanici.sql okunamadi: %v", err)
	}
	for _, beklenen := range []string{
		"CREATE TABLE IF NOT EXISTS users",
		"users_email_lower_key",
		"CREATE TABLE IF NOT EXISTS refresh_tokens",
		"refresh_tokens_family_idx",
	} {
		if !strings.Contains(string(icerik), beklenen) {
			t.Errorf("migration %q icermiyor", beklenen)
		}
	}
}

func TestMigrationSirali(t *testing.T) {
	adlar, err := migrationAdlari()
	if err != nil {
		t.Fatalf("migrationAdlari: %v", err)
	}
	for i := 1; i < len(adlar); i++ {
		if adlar[i-1] >= adlar[i] {
			t.Errorf("migrationlar sirali degil: %q, %q", adlar[i-1], adlar[i])
		}
	}
}
```

- [ ] **Step 3: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run TestMigration -v
```
Beklenen: FAIL — `undefined: migrationFS`.

- [ ] **Step 4: veritabani.go'yu yaz**

`core-service/internal/kimlik/veritabani.go`:

```go
package kimlik

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Baglan, Postgres havuzunu kurar ve baglantinin gercekten kuruldugunu
// dogrular. DSN bos ise hata doner: kimlik saglayicisi veritabani olmadan
// calisamaz, sessizce devam etmek kullanici hesaplarini kaybettirir.
func Baglan(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("auth veritabani DSN'i bos")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN cozulemedi: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour

	havuz, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("havuz kurulamadi: %w", err)
	}
	pingCtx, iptal := context.WithTimeout(ctx, 10*time.Second)
	defer iptal()
	if err := havuz.Ping(pingCtx); err != nil {
		havuz.Close()
		return nil, fmt.Errorf("veritabanina ulasilamadi: %w", err)
	}
	return havuz, nil
}

func migrationAdlari() ([]string, error) {
	girdiler, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var adlar []string
	for _, g := range girdiler {
		if !g.IsDir() {
			adlar = append(adlar, g.Name())
		}
	}
	sort.Strings(adlar)
	return adlar, nil
}

// Migrate, uygulanmamis migration'lari sirayla calistirir. Her migration
// kendi islemi icinde kosar ve schema_migrations'a yazilir; yarida kalan
// bir migration kismi sema birakmaz.
func Migrate(ctx context.Context, havuz *pgxpool.Pool) error {
	_, err := havuz.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("schema_migrations olusturulamadi: %w", err)
	}

	adlar, err := migrationAdlari()
	if err != nil {
		return err
	}
	for _, ad := range adlar {
		var varMi bool
		err := havuz.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, ad,
		).Scan(&varMi)
		if err != nil {
			return fmt.Errorf("%s kontrol edilemedi: %w", ad, err)
		}
		if varMi {
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + ad)
		if err != nil {
			return fmt.Errorf("%s okunamadi: %w", ad, err)
		}
		islem, err := havuz.Begin(ctx)
		if err != nil {
			return fmt.Errorf("%s icin islem baslatilamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx, string(sql)); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s uygulanamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, ad); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s kaydedilemedi: %w", ad, err)
		}
		if err := islem.Commit(ctx); err != nil {
			return fmt.Errorf("%s commit edilemedi: %w", ad, err)
		}
	}
	return nil
}
```

- [ ] **Step 5: Testi calistir, gectigini gor**

```bash
cd core-service && go test ./internal/kimlik/ -run TestMigration -v
```
Beklenen: PASS (iki test).

- [ ] **Step 6: Config'e AuthConfig ekle**

`internal/config/config.go`'da `Config` struct'ina (satir 31-42 arasi) `Auth AuthConfig` alani ekleyin ve tip tanimini `GatewayConfig`'ten (satir 116-118) sonra koyun:

```go
// AuthConfig, kendi OpenID Provider'imizin yapilandirmasi.
// Issuer bos ise OP hic baslamaz; API o zaman eski auth yolunda kalir.
type AuthConfig struct {
	Issuer        string        // https://makesinger-auth.celalettindemir.dev
	Port          string        // ikinci dinleyicinin portu
	DBURL         string        // Postgres DSN
	SigningKeyPEM string        // RSA ozel anahtar, PEM
	CryptoKey     string        // op.Config.CryptoKey icin 32 baytlik sir
	ClientID      string        // mobil public client
	RedirectURIs  []string      // izinli redirect adresleri
	AccessTTL     time.Duration // access token omru
	RefreshTTL    time.Duration // refresh token omru
}
```

`Load()` icinde `readSecret` cagrilarina (satir 122-128) ekleyin:

```go
	readSecret("AUTH_DB_URL")
	readSecret("AUTH_SIGNING_KEY")
	readSecret("AUTH_CRYPTO_KEY")
```

`BindEnv` blokuna (satir 139-165) ekleyin:

```go
	_ = viper.BindEnv("auth.issuer", "AUTH_ISSUER")
	_ = viper.BindEnv("auth.port", "AUTH_PORT")
	_ = viper.BindEnv("auth.db_url", "AUTH_DB_URL")
	_ = viper.BindEnv("auth.signing_key_pem", "AUTH_SIGNING_KEY")
	_ = viper.BindEnv("auth.crypto_key", "AUTH_CRYPTO_KEY")
	_ = viper.BindEnv("auth.client_id", "AUTH_CLIENT_ID")
	_ = viper.BindEnv("auth.redirect_uris", "AUTH_REDIRECT_URIS")
	_ = viper.BindEnv("auth.access_ttl", "AUTH_ACCESS_TTL")
	_ = viper.BindEnv("auth.refresh_ttl", "AUTH_REFRESH_TTL")
```

Varsayilanlar blokuna (satir 167-197) ekleyin:

```go
	viper.SetDefault("auth.port", "8001")
	viper.SetDefault("auth.access_ttl", "15m")
	viper.SetDefault("auth.refresh_ttl", "1440h") // 60 gun
```

`cfg := &Config{...}` icine ekleyin:

```go
		Auth: AuthConfig{
			Issuer:        viper.GetString("auth.issuer"),
			Port:          viper.GetString("auth.port"),
			DBURL:         viper.GetString("auth.db_url"),
			SigningKeyPEM: viper.GetString("auth.signing_key_pem"),
			CryptoKey:     viper.GetString("auth.crypto_key"),
			ClientID:      viper.GetString("auth.client_id"),
			RedirectURIs:  viper.GetStringSlice("auth.redirect_uris"),
			AccessTTL:     viper.GetDuration("auth.access_ttl"),
			RefreshTTL:    viper.GetDuration("auth.refresh_ttl"),
		},
```

- [ ] **Step 7: Config varsayilanlarini dogrulayan testi yaz**

`internal/config/config_test.go` sonuna:

```go
func TestAuthVarsayilanlari(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Port != "8001" {
		t.Errorf("Auth.Port = %q, beklenen %q", cfg.Auth.Port, "8001")
	}
	if cfg.Auth.AccessTTL != 15*time.Minute {
		t.Errorf("Auth.AccessTTL = %v, beklenen 15m", cfg.Auth.AccessTTL)
	}
	if cfg.Auth.RefreshTTL != 1440*time.Hour {
		t.Errorf("Auth.RefreshTTL = %v, beklenen 1440h", cfg.Auth.RefreshTTL)
	}
}
```

`time` importunun dosyada oldugundan emin olun.

- [ ] **Step 8: Testleri calistir**

```bash
cd core-service && go test ./internal/config/ ./internal/kimlik/ -v 2>&1 | tail -20
```
Beklenen: hepsi PASS.

- [ ] **Step 9: Commit**

```bash
git add core-service/internal/kimlik core-service/internal/config
git commit -m "feat(kimlik): Postgres baglantisi, gomulu migrationlar ve AuthConfig"
```

---

## Task 3: Kullanici deposu ve bcrypt

**Files:**
- Create: `core-service/internal/kimlik/kullanici.go`
- Create: `core-service/internal/kimlik/kullanici_depo.go`
- Create: `core-service/internal/kimlik/kullanici_depo_sahte.go`
- Create: `core-service/internal/kimlik/kullanici_test.go`

**Interfaces:**
- Consumes: `kimlik.Baglan`, `kimlik.Migrate` (Task 2)
- Produces: `User`, `UserStore`, `NewPostgresUserStore(havuz *pgxpool.Pool) UserStore`, `NewSahteUserStore() *SahteUserStore`, `ErrKullaniciYok`, `ErrEpostaKullanimda`, `(*User).SifreDogru(sifre string) bool`

- [ ] **Step 1: Testleri yaz**

`core-service/internal/kimlik/kullanici_test.go`:

```go
package kimlik

import (
	"context"
	"errors"
	"testing"
)

func TestSifreHashlenirVeDogrulanir(t *testing.T) {
	depo := NewSahteUserStore()
	k, err := depo.Create(context.Background(), "ali@ornek.com", "Ali", "cokGizli123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if k.PasswordHash == "" {
		t.Fatal("PasswordHash bos")
	}
	if k.PasswordHash == "cokGizli123" {
		t.Fatal("sifre duz metin saklanmis")
	}
	if !k.SifreDogru("cokGizli123") {
		t.Error("dogru sifre reddedildi")
	}
	if k.SifreDogru("yanlis") {
		t.Error("yanlis sifre kabul edildi")
	}
}

// Sifresi olmayan kullanici (Faz 2'de yalnizca federe giris) hicbir
// sifreyi kabul etmemeli. Bos hash ile bos sifre eslesirse, federe
// hesaplara sifresiz girilir.
func TestSifresizKullaniciHicbirSifreyiKabulEtmez(t *testing.T) {
	k := &User{ID: "1", Email: "f@ornek.com", PasswordHash: ""}
	if k.SifreDogru("") {
		t.Error("bos sifre kabul edildi")
	}
	if k.SifreDogru("herhangi") {
		t.Error("rastgele sifre kabul edildi")
	}
}

func TestEpostaBuyukKucukDuyarsiz(t *testing.T) {
	depo := NewSahteUserStore()
	ctx := context.Background()
	if _, err := depo.Create(ctx, "Ali@Ornek.com", "Ali", "sifre12345"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := depo.Create(ctx, "ali@ornek.COM", "Ali2", "sifre12345"); !errors.Is(err, ErrEpostaKullanimda) {
		t.Errorf("ikinci kayit hatasi = %v, beklenen ErrEpostaKullanimda", err)
	}
	k, err := depo.ByEmail(ctx, "ALI@ORNEK.COM")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if k.Name != "Ali" {
		t.Errorf("Name = %q, beklenen %q", k.Name, "Ali")
	}
}

func TestOlmayanKullanici(t *testing.T) {
	depo := NewSahteUserStore()
	if _, err := depo.ByEmail(context.Background(), "yok@ornek.com"); !errors.Is(err, ErrKullaniciYok) {
		t.Errorf("hata = %v, beklenen ErrKullaniciYok", err)
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestSifre|TestEposta|TestOlmayan" -v
```
Beklenen: FAIL — `undefined: NewSahteUserStore`.

- [ ] **Step 3: kullanici.go'yu yaz**

```go
package kimlik

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost 12: 2026 icin makul bir denge. Dusurmek sifre kirmayi
// kolaylastirir, yukseltmek giris gecikmesini hissedilir yapar.
const bcryptCost = 12

var (
	ErrKullaniciYok     = errors.New("kullanici bulunamadi")
	ErrEpostaKullanimda = errors.New("eposta kullanimda")
	ErrSifreKisa        = errors.New("sifre en az 10 karakter olmali")
)

type User struct {
	ID            string
	Email         string
	EmailVerified bool
	Name          string
	PasswordHash  string
	CreatedAt     time.Time
}

// SifreDogru, verilen sifrenin hash ile uyustugunu soyler. Hash bos ise
// HER ZAMAN false doner: sifresiz (yalnizca federe) hesaplara sifreyle
// girilemez.
func (u *User) SifreDogru(sifre string) bool {
	if u == nil || u.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(sifre)) == nil
}

func sifreHashle(sifre string) (string, error) {
	if len([]rune(sifre)) < 10 {
		return "", ErrSifreKisa
	}
	b, err := bcrypt.GenerateFromPassword([]byte(sifre), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

- [ ] **Step 4: Sahte depoyu yaz**

`core-service/internal/kimlik/kullanici_depo_sahte.go`:

```go
package kimlik

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SahteUserStore, testler icin bellek ici UserStore. Postgres'in
// lower(email) unique index davranisini taklit eder.
type SahteUserStore struct {
	mu          sync.Mutex
	epostayaGor map[string]*User
	idyeGore    map[string]*User
}

func NewSahteUserStore() *SahteUserStore {
	return &SahteUserStore{
		epostayaGor: map[string]*User{},
		idyeGore:    map[string]*User{},
	}
}

func (s *SahteUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	hash, err := sifreHashle(password)
	if err != nil {
		return nil, err
	}
	anahtar := strings.ToLower(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.epostayaGor[anahtar]; ok {
		return nil, ErrEpostaKullanimda
	}
	k := &User{
		ID:           uuid.NewString(),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	s.epostayaGor[anahtar] = k
	s.idyeGore[k.ID] = k
	return k, nil
}

func (s *SahteUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.epostayaGor[strings.ToLower(email)]; ok {
		return k, nil
	}
	return nil, ErrKullaniciYok
}

func (s *SahteUserStore) ByID(ctx context.Context, id string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.idyeGore[id]; ok {
		return k, nil
	}
	return nil, ErrKullaniciYok
}
```

`ok` adi Go'nun `var` anahtar kelimesiyle cakismamak icindir; `ok` de kullanilabilir — mevcut kod tabaninda `ok` yaygin, onu tercih edin ve bu dosyada da `ok` kullanin.

- [ ] **Step 5: Postgres deposunu yaz**

`core-service/internal/kimlik/kullanici_depo.go`:

```go
package kimlik

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserStore interface {
	Create(ctx context.Context, email, name, password string) (*User, error)
	ByEmail(ctx context.Context, email string) (*User, error)
	ByID(ctx context.Context, id string) (*User, error)
}

type PostgresUserStore struct {
	havuz *pgxpool.Pool
}

func NewPostgresUserStore(havuz *pgxpool.Pool) *PostgresUserStore {
	return &PostgresUserStore{havuz: havuz}
}

func (s *PostgresUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	hash, err := sifreHashle(password)
	if err != nil {
		return nil, err
	}
	k := &User{
		ID:           uuid.NewString(),
		Email:        strings.TrimSpace(email),
		Name:         strings.TrimSpace(name),
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	_, err = s.havuz.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at)
		 VALUES ($1, $2, false, $3, $4, $5)`,
		k.ID, k.Email, k.Name, k.PasswordHash, k.CreatedAt)
	if err != nil {
		// 23505 = unique_violation. Tek unique kisit lower(email)
		// uzerindedir, yani bu hata her zaman "eposta kullanimda" demektir.
		var pgHata *pgconn.PgError
		if errors.As(err, &pgHata) && pgHata.Code == "23505" {
			return nil, ErrEpostaKullanimda
		}
		return nil, fmt.Errorf("kullanici olusturulamadi: %w", err)
	}
	return k, nil
}

func (s *PostgresUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	return s.tekil(ctx,
		`SELECT id, email, email_verified, name, coalesce(password_hash, ''), created_at
		 FROM users WHERE lower(email) = lower($1)`, email)
}

func (s *PostgresUserStore) ByID(ctx context.Context, id string) (*User, error) {
	return s.tekil(ctx,
		`SELECT id, email, email_verified, name, coalesce(password_hash, ''), created_at
		 FROM users WHERE id = $1`, id)
}

func (s *PostgresUserStore) tekil(ctx context.Context, sorgu string, arg any) (*User, error) {
	var k User
	err := s.havuz.QueryRow(ctx, sorgu, arg).Scan(
		&k.ID, &k.Email, &k.EmailVerified, &k.Name, &k.PasswordHash, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKullaniciYok
	}
	if err != nil {
		return nil, fmt.Errorf("kullanici okunamadi: %w", err)
	}
	return &k, nil
}
```

- [ ] **Step 6: Iki uygulamanin ayni arayuzu karsiladigini derleme aninda zorla**

`kullanici_depo.go` sonuna:

```go
// Iki uygulama da arayuzden sapmasin: biri degisirse derleme kirilir.
var (
	_ UserStore = (*PostgresUserStore)(nil)
	_ UserStore = (*SahteUserStore)(nil)
)
```

- [ ] **Step 7: Testleri calistir**

```bash
cd core-service && go test ./internal/kimlik/ -v 2>&1 | tail -20
```
Beklenen: tum testler PASS.

- [ ] **Step 8: Kisa sifrenin reddedildigini dogrulayan testi ekle ve calistir**

`kullanici_test.go` sonuna:

```go
func TestKisaSifreReddedilir(t *testing.T) {
	depo := NewSahteUserStore()
	if _, err := depo.Create(context.Background(), "a@ornek.com", "A", "kisa"); !errors.Is(err, ErrSifreKisa) {
		t.Errorf("hata = %v, beklenen ErrSifreKisa", err)
	}
}
```

```bash
cd core-service && go test ./internal/kimlik/ -run TestKisaSifre -v
```
Beklenen: PASS.

- [ ] **Step 9: Commit**

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): kullanici deposu, bcrypt ve harf duyarsiz eposta tekilligi"
```

---

## Task 4: Imzalama anahtari ve crypto anahtari

**Files:**
- Create: `core-service/internal/kimlik/anahtar.go`
- Create: `core-service/internal/kimlik/anahtar_test.go`

**Interfaces:**
- Consumes: `config.AuthConfig` (Task 2)
- Produces: `AnahtarYukle(pem string) (*Anahtar, error)`; `(*Anahtar)` hem `op.SigningKey` hem `op.Key` karsilar; `CryptoAnahtar(s string) ([32]byte, error)`

`op` paketinin bekledigi arayuzler (modul icinden okundu, `pkg/op/signer.go:11-36`):

```go
type SigningKey interface {
	SignatureAlgorithm() jose.SignatureAlgorithm
	Key() any
	ID() string
}
type Key interface {
	ID() string
	Algorithm() jose.SignatureAlgorithm
	Use() string
	Key() any
}
```

- [ ] **Step 1: Testleri yaz**

`core-service/internal/kimlik/anahtar_test.go`:

```go
package kimlik

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func testPEM(t *testing.T) string {
	t.Helper()
	anahtar, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("anahtar uretilemedi: %v", err)
	}
	blok := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(anahtar)}
	return string(pem.EncodeToMemory(blok))
}

func TestAnahtarYuklenirVeArayuzleriKarsilar(t *testing.T) {
	a, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	var _ op.SigningKey = a
	var _ op.Key = a

	if a.SignatureAlgorithm() != jose.RS256 {
		t.Errorf("algoritma = %v, beklenen RS256", a.SignatureAlgorithm())
	}
	if a.Use() != "sig" {
		t.Errorf("Use() = %q, beklenen %q", a.Use(), "sig")
	}
	if a.ID() == "" {
		t.Error("anahtar ID'si bos")
	}
}

// Anahtar ID'si icerikten turetilmeli: ayni PEM her zaman ayni kid
// vermeli, aksi halde her yeniden baslatmada istemcilerin onbellekledigi
// JWKS gecersizlesir.
func TestAnahtarIDKararli(t *testing.T) {
	p := testPEM(t)
	a1, err := AnahtarYukle(p)
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	a2, err := AnahtarYukle(p)
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	if a1.ID() != a2.ID() {
		t.Errorf("ayni PEM farkli kid verdi: %q, %q", a1.ID(), a2.ID())
	}
}

func TestAnahtarIDFarkliAnahtarlardaFarkli(t *testing.T) {
	a1, _ := AnahtarYukle(testPEM(t))
	a2, _ := AnahtarYukle(testPEM(t))
	if a1.ID() == a2.ID() {
		t.Error("farkli anahtarlar ayni kid verdi")
	}
}

func TestBozukPEMReddedilir(t *testing.T) {
	for ad, girdi := range map[string]string{
		"bos":         "",
		"cop":         "bu bir PEM degil",
		"govde yok":   "-----BEGIN RSA PRIVATE KEY-----\n-----END RSA PRIVATE KEY-----\n",
	} {
		if _, err := AnahtarYukle(girdi); err == nil {
			t.Errorf("%s: hata beklenirken nil dondu", ad)
		}
	}
}

// Sifreleme anahtari tam 32 bayt olmali; kisa bir sir sessizce
// sifirlarla doldurulursa sifreleme zayiflar.
func TestCryptoAnahtarUzunlugu(t *testing.T) {
	if _, err := CryptoAnahtar(strings.Repeat("a", 31)); err == nil {
		t.Error("31 baytlik anahtar kabul edildi")
	}
	if _, err := CryptoAnahtar(strings.Repeat("a", 33)); err == nil {
		t.Error("33 baytlik anahtar kabul edildi")
	}
	b, err := CryptoAnahtar(strings.Repeat("a", 32))
	if err != nil {
		t.Fatalf("32 baytlik anahtar reddedildi: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("uzunluk = %d, beklenen 32", len(b))
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestAnahtar|TestBozukPEM|TestCryptoAnahtar" -v
```
Beklenen: FAIL — `undefined: AnahtarYukle`.

- [ ] **Step 3: anahtar.go'yu yaz**

```go
package kimlik

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"

	jose "github.com/go-jose/go-jose/v4"
)

// Anahtar, RS256 imzalama anahtarini tasir ve op paketinin hem
// SigningKey hem Key arayuzunu karsilar.
type Anahtar struct {
	ozel *rsa.PrivateKey
	kid  string
}

// AnahtarYukle, PEM kodlu RSA ozel anahtarini okur. PKCS#1 ve PKCS#8
// formatlarinin ikisi de kabul edilir; hangi formatta uretildigi
// isletim tarafina gore degisir.
func AnahtarYukle(pemMetni string) (*Anahtar, error) {
	blok, _ := pem.Decode([]byte(pemMetni))
	if blok == nil || len(blok.Bytes) == 0 {
		return nil, fmt.Errorf("gecerli bir PEM blogu bulunamadi")
	}
	var ozel *rsa.PrivateKey
	if a, err := x509.ParsePKCS1PrivateKey(blok.Bytes); err == nil {
		ozel = a
	} else {
		herhangi, err8 := x509.ParsePKCS8PrivateKey(blok.Bytes)
		if err8 != nil {
			return nil, fmt.Errorf("RSA ozel anahtari cozulemedi: %w", err)
		}
		rsaAnahtar, uygun := herhangi.(*rsa.PrivateKey)
		if !uygun {
			return nil, fmt.Errorf("anahtar RSA degil: %T", herhangi)
		}
		ozel = rsaAnahtar
	}
	if ozel.N.BitLen() < 2048 {
		return nil, fmt.Errorf("anahtar %d bit, en az 2048 olmali", ozel.N.BitLen())
	}
	return &Anahtar{ozel: ozel, kid: kidUret(&ozel.PublicKey)}, nil
}

// kidUret, kid'i acik anahtarin icerigine baglar. Rastgele bir kid her
// yeniden baslatmada degisir ve istemcilerin onbellekledigi JWKS'i
// gecersiz kilar.
func kidUret(acik *rsa.PublicKey) string {
	turetilmis, err := x509.MarshalPKIXPublicKey(acik)
	if err != nil {
		// MarshalPKIXPublicKey RSA icin hata vermez; yine de sessiz
		// kalmamak icin modulus'e duseriz.
		turetilmis = acik.N.Bytes()
	}
	ozet := sha256.Sum256(turetilmis)
	return base64.RawURLEncoding.EncodeToString(ozet[:8])
}

func (a *Anahtar) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (a *Anahtar) Algorithm() jose.SignatureAlgorithm          { return jose.RS256 }
func (a *Anahtar) Use() string                                 { return "sig" }
func (a *Anahtar) ID() string                                  { return a.kid }

// Key, op.SigningKey icin ozel anahtari, op.Key icin acik anahtari
// dondurmelidir. op paketi SigningKey'i imzalarken, Key'i JWKS
// yayinlarken kullanir; ikisi ayni metot adini paylasiyor.
func (a *Anahtar) Key() any { return a.ozel }

// AcikAnahtar, JWKS yayini icin op.Key olarak kullanilacak sarmalayiciyi
// dondurur.
func (a *Anahtar) AcikAnahtar() *AcikAnahtarKey { return &AcikAnahtarKey{a: a} }

type AcikAnahtarKey struct{ a *Anahtar }

func (k *AcikAnahtarKey) ID() string                         { return k.a.kid }
func (k *AcikAnahtarKey) Algorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k *AcikAnahtarKey) Use() string                        { return "sig" }
func (k *AcikAnahtarKey) Key() any                           { return &k.a.ozel.PublicKey }

// CryptoAnahtar, op.Config.CryptoKey icin tam 32 bayt dondurur. Kisa bir
// sir sessizce doldurulmaz: sifreleme zayiflar ve bu sessizce olur.
func CryptoAnahtar(s string) ([32]byte, error) {
	var b [32]byte
	if len(s) != 32 {
		return b, fmt.Errorf("crypto anahtari %d bayt, tam 32 olmali", len(s))
	}
	copy(b[:], s)
	return b, nil
}
```

`AcikAnahtarKey`, `KeySet` icin gerekli: `op.Key` olarak **acik** anahtar yayinlanir, `op.SigningKey` olarak **ozel** anahtarla imzalanir. Ikisini ayni tipte birlestirmek JWKS'e ozel anahtar sizdirma riski tasir; bu yuzden ayri tip.

- [ ] **Step 4: Testleri calistir**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestAnahtar|TestBozukPEM|TestCryptoAnahtar" -v
```
Beklenen: hepsi PASS.

- [ ] **Step 5: JWKS'in ozel anahtar sizdirmadigini dogrulayan testi ekle**

`anahtar_test.go` sonuna:

```go
// JWKS'e ozel anahtar asla girmemeli.
func TestAcikAnahtarOzelAnahtarSizdirmaz(t *testing.T) {
	a, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	var _ op.Key = a.AcikAnahtar()
	switch a.AcikAnahtar().Key().(type) {
	case *rsa.PublicKey:
		// dogru
	default:
		t.Fatalf("AcikAnahtar().Key() tipi %T, *rsa.PublicKey olmali", a.AcikAnahtar().Key())
	}
}
```

```bash
cd core-service && go test ./internal/kimlik/ -run TestAcikAnahtar -v
```
Beklenen: PASS.

- [ ] **Step 6: Commit**

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): RS256 imzalama anahtari, kararli kid ve crypto anahtari"
```

---

## Task 5: op.Client uygulamasi (mobil public client)

**Files:**
- Create: `core-service/internal/kimlik/istemci.go`
- Create: `core-service/internal/kimlik/istemci_test.go`

**Interfaces:**
- Consumes: `config.AuthConfig` (Task 2)
- Produces: `NewMobilIstemci(cfg *config.AuthConfig) *MobilIstemci`; `op.Client` karsilar

Referans: `example/server/storage/client.go:1-235`. Bizim istemcimiz **tek** ve sabittir; ornekteki dinamik kayit gerekmez.

- [ ] **Step 1: Testleri yaz**

`core-service/internal/kimlik/istemci_test.go`:

```go
package kimlik

import (
	"testing"

	"github.com/makeasinger/api/internal/config"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func testAuthCfg() *config.AuthConfig {
	return &config.AuthConfig{
		Issuer:   "https://makesinger-auth.ornek.dev",
		ClientID: "makesinger-mobil",
		RedirectURIs: []string{
			"com.makesinger.app:/oauth2redirect",
			"https://makesinger.ornek.dev/oauth2redirect",
		},
	}
}

func TestMobilIstemciArayuzuKarsilar(t *testing.T) {
	var _ op.Client = NewMobilIstemci(testAuthCfg())
}

// Public client: secret tasimaz, bu yuzden kimlik dogrulama yontemi
// "none" olmali. "basic" secilirse kutuphane secret bekler ve token
// istegi reddedilir.
func TestMobilIstemciPublicVeNative(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
	if c.AuthMethod() != oidc.AuthMethodNone {
		t.Errorf("AuthMethod = %v, beklenen none", c.AuthMethod())
	}
	if c.ApplicationType() != op.ApplicationTypeNative {
		t.Errorf("ApplicationType = %v, beklenen native", c.ApplicationType())
	}
	if c.GetID() != "makesinger-mobil" {
		t.Errorf("GetID = %q", c.GetID())
	}
}

// Yalnizca authorization code akisi. Implicit veya token response type
// public client'ta jetonu adres cubuguna dusurur.
func TestMobilIstemciYalnizcaKodAkisi(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
	tipler := c.ResponseTypes()
	if len(tipler) != 1 || tipler[0] != oidc.ResponseTypeCode {
		t.Errorf("ResponseTypes = %v, beklenen [code]", tipler)
	}
	var refreshVar bool
	for _, g := range c.GrantTypes() {
		if g == oidc.GrantTypeImplicit {
			t.Error("implicit grant acik")
		}
		if g == oidc.GrantTypeRefreshToken {
			refreshVar = true
		}
	}
	if !refreshVar {
		t.Error("refresh_token grant kapali; oturum yenilenemez")
	}
}

func TestMobilIstemciRedirectleri(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
	if len(c.RedirectURIs()) != 2 {
		t.Errorf("RedirectURIs = %v", c.RedirectURIs())
	}
}

// DevMode acik kalirsa kutuphane redirect URI dogrulamasini gevsetir;
// uretimde bu, jetonlari saldirganin adresine yollamak demektir.
func TestMobilIstemciDevModeKapali(t *testing.T) {
	if NewMobilIstemci(testAuthCfg()).DevMode() {
		t.Error("DevMode acik")
	}
}

func TestMobilIstemciIzinliScopelar(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
	for _, s := range []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess} {
		if !c.IsScopeAllowed(s) {
			t.Errorf("%q scope'u reddedildi", s)
		}
	}
	if c.IsScopeAllowed("admin") {
		t.Error("tanimsiz scope kabul edildi")
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run TestMobilIstemci -v
```
Beklenen: FAIL — `undefined: NewMobilIstemci`.

- [ ] **Step 3: istemci.go'yu yaz**

```go
package kimlik

import (
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// MobilIstemci, tek birinci-taraf istemcimizi tanimlar: native public
// client, yalnizca authorization code + PKCE.
type MobilIstemci struct {
	cfg *config.AuthConfig
}

func NewMobilIstemci(cfg *config.AuthConfig) *MobilIstemci {
	return &MobilIstemci{cfg: cfg}
}

var _ op.Client = (*MobilIstemci)(nil)

func (c *MobilIstemci) GetID() string          { return c.cfg.ClientID }
func (c *MobilIstemci) RedirectURIs() []string { return c.cfg.RedirectURIs }

// Cikis sonrasi geri donus de ayni adreslere sinirlidir.
func (c *MobilIstemci) PostLogoutRedirectURIs() []string { return c.cfg.RedirectURIs }

func (c *MobilIstemci) ApplicationType() op.ApplicationType { return op.ApplicationTypeNative }
func (c *MobilIstemci) AuthMethod() oidc.AuthMethod         { return oidc.AuthMethodNone }

func (c *MobilIstemci) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}

func (c *MobilIstemci) GrantTypes() []oidc.GrantType {
	return []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken}
}

// LoginURL, kullaniciyi kendi giris sayfamiza yollar. authRequestID'yi
// tasimak zorunludur; sayfa girisi tamamladiktan sonra bu id ile
// akisi surdurur.
func (c *MobilIstemci) LoginURL(authRequestID string) string {
	return yolGiris + "?authRequestID=" + authRequestID
}

// JWT access token: /api/* dogrulamasi veri deposuna gitmez.
func (c *MobilIstemci) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeJWT }

func (c *MobilIstemci) IDTokenLifetime() time.Duration { return time.Hour }
func (c *MobilIstemci) DevMode() bool                  { return false }

func (c *MobilIstemci) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}

func (c *MobilIstemci) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}

// Izin verilen scope'lar acikca listelenir; tanimsiz bir scope sessizce
// kabul edilmez.
func (c *MobilIstemci) IsScopeAllowed(scope string) bool {
	switch scope {
	case oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess:
		return true
	}
	return false
}

// id_token, userinfo claim'lerini de tasir: mobil ayrica /userinfo
// cagirmak zorunda kalmaz.
func (c *MobilIstemci) IDTokenUserinfoClaimsAssertion() bool { return true }

func (c *MobilIstemci) ClockSkew() time.Duration { return 0 }
```

`yolGiris` sabiti Task 10'da `sayfa.go` icinde tanimlanir (`const yolGiris = "/giris"`). Task 5 tek basina derlenmez; Task 10'a kadar bu sabiti gecici olarak `istemci.go` icinde tanimlayip Task 10'da `sayfa.go`'ya tasimayin — bunun yerine **simdi** `sayfa.go` dosyasini yalnizca su iceriklle olusturun:

```go
package kimlik

// Giris ve kayit sayfalarinin yollari. Hem istemcinin LoginURL'i hem
// sayfa handler'lari buradan okur.
const (
	yolGiris = "/giris"
	yolKayit = "/kayit"
)
```

- [ ] **Step 4: Testleri calistir**

```bash
cd core-service && go test ./internal/kimlik/ -run TestMobilIstemci -v
```
Beklenen: hepsi PASS.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): mobil public client tanimi, yalnizca kod akisi"
```

---

## Task 6: Authorization request ve code (Redis)

**Files:**
- Create: `core-service/internal/kimlik/istek.go`
- Create: `core-service/internal/kimlik/istek_test.go`

**Interfaces:**
- Consumes: `redis.Client`
- Produces: `NewIstekDepo(rdb *redis.Client) *IstekDepo`; `AuthIstek` tipi (`op.AuthRequest` karsilar); `IstekDepo` metotlari: `Olustur`, `IDileOku`, `KodlaOku`, `KodKaydet`, `Sil`, `TamamlandiIsaretle`

Referans: `example/server/storage/storage.go` icindeki `AuthRequest` bolumu ve `example/server/storage/oidc.go`.

Anahtar semasi (Redis):

```
kimlik:istek:<id>   -> JSON, TTL 10 dakika  (giris tamamlanana kadar)
kimlik:kod:<kod>    -> istek id'si, TTL 60 saniye
```

Kod TTL'i 60 saniyedir: spec'in karari. Kod tek kullanimliktir — `KodlaOku` kodu okur ve **ayni islemde siler**.

- [ ] **Step 1: Testleri yaz**

`core-service/internal/kimlik/istek_test.go`:

```go
package kimlik

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// testRedis, yerel Redis ister. Yoksa test atlanir: birim testler agsiz
// gecmek zorunda (bkz. Global Constraints).
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

func TestAuthIstekArayuzuKarsilar(t *testing.T) {
	var _ op.AuthRequest = &AuthIstek{}
}

func TestIstekOlusturVeOku(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{
		ClientID:            "makesinger-mobil",
		RedirectURI:         "com.makesinger.app:/oauth2redirect",
		Scopes:              oidc.SpaceDelimitedArray{oidc.ScopeOpenID},
		ResponseType:        oidc.ResponseTypeCode,
		CodeChallenge:       "abc",
		CodeChallengeMethod: oidc.CodeChallengeMethodS256,
		State:               "durum1",
		Nonce:               "n1",
	}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	okunan, err := depo.IDileOku(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if okunan.GetNonce() != "n1" {
		t.Errorf("nonce = %q, beklenen n1", okunan.GetNonce())
	}
	if okunan.GetCodeChallenge() == nil || okunan.GetCodeChallenge().Challenge != "abc" {
		t.Errorf("code challenge kaybolmus: %+v", okunan.GetCodeChallenge())
	}
}

// Kod TEK kullanimlik olmali. Ikinci kez kullanilabilirse, adres
// cubugundan veya loglardan kod kapan biri jeton alir.
func TestKodTekKullanimlik(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{
		ClientID:     "makesinger-mobil",
		ResponseType: oidc.ResponseTypeCode,
	}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.KodKaydet(ctx, istek.GetID(), "kod123"); err != nil {
		t.Fatalf("KodKaydet: %v", err)
	}
	if _, err := depo.KodlaOku(ctx, "kod123"); err != nil {
		t.Fatalf("ilk okuma basarisiz: %v", err)
	}
	if _, err := depo.KodlaOku(ctx, "kod123"); !errors.Is(err, ErrIstekYok) {
		t.Errorf("ikinci okuma hatasi = %v, beklenen ErrIstekYok", err)
	}
}

func TestOlmayanIstek(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	if _, err := depo.IDileOku(context.Background(), "yok"); !errors.Is(err, ErrIstekYok) {
		t.Errorf("hata = %v, beklenen ErrIstekYok", err)
	}
}

// Giris tamamlanmadan istek "done" olmamali: aksi halde kod, kullanici
// hic dogrulanmadan verilir.
func TestIstekBaslangictaTamamlanmamis(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if istek.Done() {
		t.Error("yeni istek tamamlanmis gorunuyor")
	}
	if istek.GetSubject() != "" {
		t.Errorf("subject = %q, bos olmali", istek.GetSubject())
	}
	if err := depo.TamamlandiIsaretle(ctx, istek.GetID(), "kullanici-1"); err != nil {
		t.Fatalf("TamamlandiIsaretle: %v", err)
	}
	okunan, err := depo.IDileOku(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if !okunan.Done() || okunan.GetSubject() != "kullanici-1" {
		t.Errorf("tamamlanmis istek yanlis: done=%v subject=%q", okunan.Done(), okunan.GetSubject())
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestAuthIstek|TestIstek|TestKod|TestOlmayanIstek" -v
```
Beklenen: FAIL — `undefined: NewIstekDepo`.

- [ ] **Step 3: istek.go'yu yaz**

`op.AuthRequest` arayuzunun tam metot listesini modulden okuyun:

```bash
awk '/^type AuthRequest interface/,/^}/' "$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/pkg/op/auth_request.go"
```

`AuthIstek` bu arayuzun tamamini karsilayan, JSON'a serilesebilen bir struct olur. Alanlar disa acik (buyuk harfli) olmali, aksi halde Redis'e yazilirken kaybolur. Zorunlu davranislar:

- `Done()` yalnizca `Subject` dolu **ve** `AuthTime` sifir degilse true doner.
- `GetCodeChallenge()` `*oidc.CodeChallenge` doner; `Challenge` bos ise nil doner.
- `GetAudience()` en az client ID'yi icerir.
- `GetAMR()` sifreyle giriste `[]string{"pwd"}` doner.

`IstekDepo`:

```go
const (
	istekTTL = 10 * time.Minute
	kodTTL   = 60 * time.Second
)

var ErrIstekYok = errors.New("auth istegi bulunamadi")

type IstekDepo struct{ rdb *redis.Client }

func NewIstekDepo(rdb *redis.Client) *IstekDepo { return &IstekDepo{rdb: rdb} }

func istekAnahtar(id string) string { return "kimlik:istek:" + id }
func kodAnahtar(kod string) string  { return "kimlik:kod:" + kod }
```

`KodlaOku` kodu **tek kullanimlik** yapar: `GETDEL` kullanin (Redis 6.2+; kumede redis:7-alpine var).

```go
func (d *IstekDepo) KodlaOku(ctx context.Context, kod string) (*AuthIstek, error) {
	id, err := d.rdb.GetDel(ctx, kodAnahtar(kod)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrIstekYok
	}
	if err != nil {
		return nil, fmt.Errorf("kod okunamadi: %w", err)
	}
	return d.IDileOku(ctx, id)
}
```

`TamamlandiIsaretle`, istegi okur, `Subject` ve `AuthTime`'i doldurur, geri yazar ve TTL'i korur.

- [ ] **Step 4: Testleri calistir**

```bash
cd core-service && docker run -d --rm -p 6379:6379 --name kimlik-redis redis:7-alpine >/dev/null
cd core-service && go test ./internal/kimlik/ -run "TestAuthIstek|TestIstek|TestKod|TestOlmayanIstek" -v
docker stop kimlik-redis >/dev/null
```
Beklenen: hepsi PASS. Redis calistirilmazsa testler `skip` olur — `skip` gordugunuzde Redis'i baslatip tekrar kosun, atlanan test dogrulama saymaz.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): auth istegi ve tek kullanimlik kod deposu"
```

---

## Task 7: Jeton deposu, refresh rotasyonu ve yeniden kullanim tespiti

**Files:**
- Create: `core-service/internal/kimlik/jeton_depo.go`
- Create: `core-service/internal/kimlik/jeton_depo_sahte.go`
- Create: `core-service/internal/kimlik/jeton_depo_test.go`

**Interfaces:**
- Consumes: `pgxpool.Pool` (Task 2), `redis.Client`
- Produces: `TokenStore` arayuzu, `AccessKayit`, `RefreshKayit`, `NewPostgresTokenStore(havuz, rdb)`, `NewSahteTokenStore()`, `ErrJetonYok`, `ErrJetonTekrar`

Bu gorevin kalbi **rotasyon ve aile iptali**. Davranis:

1. `RefreshOlustur` yeni bir aile (`family_id`) baslatir, rastgele 32 bayt jeton uretir, `sha256` ozetini saklar, jetonun kendisini dondurur.
2. `RefreshDondur(sunulan, yeni)`: sunulan jetonun ozetini bulur.
   - Kayit yoksa → `ErrJetonYok`.
   - `revoked_at` dolu → `ErrJetonYok`.
   - `used_at` **dolu** → jeton yeniden kullanilmis: **ailenin tamamini iptal et** ve `ErrJetonTekrar` dondur.
   - Suresi gecmis → `ErrJetonYok`.
   - Aksi halde: eskiyi `used_at = now()` isaretle, yeni kaydi **ayni aileye** ekle, yeni jetonu dondur. Ikisi tek islemde olur.

- [ ] **Step 1: Testleri yaz**

`core-service/internal/kimlik/jeton_depo_test.go`:

```go
package kimlik

import (
	"context"
	"errors"
	"testing"
	"time"
)

func yeniRefresh(userID string) *RefreshKayit {
	return &RefreshKayit{
		UserID:    userID,
		ClientID:  "makesinger-mobil",
		Scopes:    []string{"openid", "offline_access"},
		Audience:  []string{"makesinger-mobil"},
		AMR:       []string{"pwd"},
		AuthTime:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
}

func TestRefreshOlusturVeOku(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	jeton, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if jeton == "" {
		t.Fatal("bos jeton dondu")
	}
	kayit, err := depo.RefreshOku(ctx, jeton)
	if err != nil {
		t.Fatalf("RefreshOku: %v", err)
	}
	if kayit.UserID != "k1" {
		t.Errorf("UserID = %q", kayit.UserID)
	}
	if kayit.FamilyID == "" {
		t.Error("FamilyID bos")
	}
}

// Rotasyon: her kullanim yeni jeton uretir ve eski jeton olur.
func TestRefreshDonerVeEskisiOlur(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	eski, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	yeni, err := depo.RefreshDondur(ctx, eski, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	if yeni == eski {
		t.Fatal("jeton donmemis, ayni deger geldi")
	}
	if _, err := depo.RefreshOku(ctx, yeni); err != nil {
		t.Errorf("yeni jeton okunamadi: %v", err)
	}
}

// Calinti jeton tespiti: kullanilmis bir jeton tekrar sunulursa ailenin
// TAMAMI iptal olur. Yalnizca sunulan jetonu reddetmek yetmez; saldirgan
// zaten yeni jetonu almis olabilir.
func TestKullanilmisJetonAileyiIptalEder(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	birinci, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	ikinci, err := depo.RefreshDondur(ctx, birinci, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	// Saldirgan eski jetonu tekrar sunuyor.
	if _, err := depo.RefreshDondur(ctx, birinci, yeniRefresh("k1")); !errors.Is(err, ErrJetonTekrar) {
		t.Fatalf("hata = %v, beklenen ErrJetonTekrar", err)
	}
	// Gercek kullanicinin elindeki gecerli jeton da artik olu olmali.
	if _, err := depo.RefreshOku(ctx, ikinci); !errors.Is(err, ErrJetonYok) {
		t.Errorf("aile iptal edilmemis, ikinci jeton hala gecerli (err=%v)", err)
	}
}

func TestSuresiGecmisRefreshReddedilir(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	k := yeniRefresh("k1")
	k.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	jeton, err := depo.RefreshOlustur(ctx, k)
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := depo.RefreshOku(ctx, jeton); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}

func TestOlmayanRefresh(t *testing.T) {
	depo := NewSahteTokenStore()
	if _, err := depo.RefreshOku(context.Background(), "uydurma"); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}

// Cikis: kullanicinin butun refresh jetonlari olur.
func TestKullaniciIptalHepsiniOldurur(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	a, _ := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	b, _ := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	c, _ := depo.RefreshOlustur(ctx, yeniRefresh("k2"))
	if err := depo.KullaniciIptal(ctx, "k1", "makesinger-mobil"); err != nil {
		t.Fatalf("KullaniciIptal: %v", err)
	}
	for ad, jeton := range map[string]string{"a": a, "b": b} {
		if _, err := depo.RefreshOku(ctx, jeton); !errors.Is(err, ErrJetonYok) {
			t.Errorf("%s hala gecerli", ad)
		}
	}
	if _, err := depo.RefreshOku(ctx, c); err != nil {
		t.Errorf("baska kullanicinin jetonu iptal edildi: %v", err)
	}
}

// Jetonun kendisi degil ozeti saklanmali.
func TestJetonDuzMetinSaklanmaz(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	jeton, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	for _, kayit := range depo.TumKayitlar() {
		if string(kayit.TokenHash) == jeton {
			t.Fatal("jeton duz metin saklanmis")
		}
	}
}

func TestAccessKaydetVeOku(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	son := time.Now().UTC().Add(15 * time.Minute)
	if err := depo.AccessKaydet(ctx, "at1", "k1", "makesinger-mobil", []string{"openid"}, son); err != nil {
		t.Fatalf("AccessKaydet: %v", err)
	}
	kayit, err := depo.AccessOku(ctx, "at1")
	if err != nil {
		t.Fatalf("AccessOku: %v", err)
	}
	if kayit.UserID != "k1" {
		t.Errorf("UserID = %q", kayit.UserID)
	}
	if _, err := depo.AccessOku(ctx, "yok"); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestRefresh|TestKullanilmis|TestSuresi|TestOlmayanRefresh|TestKullaniciIptal|TestJetonDuz|TestAccess" -v
```
Beklenen: FAIL — `undefined: NewSahteTokenStore`.

- [ ] **Step 3: Tipleri ve arayuzu yaz**

`jeton_depo.go` basi:

```go
package kimlik

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

var (
	ErrJetonYok   = errors.New("jeton bulunamadi")
	ErrJetonTekrar = errors.New("jeton yeniden kullanildi")
)

type AccessKayit struct {
	ID        string
	UserID    string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
}

type RefreshKayit struct {
	ID        string
	FamilyID  string
	UserID    string
	ClientID  string
	TokenHash []byte
	Scopes    []string
	Audience  []string
	AMR       []string
	AuthTime  time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

type TokenStore interface {
	AccessKaydet(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error
	AccessOku(ctx context.Context, id string) (*AccessKayit, error)
	RefreshOlustur(ctx context.Context, k *RefreshKayit) (string, error)
	RefreshDondur(ctx context.Context, sunulan string, yeni *RefreshKayit) (string, error)
	RefreshOku(ctx context.Context, sunulan string) (*RefreshKayit, error)
	AileIptal(ctx context.Context, familyID string) error
	KullaniciIptal(ctx context.Context, userID, clientID string) error
}

// jetonUret, 32 baytlik kriptografik rastgele jeton uretir ve URL'de
// tasinabilir bicimde dondurur.
func jetonUret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("jeton uretilemedi: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// jetonOzet, saklanacak degeri uretir. Jetonun kendisi HICBIR ZAMAN
// saklanmaz: veritabani sizarsa jetonlar kullanilamaz olmali.
func jetonOzet(jeton string) []byte {
	o := sha256.Sum256([]byte(jeton))
	return o[:]
}
```

- [ ] **Step 4: Sahte jeton deposunu yaz**

`jeton_depo_sahte.go` — bellek ici, `sync.Mutex` korumali. Rotasyon ve aile iptali mantigi Postgres uygulamasiyla **birebir ayni** olmali; testler ikisini de ayni sekilde dogrular. Ek olarak test icin:

```go
// TumKayitlar, testlerin depolanan bicimi denetlemesi icindir.
func (s *SahteTokenStore) TumKayitlar() []*RefreshKayit
```

- [ ] **Step 5: Testleri calistir, sahte depoda gectigini gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestRefresh|TestKullanilmis|TestSuresi|TestOlmayanRefresh|TestKullaniciIptal|TestJetonDuz|TestAccess" -v
```
Beklenen: hepsi PASS.

- [ ] **Step 6: Postgres uygulamasini yaz**

`PostgresTokenStore`: refresh kayitlari Postgres'te, access kayitlari Redis'te (`kimlik:at:<id>`, TTL = access omru).

`RefreshDondur` **tek islemde** kosar ve satiri kilitler:

```go
func (s *PostgresTokenStore) RefreshDondur(ctx context.Context, sunulan string, yeni *RefreshKayit) (string, error) {
	islem, err := s.havuz.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("islem baslatilamadi: %w", err)
	}
	defer func() { _ = islem.Rollback(ctx) }()

	var (
		id, familyID string
		expiresAt    time.Time
		usedAt       *time.Time
		revokedAt    *time.Time
	)
	// FOR UPDATE: ayni jetonla gelen iki istek yarisirsa ikincisi
	// birincinin used_at yazmasini gormeli, yoksa yeniden kullanim
	// tespiti sessizce kacar.
	err = islem.QueryRow(ctx,
		`SELECT id, family_id, expires_at, used_at, revoked_at
		   FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
		jetonOzet(sunulan)).Scan(&id, &familyID, &expiresAt, &usedAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrJetonYok
	}
	if err != nil {
		return "", fmt.Errorf("jeton okunamadi: %w", err)
	}
	if usedAt != nil {
		// Yeniden kullanim: ailenin tamamini oldur ve ayri bir hata don.
		if _, err := islem.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = now()
			  WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
			return "", fmt.Errorf("aile iptal edilemedi: %w", err)
		}
		if err := islem.Commit(ctx); err != nil {
			return "", fmt.Errorf("iptal commit edilemedi: %w", err)
		}
		return "", ErrJetonTekrar
	}
	if revokedAt != nil || time.Now().UTC().After(expiresAt) {
		return "", ErrJetonYok
	}
	// ... eskiyi used_at ile isaretle, yeni kaydi ayni family_id ile ekle,
	//     commit et, yeni jetonu don.
}
```

`FOR UPDATE` satiri yorumdaki gerekce yuzunden zorunludur; atlanmasi yarista tespiti kaciran bir hata uretir.

- [ ] **Step 7: Iki uygulamanin arayuzden sapmadigini zorla**

`jeton_depo.go` sonuna:

```go
var (
	_ TokenStore = (*PostgresTokenStore)(nil)
	_ TokenStore = (*SahteTokenStore)(nil)
)
```

- [ ] **Step 8: Tum kimlik testlerini calistir**

```bash
cd core-service && go build ./... && go test ./internal/kimlik/ -v 2>&1 | tail -25
```
Beklenen: derleme temiz, tum testler PASS.

- [ ] **Step 9: Commit**

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): refresh rotasyonu, aile iptali ve yeniden kullanim tespiti"
```

---

## Task 8: op.Storage'in tamami

**Files:**
- Create: `core-service/internal/kimlik/depo.go`
- Create: `core-service/internal/kimlik/depo_test.go`

**Interfaces:**
- Consumes: `UserStore` (Task 3), `Anahtar` (Task 4), `MobilIstemci` (Task 5), `IstekDepo` (Task 6), `TokenStore` (Task 7)
- Produces: `NewDepo(...) *Depo`; `Depo` `op.Storage` arayuzunun tamamini karsilar

Bu gorev mekanik ve hacimlidir: `op.Storage` = `AuthStorage` (13 metot) + `OPStorage` (8 metot) + `Health`. Referans uygulamayi acin ve imzalari oradan alin:

```bash
$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/example/server/storage/storage.go
$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/example/server/storage/oidc.go
```

Arayuzun kesin listesini dogrulayin:

```bash
awk '/^type AuthStorage interface/,/^}/;/^type OPStorage interface/,/^}/' \
  "$(go env GOMODCACHE)/github.com/zitadel/oidc/v3@v3.51.8/pkg/op/storage.go"
```

Ornekten **sapmasi gereken** noktalar (bunlar bizim kararlarimiz):

| Metot | Ornek ne yapiyor | Biz ne yapiyoruz |
|---|---|---|
| `CreateAccessAndRefreshTokens` | Bellekte tutuyor, rotasyon yok | `TokenStore.RefreshDondur` ile rotasyon; `currentRefreshToken` bos ise `RefreshOlustur` |
| `TokenRequestByRefreshToken` | Bellekten okuyor | `RefreshOku`; `ErrJetonTekrar` gelirse `op.ErrInvalidRefreshToken` doner |
| `GetRefreshTokenInfo` | Jetonu ayristiriyor | `RefreshOku` ile userID ve tokenID doner |
| `TerminateSession` | Bellek temizliyor | `TokenStore.KullaniciIptal` |
| `RevokeToken` | Bellek temizliyor | Refresh ise aileyi, access ise Redis kaydini siler |
| `GetClientByClientID` | Coklu istemci | Yalnizca yapilandirilan ID; baskasi icin hata |
| `SigningKey` / `KeySet` | Dosyadan | `Anahtar` ve `Anahtar.AcikAnahtar()` |
| `SetUserinfoFromScopes` | Dolduruyor | **Bos implementasyon** — kutuphane deprecated diyor |
| `AuthorizeClientIDSecret` | Secret dogruluyor | Public client: her zaman hata (secret yok) |
| `ValidateJWTProfileScopes` | Destekliyor | Desteklenmiyor: hata doner |
| `Health` | nil | Postgres `Ping` + Redis `Ping` |

- [ ] **Step 1: Arayuz uyumunu ve kritik davranislari test et**

`core-service/internal/kimlik/depo_test.go`:

```go
package kimlik

import (
	"context"
	"errors"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/op"
)

func testDepo(t *testing.T) *Depo {
	t.Helper()
	a, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	return NewDepo(testAuthCfg(), NewSahteUserStore(), NewSahteTokenStore(), nil, a)
}

func TestDepoArayuzuKarsilar(t *testing.T) {
	var _ op.Storage = testDepo(t)
}

// Yapilandirilmamis bir client ID ile jeton alinamamali.
func TestTanimsizIstemciReddedilir(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	if _, err := d.GetClientByClientID(ctx, "baska-uygulama"); err == nil {
		t.Error("tanimsiz istemci kabul edildi")
	}
	if _, err := d.GetClientByClientID(ctx, "makesinger-mobil"); err != nil {
		t.Errorf("tanimli istemci reddedildi: %v", err)
	}
}

// Public client secret tasimaz; secret ile dogrulama HER ZAMAN
// basarisiz olmali. Basarili donerse bos secret'la istemci taklit edilir.
func TestSecretIleDogrulamaHepReddedilir(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	for _, secret := range []string{"", "herhangi"} {
		if err := d.AuthorizeClientIDSecret(ctx, "makesinger-mobil", secret); err == nil {
			t.Errorf("secret %q kabul edildi", secret)
		}
	}
}

// Yeniden kullanilmis refresh jetonu kutuphanenin bekledigi hataya
// cevrilmeli; aksi halde istemciye 500 doner ve neden anlasilmaz.
func TestTekrarKullanimInvalidRefreshOlur(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	birinci, err := d.jetonlar.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := d.jetonlar.RefreshDondur(ctx, birinci, yeniRefresh("k1")); err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	if _, err := d.TokenRequestByRefreshToken(ctx, birinci); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
}

func TestJWTProfileDesteklenmez(t *testing.T) {
	d := testDepo(t)
	if _, err := d.ValidateJWTProfileScopes(context.Background(), "k1", []string{"openid"}); err == nil {
		t.Error("JWT profile kabul edildi")
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestDepo|TestTanimsiz|TestSecret|TestTekrarKullanim|TestJWTProfile" -v
```
Beklenen: FAIL — `undefined: NewDepo`.

- [ ] **Step 3: depo.go'yu yaz**

`Depo` struct'i:

```go
type Depo struct {
	cfg      *config.AuthConfig
	kullanici UserStore
	jetonlar  TokenStore
	istekler  *IstekDepo
	anahtar   *Anahtar
	istemci   *MobilIstemci
	havuz     *pgxpool.Pool // Health icin; nil olabilir
	rdb       *redis.Client // Health icin; nil olabilir
}
```

`NewDepo(cfg, kullanici, jetonlar, istekler, anahtar)` imzasini test kullaniyor; `havuz` ve `rdb` icin ayri bir `SaglikBagla(havuz, rdb)` metodu ekleyin, boylece testler onlar olmadan kurulabilir.

`op.Storage`'in tamamini uygulayin. Tablodaki sapmalara uyun.

- [ ] **Step 4: Testleri calistir**

```bash
cd core-service && go build ./... && go test ./internal/kimlik/ -v 2>&1 | tail -25
```
Beklenen: derleme temiz, hepsi PASS.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): op.Storage uygulamasi"
```

---

## Task 9: Provider kurulumu ve ikinci dinleyici

**Files:**
- Create: `core-service/internal/kimlik/sunucu.go`
- Create: `core-service/internal/kimlik/sunucu_test.go`
- Modify: `core-service/cmd/server/main.go`

**Interfaces:**
- Consumes: `Depo` (Task 8), `config.AuthConfig`
- Produces: `kimlik.Start(ctx context.Context, cfg *config.AuthConfig, rdb *redis.Client) (*Sunucu, error)`; `(*Sunucu).Kapat(ctx) error`

Constructor (modulden okundu, `pkg/op/op.go:215`):

```go
func NewOpenIDProvider(issuer string, config *Config, storage Storage, opOpts ...Option) (*Provider, error)
```

`op.Config` alanlari (`pkg/op/op.go:162`): `CryptoKey [32]byte`, `CodeMethodS256 bool`, `AuthMethodPost bool`, `GrantTypeRefreshToken bool`, `SupportedUILocales []language.Tag`, `SupportedScopes []string`, ... Bizim degerlerimiz:

```go
opCfg := &op.Config{
	CryptoKey:             cryptoAnahtar,
	CodeMethodS256:        true,  // PKCE S256 zorunlu
	AuthMethodPost:        false, // public client, secret yok
	GrantTypeRefreshToken: true,
	SupportedUILocales:    []language.Tag{language.Turkish, language.English},
	SupportedScopes: []string{
		oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess,
	},
}
```

- [ ] **Step 1: Testleri yaz**

`core-service/internal/kimlik/sunucu_test.go`:

```go
package kimlik

import (
	"context"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

// Issuer bossa OP hic baslamamali: bos issuer ile uretilen discovery
// belgesi kullanilamaz ve hata ancak ilk giris denemesinde gorunur.
func TestIssuerBossaBaslamaz(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = ""
	if _, err := Start(context.Background(), cfg, nil); err == nil {
		t.Error("bos issuer ile baslatildi")
	}
}

// Sirlar eksikse baslamamali; sessizce zayif varsayilana dusmek
// uretimde fark edilmez.
func TestEksikSirlarlaBaslamaz(t *testing.T) {
	temel := func() *config.AuthConfig {
		c := testAuthCfg()
		c.DBURL = "postgres://yok/yok"
		c.SigningKeyPEM = "gecersiz"
		c.CryptoKey = "kisa"
		return c
	}
	if _, err := Start(context.Background(), temel(), nil); err == nil {
		t.Error("gecersiz sirlarla baslatildi")
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestIssuerBossa|TestEksikSirlar" -v
```
Beklenen: FAIL — `undefined: Start`.

- [ ] **Step 3: sunucu.go'yu yaz**

`Start` sirasi:

1. `cfg.Issuer` bos → hata.
2. `AnahtarYukle(cfg.SigningKeyPEM)` → hata ise don.
3. `CryptoAnahtar(cfg.CryptoKey)` → hata ise don.
4. `Baglan(ctx, cfg.DBURL)` → hata ise don. Ardindan `Migrate`.
5. `NewPostgresUserStore`, `NewPostgresTokenStore`, `NewIstekDepo` kur.
6. `NewDepo(...)` + `SaglikBagla(havuz, rdb)`.
7. `op.NewOpenIDProvider(cfg.Issuer, opCfg, depo)`.
8. `http.ServeMux`: provider'i kok yola, `yolGiris` ve `yolKayit`'i sayfa handler'larina bagla (Task 10).
9. `&http.Server{Addr: ":" + cfg.Port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}` ve `go srv.ListenAndServe()`.

`ReadHeaderTimeout` atlanmasin: `gosec`/linter bunu isaretler ve yavas istemci saldirisina kapi acar.

`Kapat(ctx)` `srv.Shutdown(ctx)` cagirir ve havuzu kapatir.

- [ ] **Step 4: main.go'ya tek cagri ekle**

`cmd/server/main.go` icinde, Redis istemcisi kurulduktan (satir 60-70) sonra:

```go
	// Kendi OpenID Provider'imiz. Issuer bos ise hic baslamaz; API o
	// zaman eski auth yolunda kalir. Hata olursa fatal degil: OP
	// olmadan da /health ve genel akis ayakta kalmali ki sorun
	// tanilanabilsin.
	var kimlikSunucu *kimlik.Sunucu
	if cfg.Auth.Issuer != "" {
		var err error
		kimlikSunucu, err = kimlik.Start(ctx, &cfg.Auth, redisClient)
		if err != nil {
			log.Printf("Uyari: kimlik saglayicisi baslatilamadi: %v", err)
		} else {
			log.Printf("Kimlik saglayicisi %s uzerinde, issuer %s", cfg.Auth.Port, cfg.Auth.Issuer)
			defer func() {
				kapatCtx, iptal := context.WithTimeout(context.Background(), 10*time.Second)
				defer iptal()
				_ = kimlikSunucu.Kapat(kapatCtx)
			}()
		}
	}
```

`/health` ciktisina (satir 195-206) ekleyin:

```go
				"kimlik": kimlikSunucu != nil,
```

- [ ] **Step 5: Derleme ve testler**

```bash
cd core-service && go build ./... && go vet ./internal/... ./cmd/... && go test ./internal/... 2>&1 | tail -15
```
Beklenen: derleme temiz, vet bos, testler PASS.

- [ ] **Step 6: Discovery belgesinin gercekten yayinlandigini elle dogrula**

```bash
cd core-service
openssl genrsa 2048 > /tmp/kimlik-deneme.pem
docker run -d --rm -p 6379:6379 --name kimlik-redis redis:7-alpine >/dev/null
docker run -d --rm -p 5432:5432 -e POSTGRES_PASSWORD=deneme -e POSTGRES_DB=kimlik --name kimlik-pg postgres:17-alpine >/dev/null
sleep 5
AUTH_ISSUER=http://localhost:8001 \
AUTH_PORT=8001 \
AUTH_DB_URL="postgres://postgres:deneme@localhost:5432/kimlik" \
AUTH_SIGNING_KEY="$(cat /tmp/kimlik-deneme.pem)" \
AUTH_CRYPTO_KEY="01234567890123456789012345678901" \
AUTH_CLIENT_ID=makesinger-mobil \
AUTH_REDIRECT_URIS="com.makesinger.app:/oauth2redirect" \
go run ./cmd/server &
sleep 8
curl -s http://localhost:8001/.well-known/openid-configuration | head -c 600
echo
curl -s http://localhost:8001/keys | head -c 300
```
Beklenen: discovery JSON'i `"issuer":"http://localhost:8001"`, `"code_challenge_methods_supported":["S256"]` icermeli; JWKS ciktisi `"kty":"RSA"` icermeli ve **`"d"` alani icermemeli** (ozel anahtar sizmasi). Bittiginde:

```bash
kill %1; docker stop kimlik-redis kimlik-pg >/dev/null; rm -f /tmp/kimlik-deneme.pem
```

- [ ] **Step 7: Commit**

```bash
git add core-service/internal/kimlik core-service/cmd/server/main.go
git commit -m "feat(kimlik): OP kurulumu ve ikinci dinleyici"
```

---

## Task 10: Giris ve kayit sayfalari

**Files:**
- Modify: `core-service/internal/kimlik/sayfa.go` (Task 5'te yol sabitleriyle olusturuldu)
- Create: `core-service/internal/kimlik/sablonlar/giris.html`
- Create: `core-service/internal/kimlik/sablonlar/kayit.html`
- Create: `core-service/internal/kimlik/sayfa_test.go`

**Interfaces:**
- Consumes: `UserStore` (Task 3), `IstekDepo` (Task 6), `op.Provider`
- Produces: `NewSayfalar(kullanici UserStore, istekler *IstekDepo, geriCagirma func(context.Context, string) string) *Sayfalar`; `(*Sayfalar).Bagla(mux *http.ServeMux, araci *op.IssuerInterceptor)`

Referans: `example/server/exampleop/login.go:1-77` ve `example/server/exampleop/op.go:68`.

Akis: `/authorize` → kutuphane `Client.LoginURL(authRequestID)` ile `/giris?authRequestID=...`'a yollar → form POST → sifre dogru ise `istekler.TamamlandiIsaretle(id, userID)` → **`http.Redirect(w, r, geriCagirma(r.Context(), id), http.StatusFound)`**.

Iki nokta modulden dogrulandi ve atlanmasi akisi kirar:

1. `geriCagirma`, `op.AuthCallbackURL(saglayici)` ile uretilir (`pkg/op/op.go:152`); imzasi `func(context.Context, string) string`. **`saglayici.AuthorizeCallback(...)` diye bir metot YOKTUR**; paketteki ad `op.AuthorizeCallbackHandler` ve tamamen baska bir amaca hizmet eder.
2. Formu isleyen **POST** handler'lari `op.NewIssuerInterceptor(saglayici.IssuerFromRequest)` ile sarilmalidir (`pkg/op/context.go:20,32`; ornekte `exampleop/op.go:68`). Araci, issuer'i istek baglamina koyar; sarilmazsa `geriCagirma` issuer'i bulamaz ve akis yonlendirme asamasinda coker.

- [ ] **Step 1: Testleri yaz**

`core-service/internal/kimlik/sayfa_test.go`:

```go
package kimlik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testSayfalar(t *testing.T) (*Sayfalar, UserStore) {
	t.Helper()
	kullanici := NewSahteUserStore()
	// Gercek geri cagirma op.AuthCallbackURL'den gelir; testte sabit bir
	// adres yeterli, cunku olculen sey giris mantigi.
	geri := func(_ context.Context, id string) string { return "/bitti?id=" + id }
	return NewSayfalar(kullanici, nil, geri), kullanici
}

func TestGirisSayfasiFormGosterir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, yolGiris+"?authRequestID=abc", nil)
	s.Giris(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	govde := w.Body.String()
	for _, beklenen := range []string{`name="eposta"`, `name="sifre"`, `name="authRequestID"`, "abc"} {
		if !strings.Contains(govde, beklenen) {
			t.Errorf("sayfa %q icermiyor", beklenen)
		}
	}
}

// authRequestID olmadan giris sayfasi anlamsizdir; form gosterilmemeli.
func TestGirisAuthRequestIDsizReddedilir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	s.Giris(w, httptest.NewRequest(http.MethodGet, yolGiris, nil))
	if w.Code == http.StatusOK {
		t.Error("authRequestID olmadan 200 dondu")
	}
}

// Yanlis sifre ile giriste hata mesaji, hesabin var olup olmadigini
// SOYLEMEMELI: aksi halde form bir e-posta sayim araci olur.
func TestYanlisGirisKullaniciVarligiSizdirmaz(t *testing.T) {
	s, kullanici := testSayfalar(t)
	if _, err := kullanici.Create(t.Context(), "var@ornek.com", "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	govdeler := map[string]string{}
	for ad, form := range map[string]url.Values{
		"var olan, yanlis sifre": {"eposta": {"var@ornek.com"}, "sifre": {"yanlis"}, "authRequestID": {"abc"}},
		"olmayan hesap":          {"eposta": {"yok@ornek.com"}, "sifre": {"herhangi"}, "authRequestID": {"abc"}},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, yolGiris, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		s.Giris(w, r)
		govdeler[ad] = w.Body.String()
	}
	if govdeler["var olan, yanlis sifre"] != govdeler["olmayan hesap"] {
		t.Error("iki hata yaniti farkli; hesabin varligi sizdiriliyor")
	}
}

// Sayfa kullanici girdisini kacirmali; sablon html/template ile
// uretildigi icin bu otomatiktir, ama regresyona karsi test edilir.
func TestSayfaGirdiKacirir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	kotu := `"><script>alert(1)</script>`
	r := httptest.NewRequest(http.MethodGet, yolGiris+"?authRequestID="+url.QueryEscape(kotu), nil)
	s.Giris(w, r)
	if strings.Contains(w.Body.String(), "<script>") {
		t.Error("girdi kacirilmamis, XSS mumkun")
	}
}

func TestKayitSayfasiFormGosterir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	s.Kayit(w, httptest.NewRequest(http.MethodGet, yolKayit+"?authRequestID=abc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d", w.Code)
	}
	for _, beklenen := range []string{`name="eposta"`, `name="sifre"`, `name="ad"`} {
		if !strings.Contains(w.Body.String(), beklenen) {
			t.Errorf("sayfa %q icermiyor", beklenen)
		}
	}
}

// Kullanilan e-posta ile kayit, kullaniciya anlasilir bir hata vermeli
// ama 500 olmamali.
func TestKullanilanEpostaIleKayit(t *testing.T) {
	s, kullanici := testSayfalar(t)
	if _, err := kullanici.Create(t.Context(), "var@ornek.com", "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	form := url.Values{
		"eposta": {"var@ornek.com"}, "ad": {"Yeni"},
		"sifre": {"baskaSifre12"}, "authRequestID": {"abc"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, yolKayit, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Kayit(w, r)
	if w.Code >= 500 {
		t.Errorf("durum = %d, 5xx olmamali", w.Code)
	}
}
```

`t.Context()` Go 1.24+ ile gelir; Go 1.25 kullandigimiz icin sorun yok.

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/kimlik/ -run "TestGiris|TestYanlisGiris|TestSayfaGirdi|TestKayit|TestKullanilanEposta" -v
```
Beklenen: FAIL — `undefined: NewSayfalar`.

- [ ] **Step 3: Sablonlari yaz**

`core-service/internal/kimlik/sablonlar/giris.html` — sade, JS'siz, mobil genislikte okunabilir. `{{.AuthRequestID}}` gizli alanda, `{{.Hata}}` varsa gosterilir, `{{.KayitYolu}}` kayit sayfasina baglanti. `html/template` kullanildigi icin kacirma otomatiktir; `template.HTML` gibi kacirmayi devre disi birakan tipler **kullanilmaz**.

`kayit.html` ayni yapida, ek `ad` alaniyla.

- [ ] **Step 4: sayfa.go'yu yaz**

```go
package kimlik

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"

	"github.com/zitadel/oidc/v3/pkg/op"
)

const (
	yolGiris = "/giris"
	yolKayit = "/kayit"
)

//go:embed sablonlar/*.html
var sablonFS embed.FS

var sablonlar = template.Must(template.ParseFS(sablonFS, "sablonlar/*.html"))

// hataGirisBasarisiz, hem "boyle bir hesap yok" hem "sifre yanlis"
// durumunda gosterilir. Ikisini ayirmak formu e-posta sayim aracina
// cevirir.
const hataGirisBasarisiz = "E-posta veya sifre hatali."

type Sayfalar struct {
	kullanici   UserStore
	istekler    *IstekDepo
	geriCagirma func(context.Context, string) string
}

func NewSayfalar(
	kullanici UserStore,
	istekler *IstekDepo,
	geriCagirma func(context.Context, string) string,
) *Sayfalar {
	return &Sayfalar{kullanici: kullanici, istekler: istekler, geriCagirma: geriCagirma}
}

// Bagla, sayfalari mux'a baglar. POST handler'lari issuer aracisiyla
// SARILIR: araci issuer'i istek baglamina koyar ve geriCagirma onu
// oradan okur. Sarmadan baglamak akisi yonlendirme asamasinda kirar.
func (s *Sayfalar) Bagla(mux *http.ServeMux, araci *op.IssuerInterceptor) {
	mux.HandleFunc("GET "+yolGiris, s.Giris)
	mux.HandleFunc("POST "+yolGiris, araci.HandlerFunc(s.Giris))
	mux.HandleFunc("GET "+yolKayit, s.Kayit)
	mux.HandleFunc("POST "+yolKayit, araci.HandlerFunc(s.Kayit))
}
```

Yol desenlerindeki metot oneki (`"GET /giris"`) Go 1.22+ `http.ServeMux` ozelligidir; Go 1.25 kullandigimiz icin gecerlidir.

`Giris`: GET ise formu render eder (authRequestID bos → 400). POST ise `ByEmail` + `SifreDogru`; basarisizsa **her iki durumda ayni** `hataGirisBasarisiz` ile formu 401 ile tekrar render eder; basariliysa `TamamlandiIsaretle` ve ardindan:

```go
	http.Redirect(w, r, s.geriCagirma(r.Context(), id), http.StatusFound)
```

`Kayit`: GET formu; POST `Create`, `ErrEpostaKullanimda` → 409 ve anlasilir mesaj, `ErrSifreKisa` → 400, basari → giris ile ayni sekilde akisi surdurur.

Sifreler **hicbir kosulda** loglanmaz; hata loglarinda yalnizca e-posta ve hata turu yer alir.

- [ ] **Step 5: Sayfalari sunucuya bagla**

`sunucu.go` icinde mux kurulumunda:

```go
	araci := op.NewIssuerInterceptor(saglayici.IssuerFromRequest)
	sayfalar := NewSayfalar(kullaniciDepo, istekDepo, op.AuthCallbackURL(saglayici))
	sayfalar.Bagla(mux, araci)
	// Kok yol en sona baglanir: kutuphanenin kendi uclari (/authorize,
	// /oauth/token, /userinfo, /keys, /end_session) buradan gecer.
	mux.Handle("/", saglayici)
```

Varsayilan uc yollari modulden okundu (`pkg/op/op.go:26-32`): `authorize`, `oauth/token`, `userinfo`, `end_session`, `keys`. Bunlari degistirmiyoruz; discovery belgesi dogru adresleri kendisi yayinlar.

- [ ] **Step 6: Testleri calistir**

```bash
cd core-service && go build ./... && go test ./internal/kimlik/ -v 2>&1 | tail -30
```
Beklenen: hepsi PASS.

- [ ] **Step 7: Commit**

```bash
git add core-service/internal/kimlik
git commit -m "feat(kimlik): giris ve kayit sayfalari, sayim sizintisina kapali hata"
```

---

## Task 11: API tarafinda kendi access token'imizi dogrulama

**Files:**
- Modify: `core-service/internal/middleware/auth.go`
- Create: `core-service/internal/middleware/auth_test.go`
- Modify: `core-service/cmd/server/main.go`

**Interfaces:**
- Consumes: `Anahtar` (Task 4), `config.AuthConfig`
- Produces: `middleware.NewOPAuthMiddleware(issuer string, acik *rsa.PublicKey) *AuthMiddleware`

Bu fazda `gateway_auth.go`, `legacy.go` ve `ZitadelConfig` **silinmez** (Faz 5'in isi). Eklenen: OP'nin urettigi access token'i dogrulayan yeni bir mod, ve `cfg.Auth.Issuer` doluysa bu modun oncelikli secilmesi.

Dogrulama **surec icinden** okunan acik anahtarla yapilir; kendi JWKS ucumuza ag uzerinden gidilmez, aksi halde servis kendi baslangicina bagimli hale gelir.

- [ ] **Step 1: Testleri yaz**

`core-service/internal/middleware/auth_test.go`:

```go
package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const denemeIssuer = "https://kimlik.ornek.dev"

func denemeAnahtar(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	return a
}

func jetonUretTest(t *testing.T, ozel *rsa.PrivateKey, talepler jwt.MapClaims) string {
	t.Helper()
	j := jwt.NewWithClaims(jwt.SigningMethodRS256, talepler)
	imzali, err := j.SignedString(ozel)
	if err != nil {
		t.Fatalf("imzalama: %v", err)
	}
	return imzali
}

func uygulama(t *testing.T, ozel *rsa.PrivateKey) *fiber.App {
	t.Helper()
	mw := NewOPAuthMiddleware(denemeIssuer, &ozel.PublicKey)
	app := fiber.New()
	app.Get("/korumali", mw.Authenticate(), func(c *fiber.Ctx) error {
		return c.SendString(GetUserID(c))
	})
	return app
}

func istek(t *testing.T, app *fiber.App, yetki string) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/korumali", nil)
	if yetki != "" {
		r.Header.Set("Authorization", yetki)
	}
	y, err := app.Test(r, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	govde := make([]byte, 256)
	n, _ := y.Body.Read(govde)
	return y.StatusCode, string(govde[:n])
}

func TestGecerliJetonKabulEdilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer,
		"sub": "kullanici-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	durum, govde := istek(t, uygulama(t, ozel), "Bearer "+jeton)
	if durum != 200 {
		t.Fatalf("durum = %d, beklenen 200", durum)
	}
	if govde != "kullanici-1" {
		t.Errorf("userId = %q, beklenen kullanici-1", govde)
	}
}

// exp claim'i olmayan jeton sonsuza kadar gecerli olur. Legacy yolun
// hatasi tam buydu; yeni yolda zorunlu.
func TestExpsizJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{"iss": denemeIssuer, "sub": "k1"})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

func TestSuresiGecmisJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1",
		"exp": time.Now().Add(-time.Minute).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Baska bir anahtarla imzalanmis jeton reddedilmeli.
func TestBaskaAnahtarlaImzaliJetonReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	sahte := denemeAnahtar(t)
	jeton := jetonUretTest(t, sahte, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// Yanlis issuer reddedilmeli: baska bir OP'nin jetonu bizim API'mize
// girmemeli.
func TestYanlisIssuerReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": "https://baska.ornek.dev", "sub": "k1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

// "alg: none" ile imzasiz jeton kabul edilmemeli.
func TestAlgNoneReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	j := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "k1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	imzasiz, err := j.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("imzasiz jeton uretilemedi: %v", err)
	}
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+imzasiz); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}

func TestEksikVeBozukHeader(t *testing.T) {
	ozel := denemeAnahtar(t)
	app := uygulama(t, ozel)
	for ad, yetki := range map[string]string{
		"header yok":     "",
		"Bearer yok":     "abc.def.ghi",
		"bos jeton":      "Bearer ",
		"yanlis sema":    "Basic abc",
	} {
		if durum, _ := istek(t, app, yetki); durum != 401 {
			t.Errorf("%s: durum = %d, beklenen 401", ad, durum)
		}
	}
}

// sub bos ise userId bos kalir ve rate limit anahtari cokur; reddedilmeli.
func TestSubBossaReddedilir(t *testing.T) {
	ozel := denemeAnahtar(t)
	jeton := jetonUretTest(t, ozel, jwt.MapClaims{
		"iss": denemeIssuer, "sub": "", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if durum, _ := istek(t, uygulama(t, ozel), "Bearer "+jeton); durum != 401 {
		t.Errorf("durum = %d, beklenen 401", durum)
	}
}
```

- [ ] **Step 2: Testi calistir, basarisiz oldugunu gor**

```bash
cd core-service && go test ./internal/middleware/ -v
```
Beklenen: FAIL — `undefined: NewOPAuthMiddleware`.

- [ ] **Step 3: NewOPAuthMiddleware'i yaz**

`internal/middleware/auth.go` icine ekleyin (mevcut `AuthMiddleware` struct'ini bozmadan; yeni bir alan ve constructor):

```go
// opDogrulayici, kendi OP'umuzun urettigi RS256 access token'ini
// dogrular. Acik anahtar surec icinden gelir; kendi JWKS ucumuza ag
// uzerinden gitmek servisi kendi baslangicina bagimli kilardi.
type opDogrulayici struct {
	issuer string
	acik   *rsa.PublicKey
}

func NewOPAuthMiddleware(issuer string, acik *rsa.PublicKey) *AuthMiddleware {
	return &AuthMiddleware{op: &opDogrulayici{issuer: issuer, acik: acik}}
}

func (d *opDogrulayici) dogrula(jetonMetni string) (string, string, string, error) {
	ek := jwt.MapClaims{}
	jeton, err := jwt.ParseWithClaims(jetonMetni, ek,
		func(t *jwt.Token) (any, error) {
			// Imza algoritmasi ACIKCA kisitlanir: aksi halde "alg: none"
			// veya HMAC'e dusurme saldirisi mumkun olur.
			if _, uygun := t.Method.(*jwt.SigningMethodRSA); !uygun {
				return nil, fmt.Errorf("beklenmeyen imza yontemi: %v", t.Header["alg"])
			}
			return d.acik, nil
		},
		jwt.WithIssuer(d.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	if err != nil {
		return "", "", "", err
	}
	if !jeton.Valid {
		return "", "", "", fmt.Errorf("jeton gecersiz")
	}
	sub, _ := ek["sub"].(string)
	if sub == "" {
		return "", "", "", fmt.Errorf("sub claim'i bos")
	}
	eposta, _ := ek["email"].(string)
	ad, _ := ek["name"].(string)
	return sub, eposta, ad, nil
}
```

`Authenticate()` icinde, `verifier` denemesinden **once** `op` doluysa onu kullanin; basarisizsa dogrudan 401 dondurun (OP modunda legacy'ye dusmek yoktur).

- [ ] **Step 4: Testleri calistir**

```bash
cd core-service && go test ./internal/middleware/ -v 2>&1 | tail -25
```
Beklenen: hepsi PASS.

- [ ] **Step 5: main.go'da mod secimini guncelle**

`main.go` satir 146-162'deki secim zincirinin **basina** ekleyin:

```go
	var apiAuthMiddleware fiber.Handler
	switch {
	case kimlikSunucu != nil:
		// Kendi OP'umuz ayakta: access token'lari onun anahtariyla
		// dogrula. Bu modda gateway veya legacy yola dusulmez.
		log.Println("Info: kimlik saglayicisi modu — RS256 access token dogrulanacak")
		apiAuthMiddleware = middleware.NewOPAuthMiddleware(
			cfg.Auth.Issuer, kimlikSunucu.APIAcikAnahtar()).Authenticate()
	case cfg.Gateway.Enabled:
		// ... mevcut kod aynen kalir
```

`kimlik.Sunucu`.ya `APIAcikAnahtar() *rsa.PublicKey` metodu ekleyin. Ad bilincli olarak `Anahtar.AcikAnahtar()`.tan farklidir: o JWKS icin `op.Key` sarmalayicisi dondurur, bu ise API dogrulamasi icin ham acik anahtari.

- [ ] **Step 6: ratelimit fail-open'i kapat**

`internal/middleware/ratelimit.go` satir 24-27:

```go
	userID := GetUserID(c)
	if userID == "" {
		// Auth middleware userId'yi garanti eder; bos gelmesi bir
		// hatadir. Eskiden burada rate limit ATLANIYORDU (return
		// c.Next()), bu da kota bypass'i demekti.
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
```

`response` paketinin import edildiginden ve `Unauthorized` yardimcisinin mevcut oldugundan emin olun (`pkg/response/`). Yoksa `fiber.NewError(fiber.StatusUnauthorized, ...)` kullanin.

- [ ] **Step 7: Authorization header'ini logdan cikar**

`main.go` satir 173-178'de debug formati `${reqHeaders}` iceriyor ve bu `Authorization` header'ini oldugu gibi loglar (`main.go:176`). Kume manifestinde seviye `info`'ya inecek (Task 12), ama birinin debug'i acmasi jetonlari loga dokmemeli. Formattan `${reqHeaders}` kaldirin:

```go
	isDebug := strings.EqualFold(cfg.Server.LogLevel, "debug")
	logFormat := "[${time}] ${status} - ${latency} ${method} ${path}\n"
	if isDebug {
		// ${reqHeaders} BILINCLI olarak yok: Authorization header'ini
		// loglamak jetonlari kalici hale getirir.
		logFormat = "[${time}] ${status} - ${latency} ${method} ${path} ${queryParams}\n"
		log.Println("Debug logging enabled (istek header'lari loglanmaz)")
	}
```

`${body}` de kaldirildi: kayit ve giris formlari POST govdesinde **duz metin sifre** tasir.

- [ ] **Step 8: /health'in yaniltici auth alanini duzelt**

`main.go` satir 204: `"auth": jwksVerifier != nil || cfg.JWT.Secret != ""` her zaman true dondurur, cunku `jwt.secret` varsayilani bos degil. Degistirin:

```go
				"auth": kimlikSunucu != nil || jwksVerifier != nil,
```

- [ ] **Step 9: Derleme ve tum testler**

```bash
cd core-service && go build ./... && go vet ./internal/... ./cmd/... && go test ./internal/... 2>&1 | tail -15
```
Beklenen: derleme temiz, vet bos, testler PASS.

- [ ] **Step 10: Commit**

```bash
git add core-service/internal/middleware core-service/cmd/server/main.go
git commit -m "feat(auth): kendi access token'imizi dogrula, rate limit fail-closed"
```

---

## Task 12: Sevkiyat — sirlar, manifestler, Postgres devri, dokuman

**Files:**
- Create: `k3s-gitops/apps/makesinger/makesinger-auth-kimlik.decrypted.yaml`
- Modify: `k3s-gitops/apps/kiracilar/makesinger-db.yaml`
- Modify: `k3s-gitops/apps/kiracilar/zitadel-db-kimlik.sops.yaml` → yeni ada tasinir
- Modify: `k3s-gitops/apps/makesinger/makesinger-backend.yaml`
- Modify: `k3s-gitops/apps/makesinger/ingress.yaml`
- Modify: `k3s-gitops/apps/makesinger/sertifikalar.yaml`
- Modify: `k3s-gitops/apps/makesinger/kustomization.yaml`, `k3s-gitops/apps/kiracilar/kustomization.yaml`
- Modify: `make-singer-backend/CLAUDE.md`, `core-service/.env.example`, `core-service/config.yaml`

**Interfaces:**
- Consumes: Task 1-11'in tamami
- Produces: kumede calisan OP

- [ ] **Step 1: Sirlari uret**

```bash
cd /Users/cos/Documents/k3s-gitops
openssl genrsa 2048 > /tmp/auth-imza.pem
CRYPTO="$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 32)"
DBSIFRE="$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 32)"
echo "crypto uzunlugu: ${#CRYPTO} (32 olmali)"
```

`CryptoAnahtar` tam 32 bayt ister; `${#CRYPTO}` 32 degilse tekrar uretin.

- [ ] **Step 2: Secret'i duz metin olarak yaz**

`k3s-gitops/apps/makesinger/makesinger-auth-kimlik.decrypted.yaml`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: makesinger-auth-kimlik
  namespace: makesinger
type: Opaque
stringData:
  # RS256 imzalama anahtari. Degisirse butun id_token/access_token'lar
  # gecersizlesir ve kullanicilar yeniden giris yapar.
  signing_key: |
    <buraya /tmp/auth-imza.pem icerigi>
  # op.Config.CryptoKey icin TAM 32 bayt.
  crypto_key: <CRYPTO>
  # Postgres DSN. Rol ve veritabani Zitadel'den devralindi.
  db_url: postgres://makesinger_auth:<DBSIFRE>@pg-kiracilar-rw.kiracilar.svc.cluster.local:5432/makesinger_auth?sslmode=require
```

Bu dosya gitignore'lu; commit sirasinda pre-commit hook `makesinger-auth-kimlik.sops.yaml` uretip stage'e ekler (bkz. `k3s-gitops/README.md` "Secret duzenleme"). `core.hooksPath` ayarli degilse hook sessizce calismaz:

```bash
git -C /Users/cos/Documents/k3s-gitops config core.hooksPath
```
Beklenen cikti: `.githooks`. Bos donerse `git config core.hooksPath .githooks` calistirin.

- [ ] **Step 3: Postgres rolunu ve veritabanini devral**

`k3s-gitops/apps/kiracilar/makesinger-db.yaml`: dosya basindaki aciklamayi guncelleyin (artik Zitadel degil, kendi kimlik saglayicimiz icin), `DatabaseRole` adini `pg-kiracilar-zitadel` → `pg-kiracilar-makesinger-auth`, rol adini `zitadel` → `makesinger_auth`, `Database` adini ve `name`/`owner` alanlarini ayni sekilde cevirin. `passwordSecret` adini `makesinger-auth-db-kimlik` yapin.

`apps/kiracilar/zitadel-db-kimlik.sops.yaml` yerine `apps/kiracilar/makesinger-auth-db-kimlik.decrypted.yaml` yazin (ayni `DBSIFRE` degeri, `namespace: kiracilar`), eskisini silin. Iki kustomization'daki girdileri guncelleyin.

`databaseReclaimPolicy: retain` ve `databaseRoleReclaimPolicy: retain` **korunur**: yanlis bir degisiklikte veritabani silinmemeli.

- [ ] **Step 4: Backend deployment'ina env ve secret bagla**

`k3s-gitops/apps/makesinger/makesinger-backend.yaml`, `api` container'ina:

```yaml
            - name: AUTH_ISSUER
              value: https://makesinger-auth.celalettindemir.dev
            - name: AUTH_PORT
              value: "8001"
            - name: AUTH_CLIENT_ID
              value: makesinger-mobil
            - name: AUTH_REDIRECT_URIS
              value: com.makesinger.app:/oauth2redirect https://makesinger.celalettindemir.dev/oauth2redirect
            - name: AUTH_DB_URL
              valueFrom: { secretKeyRef: { name: makesinger-auth-kimlik, key: db_url } }
            - name: AUTH_SIGNING_KEY
              valueFrom: { secretKeyRef: { name: makesinger-auth-kimlik, key: signing_key } }
            - name: AUTH_CRYPTO_KEY
              valueFrom: { secretKeyRef: { name: makesinger-auth-kimlik, key: crypto_key } }
```

`AUTH_REDIRECT_URIS` bosluklarla ayrilir; `viper.GetStringSlice` bunu boler.

Ayni container'a `containerPort: 8001` ekleyin ve **`LOG_LEVEL`'i `debug`'dan `info`'ya cekin** (satir 33-36): debug formati `${reqHeaders}` ile `Authorization` header'ini logluyor (`main.go:176`).

`makesinger-api` Service'ine 8001 portunu ekleyin.

- [ ] **Step 5: Sertifika ve IngressRoute ekle**

`sertifikalar.yaml`'a `makesinger-auth-tls` Certificate'i (tek `dnsName: makesinger-auth.celalettindemir.dev`, ClusterIssuer `letsencrypt`), mevcut `makesinger-app-tls` girdisiyle ayni bicimde.

`ingress.yaml`'a **iki** IngressRoute: `makesinger-auth` (`websecure`, `tls: { secretName: makesinger-auth-tls }`) ve `makesinger-auth-http` (`web`, `tls:` blogu **YOK**). Ikisi de `makesinger-api:8001`'e gider. Cloudflare "Flexible" modu ikinci rotayi zorunlu kilar; atlanirsa http bacagi 404 doner.

Dosya basindaki, API'nin neden acilmadigini anlatan yorumu (satir 1-14) guncelleyin: `makesinger-api` bu fazda **hala acilmiyor** (Faz 4'un isi), yalnizca auth hostname'i aciliyor.

- [ ] **Step 6: Dry-run ile dogrula**

```bash
export KUBECONFIG=$HOME/.kube/config-k3s
kubectl config current-context
```
Beklenen: `k3s-mba`. Baska bir sey gorurseniz **devam etmeyin** — baglam kaymasi baska bir kumeye uygulamaya yol acar (bkz. `k3s-gitops/RUNBOOK.md`).

```bash
kubectl kustomize apps/makesinger | kubectl apply --dry-run=server -f - 2>&1 | tail -20
```
Beklenen: her kaynak icin `(server dry run)`, hata yok. sops'lu secret'lar dry-run'da cozulemezse bu adimi Flux'a birakip Step 8'deki dogrulamaya guvenin.

- [ ] **Step 7: Dokumani guncelle**

`core-service/.env.example`: `AUTH_*` degiskenlerini ekleyin (gercek sir yazmayin, bicim ornegi verin).

`core-service/config.yaml`: `auth:` blogu ekleyin, sir alanlarini **bos** birakin.

`make-singer-backend/CLAUDE.md`: "Repository Structure" (satir 9-12) ve "Auth" (satir 89-91) bolumlerini guncelleyin — artik kendi OP'umuz var, `internal/kimlik/` paketi eklendi, Postgres ikinci veri deposu oldu. Satir 40'taki "Redis is the only data store" ifadesi artik **yanlis**, duzeltin. `Zitadel | OIDC authentication` tablo satirini (satir 68) kendi OP'umuzla degistirin.

- [ ] **Step 8: Commit ve sevk**

```bash
cd /Users/cos/Documents/make-singer/make-singer-backend
git add -A && git commit -m "docs: kendi OP'umuz icin config ve dokuman guncellemesi"
git push
```

Imaj icin etiket atin ve CI'nin yesil oldugunu, iki imajin da uretildigini dogrulayin; digest'leri CI loglarindan alip `makesinger-backend.yaml`'a yazin (Docker isleri yalnizca `refs/tags/v*` ile kosar ve `metadata-action` bastaki `v`'yi atar).

```bash
cd /Users/cos/Documents/k3s-gitops
git add -A && git commit -m "feat(makesinger): kendi kimlik saglayicimiz — secret, ingress, Postgres devri"
git push
flux reconcile kustomization apps --with-source
```

- [ ] **Step 9: Canli dogrulama**

```bash
export KUBECONFIG=$HOME/.kube/config-k3s
kubectl -n makesinger get pods
kubectl -n makesinger logs deploy/makesinger-api --tail=40 | grep -i "kimlik\|auth\|hata\|error"
kubectl -n makesinger exec deploy/makesinger-api -- wget -qO- -T5 http://127.0.0.1:8000/health
curl -s https://makesinger-auth.celalettindemir.dev/.well-known/openid-configuration | head -c 400
curl -s https://makesinger-auth.celalettindemir.dev/keys
```

Beklenen: pod `1/1 Running`; `/health` ciktisinda `"kimlik":true` ve `"auth":true`; discovery belgesinde `"issuer":"https://makesinger-auth.celalettindemir.dev"` ve `"code_challenge_methods_supported":["S256"]`; JWKS ciktisinda `"kty":"RSA"` var ve **`"d"` YOK**.

- [ ] **Step 10: Uctan uca giris denemesi**

Tarayicida acin:

```
https://makesinger-auth.celalettindemir.dev/authorize?client_id=makesinger-mobil&response_type=code&scope=openid%20profile%20email%20offline_access&redirect_uri=com.makesinger.app:/oauth2redirect&code_challenge=<S256>&code_challenge_method=S256&state=deneme&nonce=deneme
```

`code_challenge` uretimi:

```bash
V="$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 64)"
echo "verifier: $V"
printf '%s' "$V" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '='
```

Beklenen: giris sayfasi acilir, kayit baglantisi calisir, kayit sonrasi `com.makesinger.app:/oauth2redirect?code=...&state=deneme`'ye yonlendirilir. Kodu jetona cevirin:

```bash
curl -s -X POST https://makesinger-auth.celalettindemir.dev/oauth/token \
  -d grant_type=authorization_code \
  -d client_id=makesinger-mobil \
  -d "code=<KOD>" \
  -d "redirect_uri=com.makesinger.app:/oauth2redirect" \
  -d "code_verifier=$V" | python3 -m json.tool
```

Token ucunun yolu modulden dogrulandi: varsayilan `oauth/token` (`pkg/op/op.go:27`). Yine de discovery belgesindeki `token_endpoint` alaniyla karsilastirin.

Beklenen: `access_token`, `id_token`, `refresh_token`, `expires_in` (900 civari). Ayni kodu **ikinci kez** kullanmayi deneyin — reddedilmeli. `refresh_token`'i iki kez kullanmayi deneyin — ikincisi reddedilmeli (aile iptali).

- [ ] **Step 11: Commit (gerekiyorsa digest duzeltmesi)**

```bash
cd /Users/cos/Documents/k3s-gitops && git add -A && git commit -m "chore(makesinger): imaj digestlerini guncelle" && git push
```

---

## Plan sonrasi durum

Bu plan bittiginde: `makesinger-auth.celalettindemir.dev` uzerinde kendi OP'umuz var, e-posta+sifre ile kayit ve giris yapilabiliyor, `/api/*` bizim access token'imizi dogruluyor, rate limit fail-closed, `Authorization` artik loglanmiyor.

Bu plan **kapsamamiz**: Apple/Google federasyonu (Faz 2), sahiplik kontrolu ve R2 anahtar duzeni (Faz 3), mobil guncellemesi (Faz 3), API'nin disa acilmasi (Faz 4), Zitadel sokumu (Faz 5). Mobil bu faz boyunca eski yapilandirmada kalir ve `makesinger-api` disa kapali oldugu icin kimse etkilenmez.

Spec'teki **hesap silme** (App Store yonergesi 5.1.1(v)) bilincli olarak Faz 3'e birakildi: ucun kendisi tek basina bir sey ifade etmiyor, mobildeki girisiyle birlikte sevk edilmesi gerekiyor ve silmesi gereken R2 nesnelerinin kullanici onekli duzeni de Faz 3'te doguyor. Faz 1'de hesap silme YOKTUR; uygulama magazaya bu faz sonunda gonderilmez.
