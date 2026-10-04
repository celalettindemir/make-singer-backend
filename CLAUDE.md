# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Structure

Monorepo with three services:

- **core-service/** — Go API backend (Fiber v2 + Asynq workers). Includes `internal/kimlik/` for custom OpenID Provider.
- **audio-service/** — Python audio processing (FFmpeg-based mastering)
- **traefik/** — Reverse proxy configuration

Notes: Zitadel service was replaced in Phase 1 with a custom OpenID Provider implemented directly in core-service.

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

- **handler/** — Fiber route handlers. One file per domain: `lyrics`, `render`, `master`, `export`, `upload`.
- **service/** — Business logic. Mirrors handler structure. Services enqueue Asynq jobs for render/master operations.
- **worker/** — Asynq job processors: `render_worker.go` (Suno API + stem splitting), `master_worker.go` (audio mastering pipeline).
- **client/** — HTTP clients for external services: Groq (AI lyrics), Suno (music generation), R2 (Cloudflare S3-compatible storage), audio-service (local mastering).
- **model/** — Request/response structs and enums. All enum values defined in `enums.go`.
- **middleware/** — `auth.go` (RS256 tokens from `internal/kimlik`, or legacy HMAC in development), `ratelimit.go` (Redis fixed window per user).
- **websocket/** — Hub for broadcasting real-time job progress to clients via `GET /ws/jobs/:jobId`.
- **config/** — Viper-based config loading from `config.yaml` with env var overrides.

### External services

| Service | Purpose | Fallback when unconfigured |
|---------|---------|---------------------------|
| Groq | AI lyrics generation | Mock data |
| Suno | Music rendering + stem splitting | Simulated render steps |
| Cloudflare R2 | File storage | Operates without it |
| Custom OpenID Provider (`internal/kimlik/`) | RS256 access token authentication, user sign-up/sign-in | Legacy HMAC JWT (development only) |
| audio-service | Audio mastering | N/A (separate container) |

### Configuration

Config priority: environment variables > `config.yaml` > Viper defaults. See `core-service/.env.example` for all available env vars. Env var names match config keys with underscores (e.g., `GROQ_API_KEY` for `groq.api_key`).

### Job lifecycle

Jobs are stored in Redis as JSON with key `job:{jobId}` and 24-hour TTL. States: `queued` → `running` → `succeeded` / `failed` / `canceled`. Progress updates (0-100%) are pushed to WebSocket subscribers. Asynq runs two queues: `render` (concurrency 6) and `master` (concurrency 4).

### API response conventions

Success responses return domain-specific JSON. Errors use a consistent envelope:

```json
{"error": {"code": "ERROR_CODE", "message": "...", "details": {}}}
```

Standard error codes: `VALIDATION_ERROR` (400), `UNAUTHORIZED` (401), `FORBIDDEN` (403), `NOT_FOUND` (404), `RATE_LIMITED` (429), `SERVICE_ERROR` (500), `AI_ERROR` (502). Response helpers are in `core-service/pkg/response/`.

### Auth

Bearer token required on all `/api/*` routes. 

**When `AUTH_ISSUER` is configured:** The middleware validates RS256 access tokens issued by the custom OpenID Provider (`internal/kimlik/`). The public key is read from the provider process (no network call to a separate JWKS endpoint), avoiding circular startup dependencies. Token validation enforces:
- Algorithm: RS256 only (no HMAC or "none")
- Issuer (`iss`): exact match to `AUTH_ISSUER`
- Audience (`aud`): must contain the `AUTH_CLIENT_ID` value
- Expiration: required (`exp` claim present and valid)
- Subject (`sub`): non-empty
- Token type: must be an access token (has `jti` claim, no `azp` or `at_hash`). ID tokens are rejected.

If `AUTH_ISSUER` is set but the OpenID Provider fails to start, the service exits with a fatal error (fail-closed design).

**When `AUTH_ISSUER` is empty:** Falls back to legacy HMAC JWT verification (development only). The gateway mode can also bypass token verification using `X-User-*` headers when `GATEWAY_ENABLED=true`.

Extracted claims (`userId`, `email`, `name`) are stored in Fiber context locals.
