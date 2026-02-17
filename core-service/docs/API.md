# Make-Singer API Documentation

## Overview

| Property | Value |
|---|---|
| **Framework** | Go Fiber |
| **Content-Type** | `application/json` (unless noted) |
| **Body Limit** | 50 MB |
| **CORS** | All origins (`*`), methods `GET,POST,PUT,DELETE,OPTIONS` |

All `/api/*` endpoints require authentication. Public endpoints (`/`, `/health`) and the WebSocket endpoint (`/ws/*`) do not.

---

## Authentication

All `/api/*` routes are protected by Bearer token authentication.

```
Authorization: Bearer <token>
```

The server supports two verification strategies, tried in order:

1. **Zitadel JWKS** -- RS256 tokens verified against a remote JWKS endpoint. Claims extracted: `userId`, `email`, `name`.
2. **Legacy HMAC** -- HS256 tokens signed with a shared secret. Claims extracted: `userId`, `email`.

If both are configured, JWKS is attempted first; on failure the server falls back to HMAC. If only one is configured, that method is used exclusively.

### Error responses

| Condition | Code | Body `error.code` |
|---|---|---|
| Missing `Authorization` header | `401` | `UNAUTHORIZED` |
| Malformed header (not `Bearer <token>`) | `401` | `UNAUTHORIZED` |
| Invalid / expired token | `401` | `UNAUTHORIZED` |

---

## Rate Limiting

Rate limits are enforced per authenticated user via Redis. When a rate limit is exceeded the server returns `429 Too Many Requests`.

### Response headers

| Header | Description |
|---|---|
| `X-RateLimit-Limit` | Maximum requests allowed in the window |
| `X-RateLimit-Remaining` | Requests remaining in the current window |
| `Retry-After` | Seconds until the window resets (only on `429`) |

### Per-group limits (defaults from config)

| Endpoint Group | Key Prefix | Window |
|---|---|---|
| `/api/lyrics/*` | `lyrics` | per minute |
| `/api/render/start` | `render` | per hour |
| `/api/master/*` | `master` | per hour |
| `/api/export/*` | `export` | per hour |
| `/api/upload/*` | `upload` | per hour |

Exact numeric limits are set via server configuration (`RateLimit.LyricsPerMin`, `RateLimit.RenderPerHour`, etc.).

### 429 response

```json
{
  "error": {
    "code": "RATE_LIMITED",
    "message": "Rate limit exceeded"
  }
}
```

---

## Error Response Format

All errors share the same envelope:

```json
{
  "error": {
    "code": "<ERROR_CODE>",
    "message": "<human-readable message>",
    "details": {}
  }
}
```

`details` is included only when additional context is available (e.g. validation errors).

### Error Codes

| Code | HTTP Status | Description |
|---|---|---|
| `VALIDATION_ERROR` | `400` | Request body or parameter failed validation |
| `UNAUTHORIZED` | `401` | Missing or invalid authentication |
| `FORBIDDEN` | `403` | Authenticated but not permitted |
| `NOT_FOUND` | `404` | Resource not found |
| `RATE_LIMITED` | `429` | Rate limit exceeded |
| `JOB_FAILED` | varies | Background job failed |
| `SERVICE_ERROR` | `500` | Internal server error |
| `AI_ERROR` | `502` | Upstream AI service error |

### Validation error details

When validation fails, `details` is an object mapping field names to the violated constraint tag:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Validation failed",
    "details": {
      "Genre": "required",
      "Vibes": "min"
    }
  }
}
```

---

## Public Endpoints

### `GET /`

Returns the current server timestamp.

**Response** `200`

```json
{
  "timestamp": 1706500000
}
```

### `GET /health`

Returns service health status.

**Response** `200`

```json
{
  "status": "ok",
  "services": {
    "groq": true,
    "suno": true,
    "r2": true,
    "audio": true,
    "auth": true
  }
}
```

Each service value is `true` if configured and available, `false` otherwise.

---

## Lyrics Endpoints

Rate limit group: `lyrics` (per minute).

### `POST /api/lyrics/generate`

Generate lyrics for a song section using AI.

**Request body**

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `genre` | string | yes | One of [Genre](#genre) values | Musical genre |
| `sectionType` | string | yes | One of [SectionType](#sectiontype) values | Song section type |
| `vibes` | string[] | yes | 1--3 items, each non-empty | Mood descriptors |
| `language` | string | no | One of [Language](#language) values | Output language (default: `en`) |

```json
{
  "genre": "pop",
  "sectionType": "chorus",
  "vibes": ["upbeat", "hopeful"],
  "language": "en"
}
```

**Response** `200`

```json
{
  "drafts": [
    ["Line 1", "Line 2", "Line 3"],
    ["Alt line 1", "Alt line 2", "Alt line 3"]
  ]
}
```

### `POST /api/lyrics/rewrite`

Rewrite existing lyrics for a song section.

**Request body**

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `currentLyrics` | string | yes | min length 1 | Existing lyrics to rewrite |
| `genre` | string | yes | One of [Genre](#genre) values | Musical genre |
| `sectionType` | string | yes | One of [SectionType](#sectiontype) values | Song section type |
| `vibes` | string[] | yes | 1--3 items, each non-empty | Mood descriptors |
| `instructions` | string | no | max 500 chars | Rewrite instructions |

```json
{
  "currentLyrics": "Old lyrics here...",
  "genre": "rock",
  "sectionType": "verse",
  "vibes": ["dark", "moody"],
  "instructions": "Make it more aggressive"
}
```

**Response** `200`

```json
{
  "lines": ["Rewritten line 1", "Rewritten line 2"]
}
```

---

## Render Endpoints

### `POST /api/render/start`

Start a new render job. Rate limit group: `render` (per hour). Only the `/start` endpoint is rate-limited in this group.

**Request body**

| Field | Type | Required | Description |
|---|---|---|---|
| `projectId` | string | yes | UUID of the project |
| `brief` | [Brief](#brief) | yes | Song parameters |
| `arrangement` | [Arrangement](#arrangement) | yes | Arrangement settings |

#### Brief

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `genre` | string | yes | One of [Genre](#genre) values | Musical genre |
| `vibes` | string[] | yes | 1--5 items | Mood descriptors |
| `bpm` | [BPMConfig](#bpmconfig) | yes | | BPM settings |
| `key` | [KeyConfig](#keyconfig) | yes | | Key settings |
| `structure` | [SongSection](#songsection)[] | yes | min 1 item | Song structure |

#### BPMConfig

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `mode` | string | yes | `auto`, `range`, or `fixed` | BPM selection mode |
| `value` | int | no | 40--220 | Fixed BPM value (when mode is `fixed`) |
| `min` | int | no | 40--220 | Range minimum (when mode is `range`) |
| `max` | int | no | 40--220 | Range maximum (when mode is `range`) |

#### KeyConfig

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `mode` | string | yes | `auto` or `manual` | Key selection mode |
| `tonic` | string | no | One of [Tonic](#tonic) values | Root note (when mode is `manual`) |
| `scale` | string | no | `major` or `minor` | Scale type (when mode is `manual`) |

#### SongSection

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `id` | string | yes | | Section identifier |
| `type` | string | yes | One of [SectionType](#sectiontype) values | Section type |
| `bars` | int | yes | 1--64 | Number of bars |

#### Arrangement

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `instruments` | string[] | yes | min 1, each one of [Instrument](#instrument) values | Instruments to use |
| `density` | string | yes | One of [Density](#density) values | Arrangement density |
| `groove` | string | yes | One of [Groove](#groove) values | Groove type |
| `sectionEmphasis` | [SectionEmphasis](#sectionemphasis)[] | no | | Per-section emphasis |

#### SectionEmphasis

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `sectionId` | string | yes | | References a `SongSection.id` |
| `emphasis` | string | yes | `bigger` or `biggest` | Emphasis level |

**Response** `202`

```json
{
  "jobId": "uuid",
  "status": "queued",
  "estimatedDuration": 120,
  "createdAt": "2025-01-30T12:00:00Z"
}
```

### `GET /api/render/status/:jobId`

Get the current status of a render job.

**Path parameters**

| Param | Type | Description |
|---|---|---|
| `jobId` | string | Job identifier |

**Response** `200`

```json
{
  "jobId": "uuid",
  "status": "running",
  "progress": 45,
  "currentStep": "Generating drums",
  "error": null,
  "createdAt": "2025-01-30T12:00:00Z",
  "startedAt": "2025-01-30T12:00:01Z",
  "completedAt": null,
  "retryCount": 0
}
```

**Errors**

| Status | Code | Condition |
|---|---|---|
| `400` | `VALIDATION_ERROR` | Missing `jobId` |
| `404` | `NOT_FOUND` | Job does not exist |

### `GET /api/render/result/:jobId`

Get the result of a completed render job.

**Path parameters**

| Param | Type | Description |
|---|---|---|
| `jobId` | string | Job identifier |

**Response** `200`

```json
{
  "id": "uuid",
  "bpm": 120,
  "duration": 180.5,
  "key": {
    "tonic": "C",
    "scale": "major"
  },
  "createdAt": "2025-01-30T12:00:00Z",
  "stems": [
    {
      "id": "stem-uuid",
      "instrument": "drums",
      "fileUrl": "https://storage.example.com/stems/drums.wav",
      "duration": 180.5,
      "waveformData": [0.1, 0.5, 0.3]
    }
  ]
}
```

**Errors**

| Status | Code | Condition |
|---|---|---|
| `400` | `VALIDATION_ERROR` | Missing `jobId` or job not completed yet |
| `404` | `NOT_FOUND` | Job does not exist |

### `POST /api/render/cancel/:jobId`

Cancel a running render job.

**Path parameters**

| Param | Type | Description |
|---|---|---|
| `jobId` | string | Job identifier |

**Response** `200`

```json
{
  "success": true,
  "jobId": "uuid",
  "status": "canceled"
}
```

**Errors**

| Status | Code | Condition |
|---|---|---|
| `400` | `VALIDATION_ERROR` | Missing `jobId` or job already completed |
| `404` | `NOT_FOUND` | Job does not exist |

---

## Master Endpoints

Rate limit group: `master` (per hour).

### `POST /api/master/preview`

Generate a preview of the mastered audio.

**Request body**

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `projectId` | string | yes | UUID | Project identifier |
| `profile` | string | yes | One of [MasterProfile](#masterprofile) values | Mastering profile |
| `stemUrls` | string[] | yes | min 1, each valid URL | Stem file URLs |
| `mixSnapshot` | [MixSnapshot](#mixsnapshot) | yes | | Current mix state |
| `previewStartTime` | int | no | min 0 | Start time in seconds for preview |

#### MixSnapshot

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `channels` | [MixChannel](#mixchannel)[] | yes | min 1 | Channel settings |
| `preset` | string | yes | One of [MixPreset](#mixpreset) values | Mix preset |

#### MixChannel

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `stemId` | string | yes | | Stem identifier |
| `volumeDb` | float | no | -60 to 12 | Volume in dB |
| `mute` | bool | no | | Mute channel |
| `solo` | bool | no | | Solo channel |

**Response** `200`

```json
{
  "fileUrl": "https://storage.example.com/previews/master-preview.wav",
  "duration": 30,
  "expiresAt": "2025-01-30T13:00:00Z"
}
```

### `POST /api/master/final`

Start a final mastering job.

**Request body**

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `projectId` | string | yes | UUID | Project identifier |
| `profile` | string | yes | One of [MasterProfile](#masterprofile) values | Mastering profile |
| `stemUrls` | string[] | yes | min 1, each valid URL | Stem file URLs |
| `mixSnapshot` | [MixSnapshot](#mixsnapshot) | yes | | Current mix state |
| `vocalTakes` | [VocalTake](#vocaltake)[] | no | | Vocal takes to include |

#### VocalTake

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `sectionId` | string | yes | | Section identifier |
| `takeId` | string | yes | | Take identifier |
| `fileUrl` | string | yes | Valid URL | Vocal file URL |
| `offsetMs` | int | no | | Timing offset in milliseconds |

**Response** `202`

```json
{
  "jobId": "uuid",
  "status": "queued",
  "estimatedDuration": 60
}
```

### `GET /api/master/status/:jobId`

Get the current status of a mastering job.

**Path parameters**

| Param | Type | Description |
|---|---|---|
| `jobId` | string | Job identifier |

**Response** `200`

```json
{
  "jobId": "uuid",
  "status": "running",
  "progress": 70,
  "currentStep": "Applying EQ"
}
```

**Errors**

| Status | Code | Condition |
|---|---|---|
| `400` | `VALIDATION_ERROR` | Missing `jobId` |
| `404` | `NOT_FOUND` | Job does not exist |

### `GET /api/master/result/:jobId`

Get the result of a completed mastering job.

**Path parameters**

| Param | Type | Description |
|---|---|---|
| `jobId` | string | Job identifier |

**Response** `200`

```json
{
  "fileUrl": "https://storage.example.com/masters/final.wav",
  "duration": 210.5,
  "profile": "warm",
  "peakDb": -0.3,
  "lufs": -14,
  "expiresAt": "2025-01-31T12:00:00Z"
}
```

**Errors**

| Status | Code | Condition |
|---|---|---|
| `400` | `VALIDATION_ERROR` | Missing `jobId` or job not completed yet |
| `404` | `NOT_FOUND` | Job does not exist |

---

## Export Endpoints

Rate limit group: `export` (per hour).

### `POST /api/export/mp3`

Export the mastered audio as MP3.

**Request body**

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `projectId` | string | yes | UUID | Project identifier |
| `masterFileUrl` | string | yes | Valid URL | Mastered file URL |
| `quality` | int | no | `128`, `192`, `256`, or `320` | Bitrate in kbps |
| `metadata` | [ExportMetadata](#exportmetadata) | no | | ID3 tag metadata |

#### ExportMetadata

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `title` | string | no | max 200 chars | Track title |
| `artist` | string | no | max 200 chars | Artist name |
| `album` | string | no | max 200 chars | Album name |
| `year` | int | no | 1900--2100 | Release year |
| `credits` | string | no | max 1000 chars | Credits text |

**Response** `200`

```json
{
  "fileUrl": "https://storage.example.com/exports/song.mp3",
  "size": 5242880,
  "format": "mp3",
  "quality": 320,
  "expiresAt": "2025-01-31T12:00:00Z"
}
```

### `POST /api/export/wav`

Export the mastered audio as WAV.

**Request body**

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `projectId` | string | yes | UUID | Project identifier |
| `masterFileUrl` | string | yes | Valid URL | Mastered file URL |
| `bitDepth` | int | no | `16`, `24`, or `32` | Bit depth |
| `sampleRate` | int | no | `44100`, `48000`, or `96000` | Sample rate in Hz |

**Response** `200`

```json
{
  "fileUrl": "https://storage.example.com/exports/song.wav",
  "size": 52428800,
  "format": "wav",
  "bitDepth": 24,
  "sampleRate": 48000,
  "expiresAt": "2025-01-31T12:00:00Z"
}
```

### `POST /api/export/stems`

Export individual stems as a bundle.

**Request body**

| Field | Type | Required | Validation | Description |
|---|---|---|---|---|
| `projectId` | string | yes | UUID | Project identifier |
| `stemUrls` | string[] | yes | min 1, each valid URL | Stem file URLs |
| `format` | string | no | `wav` or `mp3` | Output format |
| `includeVocals` | bool | no | | Include vocal tracks |
| `vocalUrls` | string[] | no | Each valid URL | Vocal file URLs |
| `includeMaster` | bool | no | | Include master file |
| `masterUrl` | string | no | Valid URL | Master file URL |

**Response** `200`

```json
{
  "fileUrl": "https://storage.example.com/exports/stems.zip",
  "size": 104857600,
  "fileCount": 8,
  "expiresAt": "2025-01-31T12:00:00Z"
}
```

---

## Upload Endpoints

Rate limit group: `upload` (per hour).

### `POST /api/upload/vocal`

Upload a vocal recording. Uses `multipart/form-data` (not JSON).

**Form fields**

| Field | Type | Required | Description |
|---|---|---|---|
| `projectId` | string | yes | Project identifier |
| `sectionId` | string | yes | Section identifier |
| `takeName` | string | no | Human-readable take name |
| `file` | file | yes | Audio file (max 50 MB) |

**Accepted content types**: `audio/wav`, `audio/x-wav`, `audio/wave`, `audio/mpeg`, `audio/mp3`, `audio/mp4`, `audio/x-m4a`, `audio/aac`, `audio/x-aac`

**Response** `201`

```json
{
  "id": "take-uuid",
  "fileUrl": "https://storage.example.com/vocals/take.wav",
  "duration": 32.5,
  "sampleRate": 44100,
  "channels": 1,
  "createdAt": "2025-01-30T12:00:00Z"
}
```

**Errors**

| Status | Code | Condition |
|---|---|---|
| `400` | `VALIDATION_ERROR` | Missing `projectId`, `sectionId`, or `file` |
| `400` | `VALIDATION_ERROR` | File exceeds 50 MB |
| `400` | `VALIDATION_ERROR` | Unsupported audio format |

### `DELETE /api/upload/vocal/:takeId`

Delete a previously uploaded vocal take.

**Path parameters**

| Param | Type | Description |
|---|---|---|
| `takeId` | string | Take identifier |

**Response** `204 No Content`

No response body.

**Errors**

| Status | Code | Condition |
|---|---|---|
| `400` | `VALIDATION_ERROR` | Missing `takeId` |

---

## WebSocket

### `GET /ws/jobs/:jobId`

Subscribe to real-time updates for a background job (render or master). The connection must be a WebSocket upgrade request.

**Path parameters**

| Param | Type | Description |
|---|---|---|
| `jobId` | string | Job identifier to subscribe to |

**Note:** If the request is not a WebSocket upgrade, the server responds with `426 Upgrade Required`.

### Server-to-client messages

#### Progress

Sent periodically as the job progresses.

```json
{
  "type": "progress",
  "jobId": "uuid",
  "progress": 45,
  "status": "running",
  "currentStep": "Generating drums"
}
```

#### Complete

Sent when the job finishes successfully. `result` contains the full job result object.

```json
{
  "type": "complete",
  "jobId": "uuid",
  "result": { }
}
```

#### Error

Sent when the job fails.

```json
{
  "type": "error",
  "jobId": "uuid",
  "error": {
    "code": "SERVICE_ERROR",
    "message": "Render failed: timeout"
  }
}
```

### Client-to-server messages

#### Ping

Clients can send a ping; the server responds with a pong.

```json
{ "type": "ping" }
```

Server response:

```json
{ "type": "pong" }
```

### Keep-alive

The server sends a WebSocket-level ping frame every 30 seconds. Clients should respond with a pong frame (most WebSocket libraries handle this automatically).

---

## Enums & Validation

### Genre

```
pop | rock | hiphop | rnb | electronic | jazz | country | folk | classical | latin | reggae | blues
```

### SectionType

```
intro | verse | prechorus | chorus | bridge | outro | instrumental
```

### Instrument

```
drums | bass | piano | guitar | synth | strings | brass | woodwinds | percussion | pads | lead | fx
```

### JobStatus

```
queued | running | succeeded | failed | canceled
```

### BPMMode

```
auto | range | fixed
```

### KeyMode

```
auto | manual
```

### Tonic

```
C | C# | D | D# | E | F | F# | G | G# | A | A# | B
```

### Scale

```
major | minor
```

### Density

```
minimal | medium | full
```

### Groove

```
straight | swing | half_time
```

### MasterProfile

```
clean | warm | loud
```

### MixPreset

```
default | vocal_friendly | bass_heavy | bright | warm
```

### Language

```
en | tr | fr
```
