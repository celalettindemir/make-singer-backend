# Make Singer Backend

Monorepo with three services: Go API backend, Python audio processing, and Zitadel identity provider.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.22+ | core-service build |
| Docker & Docker Compose | latest | Container orchestration |
| Python | 3.11+ | audio-service (local dev only) |
| Redis | 7+ | Data store & task queue |
| FFmpeg | latest | Audio processing |

## Project Structure

```
make-singer-backend/
├── core-service/          # Go API backend (Fiber v2 + Asynq workers)
│   ├── cmd/server/        # Entry point, router setup
│   ├── internal/
│   │   ├── config/        # Viper config management
│   │   ├── handler/       # HTTP handlers (lyrics, render, master, export, upload)
│   │   ├── middleware/     # JWT auth, rate limiting
│   │   ├── model/         # Request/response structs
│   │   ├── service/       # Business logic
│   │   ├── worker/        # Asynq job processors (render, master)
│   │   ├── client/        # HTTP clients (Groq, Suno, R2, audio-service)
│   │   └── websocket/     # WebSocket broadcast hub
│   └── pkg/response/      # Standardized API responses
├── audio-service/         # Python FFmpeg-based audio mastering
├── zitadel-service/       # Zitadel identity provider config
├── traefik/               # Reverse proxy configuration
├── docker-compose.yml     # Full stack orchestration
└── docker-stack.yml       # Production stack
```

## Build

### core-service (Go)

```bash
cd core-service
go build ./cmd/server
```

Binary `server` oluşturulur. Swagger doku güncellemek için:

```bash
cd core-service
swag init -g cmd/server/main.go
```

### audio-service (Python)

Docker ile build:

```bash
docker build -t make-singer-audio ./audio-service
```

Lokal geliştirme:

```bash
cd audio-service
pip install -r requirements.txt
uvicorn main:app --host 0.0.0.0 --port 8084
```

### Docker ile tum servisleri build etme

```bash
docker-compose build
```

## Run

### Lokal (sadece core-service, Redis gerekli)

```bash
# Redis'i baslatir
docker run -d --name redis -p 6379:6379 redis:7-alpine

# core-service calistirir
cd core-service
go run ./cmd/server
```

### Docker Compose (tum servisler)

```bash
docker-compose up -d
```

## Health Check

```bash
curl http://localhost:8000/health
```

## Configuration

Config onceligi: environment variables > `config.yaml` > Viper defaults.

Tum env degiskenleri icin: `core-service/.env.example`

Ornek: `GROQ_API_KEY` -> `groq.api_key`

## API Test

```bash
# Lyrics olustur
curl -X POST http://localhost:8000/api/lyrics/generate \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"genre":"pop","sectionType":"verse","vibes":["happy"]}'
```

Swagger UI: `http://localhost:8000/swagger/index.html`
