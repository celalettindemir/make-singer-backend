# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Structure

Monorepo with two services plus reverse proxy configuration:

- **core-service/** — Go API backend (Fiber v2 + Asynq workers). Includes `internal/kimlik/` for custom OpenID Provider and `internal/sosyal/` for Keşfet (social).
- **audio-service/** — Python audio processing (FFmpeg-based mastering, Keşfet waveform and preview via `/analiz`)
- **traefik/** — Reverse proxy configuration

Notes: Zitadel was removed in Phase 5; authentication is the custom OpenID Provider in core-service (internal/kimlik/).

## Build & Run Commands

```bash
# Build core-service
cd core-service && go build ./cmd/server

# Run core-service (requires Redis on localhost:6379)
cd core-service && go run ./cmd/server

# Run with Docker (includes all services)
docker-compose up -d

# Health check
curl http://localhost:8000/health

# Regenerate Swagger docs
cd core-service && swag init -g cmd/server/main.go
```

Testler: `cd core-service && go test ./internal/...` (birim testler, ag gerekmez).
`./e2e/...` canli Redis ve gecerli Groq/R2 anahtarlari ister; CI onu calistirmaz.
Ucretli gercek Suno testi (`TestRenderFullPipeline_RealSuno`) yalnizca `SUNO_E2E_ONAY=1` ile kosar; istek `stemMode` gondermedigi icin varsayilan `mix` ile 12 kredi harcar.
audio-service: `cd audio-service && python3 -m pytest tests/ -v`.
Standard Go tooling (`go fmt`, `go vet`) is used for formatting and linting.

## Architecture

Go 1.25 API service using the **Fiber v2** web framework. Data storage uses **Redis** (job queue, session cache, rate limits) and **PostgreSQL** (user accounts, password hashes, refresh tokens maintained by the OpenID Provider). Long-running work is processed asynchronously via **Asynq** (Redis-backed task queue).

### Request flow

```
HTTP Request → Fiber middleware (auth, rate-limit) → Handler → Service → Client (external API) → Response
                                                                  ↓
                                                         Asynq task queue → Worker → WebSocket Hub → Client
```

### Key layers (`core-service/internal/`)

- **handler/** — Fiber route handlers. One file per domain: `lyrics`, `render`, `master`, `export`, `upload`, `hesap` (`DELETE /api/account`), `files` (`POST /api/files/sign`), `sosyal*` (`/api/social/*`, routes bound by `SosyalHandler.Rotalar`).
- **service/** — Business logic. Mirrors handler structure. Services enqueue Asynq jobs for render/master operations.
- **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate`, then by stemMode: none / `split_stem_advanced` / `split_stem`; stems downloaded and uploaded to R2 as `stems/{userId}/{projectId}/{stemId}.mp3` via `client.KullaniciAnahtari`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline), `hesap_worker.go` (delayed R2 cleanup `hesap:temizle` after account deletion), `sosyal_worker.go` (`sosyal:hazirla`: copies a publication's master and stems to `sosyal/{yayinId}/`, asks audio-service `/analiz` for 64 peaks and a 30 s preview, sets `yayinda`; `sosyal:dosya-sil`: deletes `sosyal/{yayinId}/` 24 h after a publication is deleted). Render and master workers refuse paid calls and R2 writes for deleted owners (`hesapisaret.Denetci`).
- **client/** — HTTP clients for external services: Groq (AI lyrics), Suno (music generation), R2 (Cloudflare S3-compatible storage), audio-service (local mastering).
- **model/** — Request/response structs and enums. All enum values defined in `enums.go`.
- **middleware/** — `auth.go` (RS256 access tokens from `internal/kimlik` — the only auth path), `ratelimit.go` (Redis fixed window per user; limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute), `silinmis.go` (`SilinmisKullaniciEngeli`: deleted-user gate on `/api`, 401 for deleted accounts, 503 + `Retry-After: 5` when Redis is unreachable).
- **sosyal/** — Keşfet (spec: `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md`). Own Postgres pool on `AUTH_DB_URL`, own schema `sosyal` and migration table `sosyal.schema_migrations` (embedded `migrations/*.sql`, advisory lock `0x736F7379616C31`, run at startup, additive only). No FK to the kimlik `users` table. `Servis` holds the rules (username, publish validation, detail visibility via `DetayGorebilir`), `PostgresDepo` the SQL (list visibility via `gorunurlukKosulu`; a live test proves the two agree). Errors map to lowercase contract codes in `handler/sosyal.go` (`bulunamadi`, `profil_yok`, `dogrulama`, `sahiplik`, `stem_yok`, `durum`, ...); social validation failures are 422, not 400. Lists are cursor-paged (`imlec`, `limit` 1-50, `{ogeler, sonrakiImlec}`).
- **hesapisaret/** — Deleted-account marker shared by middleware, `HesapService` and workers: `Anahtar(userID)` (`kimlik:silindi:<userId>`), `Omur` (24 h), `Denetci` interface and `RedisDenetci` (EXISTS).
- **auth/** — `jwks_verifier.go`: token verifiers. `YukariAkisDogrulayici` validates **upstream** (Apple/Google) `id_token`s for the federation flow — RS256 pinned, `iss`/`aud`/`exp` enforced, `nonce` required and compared in constant time.
- **websocket/** — Hub for broadcasting real-time job progress to clients via `GET /ws/jobs/:jobId?ticket=` (ticket from `POST /api/ws/ticket`).
- **config/** — Viper-based config loading from `config.yaml` with env var overrides.

### External services

| Service | Purpose | Fallback when unconfigured |
|---------|---------|---------------------------|
| Groq | AI lyrics generation | Mock data |
| Suno (`api.sunoapi.org`, model `SUNO_MODEL`, default `V6`) | Instrumental rendering. Per-render stem level chosen by the user via `stemMode`: `mix` (default, 12 credits: the generated track as one `mix` stem), `focus` (32: `split_stem_advanced` on `focusInstrument` → that instrument + `rest`), `stems` (62: `split_stem`, 10 instrument stems kept; vocals and woodwinds skipped) | Simulated render steps |
| Cloudflare R2 | File storage | Operates without it |
| Custom OpenID Provider (`internal/kimlik/`) | RS256 access token authentication, user sign-up/sign-in | None — `AUTH_ISSUER` is required; the service refuses to start without it |
| audio-service | Audio mastering | N/A (separate container) |

### Configuration

Config priority: environment variables > `config.yaml` > Viper defaults. See `core-service/.env.example` for all available env vars. Env var names match config keys with underscores (e.g., `GROQ_API_KEY` for `groq.api_key`).

### Job lifecycle

Jobs are stored in Redis as JSON with key `job:{jobId}` and 24-hour TTL. States: `queued` → `running` → `succeeded` / `failed` / `canceled`. Progress updates (0-100%) are pushed to WebSocket subscribers. Asynq runs four queues: `render` (weight 6), `master` (weight 4), `sosyal` (weight 2, publication preparation) and `bakim` (weight 1, delayed account and publication file cleanup).
Each job record carries `ownerId` (the `sub` of the user who started it). `status`, `result` and `cancel` load the job through `sahipliJobOku` (`internal/service/job_kayit.go`) and answer **404** — not 403 — when the caller is not the owner; records without `ownerId` are visible to nobody.

### API response conventions

Success responses return domain-specific JSON. Errors use a consistent envelope:

```json
{"error": {"code": "ERROR_CODE", "message": "...", "details": {}}}
```

Standard error codes: `VALIDATION_ERROR` (400), `UNAUTHORIZED` (401), `FORBIDDEN` (403), `NOT_FOUND` (404), `RATE_LIMITED` (429), `SERVICE_ERROR` (500), `AI_ERROR` (502). Response helpers are in `core-service/pkg/response/`.

### Auth

Bearer token required on all `/api/*` routes. 

**`AUTH_ISSUER` is required.** The middleware validates RS256 access tokens issued by the custom OpenID Provider (`internal/kimlik/`). The public key is read from the provider process (no network call to a separate JWKS endpoint), avoiding circular startup dependencies. Token validation enforces:
- Algorithm: RS256 only (no HMAC or "none")
- Issuer (`iss`): exact match to `AUTH_ISSUER`
- Audience (`aud`): must contain the `AUTH_CLIENT_ID` value
- Expiration: required (`exp` claim present and valid)
- Subject (`sub`): non-empty
- Token type: must be an access token (has `jti` claim, no `azp` or `at_hash`). ID tokens are rejected.

If `AUTH_ISSUER` is empty or the OpenID Provider fails to start, the service exits with a fatal error (fail-closed design).

**Account deletion (`DELETE /api/account`):** `service.HesapService.Sil` validates the id, deletes the user via `kimlik.Sunucu.HesapSil` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`hesapisaret.Omur` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.KullaniciOnekleri`), then the Keşfet data through the optional `SosyalTemizleyici` (every publication id, soft-deleted included, gets `sosyal/{id}/` deleted; the rows are deleted in one transaction only if all those prefixes succeeded, so the retry can list the ids again; other users' covers stay and show the source as deleted), and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.SilinmisKullaniciEngeli` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `hesapisaret.Denetci`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.

Extracted claims (`userId`, `email`, `name`) are stored in Fiber context locals.

**Ownership (Phase 3):** R2 keys are prefixed with the owner: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3`, `exports/<userId>/<exportId>.<ext>`, `previews/<userId>/<id>.mp3`. Keys are only built with `client.KullaniciAnahtari`, which rejects any client-supplied segment containing `/` or `..`. Every URL a client sends back (master stems/vocals, export inputs) is resolved with `client.SahipliAnahtarCoz`; a key outside the caller's prefix answers 404. `DELETE /api/upload/vocal/:takeId` requires `?projectId=&sectionId=`. `POST /api/files/sign` re-signs stored URLs (1-50, same order) after resolving each with `client.SahipliAnahtarCoz`; a single foreign key fails the whole request with 404 and nothing is signed. Publication copies live under `sosyal/{yayinId}/` (`client.SosyalAnahtari`); they belong to a publication, not a user prefix, so `files/sign` never signs them and only `POST /api/social/yayinlar/{id}/dosyalar` hands out a signed listen URL after the visibility check. Publishing accepts `masterUrl` and stem `url` as a full URL or a bare key (`client.SahipliAnahtarVeyaURLCoz`); a foreign key is 403 `sahiplik`.

**WebSocket:** `GET /ws/jobs/:jobId` requires `?ticket=` from `POST /api/ws/ticket` (body `{"jobId"}`, owner only). Tickets live 30 s in Redis (`wsbilet:<ticket>`) and are consumed with `GETDEL` before the upgrade.

**Federation (Phase 2):** The hosted login page offers Apple and Google. Buttons and endpoints appear **only** for providers that are actually configured (`AUTH_FED_GOOGLE_CLIENT_ID` / `AUTH_FED_APPLE_CLIENT_ID`); with none configured, `/federasyon/*` and `/baglama` are not bound at all (404). Upstream URLs are constants in the code — only credentials come from the environment. The provider acts as an RP: the upstream `id_token` is verified against the provider's JWKS (`internal/auth/jwks_verifier.go`, RS256 pinned, `nonce` required) and the user is located by `(provider, sub)` in the `federe_kimlikler` table. Email is never used to link accounts automatically — on conflict the user is asked for the existing account's password once (`/baglama`), or told to use the original provider (409) when that account has no password. Apple private relay addresses are never trusted for linking. The federation callback uses a dedicated `SameSite=None` cookie because Apple returns via a cross-site `form_post`. Each federation endpoint has its own Redis rate-limit counter so federation traffic cannot drain the password-attempt budget of `/giris`. Counter name suffixes under the `kimlik:hizlimit:` prefix: `federasyon-baslat` (GET start), `federasyon-geri` (callback), `baglama-get` (GET `/baglama` — `SayfaGetSar` appends `-get`), `baglama` (POST `/baglama`, per IP) and `baglama-hesap` (POST `/baglama`, per target account). Mobile carries no Apple/Google SDK and is unaware of this flow.
