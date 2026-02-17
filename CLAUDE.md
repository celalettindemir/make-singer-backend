# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Structure

Monorepo with three services:

- **core-service/** — Go API backend (Fiber v2 + Asynq workers)
- **audio-service/** — Python audio processing (FFmpeg-based mastering)
- **zitadel-service/** — Zitadel identity provider (Docker Compose config)
- **traefik/** — Reverse proxy configuration

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

There is no test suite — no `_test.go` files exist. Standard Go tooling (`go fmt`, `go vet`) is used for formatting and linting.

## Architecture

Go 1.22 API service using the **Fiber v2** web framework. Redis is the only data store (no SQL database). Long-running work is processed asynchronously via **Asynq** (Redis-backed task queue).

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
- **middleware/** — `auth.go` (dual JWT: Zitadel JWKS or legacy HMAC), `ratelimit.go` (Redis sliding window per user).
- **websocket/** — Hub for broadcasting real-time job progress to clients via `GET /ws/jobs/:jobId`.
- **config/** — Viper-based config loading from `config.yaml` with env var overrides.

### External services

| Service | Purpose | Fallback when unconfigured |
|---------|---------|---------------------------|
| Groq | AI lyrics generation | Mock data |
| Suno | Music rendering + stem splitting | Simulated render steps |
| Cloudflare R2 | File storage | Operates without it |
| Zitadel | OIDC authentication | Legacy HMAC JWT |
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

Bearer token required on all `/api/*` routes. The middleware tries Zitadel JWKS verification first, then falls back to legacy HMAC if configured. Extracted claims (`userId`, `email`, `name`) are stored in Fiber context locals.
