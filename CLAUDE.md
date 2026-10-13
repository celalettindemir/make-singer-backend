# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Structure

Monorepo with two services plus reverse proxy configuration:

- **core-service/** — Go API backend (Fiber v2 + Asynq workers). Includes `internal/identity/` for custom OpenID Provider and `internal/social/` for Keşfet (social).
- **audio-service/** — Python audio processing (FFmpeg-based mastering, Keşfet waveform and preview via `/analiz`)
- **traefik/** — Reverse proxy configuration

Notes: Zitadel was removed in Phase 5; authentication is the custom OpenID Provider in core-service (internal/identity/).

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

- **handler/** — Fiber route handlers. One file per domain: `lyrics`, `render`, `master`, `export`, `upload`, `account` (`DELETE /api/account`), `files` (`POST /api/files/sign`), `social*` (`/api/social/*`, routes bound by `SocialHandler.Routes`).
- **service/** — Business logic. Mirrors handler structure. Services enqueue Asynq jobs for render/master operations.
- **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate`, then by stemMode: none / `split_stem_advanced` / `split_stem`; stems downloaded and uploaded to R2 as `stems/{userId}/{projectId}/{stemId}.mp3` via `client.UserKey`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline), `account_worker.go` (delayed R2 cleanup `hesap:temizle` after account deletion), `social_worker.go` (`sosyal:hazirla`: copies a publication's master and stems to `sosyal/{yayinId}/`, asks audio-service `/analiz` for 64 peaks and a 30 s preview, sets `yayinda`; `sosyal:dosya-sil`: deletes `sosyal/{yayinId}/` 24 h after a publication is deleted; `sosyal:bayat-tara`: fails publications stuck in `hazirlaniyor` for longer than `SOCIAL_STALE_PREPARATION_AFTER` (default 1h, measured from the `hazirlik_baslangic` column that `ResetForRetry` refreshes) so the owner can retry; scheduled by an `asynq.Scheduler` started next to the worker server in `cmd/server/main.go` every `SOCIAL_STALE_SWEEP_INTERVAL` (default 15m, `<= 0` disables) on the `sosyal` queue with `MaxRetry(0)` and `Unique`, so API replicas do not pile up duplicates). Render and master workers refuse paid calls and R2 writes for deleted owners (`deletionmark.Checker`).
- **client/** — HTTP clients for external services: Groq (AI lyrics), Suno (music generation), R2 (Cloudflare S3-compatible storage), audio-service (local mastering).
- **model/** — Request/response structs and enums. All enum values defined in `enums.go`.
- **middleware/** — `auth.go` (RS256 access tokens from `internal/identity` — the only auth path), `ratelimit.go` (Redis fixed window per user; limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute, Keşfet publish/cover per hour and comments per minute; `LimitWithCode` answers 429 with a custom code, e.g. `yavasla` for comments; `LimitIP` limits unauthenticated routes per client IP from Traefik's `X-Real-Ip` (falls back to `c.IP()`, Fiber's `ProxyHeader` is not set), replaced by `CF-Connecting-IP` only when that peer address is inside `ratelimit.cloudflare_ranges` (`RATELIMIT_CLOUDFLARE_RANGES`; default = Cloudflare's published ranges in `internal/clientip`; empty disables; invalid entry = startup error), because behind Cloudflare the peer is the shared edge IP and the header is forgeable from any other peer; the OP listener's login/signup limits apply the same rule on the address chosen from `X-Forwarded-For` under `ratelimit:<prefix>:ip:<ip>` (IPv4-mapped addresses as IPv4, IPv6 per /64), and moves requests carrying the configured `X-Web-Anahtari` (constant-time compare) to the shared `ratelimit:<prefix>:web` counter; if such a request also has a parseable `X-Ziyaretci-Ip` (the visitor IP sent by make-singer-frontend, trusted only together with the valid key) it must first fit the per-visitor bucket `ratelimit:<prefix>:web:ip:<ipKey(visitor)>` (`RATELIMIT_PUBLIC_WEB_VISITOR_PER_MIN`, default 30, `<=0` disables) and then the shared one), `deleted_user.go` (`DeletedUserGate`: deleted-user gate on `/api`, 401 for deleted accounts, 503 + `Retry-After: 5` when Redis is unreachable).
- **social/** — Keşfet (spec: `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md`). Own Postgres pool on `AUTH_DB_URL`, own schema `sosyal` and migration table `sosyal.schema_migrations` (embedded `migrations/*.sql`, advisory lock `0x736F7379616C31`, run at startup, additive only). No FK to the identity `users` table. `Service` holds the rules (username, publish validation, detail visibility via `CanSeeDetail`), `PostgresStore` the SQL (list visibility via `visibilityCondition`; a live test proves the two agree). Errors map to lowercase contract codes in `handler/social.go` (`bulunamadi`, `profil_yok`, `dogrulama`, `sahiplik`, `stem_yok`, `durum`, `kendini_takip`, `yorum_kapali`, `yetki`, `altyapi_kapali`, ...; unmapped errors are 500 `sunucu_hatasi`); social validation failures are 422, not 400. Lists are cursor-paged (`imlec`, `limit` 1-50, `{ogeler, sonrakiImlec}`); comments use a two-group cursor (timed by `zaman_ms` ascending, then untimed by `olusturma` descending). Comments, reports and comment deletion are soft or idempotent as the contract requires (a repeat report is 201 without a new row). `cover-baslat` copies the publication's `stem_listesi` server-side to `stems/{caller}/cover-{sourcePublicationID}/{stemId}.{ext}` (`client.Copier`; one deterministic folder per caller and source, repeat starts overwrite the same objects because source stems are immutable) so `/api/files/sign` can re-sign them. `GET /api/public/s/{slug}` is the only unauthenticated Keşfet route (`SocialHandler.PublicRoutes`, registered before the `/api` auth group): `herkes`/`link` + `yayinda` only, `onizlemeUrl` signed for 1 h, `Cache-Control: no-store`. Migration `002_hazirlik_baslangic.sql` adds `yayin.hazirlik_baslangic` (set on insert by default and by `ResetForRetry`) plus a partial index on `hazirlaniyor` rows; `PostgresStore.MarkStalePreparations` (the `StaleSweeper` interface) moves non-deleted rows stuck in `hazirlaniyor` longer than the threshold to `hata`.
- **deletionmark/** — Deleted-account marker shared by middleware, `AccountService` and workers: `Key(userID)` (`kimlik:silindi:<userId>`), `TTL` (24 h), `Checker` interface and `RedisChecker` (EXISTS).
- **auth/** — `jwks_verifier.go`: token verifiers. `UpstreamVerifier` validates **upstream** (Apple/Google) `id_token`s for the federation flow — RS256 pinned, `iss`/`aud`/`exp` enforced, `nonce` required and compared in constant time.
- **websocket/** — Hub for broadcasting real-time job progress to clients via `GET /ws/jobs/:jobId?ticket=` (ticket from `POST /api/ws/ticket`).
- **config/** — Viper-based config loading from `config.yaml` with env var overrides.

### External services

| Service | Purpose | Fallback when unconfigured |
|---------|---------|---------------------------|
| Groq | AI lyrics generation | Mock data |
| Suno (`api.sunoapi.org`, model `SUNO_MODEL`, default `V6`) | Instrumental rendering. Per-render stem level chosen by the user via `stemMode`: `mix` (default, 12 credits: the generated track as one `mix` stem), `focus` (32: `split_stem_advanced` on `focusInstrument` → that instrument + `rest`), `stems` (62: `split_stem`, 10 instrument stems kept; vocals and woodwinds skipped) | Simulated render steps |
| Cloudflare R2 | File storage | Operates without it |
| Custom OpenID Provider (`internal/identity/`) | RS256 access token authentication, user sign-up/sign-in | None — `AUTH_ISSUER` is required; the service refuses to start without it |
| audio-service | Audio mastering | N/A (separate container) |

### Configuration

Config priority: environment variables > `config.yaml` > Viper defaults. See `core-service/.env.example` for all available env vars. Env var names match config keys with underscores (e.g., `GROQ_API_KEY` for `groq.api_key`). Secrets can also come from `<NAME>_FILE` (Docker secrets, `readSecret`), including `SOSYAL_WEB_ANAHTARI`.

### Job lifecycle

Jobs are stored in Redis as JSON with key `job:{jobId}` and 24-hour TTL. States: `queued` → `running` → `succeeded` / `failed` / `canceled`. Progress updates (0-100%) are pushed to WebSocket subscribers. Asynq runs four queues: `render` (weight 6), `master` (weight 4), `sosyal` (weight 2, publication preparation) and `bakim` (weight 1, delayed account and publication file cleanup).
Each job record carries `ownerId` (the `sub` of the user who started it). `status`, `result` and `cancel` load the job through `readOwnedJob` (`internal/service/job_record.go`) and answer **404** — not 403 — when the caller is not the owner; records without `ownerId` are visible to nobody.

### API response conventions

Success responses return domain-specific JSON. Errors use a consistent envelope:

```json
{"error": {"code": "ERROR_CODE", "message": "...", "details": {}}}
```

Standard error codes: `VALIDATION_ERROR` (400), `UNAUTHORIZED` (401), `FORBIDDEN` (403), `NOT_FOUND` (404), `RATE_LIMITED` (429), `SERVICE_ERROR` (500), `AI_ERROR` (502). Response helpers are in `core-service/pkg/response/`.

### Naming and messages

- Go identifiers, file names, comments, log and error texts and test names are English (`TestType_Behavior`). Package names: `identity` (custom OpenID Provider), `social` (Keşfet), `deletionmark` (deleted-account marker).
- The wire contract keeps its Turkish values: routes, JSON/query/form keys, error codes, DB schema and migration file names, Redis keys, asynq task and queue names, R2 key layouts, env and config keys, cookie and header names. `go test ./tools/wirecheck` fails on any change to them; an intended change is recorded with `-update` and reviewed.
- API error `message`s come from `internal/i18n` (TR default, EN by `Accept-Language`); never pass free text to `pkg/response`. Codes do not change with the language.

### Auth

Bearer token required on all `/api/*` routes, except `GET /api/public/s/{slug}` (Keşfet web preview; per-IP limit `RATELIMIT_PUBLIC_PER_MIN`, web server bypass via `X-Web-Anahtari` = `SOSYAL_WEB_ANAHTARI` with the shared `RATELIMIT_PUBLIC_WEB_PER_MIN` plus the per-visitor `RATELIMIT_PUBLIC_WEB_VISITOR_PER_MIN` keyed on `X-Ziyaretci-Ip`; empty key disables the bypass). 

**`AUTH_ISSUER` is required.** The middleware validates RS256 access tokens issued by the custom OpenID Provider (`internal/identity/`). The public key is read from the provider process (no network call to a separate JWKS endpoint), avoiding circular startup dependencies. Token validation enforces:
- Algorithm: RS256 only (no HMAC or "none")
- Issuer (`iss`): exact match to `AUTH_ISSUER`
- Audience (`aud`): must contain the `AUTH_CLIENT_ID` value
- Expiration: required (`exp` claim present and valid)
- Subject (`sub`): non-empty
- Token type: must be an access token (has `jti` claim, no `azp` or `at_hash`). ID tokens are rejected.

If `AUTH_ISSUER` is empty or the OpenID Provider fails to start, the service exits with a fatal error (fail-closed design).

**Account deletion (`DELETE /api/account`):** `service.AccountService.Delete` validates the id, deletes the user via `identity.Server.DeleteAccount` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`deletionmark.TTL` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.UserPrefixes`), then the Keşfet data through the optional `SocialCleaner` (first `DeleteUserTraces` deletes the profile, likes, comments, follows, reports filed and reports on the user's comments in one transaction, so the user and their publications vanish from every read path at once even if R2 is down; its error is joined but does not stop the next steps; then every publication id, soft-deleted included, gets `sosyal/{id}/` deleted; the `yayin` rows are deleted by `DeleteUserData` only if all those prefixes succeeded, so the retry can list the ids again; other users' covers stay and show the source as deleted; cover stem copies live under `stems/<userId>/` and go with `UserPrefixes`), and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.DeletedUserGate` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `deletionmark.Checker`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.

Extracted claims (`userId`, `email`, `name`) are stored in Fiber context locals.

**Ownership (Phase 3):** R2 keys are prefixed with the owner: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3`, `exports/<userId>/<exportId>.<ext>`, `previews/<userId>/<id>.mp3`. Keys are only built with `client.UserKey`, which rejects any client-supplied segment containing `/` or `..`. Every URL a client sends back (master stems/vocals, export inputs) is resolved with `client.ResolveOwnedKey`; a key outside the caller's prefix answers 404. `DELETE /api/upload/vocal/:takeId` requires `?projectId=&sectionId=`. `POST /api/files/sign` re-signs stored URLs (1-50, same order) after resolving each with `client.ResolveOwnedKey`; a single foreign key fails the whole request with 404 and nothing is signed. Publication copies live under `sosyal/{yayinId}/` (`client.SocialKey`); they belong to a publication, not a user prefix, so `files/sign` never signs them and only `POST /api/social/yayinlar/{id}/dosyalar` hands out a signed listen URL after the visibility check. Publishing accepts `masterUrl` and stem `url` as a full URL or a bare key (`client.ResolveOwnedKeyOrURL`); a foreign key is 403 `sahiplik`. `POST /api/social/yayinlar/{id}/cover-baslat` is the only path from `sosyal/` back into a user prefix: it copies the stems to `stems/<callerId>/cover-<sourcePublicationId>/` (idempotent per caller and source) and returns ordinary user URLs.

**WebSocket:** `GET /ws/jobs/:jobId` requires `?ticket=` from `POST /api/ws/ticket` (body `{"jobId"}`, owner only). Tickets live 30 s in Redis (`wsbilet:<ticket>`) and are consumed with `GETDEL` before the upgrade.

**Federation (Phase 2):** The hosted login page offers Apple and Google. Buttons and endpoints appear **only** for providers that are actually configured (`AUTH_FED_GOOGLE_CLIENT_ID` / `AUTH_FED_APPLE_CLIENT_ID`); with none configured, `/federasyon/*` and `/baglama` are not bound at all (404). Upstream URLs are constants in the code — only credentials come from the environment. The provider acts as an RP: the upstream `id_token` is verified against the provider's JWKS (`internal/auth/jwks_verifier.go`, RS256 pinned, `nonce` required) and the user is located by `(provider, sub)` in the `federe_kimlikler` table. Email is never used to link accounts automatically — on conflict the user is asked for the existing account's password once (`/baglama`), or told to use the original provider (409) when that account has no password. Apple private relay addresses are never trusted for linking. The federation callback uses a dedicated `SameSite=None` cookie because Apple returns via a cross-site `form_post`. Each federation endpoint has its own Redis rate-limit counter so federation traffic cannot drain the password-attempt budget of `/giris`. Counter name suffixes under the `kimlik:hizlimit:` prefix: `federasyon-baslat` (GET start), `federasyon-geri` (callback), `baglama-get` (GET `/baglama` — `WrapPageGet` appends `-get`), `baglama` (POST `/baglama`, per IP) and `baglama-hesap` (POST `/baglama`, per target account). Mobile carries no Apple/Google SDK and is unaware of this flow.
