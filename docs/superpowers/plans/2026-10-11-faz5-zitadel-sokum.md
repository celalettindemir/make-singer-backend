# Faz 5 — Rate Limit + Zitadel Sokumu Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Herkese acik API'deki iki sinirsiz uca (`/api/ws/ticket`, `/api/files/sign`) rate limit eklemek, ardindan Zitadel'i ve onun etrafindaki legacy/gateway auth yollarini koddan, compose/stack dosyalarindan, dokumanlardan ve k3s kumesinden tamamen sokmek.

**Architecture:** Rate limit mevcut `RateLimiter.Limit` (Redis sabit pencere, fail-closed) ile kullanici basina dakikalik sayac. Sokumden sonra `/api/*` icin TEK auth yolu kendi OP'umuzun RS256 access token'idir; `AUTH_ISSUER` bos ise servis baslamaz (fail-closed, legacy HMAC yolu yok). `internal/auth/jwks_verifier.go` yalnizca federasyon icin (`YukariAkisDogrulayici`) kalir. Gitops tarafinda Zitadel Deployment/Service/PVC/secret'lari ve `zitadel` CNPG rol/DB CR'leri git'ten silinir; flux prune kumeden kaldirir; `retain` politikasi yuzunden Postgres'teki `zitadel` DB/rol elle DROP edilir.

**Tech Stack:** Go 1.25, Fiber v2, Redis, golang-jwt v5, golangci-lint v2.14.0, Flux/kustomize, CNPG.

**Spec:** `docs/superpowers/specs/2026-09-29-kendi-oidc-saglayicimiz-design.md` (bolumler "Sokulenler", "Sizmis sirlar", "Sevkiyat" Faz 5)

## Global Constraints

- Backend Go yorumlari Turkce, aksansiz (mevcut dosyalar gibi).
- `./e2e/...` testleri ASLA calistirilmaz (ucretli Suno + canli anahtarlar). e2e degisiklikleri yalnizca derlenir: `go vet ./e2e/...` (ve `go test -count=1 -run XXX_YOK ./e2e/...` derleme kontrolu icin KULLANILMAZ — vet yeterli).
- Testlerde gercek Suno/R2/Groq cagrisi yok.
- Birim testler: `cd core-service && go test ./internal/...`. Canli Redis testleri localhost:6379, canli PG testleri `KIMLIK_TEST_DB_URL='postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable'`.
- Lint: `cd core-service && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` her backend gorevinin sonunda temiz olmali.
- Commit sonu: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Yeni rate limit degerleri: `ratelimit.ws_ticket_per_min: 60`, `ratelimit.sign_per_min: 60`.
- `jwks_verifier.go` KALIR: `YukariAkisDogrulayici`, `NewYukariAkisDogrulayici`, `DogrulaNonce`, `Claims`, `EsnekBool` dokunulmaz.
- k3s-gitops reposu (Task 5) paylasimli `main`: push oncesi `git pull --rebase`, asla force push. Task 5 ve Task 6 kumeyi geri donulmez degistirir; controller bunlari kullanici onayi ile kendisi yapar, subagent'a verilmez.

---

### Task 1: `/api/ws/ticket` ve `/api/files/sign` rate limit

**Files:**
- Modify: `core-service/internal/middleware/ratelimit.go` (helper'larin yanina iki yeni helper)
- Modify: `core-service/internal/config/config.go` (`RateLimitConfig`, defaultlar ~264-268, insa ~355-360)
- Modify: `core-service/config.yaml` (`ratelimit:` blogu, satir 15-20)
- Modify: `core-service/cmd/server/main.go` (rotalar ~343 ve ~346)
- Test: `core-service/internal/middleware/ratelimit_canli_test.go`

**Interfaces:**
- Produces: `func (rl *RateLimiter) WSTicketLimit(maxPerMin int) fiber.Handler` (anahtar oneki `"wsticket"`, pencere `time.Minute`), `func (rl *RateLimiter) SignLimit(maxPerMin int) fiber.Handler` (onek `"sign"`, pencere `time.Minute`); `RateLimitConfig.WSTicketPerMin int`, `RateLimitConfig.SignPerMin int`.

- [ ] **Step 1: Failing test yaz**

`ratelimit_canli_test.go` icindeki `TestCanliMutluYolVeSayac` desenini (canli Redis, Redis yoksa `t.Skip`, test kullanicisi Locals ile) oku ve ayni yardimcilarla yeni test ekle. Test, iki helper'in da limit asiminda 429 dondugunu ve dogru onek/pencere ile calistigini dogrular:

```go
// ws bileti ve imzalama uclari herkese acik API'de sinirsiz kalmamali:
// limit asilinca 429, sayac kendi onekinde ve dakikalik pencerede.
func TestCanliWSTicketVeSignLimit(t *testing.T) {
	for _, d := range []struct {
		ad    string
		onek  string
		yap   func(rl *RateLimiter, n int) fiber.Handler
	}{
		{"wsticket", "wsticket", func(rl *RateLimiter, n int) fiber.Handler { return rl.WSTicketLimit(n) }},
		{"sign", "sign", func(rl *RateLimiter, n int) fiber.Handler { return rl.SignLimit(n) }},
	} {
		t.Run(d.ad, func(t *testing.T) {
			// Mevcut canli testlerdeki Redis kurulum/temizlik yardimcilarini
			// ve benzersiz kullanici id uretimini AYNEN kullan; burada
			// limit 2 ile handler'i kur, ucuncu istekte 429 bekle, sonra
			// sayac anahtarinin TTL'inin 0 < ttl <= 60s oldugunu dogrula.
		})
	}
}
```

Implementer: yukaridaki govde yorumunu mevcut dosyadaki gercek yardimci adlariyla (Redis istemcisi kurulumu, anahtar bicimi `Limit` icinde nasil uretiliyorsa o) somut koda cevir; anahtar bicimini `ratelimit.go:23-97` icindeki `Limit` govdesinden oku, uydurma.

- [ ] **Step 2: Testin derlenmedigini/kirildigini gor**

Run: `cd core-service && go test ./internal/middleware/ -run TestCanliWSTicketVeSignLimit -v`
Expected: FAIL — `rl.WSTicketLimit undefined`

- [ ] **Step 3: Helper'lari ekle** (`ratelimit.go`, `UploadLimit`'in altina)

```go
// WSTicketLimit, WebSocket bileti ucu icin dakikalik limit.
func (rl *RateLimiter) WSTicketLimit(maxPerMin int) fiber.Handler {
	return rl.Limit("wsticket", maxPerMin, time.Minute)
}

// SignLimit, URL imzalama ucu icin dakikalik limit. Tek istek en fazla
// 50 anahtar imzalar; sayac istek basinadir.
func (rl *RateLimiter) SignLimit(maxPerMin int) fiber.Handler {
	return rl.Limit("sign", maxPerMin, time.Minute)
}
```

- [ ] **Step 4: Testi gecir**

Run: `cd core-service && go test ./internal/middleware/ -run TestCanliWSTicketVeSignLimit -v`
Expected: PASS (Redis localhost:6379 ayakta)

- [ ] **Step 5: Config ve rotalar**

`config.go`:
```go
type RateLimitConfig struct {
	LyricsPerMin   int
	RenderPerHour  int
	MasterPerHour  int
	ExportPerHour  int
	UploadPerHour  int
	WSTicketPerMin int
	SignPerMin     int
}
```
defaultlar: `viper.SetDefault("ratelimit.ws_ticket_per_min", 60)` ve `viper.SetDefault("ratelimit.sign_per_min", 60)`; insa: `WSTicketPerMin: viper.GetInt("ratelimit.ws_ticket_per_min")`, `SignPerMin: viper.GetInt("ratelimit.sign_per_min")`.

`config.yaml` `ratelimit:` blogunun sonuna:
```yaml
  ws_ticket_per_min: 60
  sign_per_min: 60
```

`main.go`:
```go
	api.Post("/ws/ticket", rateLimiter.WSTicketLimit(cfg.RateLimit.WSTicketPerMin), wsTicketHandler.Issue)
	api.Post("/files/sign", rateLimiter.SignLimit(cfg.RateLimit.SignPerMin), fileHandler.Sign)
```

- [ ] **Step 6: Tum birim testler + lint**

Run: `cd core-service && go build ./... && go test ./internal/... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...`
Expected: hepsi gecer, lint 0 issue.

- [ ] **Step 7: Commit**

```bash
git add core-service/internal/middleware/ratelimit.go core-service/internal/middleware/ratelimit_canli_test.go core-service/internal/config/config.go core-service/config.yaml core-service/cmd/server/main.go
git commit -m "feat(ratelimit): ws bileti ve url imzalama uclarina dakikalik limit"
```

---

### Task 2: Legacy/gateway/Zitadel Go kodunun sokulmesi (+ e2e derleme uyumu)

**Files:**
- Delete: `core-service/internal/auth/legacy.go`, `core-service/internal/auth/legacy_test.go`
- Delete: `core-service/internal/middleware/gateway_auth.go`, `core-service/internal/middleware/mod.go`, `core-service/internal/middleware/mod_test.go`
- Delete: `core-service/internal/handler/auth_handler.go`, `core-service/internal/handler/auth_handler_test.go`
- Modify: `core-service/internal/auth/jwks_verifier.go` (+ `jwks_verifier_test.go` icindeki JWKSVerifier testleri)
- Modify: `core-service/internal/middleware/auth.go`, `core-service/internal/middleware/auth_test.go` (legacy testleri varsa)
- Modify: `core-service/internal/config/config.go`, `core-service/config.yaml`, `core-service/.env.example`
- Modify: `core-service/cmd/server/main.go`
- Modify: `core-service/e2e/testhelper_test.go`, `e2e/render_real_test.go`, `e2e/lyrics_real_test.go`, `e2e/health_test.go`

**Interfaces:**
- Consumes: Task 1'in `rateLimiter.WSTicketLimit/SignLimit` rotalari (dokunma).
- Produces: `middleware.NewOPAuthMiddleware(issuer, istemci string, acik *rsa.PublicKey) *AuthMiddleware` tek constructor; `AuthMiddleware` yalnizca `op *opDogrulayici` alani tasir. `GetUserID/GetUserEmail/GetUserName` aynen kalir.

- [ ] **Step 1: `internal/auth`**

`jwks_verifier.go`'dan sil: `TokenVerifier` arayuzu, `JWKSVerifier` tipi, `NewJWKSVerifier`, `discoverJWKSURL`, `(*JWKSVerifier).Validate`, `(*JWKSVerifier).Close`, `contains` (yalnizca bunlar kullaniyorsa; `YukariAkisDogrulayici` kullaniyorsa kalir). Artik kullanilmayan importlari (ozellikle `internal/config`) temizle. `jwks_verifier_test.go`'da yalnizca silinen tipleri test eden testleri sil; `YukariAkisDogrulayici` testleri kalir. `legacy.go` ve `legacy_test.go` dosyalarini sil.

Kontrol: `grep -rn "JWKSVerifier\|TokenVerifier\|LegacyClaims\|ValidateLegacy" core-service --include='*.go'` → yalnizca e2e (Step 5'te duzelir) ve main.go (Step 4).

- [ ] **Step 2: `internal/middleware`**

`auth.go`:
- `AuthMiddleware` struct'i yalnizca `op *opDogrulayici` alanini tutar.
- Sil: `UserClaims` takma adi, `NewAuthMiddleware`, `NewAuthMiddlewareWithFallback`, `NewLegacyAuthMiddleware`, `validateLegacyToken`, `GenerateToken`.
- `Authenticate()` icinde `m.op != nil` dalindan sonraki verifier/legacy dallari silinir; `m.op` nil ise (ulasilamaz olmali) `response.Unauthorized(c, "Invalid or expired token")` doner. Bos/bozuk header davranisi aynen kalir.
- `gateway_auth.go`, `mod.go`, `mod_test.go` silinir.
- `auth_test.go`'da legacy/gateway'e dokunan test varsa sil; OP testleri (`TestGecerliJetonKabulEdilir` vb.) degismeden gecmeli.

- [ ] **Step 3: `internal/config` + yapilandirma dosyalari**

`config.go`'dan sil: `JWTConfig` tipi ve `Config.JWT` alani, `ZitadelConfig` ve `Config.Zitadel`, `GatewayConfig` ve `Config.Gateway`; `jwt.*`, `zitadel.*`, `gateway.*` BindEnv/SetDefault satirlari (`jwt.secret "change-me-in-production"` dahil); `readSecret("ZITADEL_CLIENT_ID")` cagrisi; insa blogundaki karsiliklari. `readSecret` baska yerde kullanilmiyorsa onu da sil (lint `unused` yakalar). Viper'in `config.yaml`'daki tanimsiz anahtarlari yok saydigini varsayma — `config.yaml`'dan da sil.

`config.yaml`'dan `jwt:` (11-13), `zitadel:` (41-45) ve `gateway:` (59-61) bloklarini ve yorumlarini sil; satir ~64'teki "falls back to legacy HMAC" yorumunu su sekilde degistir: `# auth.issuer zorunludur: bos ise servis baslamaz (legacy yol yok).`

`.env.example`'dan Zitadel blogunu (34-37) ve `GATEWAY_ENABLED` blogunu (49-50) sil; `JWT_*` satiri varsa onu da.

- [ ] **Step 4: `cmd/server/main.go`**

- Kimlik baslatma blogunu fail-closed yap: `if cfg.Auth.Issuer == "" { log.Fatalf("AUTH_ISSUER zorunlu: kimlik saglayicisi olmadan /api korunamaz") }`, ardindan kosulsuz `kimlik.Start` (hata fatal). Ustteki "Issuer bos ise hic baslamaz; API o zaman eski auth yolunda kalir" yorumunu yeni duruma gore yeniden yaz (legacy yolun neden kaldirildigi tek cumle).
- Sil: JWKS verifier blogu (~142-152), `authModu`/`APIAuthModuSec`, `tokenVerifier`/`legacySir`/`authHandler`, `switch authModu` blogu, `/auth/verify` rotasi ve yorumu.
- Yerine:
```go
	// /api/* icin tek yol: kendi OP'umuzun RS256 access token'i. Acik
	// anahtar surec icinden okunur, JWKS ucuna ag uzerinden gidilmez.
	apiAuthMiddleware := middleware.NewOPAuthMiddleware(
		cfg.Auth.Issuer, cfg.Auth.ClientID, kimlikSunucu.APIAcikAnahtar()).Authenticate()
```
- `hesapService` bloğundaki `if kimlikSunucu != nil` kosulu artik hep dogru: kosulu kaldirip dogrudan olustur; `hesapHandler` nil kontrolleri (rota kaydinda varsa) de sadelesir.
- `/health`: `"auth": kimlikHazir`; `durum` hesabi `if !kimlikHazir` olur (Issuer artik hep dolu).
- `auth` paketi importu kullanilmiyorsa kaldir.

- [ ] **Step 5: e2e derleme uyumu (CALISTIRMA YOK)**

`e2e/testhelper_test.go`:
- `testJWTSecret` sabitini sil; yerine paket duzeyinde test anahtari ve sabitler:
```go
const (
	testIssuer   = "https://kimlik.e2e.test"
	testClientID = "e2e-istemci"
)

// testAnahtar: e2e jetonlarini imzalayan RSA anahtari, paket basina bir kez.
var testAnahtar = func() *rsa.PrivateKey {
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return a
}()
```
- `setupApp`: `authHandler` satirini ve `/auth/verify` rotasini sil; auth middleware: `middleware.NewOPAuthMiddleware(testIssuer, testClientID, &testAnahtar.PublicKey)` ve `.Authenticate()` mevcut kullanimla ayni sekilde.
- `generateToken`:
```go
// generateToken, test istekleri icin OP bicimli bir RS256 access token uretir.
func generateToken(t *testing.T) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   testIssuer,
		"sub":   "test-user-123",
		"aud":   []string{testClientID},
		"jti":   "e2e-jeton",
		"email": "test@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString(testAnahtar)
	if err != nil {
		t.Fatalf("failed to generate test token: %v", err)
	}
	return signed
}
```
- `render_real_test.go` (~123-126, ~195-212) ve `lyrics_real_test.go` (~51): ayni `NewOPAuthMiddleware(testIssuer, testClientID, &testAnahtar.PublicKey)`; `render_real_test.go`'daki kendi `LegacyClaims` jeton ureticisini `generateToken(t)` cagrisiyla degistir (farkli bir userId kullaniyorsa `generateToken`'a parametre ekleme — o userId'yi koruyan kucuk bir `generateTokenFor(t, sub string)` yaz ve `generateToken` onu `"test-user-123"` ile cagirsin).
- `health_test.go`: `/auth/verify` testlerini (`TestAuthVerify_*`) sil.

Run: `cd core-service && go vet ./e2e/...`
Expected: hata yok. (`go test ./e2e/...` KOSMA.)

- [ ] **Step 6: Tam dogrulama**

Run:
```bash
cd core-service && go build ./... && go vet ./... && go test ./internal/... \
 && KIMLIK_TEST_DB_URL='postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable' go test ./internal/kimlik/... \
 && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
grep -rn -i "zitadel\|gateway\|legacy\|JWT_SECRET\|jwt.secret" --include='*.go' --include='*.yaml' --include='.env.example' . | grep -v "_test.go:.*Yukari"
```
Expected: testler ve lint temiz; grep ciktisinda yalnizca `zitadel/oidc` kutuphane import yollari (go.mod/importlar) kalir — baska eslesme yok. Eslesme kalirsa ya sil ya da neden kaldigini rapora yaz.

- [ ] **Step 7: Commit**

```bash
git add -A core-service
git commit -m "refactor(auth): legacy HMAC, gateway, ForwardAuth ve Zitadel JWKS yollarini sok; AUTH_ISSUER zorunlu"
```

---

### Task 3: Compose/Swarm dosyalari ve `zitadel-service/`

**Files:**
- Delete: `zitadel-service/` (tum klasor: `admin.pat`, `docker-compose.yaml`, `login-client.pat`)
- Modify: `docker-compose.yml`
- Modify: `docker-stack.yml`

- [ ] **Step 1: `zitadel-service/` sil**

```bash
git rm -r zitadel-service
```

- [ ] **Step 2: `docker-compose.yml`**

- `include:` blogunu (satir 1-2) sil.
- traefik `networks:` listesinden `- zitadel`'i sil (yalnizca `default` kalir; liste tek elemanli kaliyorsa `networks:` anahtarini tamamen kaldir).
- api ortamindan `GATEWAY_ENABLED=true` ve `# Zitadel OIDC` + uc `ZITADEL_*` satirini sil; yerine:
```yaml
      # Kendi OpenID Provider'imiz (zorunlu; bos ise api baslamaz).
      # Yerel Postgres ve anahtar uretimi icin bkz. core-service/.env.example.
      - AUTH_ISSUER=${AUTH_ISSUER:-}
      - AUTH_CLIENT_ID=${AUTH_CLIENT_ID:-}
      - AUTH_PORT=8001
      - AUTH_DB_URL=${AUTH_DB_URL:-}
      - AUTH_SIGNING_KEY=${AUTH_SIGNING_KEY:-}
      - AUTH_CRYPTO_KEY=${AUTH_CRYPTO_KEY:-}
```
- `api-protected` router'inin `middlewares=` label satirini tamamen sil: hem `auth-forward@file` hem `rate-limit-global@file` tanimsizdir (`traefik/dynamic/` bos) ve tanimsiz middleware Traefik'te router'i dusurur. Ustteki yorumu `# /api/* (auth ve rate limit api icinde)` yap.
- `.env.example`'da `AUTH_*` anahtarlari yoksa Task 2'nin dokundugu `.env.example`'a bir `# Kendi OpenID Provider` blogu ekle (`AUTH_ISSUER=http://localhost:8001`, `AUTH_CLIENT_ID=`, `AUTH_PORT=8001`, `AUTH_DB_URL=postgres://...`, `AUTH_SIGNING_KEY=` (PEM), `AUTH_CRYPTO_KEY=` (32 bayt)). Degerlerin bicimini `internal/config/config.go` AuthConfig ve `internal/kimlik` baslatmasindan oku.

- [ ] **Step 3: `docker-stack.yml`**

- Ustteki env aciklama yorumlarindan `ZITADEL_EXTERNAL_DOMAIN` satirini (18) ve yalnizca Zitadel icin olan `POSTGRES_*` yorumlarini (15-17; Postgres'i baska servis kullanmiyorsa) sil.
- api: `GATEWAY_ENABLED` (39), `ZITADEL_*` (52-54), secrets listesindeki `zitadel_client_id` (65) sil; Task 3 Step 2'deki `AUTH_*` ortamini Swarm sozdizimiyle ekle (`AUTH_ISSUER: ${AUTH_ISSUER}` vb.).
- ForwardAuth middleware label'lari (92-95) sil; `ms-api-protected` middleware listesinden `ms-auth-forward@docker`'i cikar (114).
- `zitadel` ve `zitadel-login` servislerini (183-317), `zitadel_data` volume'unu (332), secret yorumunu (340) ve `zitadel_masterkey`, `zitadel_db_password`, `zitadel_client_id`, `postgres_password` secret tanimlarini (355-361; `postgres_password`'u baska servis kullaniyorsa birak) sil.

- [ ] **Step 4: Dogrula**

Run:
```bash
docker compose -f docker-compose.yml config -q && echo compose-ok
docker compose -f docker-stack.yml config -q 2>&1 | head -5
grep -n -i "zitadel\|gateway_enabled\|auth-forward" docker-compose.yml docker-stack.yml
```
Expected: `compose-ok`; stack config hatasizdir ya da yalnizca Swarm'a ozgu `deploy:` uyarilari verir; grep bos.

- [ ] **Step 5: Commit**

```bash
git add -A docker-compose.yml docker-stack.yml zitadel-service core-service/.env.example
git commit -m "chore(compose): zitadel servisini, ForwardAuth ve gateway girdilerini sok"
```

---

### Task 4: Dokumanlar

**Files:**
- Modify: `CLAUDE.md`, `README.md`, `core-service/docs/API.md`

- [ ] **Step 1: `CLAUDE.md`**

- Satir 13 notunu: `Notes: Zitadel was removed in Phase 5; authentication is the custom OpenID Provider in core-service (internal/kimlik/).`
- `middleware/` maddesinde `auth.go` aciklamasi: `auth.go` (RS256 access tokens from `internal/kimlik` — the only auth path); `ratelimit.go` maddesine: limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute.
- `auth/` maddesinden `JWKSVerifier` cumlesini sil.
- External services tablosunda Custom OpenID Provider satirinin fallback hucresi: `None — AUTH_ISSUER is required; the service refuses to start without it`.
- Auth bolumunde "When `AUTH_ISSUER` is configured:" basligini "`AUTH_ISSUER` is required." yap; "When `AUTH_ISSUER` is empty: Falls back to legacy HMAC..." paragrafini sil.

- [ ] **Step 2: `README.md`**

- Satir 3: `Monorepo with two services: Go API backend (with its own OpenID Provider) and Python audio processing.`
- Satir 24 `# JWT auth, rate limiting` → `# OP access-token auth, rate limiting`.
- Satir 32 `zitadel-service/` satirini agac gorunumunden sil.
- Dosyada baska Zitadel/legacy anisi varsa (`grep -n -i "zitadel\|legacy\|gateway" README.md`) ayni sekilde guncelle.

- [ ] **Step 3: `core-service/docs/API.md`**

Auth bolumunu (16-37) tek yolu anlatacak sekilde yeniden yaz: Bearer RS256 access token, issuer = `AUTH_ISSUER`, audience `AUTH_CLIENT_ID` icermeli, `exp` ve `sub` zorunlu, ID token reddedilir, claim'ler `userId`/`email`/`name`. Zitadel JWKS ve Legacy HMAC maddeleri silinir; `/auth/verify` geciyorsa silinir. Rate limit tablosu varsa `POST /api/ws/ticket` 60/dk ve `POST /api/files/sign` 60/dk ekle.

- [ ] **Step 4: Dogrula ve commit**

Run: `grep -rn -i "zitadel\|legacy hmac\|GATEWAY_ENABLED\|auth/verify" CLAUDE.md README.md core-service/docs/API.md`
Expected: yalnizca Step 1'deki "Zitadel was removed in Phase 5" notu.

```bash
git add CLAUDE.md README.md core-service/docs/API.md
git commit -m "docs: Zitadel ve legacy auth anlatimini kaldir, yeni rate limitleri ekle"
```

---

### Task 5: k3s-gitops — Zitadel sokumu (CONTROLLER, kullanici onayi ile)

Bu gorev `~/Documents/k3s-gitops` reposunda calisir ve kumeden PVC (Zitadel verisi), Deployment ve secret'lari kalici olarak siler. Subagent'a verilmez. Backend surumu (Task 7) ile ayni gitops commit'inde ya da hemen ardindan yapilabilir; kod ile bagimliligi yok (backend `ZITADEL_*`/`GATEWAY_ENABLED` ortam degiskenlerini zaten okumuyor olacak, okumadigi env zararsizdir).

**Files (k3s-gitops):**
- Delete: `apps/makesinger/makesinger-zitadel.yaml`, `apps/makesinger/zitadel-masterkey.sops.yaml`, `apps/makesinger/zitadel-db-kimlik.sops.yaml`, `apps/kiracilar/zitadel-db-kimlik.sops.yaml`, `apps/kiracilar/makesinger-db.yaml`
- Modify: `apps/makesinger/kustomization.yaml`, `apps/kiracilar/kustomization.yaml`, `apps/makesinger/makesinger-backend.yaml`, `apps/kiracilar/makesinger-auth-db.yaml` (baslik), `infrastructure/policy/quotas.yaml`, `apps/kiracilar/postgres.yaml`, `apps/kiracilar/yedek-yetkisi.yaml`

Spec sapmasi (ledger'a yazilacak): spec `makesinger-db.yaml`'in devralinmasini istiyordu; Faz 1 bunun yerine ayri `makesinger-auth-db.yaml` olusturdu (gerekcesi o dosyanin basliginda: CNPG CR kimligi = `metadata.name`, yeniden adlandirma = sil+olustur). Bu yuzden Faz 5'te `makesinger-db.yaml` SILINIR; `retain` politikasi nedeniyle `zitadel` DB/rolu Task 6'da elle DROP edilir.

- [ ] **Step 1:** `git pull --rebase`; yukaridaki 5 dosyayi `git rm`.
- [ ] **Step 2:** `apps/makesinger/kustomization.yaml`'dan `zitadel-db-kimlik.sops.yaml`, `zitadel-masterkey.sops.yaml`, `makesinger-zitadel.yaml` satirlarini; `apps/kiracilar/kustomization.yaml`'dan `makesinger-db.yaml`, `zitadel-db-kimlik.sops.yaml` satirlarini ve "zitadel ayagi YUKARIDA oldugu gibi kalir -- Faz 5 onu sokecek" yorumunu sil (yerine: `# Faz 5 (2026-10-11): zitadel ayagi sokuldu; tek auth veritabani bu.`).
- [ ] **Step 3:** `makesinger-backend.yaml`: baslik yorumundaki `ZITADEL_CLIENT_ID BILINCLI OLARAK BOS...` paragrafini (7-12) ve "api Postgres KULLANMIYOR" cumlesini guncel duruma cevir (`api, kendi OP'u icin pg-kiracilar'daki makesinger_auth veritabanini kullanir (AUTH_DB_URL).`); `GATEWAY_ENABLED` env'ini ve ustundeki yorumu, `ZITADEL_DOMAIN/ISSUER/CLIENT_ID` env'lerini ve ustundeki yorumu sil.
- [ ] **Step 4:** `makesinger-auth-db.yaml` basligindaki "zitadel ayagi makesinger-db.yaml'da OLDUGU GIBI BIRAKILDI; Faz 5 o dosyayi silecek." cumlesini `Faz 5 (2026-10-11) makesinger-db.yaml'i sildi; zitadel rol/DB'si retain nedeniyle elle DROP edildi.` yap ("Ayni desen: ... makesinger-db.yaml" referansini `bicay-db.yaml` ile sinirla).
- [ ] **Step 5:** `quotas.yaml:267-279`: makesinger requests/limits toplamlarindan zitadel (250m/512Mi; 1000m/2Gi) ve zitadel-login (50m/128Mi; 300m/512Mi) cikarilir → requests `560m / 932Mi`, limits `2500m / 3776Mi`; "alti pod" → "dort pod"; PVC cumlesi: `PVC: kullanilmiyor (Faz 5'te zitadel-data silindi).` Quota spec degerlerini (cpu/memory/pvc tavanlari) DEGISTIRME — yalnizca yorum; `persistentvolumeclaims` tavani varsa oldugu gibi kalir (kucuk pay zarar vermez). Tam sayilari yeniden toplayarak dogrula (app 10m/100Mi + api 250m/256Mi + audio 250m/512Mi + redis 50m/64Mi; limitler 300m/512Mi + 1000m/1Gi + 1000m/2Gi + 200m/256Mi).
- [ ] **Step 6:** `postgres.yaml:12,74` ve `yedek-yetkisi.yaml:19` yorumlarinda `zitadel` → `makesinger_auth` (12'deki "zitadel 16" olcum notu tarihsel; `(zitadel 16 MB, Faz 5'te silindi)` seklinde isaretle).
- [ ] **Step 7:** Dogrula: `kustomize build apps/makesinger >/dev/null && kustomize build apps/kiracilar >/dev/null && echo ok` (yoksa `kubectl kustomize`); `grep -rn -i zitadel apps infrastructure --include='*.yaml' | grep -v decrypted` → yalnizca tarihsel notlar.
- [ ] **Step 8 (KULLANICI ONAYI):** Commit `chore(makesinger): Faz 5 -- Zitadel sokumu` + trailer; `git pull --rebase && git push`; `flux reconcile kustomization flux-system --with-source --context k3s-mba && flux reconcile kustomization apps --context k3s-mba`.
- [ ] **Step 9:** Dogrula: `kubectl --context k3s-mba -n makesinger get deploy,svc,pvc,secret | grep -i zitadel` bos; `kubectl --context k3s-mba -n kiracilar get databaserole,database | grep -i zitadel` bos; `curl -s https://makesinger-api.celalettindemir.dev/health` 200 ve `kimlik: true`.

---

### Task 6: Postgres'te `zitadel` DB/rol temizligi (CONTROLLER, kullanici onayi ile)

`databaseReclaimPolicy: retain` / `databaseRoleReclaimPolicy: retain` nedeniyle CR silinse de veritabani ve rol kalir.

- [ ] **Step 1:** Primary pod'u bul: `kubectl --context k3s-mba -n kiracilar get pods -l cnpg.io/cluster=pg-kiracilar,cnpg.io/instanceRole=primary -o name`
- [ ] **Step 2:** Bagli oturum yok mu bak: `kubectl --context k3s-mba -n kiracilar exec <pod> -c postgres -- psql -U postgres -tAc "select count(*) from pg_stat_activity where datname='zitadel'"` → `0`.
- [ ] **Step 3 (KULLANICI ONAYI):** `... psql -U postgres -c 'DROP DATABASE zitadel' && ... psql -U postgres -c 'DROP ROLE zitadel'`
- [ ] **Step 4:** `... psql -U postgres -tAc "select datname from pg_database where datname='zitadel'"` bos; `makesinger_auth` DB'si duruyor.

---

### Task 7: Backend surumu (CONTROLLER, kullanici onayi ile)

- [ ] **Step 1:** main'de `cd core-service && go test ./internal/... && golangci-lint ...` temiz.
- [ ] **Step 2 (KULLANICI ONAYI):** `git push origin main && git tag v1.6.0 && git push origin v1.6.0`; CI (Build, Lint, Docker) yesil.
- [ ] **Step 3:** `docker buildx imagetools inspect celal258/make-singer-backend:1.6.0` → digest; Task 5 commit'iyle birlikte (veya ayri commit) `makesinger-backend.yaml` digest'i guncellenir, push, reconcile, `kubectl --context k3s-mba -n makesinger rollout status deploy/makesinger-api`.
- [ ] **Step 4:** Canli kontrol: `/health` 200; `POST /api/ws/ticket` ve `POST /api/files/sign` jetonsuz 401; `/auth/verify` 418 (rota yok).
