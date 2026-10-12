# Keşfet Backend 5a Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the Keşfet backend for mobile phase 5a: profiles, publishing (with server-side file copies, waveform and preview), detail by id and slug, PATCH, DELETE, retry, listen URL, the three feeds, profile publication lists, likes, visibility, and account-deletion cleanup.

**Architecture:** A new package `core-service/internal/sosyal` owns its own Postgres pool (same `AUTH_DB_URL` database), its own schema `sosyal` with ALL Keşfet tables, embedded migrations, a repository (`PostgresDepo`) and a service (`Servis`). Fiber handlers in `internal/handler/sosyal*.go` map service errors to the contract's lowercase codes. Publishing stores the source keys and enqueues `sosyal:hazirla`; the worker in `internal/worker/sosyal_worker.go` copies the master and stems inside the private R2 bucket to `sosyal/{yayinId}/…`, asks the audio-service `POST /analiz` for 64 peaks and a 30 s mp3 preview, then marks the publication `yayinda`.

**Tech Stack:** Go 1.25, Fiber v2, pgx v5, Asynq, AWS SDK v2 (S3 CopyObject on R2), Python 3.11 FastAPI + pydub + numpy (audio-service). No new dependencies in `go.mod` or `requirements*.txt`.

**Spec:** `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md` (§3, §4.1–4.4, §4.9, §5.1, §5.3). Where it differs from the backend copy (`docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md`), the mobile copy wins; where the spec text differs from the mobile app's parser, the parser contract (reproduced under Global Constraints) wins.

## Global Constraints

**Process**
- Every code block in this plan was compiled and tested before the plan was written. Apply each block exactly. **Create** blocks are whole files; **Modify** blocks are unified diffs (`git apply` accepts them from the repository root).
- Module `github.com/makeasinger/api`; Go commands run in `core-service/`. Plain `testing`, hand-written fakes, Turkish test names and messages; no testify.
- After every Go task: `gofmt -l .` prints nothing, `go vet ./...` and `go build ./...` are clean. golangci-lint v2.14.0 (CI version, config `core-service/.golangci.yml`) reports `0 issues` after every task.
- audio-service: `python3 -m pytest tests/`, `ruff check .`, `ruff format --check .` (ruff 0.16.9 from `requirements-dev.txt`).
- Commit messages are Turkish and end exactly with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
  ```

**Test database**
- Live Postgres DSN (local docker and CI service): `postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable`, overridable with `KIMLIK_TEST_DB_URL`. Redis: `localhost:6379`.
- Repository tests call `testHavuz(t)`: connect with a 3 s timeout (`t.Skip("yerel Postgres yok, atlaniyor")` when unreachable), run `sosyal.Migrate`, then `TRUNCATE sosyal.sikayet, sosyal.yorum, sosyal.begeni, sosyal.takip, sosyal.yayin, sosyal.profil`. Only `sosyal.*` is truncated and no sosyal table references `public.users`, so kimlik's tests (which truncate `users CASCADE` in another test process) are unaffected. All DB-backed sosyal tests live in the one `sosyal` package, so they never run concurrently with each other.

**HTTP**
- Base path `/api/social`, Bearer auth, inside the existing `api` group (deleted-user gate applies). CORS `AllowMethods` gains `PATCH`.
- Every error body is `{"error":{"code","message"}}` via `response.Error`. Social validation failures are **422 `dogrulama`**, never the validator's 400.

| Code | Status | When |
|---|---|---|
| `bulunamadi` | 404 | not visible, not found, not the owner (PATCH/DELETE/retry), malformed id/slug/username |
| `profil_yok` | 404 on `GET /profil/ben`; 409 on `POST /yayinlar` | no profile yet |
| `gecersiz_kullanici_adi` | 422 | username breaks `^[a-z0-9_.]{3,20}$` or starts/ends with `.` |
| `kullanici_adi_alinmis` | 409 | username owned by someone else |
| `dogrulama` | 422 | any other validation failure, bad body, bad `sekme`/`tur`/cursor, bad or public-bucket file URL |
| `sahiplik` | 403 | `masterUrl` or a stem `url` is in another user's prefix |
| `kaynak_bulunamadi` | 404 | cover source missing, invisible, not `yayinda`, or itself a cover |
| `altyapi_kapali` | 403 | cover source has its backing closed |
| `stem_yok` | 422 | PATCH `altyapiAcik:true` on a publication without stem copies (or on a cover) |
| `durum` | 409 | retry when not `hata`; `dosyalar` before `yayinda` |
| `sunucu_hatasi` | 500 | anything else (detail only in the log) |
| `RATE_LIMITED` | 429 | publish rate limit (`sosyal-yayin`, default 30/hour/user) |
| `UNAUTHORIZED` | 401 | no user id (existing middleware behaviour) |

**JSON contract (what the mobile Zod schemas parse)**
- `ProfilOzeti {kullaniciAdi, gorunenAd, avatarRengi: int 0..7}`; `avatarRengi = fnv32a(kullanici_id) % 8`.
- `Profil = ProfilOzeti + {bio, istatistik:{sarki, takipci, takip, cover}, takipEdiyorMuyum, benMiyim}`.
- `YayinOzeti {id, slug, baslik, sahip: ProfilOzeti, tur, bpm, ton:{tonik, mod}, sureSn, dalgaFormu: number[] (never null), begeniSayisi, yorumSayisi, coverSayisi, begendimMi, altyapiAcik, kaynak: KaynakOzeti|null (key always present), olusturma: RFC3339 UTC}`.
- `KaynakOzeti {yayinId, baslik, sahipKullaniciAdi, silindi}`.
- `YayinDetayi = YayinOzeti + {aciklama, yorumAcik, sozler:{bolumler:[{bolumId, tur, satirlar: string[]}]}, yapi:[{id, tur, olcu}], durum: hazirlaniyor|yayinda|hata, benimMi, gorunurluk (always present)}`.
- Pages: `{ogeler: [...] (never null), sonrakiImlec: string|null (key always present)}`; query `imlec`, optional `limit` (default 20, max 50).
- `POST /yayinlar` → 202 `{id, slug, durum:"hazirlaniyor"}`. `POST /yayinlar/{id}/tekrar-dene` → 202, same shape. `DELETE /yayinlar/{id}` → 204. `POST /yayinlar/{id}/dosyalar` → `{master:{url, expiresAt?}}`. `PUT|DELETE /yayinlar/{id}/begeni` → `{begendimMi, begeniSayisi}`. `GET /profil/kullanici-adi/uygun?ad=` → `{uygun, oneri: string|null}`.
- Publish body (`PublishInput`): `{baslik, aciklama, gorunurluk, altyapiAcik, yorumAcik, masterUrl, stemler:[{id, enstruman, url, sure}], sozler, yapi, tur, bpm, ton, sureSn, kaynakYayinId: string|null}`. `masterUrl` and stem `url` are a full R2/CDN URL **or** a bare key.

**Queues and tasks**
- `sosyal:hazirla`, payload `{"yayinId"}`, queue `sosyal` (weight 2), `MaxRetry(3)`, `Retention(24h)`.
- `sosyal:dosya-sil`, payload `{"yayinId"}`, queue `bakim`, `ProcessIn(24h)`, `MaxRetry(10)`.
- Server queue map becomes `{"render":6, "master":4, "sosyal":2, "bakim":1}`.

**R2 key layout (private bucket)**
- `sosyal/{yayinId}/master.{ext}`, `sosyal/{yayinId}/onizleme.mp3`, `sosyal/{yayinId}/stems/{stemId}.{ext}`; `{ext}` comes from the source key (empty if it does not match `^\.[a-z0-9]{1,5}$`).
- Built only with `client.SosyalAnahtari`; prefix `sosyal/{yayinId}/` from `client.SosyalOneki` (passes `DeletePrefix`'s 3-part rule). `sosyal` is NOT an owned type: `/api/files/sign` never signs these keys.

**audio-service `POST /analiz`**
- Request `{input_key, preview_key, peak_count=64, preview_seconds=30}` with `extra: forbid`; response `{peaks: float[peak_count] in 0..1 normalised to the loudest, duration: float, preview_url: str}`. The preview is the highest-RMS 30 s window (whole track if shorter), mp3 192k, written to `preview_key`.

---

## Rulings

These are binding for every task. R1–R10 reproduce the controller rulings (`backend-rulings.md`, 2026-10-12) that apply to 5a. N1–N16 are **NEW rulings made while writing and testing the code**; they are flagged so a reviewer can challenge them.

### Controller rulings that apply to 5a

**R1. Package, pool, schema, migrations (B1).** `internal/sosyal` holds repository, service, visibility and migrations; handlers in `internal/handler/sosyal*.go`, worker in `internal/worker/sosyal_worker.go`. Its own pool via `kimlik.Baglan(ctx, cfg.Auth.DBURL)` (kimlik's pool stays private). Schema `sosyal`, every table schema-qualified. Own embedded migrations and `sosyal.schema_migrations`, run at startup under its own advisory lock, additive and `IF NOT EXISTS` because replicas roll start-first. **No FK to `public.users`**: kimlik deletes the user first and a cascade would destroy the publication ids needed for R2 cleanup. Inside the schema, `yayin` cascades to `begeni`, `yorum`, `sikayet`; `profil` does not cascade to `yayin`. `kaynak_yayin_id` is a plain uuid; covers snapshot `kaynak_baslik` and `kaynak_sahip_adi` so `KaynakOzeti` can show last known values with `silindi=true`. All tables (including 5b–5d's) are created now.

**R2. Account deletion (B2).** `HesapService` gains an optional, nil-safe `SosyalTemizleyici` (`YayinlariListele` incl. soft-deleted; `KullaniciVerisiniSil` in one transaction). Order: list ids → `DeletePrefix("sosyal/{id}/")` each → delete rows. If R2 fails the rows stay so the 1 h retry can list the ids again (idempotent). Every `NewHesapService` caller is updated; the existing "5 prefixes" assertions stay, sosyal prefixes go through the new dependency.

**R3. R2 copy and keys (B3).** `client.Kopyalayici.Kopyala(ctx, kaynak, hedef)` on `*R2Client` via S3 CopyObject inside one bucket; different buckets → error. `client.SosyalAnahtari` validates every segment with the existing segment regex. Publishing resolves URLs with the ownership check; full URLs and bare keys both work. `ErrYabanciAnahtar` → 403 `sahiplik`; `ErrGecersizURL` → 422 `dogrulama`.

**R4. audio-service analysis (B4).** `POST /analiz` as in Global Constraints; pure functions in `services/analiz.py` with synthetic-audio unit tests; Go side `client.AudioAnalizci` (separate interface so `AudioProcessor` fakes do not break) through the existing `post` helper. **Deliberate deviation from spec §3.1:** the preview is generated for EVERY publication, so a later PATCH to `herkes`/`link` needs no regeneration; the public endpoint (5d) still serves only `herkes`/`link`.

**R5. The `sosyal:hazirla` job (B5).** Source keys live on the row in `hazirlik` jsonb. Steps: load row (missing, deleted or not `hazirlaniyor` → return nil); owner check (deleted → `SkipRetry` + `hata`); copy master, copy stems; `Analiz` (failure is non-fatal: empty waveform, no preview); set `yayinda`. Copy errors are returned so asynq retries; on the last attempt (`GetRetryCount >= GetMaxRetry`) the row becomes `hata`. `tekrar-dene`: owner only, only in `hata` → `hazirlaniyor` + re-enqueue; otherwise 404 or 409 `durum`. `DELETE`: owner only, soft delete, `sosyal:dosya-sil` on `bakim` 24 h later; a repeat is 404 (the app treats it as success).

**R6. Profile (B6).** Username `^[a-z0-9_.]{3,20}$` without leading/trailing dot; `gorunenAd` 1–40 runes after trim; `bio` ≤ 160 runes. Invalid name → 422 `gecersiz_kullanici_adi`; taken → 409 `kullanici_adi_alinmis`; other → 422 `dogrulama`. Suggestion: `ad + n`, n = 2..99, base trimmed so the result is ≤ 20 chars, first free one or null. `istatistik.sarki`/`cover`: originals/covers that are `yayinda`, not deleted, visible to the viewer; `takipci`/`takip`: counts. `takipEdiyorMuyum`, `benMiyim` as named.

**R7. Visibility (B7, spec §4.9).** One SQL predicate builder plus a pure Go function for the detail check, unit-tested on the table. Owner sees everything (incl. `hazirlaniyor`, `hata`); non-owner only `yayinda` and not deleted, and then `herkes` always, `takipciler` only when following, `link` only by id or slug, never in lists. Not visible → 404 `bulunamadi`.

**R8. Feeds and lists (B8).** Cursor = base64url JSON: time order `{t: RFC3339Nano, id}`, `senin-icin` `{p, id, n: nowUnixMilli}` with the same `n` reused on later pages. `limit` default 20, cap 50. `sonrakiImlec` always present, null on the last page. `senin-icin`: `herkes`, `yayinda`, not deleted, not the viewer's own, score `(1 + b + 2y + 3c) / (hours + 2)^1.5` over 7-day counts. `takip`: followed users' `herkes` + `takipciler`. `yeni`: `herkes`, newest first. Summary counts via correlated subqueries (`coverSayisi` counts covers that are `yayinda` and not deleted, whatever their visibility).

**R9. HTTP and errors (B9).** Lowercase contract codes via `response.Error`; PATCH in CORS; `api.Group("/social")`; publish limit `rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SosyalYayinPerHour, time.Hour)`, default 30, with config field, default, explicit `BindEnv`, `config.yaml` and `.env.example`. 409 `profil_yok` on publish without profile, 404 `profil_yok` on `GET /profil/ben`. Publish validation: `baslik` 1–80 runes, `aciklama` ≤ 300, `gorunurluk` and `ton.mod` enums, `sureSn` > 0, `bpm` 20–300, ≥ 1 stem when `altyapiAcik` and not a cover; a cover forces `altyapiAcik=false` and ignores `stemler`; `kaynakYayinId` must be visible, not itself a cover, backing open (else 404 `kaynak_bulunamadi` / 403 `altyapi_kapali`). Slug: 8 chars `[A-Za-z0-9]` from crypto/rand, retried on unique violation.

**R10. Testing and deployment (B10, B11).** Repository contract tests on live Postgres (skip if unreachable, TRUNCATE isolation), service tests with a fake repository, handler tests with `app.Test` + `kullaniciOlarak`, worker tests with fakes, pytest for `analiz`. No new stack env except the optional rate-limit key; CI builds on a `v*` tag; rollout via Portainer `API_VERSION`.

### NEW rulings made while coding (challenge these first)

**N1 (NEW). `sikayet.yayin_id` column.** A report targets a publication or a comment (`hedef_id` is polymorphic), so it cannot carry an FK. The table gets `yayin_id uuid NOT NULL REFERENCES sosyal.yayin ON DELETE CASCADE` (the publication the target belongs to). This is how R1's "yayin cascades to sikayet" is realised; 5b fills it.

**N2 (NEW). Extra columns.** `yayin.master_anahtari` (set by the worker; `dosyalar` signs it), `yayin.onizleme_anahtari` (NULL when analysis failed; 5d's public endpoint reads it), and `stem_listesi` items carry an internal `anahtar` (5d's cover start copies from it). `hazirlik.stems[]` stores `{id, enstruman, sure, key}` so the worker can build `stem_listesi` without the request.

**N3 (NEW). Reopening the backing.** The worker copies every stem in `hazirlik`, regardless of the current `altyapi_acik` (the owner may PATCH it to false while `hazirlaniyor`). PATCH `altyapiAcik:true` is allowed only on an original whose `hazirlik` has stems; otherwise 422 `stem_yok` (covers always get `stem_yok`). A publication published with the backing closed ignores `stemler` entirely (not validated, not copied) and can never reopen it.

**N4 (NEW). Soft-deleted publications are invisible to everyone, owner included.** Detail, PATCH, retry and `dosyalar` answer 404 once `silinme` is set. This matches "a repeat DELETE is 404" and avoids an owner-only zombie state.

**N5 (NEW). Owner lists vs. counters.** Profile publication lists apply R7 literally: the owner also sees their own `hazirlaniyor`/`hata` items (so they can retry or delete them), while `istatistik` counts only `yayinda` (R6 literal). Feeds always require `yayinda`.

**N6 (NEW). Public-bucket sources are rejected at publish.** R3 makes copies same-bucket only, so a `masterUrl`/stem in `exports/` (public bucket) is answered with 422 `dogrulama` at publish time instead of failing three times in the worker.

**N7 (NEW). Bare-key resolution.** `client.SahipliAnahtarVeyaURLCoz`: a string containing `://` goes through `SahipliAnahtarCoz`; otherwise it must look like `<owned type>/<userId>/…` (else `ErrGecersizURL` → 422) and pass `AnahtarSahibiDogrula` (else `ErrYabanciAnahtar` → 403 `sahiplik`). Note the deliberate difference from `/api/files/sign`, which maps a foreign key to 404.

**N8 (NEW). Queue failures.** If `sosyal:hazirla` cannot be enqueued at publish, the just-inserted row is hard-deleted (`YayinKaldir`) and 500 `sunucu_hatasi` is returned, so no orphan `hazirlaniyor` row stays behind. On `tekrar-dene` the row is put back to `hata`. On `DELETE` the enqueue error is only logged (account deletion still removes the prefix).

**N9 (NEW). Validation beyond the spec.** `ton.tonik` 1–16 runes, `tur` 1–40, `enstruman` 1–40, stem ids `^[A-Za-z0-9_-]{1,64}$` and unique (they become key segments), stem `sure` > 0 (the cover contract requires positive stem durations later), at most 32 stems, 200 `yapi` items, 200 `bolumler`, 200 `satirlar` per section; `yapi` ids non-empty, `olcu` ≥ 0; `masterUrl` required; `kaynakYayinId: ""` means "original". `baslik`, `aciklama`, `gorunenAd`, `bio` are trimmed; the username is neither trimmed nor lower-cased (strict, the app already enforces the pattern). Null `satirlar`/`bolumler`/`yapi` are normalised to `[]` so the app never receives null arrays.

**N10 (NEW). Response details.** 500 uses the lowercase code `sunucu_hatasi`; an unparsable body is 422 `dogrulama`; a malformed username in `/profiller/{ad}` is 404 `bulunamadi`; `tur` empty means `sarkilar`; `tekrar-dene` returns 202 `{id, slug, durum:"hazirlaniyor"}`; `dosyalar` omits `expiresAt` when the URL is permanent (mock/dev); `dosyalar` before `yayinda` is 409 `durum`.

**N11 (NEW). Likes.** Allowed on any publication the viewer can see by detail rules; no profile required; the response is re-read from the database after the write.

**N12 (NEW). Stable `senin-icin` pages.** Publications created after the reference time `n` are excluded, and only interactions in `(n − 7 days, n]` are counted, so every page computed with the same `n` sees the same scores; ties break by id descending. Cursor ids are validated as uuids (a bad cursor is 422, never a SQL error).

**N13 (NEW). Worker hardening.** The owner check runs before EVERY R2 write (each copy and the analysis), as the render worker does before every upload. The analysis result is accepted only with exactly 64 peaks; values are clamped to 0..1 and NaN becomes 0; anything else means an empty waveform and no preview. R2 or audio not configured → the step is skipped (dev). Last-attempt detection is injectable (`sonDeneme`) and false outside an asynq context. `sosyal:dosya-sil` refuses to delete the prefix of a live (not soft-deleted) row.

**N14 (NEW). Migrate takes its own lock.** `sosyal.Migrate` acquires one connection, takes `pg_advisory_lock(0x736F7379616C31)` ("sosyal1", distinct from kimlik's key), creates schema `sosyal` and `sosyal.schema_migrations`, then applies files in name order, each in its own transaction. A failure at startup is fatal, like kimlik.

**N15 (NEW). Visibility split.** The repository returns detail rows including soft-deleted ones (with `Silindi`) and `TakipEdiyor`; the service applies `DetayGorebilir`. Lists and counters use the SQL predicate `gorunurlukKosulu`. A live test checks that SQL and Go agree on all 18 publication variants × 3 viewers × 2 contexts.

**N16 (NEW). Python details.** Preview mp3 bitrate 192k; peaks rounded to 4 decimals; `peak_count` 1–1024 and `preview_seconds` 1–120 bounds in the request model.

---

## File Structure

| File | Responsibility |
|---|---|
| `core-service/internal/sosyal/migrations/001_sosyal.sql` | All Keşfet tables and indexes (schema `sosyal`) |
| `core-service/internal/sosyal/veritabani.go` | Embedded migrations, `Migrate` under its own advisory lock |
| `core-service/internal/sosyal/hatalar.go` | Service errors (`ErrBulunamadi`, …) and `dogrulamaHatasi` |
| `core-service/internal/sosyal/model.go` | Profile types (JSON contract + repository records) |
| `core-service/internal/sosyal/model_yayin.go` | Publication types (contract, request, PATCH, hazirlik, records) |
| `core-service/internal/sosyal/kurallar.go` | Username rule, avatar colour, suggestions, profile input rules, uuid parsing |
| `core-service/internal/sosyal/yayin_kurallari.go` | Publish and PATCH validation |
| `core-service/internal/sosyal/gorunurluk.go` | §4.9 rule in Go and as a SQL predicate |
| `core-service/internal/sosyal/slug.go` | Slug generation and validation |
| `core-service/internal/sosyal/anahtarlar.go` | Copy target keys (master, stem, preview) |
| `core-service/internal/sosyal/imlec.go` | Page envelope, `limit`, cursors |
| `core-service/internal/sosyal/depo.go` | `Depo` interface, `PostgresDepo`, profile SQL |
| `core-service/internal/sosyal/depo_yayin.go` | Publication SQL (detail, insert, PATCH, delete, retry, hazirlik) |
| `core-service/internal/sosyal/depo_akis.go` | Feeds, profile lists, likes |
| `core-service/internal/sosyal/depo_temizlik.go` | Account-deletion listing and row deletion |
| `core-service/internal/sosyal/servis.go` | `Servis`, constructor, profile use cases |
| `core-service/internal/sosyal/servis_yayin.go` | Publish, detail, PATCH, DELETE, retry, listen URL, task types |
| `core-service/internal/sosyal/servis_akis.go` | Feeds, profile lists, likes |
| `core-service/internal/client/sosyal_anahtar.go` | `SosyalAnahtari`, `SosyalOneki`, bare-key-or-URL resolver |
| `core-service/internal/client/kopya.go` | `Kopyalayici`, `R2Client.Kopyala` |
| `core-service/internal/client/audio_analiz.go` | `AudioAnalizci`, `AudioClient.Analiz` |
| `core-service/internal/handler/sosyal.go` | `SosyalServisi` interface, routes, error mapping |
| `core-service/internal/handler/sosyal_profil.go`, `sosyal_yayin.go`, `sosyal_akis.go` | Endpoint handlers |
| `core-service/internal/worker/sosyal_worker.go` | `sosyal:hazirla`, `sosyal:dosya-sil` |
| `core-service/internal/service/hesap_service.go` | Account deletion extended with `SosyalTemizleyici` |
| `core-service/internal/config/config.go`, `config.yaml`, `.env.example` | `ratelimit.sosyal_yayin_per_hour` |
| `core-service/cmd/server/main.go` | Pool, migration, service, routes, worker, queue, CORS |
| `audio-service/services/analiz.py`, `audio-service/main.py` | `/analiz` |
| `CLAUDE.md` | Module documentation |

Test files sit next to the code they test (`*_test.go`, `tests/test_analiz.py`). `sosyal/depo_yardimci_test.go` holds raw-SQL fixtures, `sosyal/sahte_depo_test.go` the fake repository and fake queue, `handler/sosyal_test.go` the fake service and request helpers.

---

## Tasks

### Task 1: Schema `sosyal`, migrations and the test database helper

Creates the `sosyal` package with the embedded migration that builds ALL Keşfet tables (R1, N1, N2, N14) and the `testHavuz` helper every repository test uses.

**Files:**

- Create: `core-service/internal/sosyal/veritabani_test.go`
- Create: `core-service/internal/sosyal/migrations/001_sosyal.sql`
- Create: `core-service/internal/sosyal/veritabani.go`

**Interfaces:**
- Consumes: `kimlik.Baglan(ctx, dsn) (*pgxpool.Pool, error)` (existing).
- Produces: `sosyal.Migrate(ctx context.Context, havuz *pgxpool.Pool) error`; tables `sosyal.profil`, `sosyal.yayin`, `sosyal.begeni`, `sosyal.takip`, `sosyal.yorum`, `sosyal.sikayet`, `sosyal.schema_migrations`; test helper `testHavuz(t *testing.T) *pgxpool.Pool` (package `sosyal`, test files only).

- [ ] **Step 1: Write the failing test**

**Create** `core-service/internal/sosyal/veritabani_test.go`:

````go
package sosyal

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/kimlik"
)

// testHavuz, yerel Postgres'e baglanir, sosyal migration'larini uygular
// ve sosyal tablolarini bosaltir. Postgres yoksa test atlanir (kimlik
// testleriyle AYNI kalip ve AYNI varsayilan DSN). TRUNCATE yalnizca
// sosyal.* tablolarina dokunur; kimlik testlerinin public tablolarini
// etkilemez.
func testHavuz(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, iptal := context.WithTimeout(context.Background(), 3*time.Second)
	defer iptal()
	havuz, err := kimlik.Baglan(ctx, dsn)
	if err != nil {
		t.Skip("yerel Postgres yok, atlaniyor")
	}
	t.Cleanup(havuz.Close)
	if err := Migrate(context.Background(), havuz); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := havuz.Exec(context.Background(),
		`TRUNCATE sosyal.sikayet, sosyal.yorum, sosyal.begeni, sosyal.takip, sosyal.yayin, sosyal.profil`); err != nil {
		t.Fatalf("sosyal tablolari bosaltilamadi: %v", err)
	}
	return havuz
}

func TestMigrationlarGomuluVeSirali(t *testing.T) {
	adlar, err := migrationAdlari()
	if err != nil || len(adlar) == 0 {
		t.Fatalf("migration yok: %v %v", adlar, err)
	}
	for i := 1; i < len(adlar); i++ {
		if adlar[i-1] >= adlar[i] {
			t.Errorf("migrationlar sirali degil: %q, %q", adlar[i-1], adlar[i])
		}
	}
	icerik, err := migrationFS.ReadFile("migrations/001_sosyal.sql")
	if err != nil {
		t.Fatalf("001_sosyal.sql okunamadi: %v", err)
	}
	for _, tablo := range []string{"sosyal.profil", "sosyal.yayin", "sosyal.begeni", "sosyal.takip", "sosyal.yorum", "sosyal.sikayet"} {
		if !strings.Contains(string(icerik), "CREATE TABLE IF NOT EXISTS "+tablo) {
			t.Errorf("migration %s tablosunu kurmuyor", tablo)
		}
	}
	if strings.Contains(string(icerik), "REFERENCES users") || strings.Contains(string(icerik), "public.users") {
		t.Error("sosyal tablolari public.users'a FK kurmamali")
	}
}

// Migrate iki kez kosunca da hata vermemeli (replikalar start-first doner).
func TestMigrate_IkiKezKosarVeTablolariKurar(t *testing.T) {
	havuz := testHavuz(t)
	ctx := context.Background()
	if err := Migrate(ctx, havuz); err != nil {
		t.Fatalf("ikinci Migrate: %v", err)
	}
	var sayi int
	if err := havuz.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'sosyal'
		  AND table_name IN ('profil','yayin','begeni','takip','yorum','sikayet','schema_migrations')`,
	).Scan(&sayi); err != nil {
		t.Fatalf("tablolar sayilamadi: %v", err)
	}
	if sayi != 7 {
		t.Fatalf("7 sosyal tablosu bekleniyordu, %d var", sayi)
	}
}

// yayin silinince begeni, yorum ve sikayet de gider (sema ici cascade).
func TestMigrate_YayinSilinceBagliKayitlarGider(t *testing.T) {
	havuz := testHavuz(t)
	ctx := context.Background()
	yayinID := "7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	kisi := "8b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	if _, err := havuz.Exec(ctx, `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, tur, bpm, ton_tonik, ton_mod, sure_sn,
			gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik, durum)
		VALUES ($1, 'Abcd1234', $2, 'b', 'pop', 120, 'A', 'minor', 60, 'herkes', false, true,
			'{}', '[]', '{}', 'yayinda')`, yayinID, kisi); err != nil {
		t.Fatalf("yayin eklenemedi: %v", err)
	}
	if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)`, kisi, yayinID); err != nil {
		t.Fatalf("begeni: %v", err)
	}
	if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), $1, $2, 'm')`, yayinID, kisi); err != nil {
		t.Fatalf("yorum: %v", err)
	}
	if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), $1, 'yayin', $2, $2, 'spam')`, kisi, yayinID); err != nil {
		t.Fatalf("sikayet: %v", err)
	}
	if _, err := havuz.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1`, yayinID); err != nil {
		t.Fatalf("yayin silinemedi: %v", err)
	}
	var kalan int
	if err := havuz.QueryRow(ctx, `SELECT (SELECT count(*) FROM sosyal.begeni) + (SELECT count(*) FROM sosyal.yorum) + (SELECT count(*) FROM sosyal.sikayet)`).Scan(&kalan); err != nil {
		t.Fatalf("sayim: %v", err)
	}
	if kalan != 0 {
		t.Fatalf("cascade calismadi, %d kayit kaldi", kalan)
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ -count=1
```

Expected: FAIL, build error `undefined: Migrate` / `undefined: migrationAdlari`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/migrations/001_sosyal.sql`:

````sql
-- Kesfet (sosyal) modulunun TUM tablolari. 5b-5d'nin tablolari da burada
-- kurulur ki sonraki asamalar yeni migration'a ihtiyac duymadan uc
-- ekleyebilsin. Her ifade IF NOT EXISTS: API replikalari start-first
-- doner, ayni migration iki kez gorulebilir.
--
-- Kimligin kullanici tablosuna FK YOK: kimlik kullaniciyi once siler; bir cascade, R2
-- temizligi icin gereken yayin kimliklerini yok ederdi (bkz. HesapService).

CREATE TABLE IF NOT EXISTS sosyal.profil (
    kullanici_id  uuid PRIMARY KEY,
    kullanici_adi text NOT NULL UNIQUE
        CHECK (kullanici_adi ~ '^[a-z0-9_.]{3,20}$'
               AND left(kullanici_adi, 1) <> '.'
               AND right(kullanici_adi, 1) <> '.'),
    gorunen_ad    text NOT NULL CHECK (char_length(gorunen_ad) BETWEEN 1 AND 40),
    bio           text NOT NULL DEFAULT '' CHECK (char_length(bio) <= 160),
    olusturma     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sosyal.yayin (
    id                uuid PRIMARY KEY,
    slug              text NOT NULL UNIQUE CHECK (slug ~ '^[A-Za-z0-9]{8}$'),
    sahip_id          uuid NOT NULL,
    baslik            text NOT NULL CHECK (char_length(baslik) BETWEEN 1 AND 80),
    aciklama          text NOT NULL DEFAULT '' CHECK (char_length(aciklama) <= 300),
    tur               text NOT NULL,
    bpm               integer NOT NULL,
    ton_tonik         text NOT NULL,
    ton_mod           text NOT NULL CHECK (ton_mod IN ('major', 'minor')),
    sure_sn           integer NOT NULL CHECK (sure_sn > 0),
    gorunurluk        text NOT NULL CHECK (gorunurluk IN ('herkes', 'takipciler', 'link')),
    altyapi_acik      boolean NOT NULL,
    yorum_acik        boolean NOT NULL,
    sozler            jsonb NOT NULL,
    yapi              jsonb NOT NULL,
    dalga_formu       jsonb NOT NULL DEFAULT '[]',
    -- [{id, enstruman, sure, anahtar}]; anahtar disari verilmez.
    stem_listesi      jsonb NOT NULL DEFAULT '[]',
    -- Kaynak anahtarlar: {masterKey, stems:[{id, enstruman, sure, key}]}.
    -- tekrar-dene ayni kopyayi yeniden kurabilsin diye satirda durur.
    hazirlik          jsonb NOT NULL,
    master_anahtari   text,
    onizleme_anahtari text,
    -- Cover ise orijinal. FK YOK: orijinal silinince cover kalir ve
    -- kaynak_baslik / kaynak_sahip_adi son bilinen degerleri tasir.
    kaynak_yayin_id   uuid,
    kaynak_baslik     text,
    kaynak_sahip_adi  text,
    durum             text NOT NULL CHECK (durum IN ('hazirlaniyor', 'yayinda', 'hata')),
    olusturma         timestamptz NOT NULL DEFAULT now(),
    silinme           timestamptz
);

CREATE INDEX IF NOT EXISTS yayin_sahip_idx ON sosyal.yayin (sahip_id, olusturma DESC, id DESC);
CREATE INDEX IF NOT EXISTS yayin_akis_idx ON sosyal.yayin (olusturma DESC, id DESC)
    WHERE silinme IS NULL AND durum = 'yayinda';
CREATE INDEX IF NOT EXISTS yayin_kaynak_idx ON sosyal.yayin (kaynak_yayin_id)
    WHERE kaynak_yayin_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS sosyal.begeni (
    kullanici_id uuid NOT NULL,
    yayin_id     uuid NOT NULL REFERENCES sosyal.yayin (id) ON DELETE CASCADE,
    olusturma    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kullanici_id, yayin_id)
);
CREATE INDEX IF NOT EXISTS begeni_yayin_idx ON sosyal.begeni (yayin_id, olusturma);

CREATE TABLE IF NOT EXISTS sosyal.takip (
    takipci_id      uuid NOT NULL,
    takip_edilen_id uuid NOT NULL,
    olusturma       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (takipci_id, takip_edilen_id),
    CHECK (takipci_id <> takip_edilen_id)
);
CREATE INDEX IF NOT EXISTS takip_edilen_idx ON sosyal.takip (takip_edilen_id);

CREATE TABLE IF NOT EXISTS sosyal.yorum (
    id        uuid PRIMARY KEY,
    yayin_id  uuid NOT NULL REFERENCES sosyal.yayin (id) ON DELETE CASCADE,
    yazar_id  uuid NOT NULL,
    metin     text NOT NULL CHECK (char_length(metin) BETWEEN 1 AND 500),
    zaman_ms  integer CHECK (zaman_ms >= 0),
    olusturma timestamptz NOT NULL DEFAULT now(),
    silinme   timestamptz
);
CREATE INDEX IF NOT EXISTS yorum_yayin_idx ON sosyal.yorum (yayin_id, olusturma);
CREATE INDEX IF NOT EXISTS yorum_yazar_idx ON sosyal.yorum (yazar_id);

-- yayin_id: hedefin ait oldugu yayin (hedef yorumsa yorumun yayini).
-- Cok bicimli hedef_id'ye FK kurulamaz; cascade bu kolon uzerinden gelir.
CREATE TABLE IF NOT EXISTS sosyal.sikayet (
    id          uuid PRIMARY KEY,
    bildiren_id uuid NOT NULL,
    hedef_turu  text NOT NULL CHECK (hedef_turu IN ('yayin', 'yorum')),
    hedef_id    uuid NOT NULL,
    yayin_id    uuid NOT NULL REFERENCES sosyal.yayin (id) ON DELETE CASCADE,
    neden       text NOT NULL CHECK (neden IN ('spam', 'taciz', 'telif', 'uygunsuz', 'diger')),
    aciklama    text NOT NULL DEFAULT '' CHECK (char_length(aciklama) <= 300),
    olusturma   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bildiren_id, hedef_turu, hedef_id)
);
````

**Create** `core-service/internal/sosyal/veritabani.go`:

````go
// Package sosyal, Kesfet'in (yayin, profil, begeni, akis) veri katmanini ve
// is kurallarini tasir. Kendi Postgres semasinda (sosyal) ve kendi
// migration tablosunda calisir; kimlik modulunun havuzunu paylasmaz.
package sosyal

import (
	"context"
	"embed"
	"fmt"
	"log"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationKilitAnahtari, sosyal migration'larini kumede tek pod'un
// kosmasini saglayan advisory lock anahtaridir. Kimligin anahtarindan
// (0x6B696D6C696B31, "kimlik1") farklidir; iki modul birbirini beklemez.
const migrationKilitAnahtari int64 = 0x736F7379616C31 // "sosyal1"

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

// Migrate, sosyal semasini ve uygulanmamis migration'lari kurar. Butun is
// tek bir baglanti uzerinde, advisory lock altinda kosar: rolling update
// sirasinda iki pod ayni anda baslarsa ikincisi birincinin bitmesini
// bekler. Her migration kendi islemindedir; yarida kalan sema birakmaz.
func Migrate(ctx context.Context, havuz *pgxpool.Pool) error {
	baglanti, err := havuz.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("sosyal migration icin baglanti alinamadi: %w", err)
	}
	defer baglanti.Release()

	if _, err := baglanti.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationKilitAnahtari); err != nil {
		return fmt.Errorf("sosyal migration kilidi alinamadi: %w", err)
	}
	defer func() {
		if _, err := baglanti.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationKilitAnahtari); err != nil {
			log.Printf("sosyal migration kilidi birakilamadi: %v", err)
		}
	}()

	if _, err := baglanti.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS sosyal`); err != nil {
		return fmt.Errorf("sosyal semasi olusturulamadi: %w", err)
	}
	if _, err := baglanti.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sosyal.schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("sosyal.schema_migrations olusturulamadi: %w", err)
	}

	adlar, err := migrationAdlari()
	if err != nil {
		return err
	}
	for _, ad := range adlar {
		var varMi bool
		if err := baglanti.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM sosyal.schema_migrations WHERE version = $1)`, ad,
		).Scan(&varMi); err != nil {
			return fmt.Errorf("%s kontrol edilemedi: %w", ad, err)
		}
		if varMi {
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + ad)
		if err != nil {
			return fmt.Errorf("%s okunamadi: %w", ad, err)
		}
		islem, err := baglanti.Begin(ctx)
		if err != nil {
			return fmt.Errorf("%s icin islem baslatilamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx, string(sql)); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s uygulanamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx,
			`INSERT INTO sosyal.schema_migrations (version) VALUES ($1)`, ad); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s kaydedilemedi: %w", ad, err)
		}
		if err := islem.Commit(ctx); err != nil {
			return fmt.Errorf("%s commit edilemedi: %w", ad, err)
		}
	}
	return nil
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/sosyal/ -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: 3 tests PASS (none SKIP while the local Postgres on port 55432 is up). Whole Go suite after this task: about 553 passing tests (`go test ./internal/... -json`, counting subtests); Python: 24.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/veritabani_test.go \
  core-service/internal/sosyal/migrations/001_sosyal.sql \
  core-service/internal/sosyal/veritabani.go
git commit -F - <<'EOF'
feat(sosyal): sosyal semasi ve migration altyapisi

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 2: R2 copy, sosyal keys, bare-key resolver and the Go analysis client

Adds the storage and audio primitives the worker and the publish flow need (R3, R4, N7). Each lives in its own file so the existing `StorageClient`, `OnekSilici` and `AudioProcessor` fakes stay untouched.

**Files:**

- Create: `core-service/internal/client/sosyal_anahtar_test.go`
- Create: `core-service/internal/client/kopya_test.go`
- Create: `core-service/internal/client/audio_analiz_test.go`
- Create: `core-service/internal/client/sosyal_anahtar.go`
- Create: `core-service/internal/client/kopya.go`
- Create: `core-service/internal/client/audio_analiz.go`

**Interfaces:**
- Consumes: `segmentDeseni`, `sahipliTurler`, `SahipliAnahtarCoz`, `AnahtarSahibiDogrula`, `BucketFor`, `ErrGecersizURL`, `ErrYabanciAnahtar`, `ErrGecersizSegment` (existing, package `client`); `AudioClient.post` (existing).
- Produces: `client.SosyalAnahtari(yayinID string, parcalar ...string) (string, error)`; `client.SosyalOneki(yayinID string) (string, error)`; `client.SahipliAnahtarVeyaURLCoz(ham, userID string, cfg *config.R2Config) (string, error)`; `type client.Kopyalayici interface { Kopyala(ctx context.Context, kaynak, hedef string) error }` implemented by `*client.R2Client`; `client.AnalizRequest{InputKey, PreviewKey string; PeakCount, PreviewSeconds int}`; `client.AnalizResponse{Peaks []float64; Duration float64; PreviewURL string}`; `type client.AudioAnalizci interface { Analiz(ctx context.Context, req *AnalizRequest) (*AnalizResponse, error) }` implemented by `*client.AudioClient`.

- [ ] **Step 1: Write the failing tests**

`kopya_test.go` points an `R2Client` at an `httptest` server (path-style S3), so the real `CopyObject` request is checked without network access. `testR2Cfg()` already exists in `client/r2_client_test.go`.

**Create** `core-service/internal/client/sosyal_anahtar_test.go`:

````go
package client

import (
	"errors"
	"testing"
)

func TestSosyalAnahtari_Duzen(t *testing.T) {
	durumlar := []struct {
		parcalar []string
		beklenen string
	}{
		{[]string{"master.wav"}, "sosyal/y1/master.wav"},
		{[]string{"onizleme.mp3"}, "sosyal/y1/onizleme.mp3"},
		{[]string{"stems", "s1.mp3"}, "sosyal/y1/stems/s1.mp3"},
	}
	for _, d := range durumlar {
		got, err := SosyalAnahtari("y1", d.parcalar...)
		if err != nil || got != d.beklenen {
			t.Errorf("SosyalAnahtari(%v) = %q, %v; beklenen %q", d.parcalar, got, err, d.beklenen)
		}
	}
}

func TestSosyalAnahtari_GecersizSegmentReddedilir(t *testing.T) {
	durumlar := []struct {
		yayin    string
		parcalar []string
	}{
		{"", []string{"master.wav"}},
		{"y.1", []string{"master.wav"}},
		{"../y", []string{"master.wav"}},
		{"y1", nil},
		{"y1", []string{"../x"}},
		{"y1", []string{"stems", "a/b.mp3"}},
		{"y1", []string{".."}},
	}
	for _, d := range durumlar {
		if _, err := SosyalAnahtari(d.yayin, d.parcalar...); !errors.Is(err, ErrGecersizSegment) {
			t.Errorf("SosyalAnahtari(%q, %v) reddedilmeliydi, err=%v", d.yayin, d.parcalar, err)
		}
	}
}

func TestSosyalOneki_UcParca(t *testing.T) {
	got, err := SosyalOneki("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")
	if err != nil || got != "sosyal/6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/" {
		t.Fatalf("SosyalOneki = %q, %v", got, err)
	}
	if _, err := SosyalOneki(""); err == nil {
		t.Fatal("bos yayin kimligi onek uretmemeli")
	}
}

// sosyal/ anahtarlari kullanici sahipli degildir: files/sign imzalamaz.
func TestSosyalAnahtari_KullaniciSahipliDegil(t *testing.T) {
	if err := AnahtarSahibiDogrula("sosyal/u1/master.wav", "u1"); !errors.Is(err, ErrYabanciAnahtar) {
		t.Fatalf("sosyal anahtari sahipli sayilmamali, err=%v", err)
	}
}

func TestSahipliAnahtarVeyaURLCoz(t *testing.T) {
	cfg := testR2Cfg()
	durumlar := []struct {
		ad, ham, beklenen string
		hata              error
	}{
		{"tam adres", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/u1/p1/m.wav?X-Amz-Signature=x", "masters/u1/p1/m.wav", nil},
		{"ciplak anahtar", "masters/u1/p1/m.wav", "masters/u1/p1/m.wav", nil},
		{"ciplak stem", "stems/u1/p1/s1.mp3", "stems/u1/p1/s1.mp3", nil},
		{"yabanci ciplak", "masters/u2/p1/m.wav", "", ErrYabanciAnahtar},
		{"yabanci adres", "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/u2/p1/m.wav", "", ErrYabanciAnahtar},
		{"bilinmeyen tur", "sosyal/y1/master.wav", "", ErrGecersizURL},
		{"kisa", "masters/u1", "", ErrGecersizURL},
		{"bos", "", "", ErrGecersizURL},
		{"tirmanma", "masters/u1/../u2/m.wav", "", ErrYabanciAnahtar},
		{"yabanci konak", "https://kotu.example.com/masters/u1/p1/m.wav", "", ErrGecersizURL},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			got, err := SahipliAnahtarVeyaURLCoz(d.ham, "u1", cfg)
			if d.hata != nil {
				if !errors.Is(err, d.hata) {
					t.Fatalf("hata = %v, beklenen %v", err, d.hata)
				}
				return
			}
			if err != nil || got != d.beklenen {
				t.Fatalf("= %q, %v; beklenen %q", got, err, d.beklenen)
			}
		})
	}
}
````

**Create** `core-service/internal/client/kopya_test.go`:

````go
package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// sahteS3, CopyObject isteklerini kaydeden yerel bir S3 ucu kurar ve
// R2Client'i ona yonlendirir. Ag disina cikilmaz.
func sahteS3(t *testing.T) (*R2Client, *[]*http.Request) {
	t.Helper()
	var (
		mu       sync.Mutex
		istekler []*http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		istekler = append(istekler, r.Clone(context.Background()))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><CopyObjectResult><ETag>"e"</ETag></CopyObjectResult>`))
	}))
	t.Cleanup(srv.Close)
	s3c := s3.New(s3.Options{
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider("a", "s", ""),
	})
	return &R2Client{s3Client: s3c, cfg: testR2Cfg()}, &istekler
}

func TestKopyala_AyniBucketIcindeCopyObject(t *testing.T) {
	c, istekler := sahteS3(t)
	if err := c.Kopyala(context.Background(), "masters/u1/p1/m.wav", "sosyal/y1/master.wav"); err != nil {
		t.Fatalf("Kopyala: %v", err)
	}
	if len(*istekler) != 1 {
		t.Fatalf("tek istek bekleniyordu: %d", len(*istekler))
	}
	r := (*istekler)[0]
	if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/makeasinger-private/sosyal/y1/master.wav") {
		t.Fatalf("istek = %s %s", r.Method, r.URL.Path)
	}
	if got := r.Header.Get("X-Amz-Copy-Source"); got != "makeasinger-private/masters/u1/p1/m.wav" {
		t.Fatalf("kopya kaynagi = %q", got)
	}
}

func TestKopyala_BucketlarArasiReddedilir(t *testing.T) {
	c, istekler := sahteS3(t)
	if err := c.Kopyala(context.Background(), "exports/u1/e1.mp3", "sosyal/y1/master.mp3"); err == nil {
		t.Fatal("public -> private kopya reddedilmeliydi")
	}
	if len(*istekler) != 0 {
		t.Fatal("reddedilen kopya R2'ye gitmemeli")
	}
}
````

**Create** `core-service/internal/client/audio_analiz_test.go`:

````go
package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func TestAnaliz_IstekVeYanit(t *testing.T) {
	var gelen map[string]any
	var yol string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		yol = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gelen)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"peaks":[0.5,1.0],"duration":12.5,"preview_url":"https://x/sosyal/y1/onizleme.mp3"}`))
	}))
	defer srv.Close()

	c := NewAudioClient(&config.AudioConfig{ServiceURL: srv.URL, Timeout: 5})
	yanit, err := c.Analiz(context.Background(), &AnalizRequest{
		InputKey: "sosyal/y1/master.wav", PreviewKey: "sosyal/y1/onizleme.mp3", PeakCount: 64, PreviewSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Analiz: %v", err)
	}
	if yol != "/analiz" {
		t.Fatalf("yol = %q", yol)
	}
	beklenen := map[string]any{"input_key": "sosyal/y1/master.wav", "preview_key": "sosyal/y1/onizleme.mp3", "peak_count": float64(64), "preview_seconds": float64(30)}
	for k, v := range beklenen {
		if gelen[k] != v {
			t.Errorf("%s = %v, beklenen %v", k, gelen[k], v)
		}
	}
	if len(gelen) != len(beklenen) {
		t.Errorf("fazladan alan var (Python extra=forbid reddeder): %v", gelen)
	}
	if len(yanit.Peaks) != 2 || yanit.Peaks[1] != 1.0 || yanit.Duration != 12.5 {
		t.Fatalf("yanit = %+v", yanit)
	}
}

func TestAnaliz_ServisHatasiDoner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"bozuk"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewAudioClient(&config.AudioConfig{ServiceURL: srv.URL, Timeout: 5})
	if _, err := c.Analiz(context.Background(), &AnalizRequest{InputKey: "k", PreviewKey: "p", PeakCount: 64, PreviewSeconds: 30}); err == nil {
		t.Fatal("500 hata olarak donmeli")
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/client/ -count=1
```

Expected: FAIL, build errors `undefined: SosyalAnahtari`, `undefined: AnalizRequest`, `c.Kopyala undefined`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/client/sosyal_anahtar.go`:

````go
package client

import (
	"fmt"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// sosyalOnek, yayin kopyalarinin koku. Bir kullaniciya degil bir yayina
// aittir: sahiplik anahtarda degil, sosyal.yayin satirindadir. Bu yuzden
// sahipliTurler'de YOKTUR ve /api/files/sign bu anahtarlari imzalamaz;
// imzali adresler yalnizca gorunurluk kuralini uygulayan sosyal uclardan
// verilir.
const sosyalOnek = "sosyal"

// yayinKimligiGecerli, yayin kimliginin tek bir anahtar segmenti olabildigini
// dogrular. Nokta iceremez: bir dosya adi degil, bir kimliktir.
func yayinKimligiGecerli(yayinID string) bool {
	return segmentDeseni.MatchString(yayinID) && !strings.Contains(yayinID, ".")
}

// SosyalAnahtari, "sosyal/<yayinId>/<parcalar...>" anahtarini kurar. Her
// parca KullaniciAnahtari ile ayni segment kuralindan gecer; "/" ya da
// ".." tasiyan bir parca reddedilir. Duzen:
//
//	sosyal/{yayinId}/master.{ext}
//	sosyal/{yayinId}/onizleme.mp3
//	sosyal/{yayinId}/stems/{stemId}.{ext}
func SosyalAnahtari(yayinID string, parcalar ...string) (string, error) {
	if !yayinKimligiGecerli(yayinID) {
		return "", ErrGecersizSegment
	}
	if len(parcalar) == 0 {
		return "", fmt.Errorf("%w: dosya adi yok", ErrGecersizSegment)
	}
	for _, p := range parcalar {
		if !segmentDeseni.MatchString(p) {
			return "", ErrGecersizSegment
		}
	}
	return sosyalOnek + "/" + yayinID + "/" + strings.Join(parcalar, "/"), nil
}

// SosyalOneki, bir yayinin tum dosyalarini kapsayan "sosyal/<yayinId>/"
// onekidir. DeletePrefix'in uc parca kuralina uyar.
func SosyalOneki(yayinID string) (string, error) {
	if !yayinKimligiGecerli(yayinID) {
		return "", ErrGecersizSegment
	}
	return sosyalOnek + "/" + yayinID + "/", nil
}

// SahipliAnahtarVeyaURLCoz, SahipliAnahtarCoz'un ciplak anahtari da kabul
// eden halidir. Render ve master sonuclarinin fileUrl'i tam adres ya da
// (mock/dev yollarinda ve eski kayitlarda) ciplak anahtar olabilir;
// KeyFromURL konaksiz bir dizgeyi ErrGecersizURL ile reddeder.
//
// Ciplak anahtar "<tur>/<userId>/..." biciminde bilinen bir turle
// baslamiyorsa ErrGecersizURL, baskasinin onekindeyse ErrYabanciAnahtar
// doner.
func SahipliAnahtarVeyaURLCoz(ham, userID string, cfg *config.R2Config) (string, error) {
	if strings.Contains(ham, "://") {
		return SahipliAnahtarCoz(ham, userID, cfg)
	}
	parcalar := strings.Split(ham, "/")
	if len(parcalar) < 3 || !sahipliTurler[parcalar[0]] {
		return "", fmt.Errorf("%w: anahtar ya da adres degil", ErrGecersizURL)
	}
	if err := AnahtarSahibiDogrula(ham, userID); err != nil {
		return "", err
	}
	return ham, nil
}
````

**Create** `core-service/internal/client/kopya.go`:

````go
package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Kopyalayici, bir nesneyi ayni bucket icinde sunucu tarafinda kopyalar.
// StorageClient'tan ayri tutulur: yalnizca sosyal yayin hazirligi ihtiyac
// duyar ve mevcut sahte depolarin hepsini bu yontemi uygulamaya zorlamak
// gereksizdir (bkz. OnekSilici).
type Kopyalayici interface {
	Kopyala(ctx context.Context, kaynak, hedef string) error
}

var _ Kopyalayici = (*R2Client)(nil)

// kopyaKaynagi, CopyObject'in x-amz-copy-source degeridir: "<bucket>/<key>".
// Anahtarlar segment deseninden gectigi icin kacis gerekmez.
func kopyaKaynagi(bucket, key string) string {
	return bucket + "/" + key
}

// Kopyala, kaynak nesneyi hedef anahtara kopyalar. Iki anahtar farkli
// bucket'lara dusuyorsa (orn. exports/ -> sosyal/) hata doner: R2'de
// bucket'lar arasi kopya ayri yetki ister ve public bir nesnenin private
// tarafa ya da tersine tasinmasi bir siniflandirma hatasidir.
func (c *R2Client) Kopyala(ctx context.Context, kaynak, hedef string) error {
	kaynakBucket := BucketFor(kaynak, c.cfg)
	hedefBucket := BucketFor(hedef, c.cfg)
	if kaynakBucket != hedefBucket {
		return fmt.Errorf("bucket'lar arasi kopya reddedildi: %s -> %s", kaynakBucket, hedefBucket)
	}
	_, err := c.s3Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(hedefBucket),
		Key:        aws.String(hedef),
		CopySource: aws.String(kopyaKaynagi(kaynakBucket, kaynak)),
	})
	if err != nil {
		return fmt.Errorf("R2 kopyalanamadi: %w", err)
	}
	return nil
}
````

**Create** `core-service/internal/client/audio_analiz.go`:

````go
package client

import "context"

// AnalizRequest, audio-service /analiz istegidir. Anahtarlar R2 nesne
// anahtaridir; servis dosyayi S3 API ile okur ve onizlemeyi PreviewKey'e
// yazar. Imzali adres tasinmaz.
type AnalizRequest struct {
	InputKey       string `json:"input_key"`
	PreviewKey     string `json:"preview_key"`
	PeakCount      int    `json:"peak_count"`
	PreviewSeconds int    `json:"preview_seconds"`
}

// AnalizResponse: Peaks en yuksek tepeye gore 0-1 araliginda normalize
// edilmis PeakCount degerdir; PreviewURL Go tarafinda kullanilmaz (anahtar
// zaten bilinir), yalnizca servisin yazdigini teyit eder.
type AnalizResponse struct {
	Peaks      []float64 `json:"peaks"`
	Duration   float64   `json:"duration"`
	PreviewURL string    `json:"preview_url"`
}

// AudioAnalizci, dalga formu ve onizleme ureten yetenek. AudioProcessor'dan
// ayri tutulur ki mevcut AudioProcessor sahteleri kirilmasin.
type AudioAnalizci interface {
	Analiz(ctx context.Context, req *AnalizRequest) (*AnalizResponse, error)
}

var _ AudioAnalizci = (*AudioClient)(nil)

// Analiz, /analiz ucunu cagirir.
func (c *AudioClient) Analiz(ctx context.Context, req *AnalizRequest) (*AnalizResponse, error) {
	var result AnalizResponse
	if err := c.post(ctx, "/analiz", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/client/ -count=1
```

Expected: `ok  github.com/makeasinger/api/internal/client`. Whole Go suite after this task: about 572 passing tests (`go test ./internal/... -json`, counting subtests); Python: 24.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/client/sosyal_anahtar_test.go \
  core-service/internal/client/kopya_test.go \
  core-service/internal/client/audio_analiz_test.go \
  core-service/internal/client/sosyal_anahtar.go \
  core-service/internal/client/kopya.go \
  core-service/internal/client/audio_analiz.go
git commit -F - <<'EOF'
feat(client): R2 kopyalama, sosyal anahtarlari ve analiz istemcisi

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 3: audio-service `POST /analiz`

Python side of R4: pure peak and best-window functions, a service that downloads the master by key, exports the preview mp3 and uploads it, and the FastAPI route. Tests use synthetic audio built directly as `AudioSegment` and monkeypatch `from_file`/`export`, so neither the network nor ffmpeg is needed (same approach as `tests/test_islemler.py`).

**Files:**

- Create: `audio-service/tests/test_analiz.py`
- Create: `audio-service/services/analiz.py`
- Modify: `audio-service/main.py`

**Interfaces:**
- Consumes: `StorageService.download_key_to_file(key, path)` and `upload_file(key, path, content_type)` (existing).
- Produces: `services.analiz.tepe_degerleri(ornekler, adet) -> list[float]`, `en_yuksek_rms_penceresi(ornekler, ornek_hizi, pencere_sn) -> tuple[int, int]`, `mono_ornekler(ses) -> np.ndarray`, `AnalizService.analiz(input_key, preview_key, peak_count, preview_seconds) -> dict`; `main.AnalizRequest`, `main.AnalizResponse`, route `POST /analiz`.

- [ ] **Step 1: Write the failing test**

**Create** `audio-service/tests/test_analiz.py`:

````python
"""Analiz testleri: sentetik ses, ag ve ffmpeg yok."""

import numpy as np
import pytest
from pydantic import ValidationError
from pydub import AudioSegment

import services.analiz as analiz_module
from main import AnalizRequest
from services.analiz import (
    AnalizService,
    en_yuksek_rms_penceresi,
    mono_ornekler,
    tepe_degerleri,
)


def test_tepe_degerleri_adet_ve_normalizasyon():
    ornekler = np.concatenate([np.full(100, 0.25), np.full(100, -0.5)])
    tepeler = tepe_degerleri(ornekler, 4)
    assert tepeler == [0.5, 0.5, 1.0, 1.0]


def test_tepe_degerleri_her_zaman_istenen_uzunlukta():
    assert len(tepe_degerleri(np.ones(10), 64)) == 64
    assert len(tepe_degerleri(np.random.default_rng(1).normal(size=9999), 64)) == 64


def test_tepe_degerleri_sessiz_ve_bos():
    assert tepe_degerleri(np.zeros(500), 8) == [0.0] * 8
    assert tepe_degerleri(np.array([]), 8) == [0.0] * 8
    assert tepe_degerleri(np.ones(5), 0) == []


def test_tepe_degerleri_aralikta():
    tepeler = tepe_degerleri(np.random.default_rng(2).normal(size=48000), 64)
    assert all(0.0 <= t <= 1.0 for t in tepeler)
    assert max(tepeler) == 1.0


def test_en_yuksek_rms_penceresi_yuksek_bolumu_bulur():
    hiz = 100
    sessiz = np.full(hiz * 40, 0.01)
    gurultulu = np.full(hiz * 30, 0.9)
    ornekler = np.concatenate([sessiz, gurultulu, sessiz])
    bas, bit = en_yuksek_rms_penceresi(ornekler, hiz, 30)
    assert (bas, bit) == (hiz * 40, hiz * 70)


def test_en_yuksek_rms_penceresi_kisa_parca_butun():
    assert en_yuksek_rms_penceresi(np.ones(500), 100, 30) == (0, 500)


def test_mono_ornekler_stereo_ortalama():
    # 16 bit stereo: sol tam, sag sifir -> mono yarim
    sol, sag = 16384, 0
    veri = np.array([sol, sag] * 4, dtype=np.int16).tobytes()
    ses = AudioSegment(data=veri, sample_width=2, frame_rate=8000, channels=2)
    mono = mono_ornekler(ses)
    assert len(mono) == 4
    assert np.allclose(mono, 0.25)


class SahteStorage:
    def __init__(self):
        self.indirilen = []
        self.yuklenen = []

    def download_key_to_file(self, key, path):
        self.indirilen.append(key)

    def upload_file(self, key, path, content_type):
        self.yuklenen.append((key, content_type))
        return f"https://makeasinger-private.r2.cloudflarestorage.com/{key}"


def _sentetik_ses(saniye: int, hiz: int = 1000) -> AudioSegment:
    # Ilk yarisi sessiz, ikinci yarisi gurultulu kare dalga.
    yari = saniye * hiz // 2
    dalga = np.concatenate(
        [np.zeros(yari), np.tile([20000, -20000], yari // 2)]
    ).astype(np.int16)
    return AudioSegment(
        data=dalga.tobytes(), sample_width=2, frame_rate=hiz, channels=1
    )


@pytest.mark.asyncio
async def test_analiz_servisi_onizleme_ve_tepeler(monkeypatch):
    ses = _sentetik_ses(80)
    monkeypatch.setattr(analiz_module.AudioSegment, "from_file", lambda path: ses)
    disa_aktarilan = []

    def sahte_export(self, path, format=None, bitrate=None):
        disa_aktarilan.append((len(self), format, bitrate))

    monkeypatch.setattr(analiz_module.AudioSegment, "export", sahte_export)

    storage = SahteStorage()
    sonuc = await AnalizService(storage).analiz(
        input_key="sosyal/y1/master.wav",
        preview_key="sosyal/y1/onizleme.mp3",
        peak_count=64,
        preview_seconds=30,
    )
    assert storage.indirilen == ["sosyal/y1/master.wav"]
    assert storage.yuklenen == [("sosyal/y1/onizleme.mp3", "audio/mpeg")]
    assert len(sonuc["peaks"]) == 64
    assert sonuc["peaks"][0] == 0.0 and sonuc["peaks"][-1] == 1.0
    assert sonuc["duration"] == 80.0
    # 30 sn'lik pencere sessiz olmayan ikinci yaridan secilir.
    assert disa_aktarilan == [(30000, "mp3", "192k")]
    assert sonuc["preview_url"].endswith("sosyal/y1/onizleme.mp3")


@pytest.mark.asyncio
async def test_analiz_servisi_kisa_parca_butunuyle(monkeypatch):
    ses = _sentetik_ses(10)
    monkeypatch.setattr(analiz_module.AudioSegment, "from_file", lambda path: ses)
    uzunluklar = []
    monkeypatch.setattr(
        analiz_module.AudioSegment,
        "export",
        lambda self, path, format=None, bitrate=None: uzunluklar.append(len(self)),
    )
    await AnalizService(SahteStorage()).analiz("k.wav", "p.mp3", 64, 30)
    assert uzunluklar == [10000]


@pytest.mark.asyncio
async def test_analiz_servisi_depolamasiz_hata():
    with pytest.raises(RuntimeError):
        await AnalizService(None).analiz("k", "p", 64, 30)


def test_analiz_request_varsayilanlar_ve_fazla_alan():
    req = AnalizRequest(
        input_key="sosyal/y1/master.wav", preview_key="sosyal/y1/onizleme.mp3"
    )
    assert req.peak_count == 64 and req.preview_seconds == 30
    with pytest.raises(ValidationError):
        AnalizRequest(input_key="a", preview_key="b", input_url="https://x")


def test_analiz_request_sinirlar():
    with pytest.raises(ValidationError):
        AnalizRequest(input_key="a", preview_key="b", peak_count=0)
    with pytest.raises(ValidationError):
        AnalizRequest(input_key="a", preview_key="b", preview_seconds=0)
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd audio-service && python3 -m pytest tests/test_analiz.py -q
```

Expected: collection ERROR: `ImportError: cannot import name 'AnalizRequest' from 'main'` (or `No module named 'services.analiz'`).

- [ ] **Step 3: Implement**

**Create** `audio-service/services/analiz.py`:

````python
"""Kesfet yayinlari icin dalga formu ve 30 sn onizleme uretimi.

Saf fonksiyonlar (tepe_degerleri, en_yuksek_rms_penceresi) numpy dizisi
uzerinde calisir ve ffmpeg istemez; AnalizService dosyayi R2'den indirir,
pydub ile cozer ve onizlemeyi mp3 olarak geri yukler.
"""

import os
import tempfile
from typing import Any

import numpy as np
from pydub import AudioSegment

from .storage import StorageService

ONIZLEME_BIT_HIZI = "192k"


def tepe_degerleri(ornekler: np.ndarray, adet: int) -> list[float]:
    """Mono ornekleri `adet` esit parcaya boler, her parcanin mutlak tepe
    degerini alir ve en yuksek tepeye gore 0-1 araligina normalize eder.

    Sessiz ya da bos girdide `adet` sifir doner; dizi her zaman `adet`
    uzunlugundadir.
    """
    if adet <= 0:
        return []
    mutlak = np.abs(np.asarray(ornekler, dtype=np.float64))
    if mutlak.size == 0:
        return [0.0] * adet
    parcalar = np.array_split(mutlak, adet)
    tepeler = np.array([p.max() if p.size else 0.0 for p in parcalar])
    en_yuksek = tepeler.max()
    if en_yuksek <= 0:
        return [0.0] * adet
    return [float(round(t / en_yuksek, 4)) for t in tepeler]


def en_yuksek_rms_penceresi(
    ornekler: np.ndarray, ornek_hizi: int, pencere_sn: float
) -> tuple[int, int]:
    """Enerjisi (karelerin toplami, yani RMS) en yuksek `pencere_sn`
    uzunlugundaki pencerenin [baslangic, bitis) ornek indekslerini doner.

    Parca pencereden kisaysa butun parca doner. Kumulatif toplamla O(n).
    """
    n = len(ornekler)
    pencere = round(pencere_sn * ornek_hizi)
    if pencere <= 0 or n <= pencere:
        return 0, n
    kareler = np.square(np.asarray(ornekler, dtype=np.float64))
    kumulatif = np.concatenate(([0.0], np.cumsum(kareler)))
    enerjiler = kumulatif[pencere:] - kumulatif[:-pencere]
    baslangic = int(np.argmax(enerjiler))
    return baslangic, baslangic + pencere


def mono_ornekler(ses: AudioSegment) -> np.ndarray:
    """AudioSegment'i -1..1 araliginda mono float diziye cevirir."""
    ham = np.array(ses.get_array_of_samples(), dtype=np.float64)
    if ses.channels > 1:
        ham = ham.reshape((-1, ses.channels)).mean(axis=1)
    olcek = float(1 << (8 * ses.sample_width - 1))
    return ham / olcek


class AnalizService:
    """Masterdan dalga formu ve onizleme uretir."""

    def __init__(self, storage: StorageService | None = None):
        self.storage = storage

    async def analiz(
        self,
        input_key: str,
        preview_key: str,
        peak_count: int,
        preview_seconds: int,
    ) -> dict[str, Any]:
        if not self.storage:
            raise RuntimeError("Storage service not configured")

        uzanti = os.path.splitext(input_key)[1] or ".wav"
        with tempfile.TemporaryDirectory() as tmpdir:
            giris = os.path.join(tmpdir, "giris" + uzanti)
            self.storage.download_key_to_file(input_key, giris)
            ses = AudioSegment.from_file(giris)

            ornekler = mono_ornekler(ses)
            tepeler = tepe_degerleri(ornekler, peak_count)
            baslangic, bitis = en_yuksek_rms_penceresi(
                ornekler, ses.frame_rate, preview_seconds
            )
            bas_ms = int(baslangic * 1000 / ses.frame_rate)
            bit_ms = int(bitis * 1000 / ses.frame_rate)
            onizleme = ses[bas_ms:bit_ms]

            cikis = os.path.join(tmpdir, "onizleme.mp3")
            onizleme.export(cikis, format="mp3", bitrate=ONIZLEME_BIT_HIZI)
            preview_url = self.storage.upload_file(preview_key, cikis, "audio/mpeg")

            return {
                "peaks": tepeler,
                "duration": len(ses) / 1000.0,
                "preview_url": preview_url,
            }
````

**Modify** `audio-service/main.py`:

````diff
diff --git a/audio-service/main.py b/audio-service/main.py
--- a/audio-service/main.py
+++ b/audio-service/main.py
@@ -12,8 +12,9 @@ import os
 from contextlib import asynccontextmanager
 
 from fastapi import FastAPI, HTTPException
-from pydantic import BaseModel
+from pydantic import BaseModel, Field
 
+from services.analiz import AnalizService
 from services.archiver import ArchiverService
 from services.encoder import EncoderService
 from services.master import MasterService
@@ -103,17 +104,35 @@ class ZipResponse(BaseModel):
     file_count: int
 
 
+class AnalizRequest(BaseModel):
+    model_config = {"extra": "forbid"}
+
+    # Anahtarlar R2 nesne anahtaridir; imzali adres tasinmaz.
+    input_key: str
+    preview_key: str
+    peak_count: int = Field(default=64, ge=1, le=1024)
+    preview_seconds: int = Field(default=30, ge=1, le=120)
+
+
+class AnalizResponse(BaseModel):
+    peaks: list[float]
+    duration: float
+    preview_url: str
+
+
 # Services
 storage_service: StorageService | None = None
 master_service: MasterService | None = None
 encoder_service: EncoderService | None = None
 archiver_service: ArchiverService | None = None
+analiz_service: AnalizService | None = None
 
 
 @asynccontextmanager
 async def lifespan(app: FastAPI):
     """Initialize services on startup."""
     global storage_service, master_service, encoder_service, archiver_service
+    global analiz_service
 
     # Initialize storage service
     if R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY:
@@ -130,6 +149,7 @@ async def lifespan(app: FastAPI):
     master_service = MasterService(storage_service)
     encoder_service = EncoderService(storage_service)
     archiver_service = ArchiverService(storage_service)
+    analiz_service = AnalizService(storage_service)
 
     yield
 
@@ -222,6 +242,29 @@ async def create_zip(request: ZipRequest):
         raise HTTPException(status_code=500, detail=str(e))
 
 
+@app.post("/analiz", response_model=AnalizResponse)
+async def analiz_audio(request: AnalizRequest):
+    """
+    Kesfet yayini icin dalga formu (peak_count tepe) ve en yuksek RMS'li
+    preview_seconds'lik mp3 onizleme uretir. Onizleme preview_key'e yazilir.
+    """
+    if analiz_service is None:
+        raise HTTPException(status_code=503, detail="Analiz service not initialized")
+
+    try:
+        result = await analiz_service.analiz(
+            input_key=request.input_key,
+            preview_key=request.preview_key,
+            peak_count=request.peak_count,
+            preview_seconds=request.preview_seconds,
+        )
+        return AnalizResponse(**result)
+    # Genis yakalama BILINCLI: FastAPI ucunda son siginak, her hata 500
+    # olarak donsun. BLE001 bu yuzden susturuldu.
+    except Exception as e:  # noqa: BLE001
+        raise HTTPException(status_code=500, detail=str(e))
+
+
 if __name__ == "__main__":
     import uvicorn
 
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd audio-service && python3 -m pytest tests/ -q && ruff check . && ruff format --check .
```

Expected: `36 passed` (24 existing + 12 new), `All checks passed!`, `11 files already formatted`. Optional manual smoke with real ffmpeg: generate a 45 s sine with `ffmpeg -f lavfi -i sine=frequency=440:duration=45 t.wav` and call `AnalizService` with a storage stub that copies it; expect 64 peaks, `duration` 45.0 and a ~720 KB preview. Whole Go suite after this task: about 572 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add audio-service/tests/test_analiz.py \
  audio-service/services/analiz.py \
  audio-service/main.py
git commit -F - <<'EOF'
feat(audio): /analiz ucu: dalga formu ve 30 sn onizleme

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 4: Rules, visibility and the profile repository

The pure rules (R6, R7, N15), the error set, the profile types and the first part of `PostgresDepo`. `depo_yardimci_test.go` adds raw-SQL fixtures (`testYayinEkle`, `testTakipEkle`) so repository tests do not depend on endpoints built later.

**Files:**

- Create: `core-service/internal/sosyal/kurallar_test.go`
- Create: `core-service/internal/sosyal/gorunurluk_test.go`
- Create: `core-service/internal/sosyal/depo_yardimci_test.go`
- Create: `core-service/internal/sosyal/depo_profil_test.go`
- Create: `core-service/internal/sosyal/hatalar.go`
- Create: `core-service/internal/sosyal/model.go`
- Create: `core-service/internal/sosyal/kurallar.go`
- Create: `core-service/internal/sosyal/gorunurluk.go`
- Create: `core-service/internal/sosyal/depo.go`

**Interfaces:**
- Consumes: `testHavuz` (Task 1).
- Produces: Errors `ErrBulunamadi`, `ErrProfilYok`, `ErrGecersizKullaniciAdi`, `ErrKullaniciAdiAlinmis`, `ErrDogrulama`, `ErrSahiplik`, `ErrKaynakBulunamadi`, `ErrAltyapiKapali`, `ErrStemYok`, `ErrDurum`, `ErrKimlik`, `dogrulamaHatasi(mesaj string) error`; types `ProfilOzeti`, `Istatistik`, `Profil`, `ProfilGirdisi`, `AdUygunlugu`, `ProfilKaydi`, `ProfilSayilari`; `KullaniciAdiGecerli(ad string) bool`, `AvatarRengi(kullaniciID string) int`, `adAdaylari(ad string) []string`, `profilGirdisiniDuzenle(ProfilGirdisi) (ProfilGirdisi, error)`, `kimlikCoz(ham string) (string, bool)`; constants `GorunurlukHerkes|Takipciler|Link`, `DurumHazirlaniyor|Yayinda|Hata`; `GorunurlukGirdisi`, `DetayGorebilir`, `ListedeGorebilir`, `gorebilir(g, detay bool)`, `gorunurlukKosulu(alias, izleyiciParam string, detay bool) string`; `type ProfilDeposu interface` (`ProfilGetir`, `ProfilAdlaGetir`, `ProfilKaydet`, `AdSahipleri`, `ProfilSayilari`), `type Depo interface { ProfilDeposu }`, `PostgresDepo`, `NewPostgresDepo(*pgxpool.Pool)`, `tekilIhlali(err) (string, bool)`; fixtures `testYayin`, `testYayinEkle`, `testTakipEkle`, `testProfilEkle`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/kurallar_test.go`:

````go
package sosyal

import (
	"errors"
	"strings"
	"testing"
)

func TestKullaniciAdiGecerli(t *testing.T) {
	gecerli := []string{"abc", "ali_veli", "a.b.c", "x123", "ab_", "_ab", "abcdefghijklmnopqrst"}
	gecersiz := []string{"", "ab", ".abc", "abc.", "Abc", "ali veli", "ali-veli", "abcdefghijklmnopqrstu", "çağrı"}
	for _, ad := range gecerli {
		if !KullaniciAdiGecerli(ad) {
			t.Errorf("%q gecerli olmaliydi", ad)
		}
	}
	for _, ad := range gecersiz {
		if KullaniciAdiGecerli(ad) {
			t.Errorf("%q gecersiz olmaliydi", ad)
		}
	}
}

func TestAvatarRengi_SabitVeAralikta(t *testing.T) {
	kimlik := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	ilk := AvatarRengi(kimlik)
	if ilk < 0 || ilk > 7 {
		t.Fatalf("renk araligin disinda: %d", ilk)
	}
	if AvatarRengi(kimlik) != ilk {
		t.Fatal("ayni kimlik ayni rengi vermeli")
	}
	// fnv32a("6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a") % 8 = 2, bagimsiz
	// hesaplanan sabit: formul degisirse herkesin rengi degisir.
	if ilk != 2 {
		t.Fatalf("renk = %d, beklenen 2", ilk)
	}
}

func TestAdAdaylari_SiraVeUzunluk(t *testing.T) {
	adaylar := adAdaylari("ali")
	if len(adaylar) != 98 || adaylar[0] != "ali2" || adaylar[97] != "ali99" {
		t.Fatalf("adaylar = %v ... (%d)", adaylar[:2], len(adaylar))
	}
	uzun := strings.Repeat("a", 20)
	for _, a := range adAdaylari(uzun) {
		if len(a) > 20 || !KullaniciAdiGecerli(a) {
			t.Fatalf("gecersiz aday %q", a)
		}
	}
	if got := adAdaylari(uzun)[0]; got != strings.Repeat("a", 19)+"2" {
		t.Fatalf("kirpilmis ilk aday = %q", got)
	}
	if got := adAdaylari(uzun)[8]; got != strings.Repeat("a", 18)+"10" {
		t.Fatalf("iki haneli ek = %q", got)
	}
}

func TestProfilGirdisiniDuzenle(t *testing.T) {
	g, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "  Ali  ", Bio: " merhaba "})
	if err != nil || g.GorunenAd != "Ali" || g.Bio != "merhaba" {
		t.Fatalf("g = %+v, err = %v", g, err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "A", GorunenAd: "x"}); !errors.Is(err, ErrGecersizKullaniciAdi) {
		t.Fatalf("gecersiz ad: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "   "}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bos gorunen ad: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: strings.Repeat("ş", 41)}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("41 karakter gorunen ad: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: strings.Repeat("ş", 40), Bio: strings.Repeat("ğ", 160)}); err != nil {
		t.Fatalf("sinirdaki rune sayilari kabul edilmeli: %v", err)
	}
	if _, err := profilGirdisiniDuzenle(ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "a", Bio: strings.Repeat("ğ", 161)}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("161 karakter bio: %v", err)
	}
}

func TestKimlikCoz(t *testing.T) {
	if got, ok := kimlikCoz("6F1C2A9E-1B2C-4D3E-8F9A-0B1C2D3E4F5A"); !ok || got != "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a" {
		t.Fatalf("kimlikCoz = %q, %v", got, ok)
	}
	if _, ok := kimlikCoz("x"); ok {
		t.Fatal("gecersiz kimlik kabul edilmemeli")
	}
}
````

**Create** `core-service/internal/sosyal/gorunurluk_test.go`:

````go
package sosyal

import "testing"

// Spec §4.9 tablosu, sahip olmayan izleyici icin.
func TestGorunurlukTablosu(t *testing.T) {
	durumlar := []struct {
		gorunurluk  string
		takipEdiyor bool
		liste       bool
		detay       bool
	}{
		{GorunurlukHerkes, false, true, true},
		{GorunurlukHerkes, true, true, true},
		{GorunurlukTakipciler, false, false, false},
		{GorunurlukTakipciler, true, true, true},
		{GorunurlukLink, false, false, true},
		{GorunurlukLink, true, false, true},
	}
	for _, d := range durumlar {
		g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: "i", Gorunurluk: d.gorunurluk, Durum: DurumYayinda, TakipEdiyor: d.takipEdiyor}
		if got := ListedeGorebilir(g); got != d.liste {
			t.Errorf("%s takip=%v liste = %v, beklenen %v", d.gorunurluk, d.takipEdiyor, got, d.liste)
		}
		if got := DetayGorebilir(g); got != d.detay {
			t.Errorf("%s takip=%v detay = %v, beklenen %v", d.gorunurluk, d.takipEdiyor, got, d.detay)
		}
	}
}

func TestGorunurluk_SahipHerDurumuGorur(t *testing.T) {
	for _, gr := range []string{GorunurlukHerkes, GorunurlukTakipciler, GorunurlukLink} {
		for _, durum := range []string{DurumHazirlaniyor, DurumYayinda, DurumHata} {
			g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: "s", Gorunurluk: gr, Durum: durum}
			if !DetayGorebilir(g) || !ListedeGorebilir(g) {
				t.Errorf("sahip %s/%s yayinini gormeli", gr, durum)
			}
		}
	}
}

func TestGorunurluk_YabanciHazirlanmayiVeHatayiGormez(t *testing.T) {
	for _, durum := range []string{DurumHazirlaniyor, DurumHata} {
		g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: "i", Gorunurluk: GorunurlukHerkes, Durum: durum, TakipEdiyor: true}
		if DetayGorebilir(g) || ListedeGorebilir(g) {
			t.Errorf("yabanci %s yayini gormemeli", durum)
		}
	}
}

func TestGorunurluk_SilinmisiKimseGormez(t *testing.T) {
	for _, izleyici := range []string{"s", "i"} {
		g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: izleyici, Gorunurluk: GorunurlukHerkes, Durum: DurumYayinda, Silindi: true}
		if DetayGorebilir(g) || ListedeGorebilir(g) {
			t.Errorf("%s silinmis yayini gormemeli", izleyici)
		}
	}
}
````

**Create** `core-service/internal/sosyal/depo_yardimci_test.go`:

````go
package sosyal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testYayin, ham SQL ile eklenecek bir yayin satiridir. Bos alanlar
// makul varsayilanlarla doldurulur; depo testleri boylece yayin ekleme
// ucunu beklemeden kurulabilir.
type testYayin struct {
	ID          string
	Slug        string
	SahipID     string
	Baslik      string
	Gorunurluk  string
	Durum       string
	AltyapiAcik bool
	KaynakID    string
	Olusturma   time.Time
	Silindi     bool
	Hazirlik    string
}

var slugSayaci int

func testYayinEkle(t *testing.T, havuz *pgxpool.Pool, y testYayin) string {
	t.Helper()
	if y.ID == "" {
		y.ID = uuid.NewString()
	}
	if y.Slug == "" {
		slugSayaci++
		y.Slug = fmt.Sprintf("T%07d", slugSayaci)
	}
	if y.Baslik == "" {
		y.Baslik = "Baslik"
	}
	if y.Gorunurluk == "" {
		y.Gorunurluk = GorunurlukHerkes
	}
	if y.Durum == "" {
		y.Durum = DurumYayinda
	}
	if y.Olusturma.IsZero() {
		y.Olusturma = time.Now()
	}
	if y.Hazirlik == "" {
		y.Hazirlik = `{"masterKey":"masters/u/p/m.wav","stems":[]}`
	}
	var kaynak any
	if y.KaynakID != "" {
		kaynak = y.KaynakID
	}
	var silinme any
	if y.Silindi {
		silinme = time.Now()
	}
	_, err := havuz.Exec(context.Background(), `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, tur, bpm, ton_tonik, ton_mod, sure_sn,
			gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik, kaynak_yayin_id,
			kaynak_baslik, kaynak_sahip_adi, durum, olusturma, silinme)
		VALUES ($1, $2, $3, $4, 'pop', 120, 'A', 'minor', 180, $5, $6, true,
			'{"bolumler":[]}', '[]', $7, $8, CASE WHEN $8::uuid IS NULL THEN NULL ELSE 'Eski baslik' END,
			CASE WHEN $8::uuid IS NULL THEN NULL ELSE 'eski_ad' END, $9, $10, $11)`,
		y.ID, y.Slug, y.SahipID, y.Baslik, y.Gorunurluk, y.AltyapiAcik, y.Hazirlik, kaynak, y.Durum, y.Olusturma, silinme)
	if err != nil {
		t.Fatalf("test yayini eklenemedi: %v", err)
	}
	return y.ID
}

func testTakipEkle(t *testing.T, havuz *pgxpool.Pool, takipci, takipEdilen string) {
	t.Helper()
	if _, err := havuz.Exec(context.Background(),
		`INSERT INTO sosyal.takip (takipci_id, takip_edilen_id) VALUES ($1, $2)`, takipci, takipEdilen); err != nil {
		t.Fatalf("takip eklenemedi: %v", err)
	}
}

func testProfilEkle(t *testing.T, d *PostgresDepo, id, ad string) {
	t.Helper()
	if _, err := d.ProfilKaydet(context.Background(), ProfilKaydi{KullaniciID: id, KullaniciAdi: ad, GorunenAd: ad}); err != nil {
		t.Fatalf("profil eklenemedi: %v", err)
	}
}
````

**Create** `core-service/internal/sosyal/depo_profil_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresDepo_ProfilKaydetVeGetir(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	ctx := context.Background()
	id := uuid.NewString()

	if _, err := d.ProfilGetir(ctx, id); !errors.Is(err, ErrProfilYok) {
		t.Fatalf("profil yokken ErrProfilYok bekleniyordu: %v", err)
	}
	k, err := d.ProfilKaydet(ctx, ProfilKaydi{KullaniciID: id, KullaniciAdi: "ali", GorunenAd: "Ali", Bio: "b"})
	if err != nil || k.KullaniciAdi != "ali" || k.Olusturma.IsZero() {
		t.Fatalf("kaydet = %+v, %v", k, err)
	}
	// Ikinci kayit gunceller (PUT hem olusturur hem gunceller).
	if _, err := d.ProfilKaydet(ctx, ProfilKaydi{KullaniciID: id, KullaniciAdi: "ali2", GorunenAd: "Ali Veli", Bio: ""}); err != nil {
		t.Fatalf("guncelle: %v", err)
	}
	k, err = d.ProfilGetir(ctx, id)
	if err != nil || k.KullaniciAdi != "ali2" || k.GorunenAd != "Ali Veli" || k.Bio != "" {
		t.Fatalf("getir = %+v, %v", k, err)
	}
	if _, err := d.ProfilAdlaGetir(ctx, "ali"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("eski ad bulunmamali: %v", err)
	}
	if k, err := d.ProfilAdlaGetir(ctx, "ali2"); err != nil || k.KullaniciID != id {
		t.Fatalf("adla getir = %+v, %v", k, err)
	}
}

func TestPostgresDepo_AlinmisAd(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, a, "ayse")
	_, err := d.ProfilKaydet(ctx, ProfilKaydi{KullaniciID: b, KullaniciAdi: "ayse", GorunenAd: "Ayse"})
	if !errors.Is(err, ErrKullaniciAdiAlinmis) {
		t.Fatalf("ErrKullaniciAdiAlinmis bekleniyordu: %v", err)
	}
	sahipler, err := d.AdSahipleri(ctx, []string{"ayse", "ayse2", "yok"})
	if err != nil || len(sahipler) != 1 || sahipler["ayse"] != a {
		t.Fatalf("AdSahipleri = %v, %v", sahipler, err)
	}
	if bos, err := d.AdSahipleri(ctx, nil); err != nil || len(bos) != 0 {
		t.Fatalf("bos liste = %v, %v", bos, err)
	}
}

// sarki/cover sayaci gorunurluk kuralini ve durum=yayinda'yi uygular.
func TestPostgresDepo_ProfilSayilari(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, takipci, yabanci := uuid.NewString(), uuid.NewString(), uuid.NewString()

	orijinal := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: GorunurlukHerkes})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: GorunurlukTakipciler})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHata})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Silindi: true})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, KaynakID: orijinal})
	testTakipEkle(t, havuz, takipci, sahip)
	testTakipEkle(t, havuz, sahip, yabanci)

	durumlar := []struct {
		ad        string
		izleyici  string
		sarki     int
		cover     int
		takipEder bool
	}{
		{"sahip", sahip, 3, 1, false},
		{"takipci", takipci, 2, 1, true},
		{"yabanci", yabanci, 1, 1, false},
	}
	for _, dr := range durumlar {
		s, err := d.ProfilSayilari(ctx, sahip, dr.izleyici)
		if err != nil {
			t.Fatalf("%s: %v", dr.ad, err)
		}
		if s.Istatistik.Sarki != dr.sarki || s.Istatistik.Cover != dr.cover || s.TakipEdiyorMuyum != dr.takipEder {
			t.Errorf("%s: %+v", dr.ad, s)
		}
		if s.Istatistik.Takipci != 1 || s.Istatistik.Takip != 1 {
			t.Errorf("%s takip sayaclari: %+v", dr.ad, s.Istatistik)
		}
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ -count=1
```

Expected: FAIL, build errors such as `undefined: KullaniciAdiGecerli`, `undefined: GorunurlukGirdisi`, `undefined: PostgresDepo`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/hatalar.go`:

````go
package sosyal

import (
	"errors"
	"fmt"
)

// Servis hatalari. HTTP katmani bunlari sozlesmedeki kucuk harfli
// kodlara cevirir (handler/sosyal.go): ErrBulunamadi -> 404 bulunamadi,
// ErrProfilYok -> 404 ya da 409 profil_yok (uca gore), vb. Mesajlar
// kullaniciya gidebilir; deger (URL, anahtar) tasimazlar.
var (
	ErrBulunamadi           = errors.New("bulunamadi")
	ErrProfilYok            = errors.New("profil henuz olusturulmamis")
	ErrGecersizKullaniciAdi = errors.New("gecersiz kullanici adi")
	ErrKullaniciAdiAlinmis  = errors.New("kullanici adi alinmis")
	ErrDogrulama            = errors.New("dogrulama hatasi")
	ErrSahiplik             = errors.New("dosya bu kullaniciya ait degil")
	ErrKaynakBulunamadi     = errors.New("kaynak yayin bulunamadi")
	ErrAltyapiKapali        = errors.New("kaynak yayinin altyapisi kapali")
	ErrStemYok              = errors.New("yayinin stem kopyasi yok")
	ErrDurum                = errors.New("yayin bu islem icin uygun durumda degil")
	ErrKimlik               = errors.New("kimlik gecersiz")
)

// dogrulamaHatasi, ErrDogrulama'yi alan bazli bir aciklamayla sarar.
func dogrulamaHatasi(mesaj string) error {
	return fmt.Errorf("%w: %s", ErrDogrulama, mesaj)
}
````

**Create** `core-service/internal/sosyal/model.go`:

````go
package sosyal

import "time"

// ProfilOzeti, sozlesme §4.1. avatarRengi saklanmaz, kullanici
// kimliginden turetilir (AvatarRengi).
type ProfilOzeti struct {
	KullaniciAdi string `json:"kullaniciAdi"`
	GorunenAd    string `json:"gorunenAd"`
	AvatarRengi  int    `json:"avatarRengi"`
}

// Istatistik, profildeki sayaclar. sarki/cover yalnizca izleyicinin
// gorebildigi, yayinda olan ve silinmemis yayinlari sayar.
type Istatistik struct {
	Sarki   int `json:"sarki"`
	Takipci int `json:"takipci"`
	Takip   int `json:"takip"`
	Cover   int `json:"cover"`
}

// Profil, sozlesme §4.1.
type Profil struct {
	ProfilOzeti
	Bio              string     `json:"bio"`
	Istatistik       Istatistik `json:"istatistik"`
	TakipEdiyorMuyum bool       `json:"takipEdiyorMuyum"`
	BenMiyim         bool       `json:"benMiyim"`
}

// ProfilGirdisi, PUT /profil/ben govdesi.
type ProfilGirdisi struct {
	KullaniciAdi string `json:"kullaniciAdi"`
	GorunenAd    string `json:"gorunenAd"`
	Bio          string `json:"bio"`
}

// AdUygunlugu, GET /profil/kullanici-adi/uygun yaniti. Oneri, ad
// alinmissa ve uygun bir sayi eki bulunduysa doludur; aksi halde null.
type AdUygunlugu struct {
	Uygun bool    `json:"uygun"`
	Oneri *string `json:"oneri"`
}

// ProfilKaydi, sosyal.profil satiri.
type ProfilKaydi struct {
	KullaniciID  string
	KullaniciAdi string
	GorunenAd    string
	Bio          string
	Olusturma    time.Time
}

// ProfilSayilari, bir profilin izleyiciye gore sayaclari.
type ProfilSayilari struct {
	Istatistik       Istatistik
	TakipEdiyorMuyum bool
}
````

**Create** `core-service/internal/sosyal/kurallar.go`:

````go
package sosyal

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// kullaniciAdiDeseni, spec §3: kucuk harf, rakam, alt cizgi, nokta;
// 3-20 karakter. Bas ve sondaki nokta ayrica reddedilir.
var kullaniciAdiDeseni = regexp.MustCompile(`^[a-z0-9_.]{3,20}$`)

const (
	kullaniciAdiEnUzun = 20
	gorunenAdEnUzun    = 40
	bioEnUzun          = 160
	avatarPaletBoyu    = 8
)

// KullaniciAdiGecerli, adin sozlesme kuralina uyup uymadigini soyler.
func KullaniciAdiGecerli(ad string) bool {
	return kullaniciAdiDeseni.MatchString(ad) &&
		!strings.HasPrefix(ad, ".") && !strings.HasSuffix(ad, ".")
}

// AvatarRengi, kullanici kimliginden 0-7 arasi sabit bir palet sirasi
// turetir: fnv32a(kullanici_id) % 8. Saklanmaz; ayni kimlik her zaman
// ayni rengi verir.
func AvatarRengi(kullaniciID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(kullaniciID))
	return int(h.Sum32() % avatarPaletBoyu)
}

// adAdaylari, alinmis bir ad icin sirayla denenecek onerileri uretir:
// ad2, ad3, ... ad99. Sonuc 20 karakteri asacaksa taban kirpilir.
func adAdaylari(ad string) []string {
	adaylar := make([]string, 0, 98)
	for n := 2; n <= 99; n++ {
		ek := strconv.Itoa(n)
		taban := ad
		if len(taban)+len(ek) > kullaniciAdiEnUzun {
			taban = taban[:kullaniciAdiEnUzun-len(ek)]
		}
		aday := taban + ek
		if KullaniciAdiGecerli(aday) {
			adaylar = append(adaylar, aday)
		}
	}
	return adaylar
}

// profilGirdisiniDuzenle, gorunen adi ve bio'yu kirpar ve kurallari
// uygular. Kullanici adi KIRPILMAZ ve kucuk harfe cevrilmez: mobil ayni
// deseni uygular; sessiz duzeltme kullaniciya gosterilenden farkli bir
// ad kaydederdi.
func profilGirdisiniDuzenle(g ProfilGirdisi) (ProfilGirdisi, error) {
	if !KullaniciAdiGecerli(g.KullaniciAdi) {
		return g, ErrGecersizKullaniciAdi
	}
	g.GorunenAd = strings.TrimSpace(g.GorunenAd)
	g.Bio = strings.TrimSpace(g.Bio)
	if n := utf8.RuneCountInString(g.GorunenAd); n < 1 || n > gorunenAdEnUzun {
		return g, dogrulamaHatasi("gorunenAd 1-40 karakter olmali")
	}
	if utf8.RuneCountInString(g.Bio) > bioEnUzun {
		return g, dogrulamaHatasi("bio en cok 160 karakter olabilir")
	}
	return g, nil
}

// kimlikCoz, bir kullanici ya da yayin kimliginin uuid oldugunu dogrular
// ve kanonik bicimini doner.
func kimlikCoz(ham string) (string, bool) {
	u, err := uuid.Parse(ham)
	if err != nil {
		return "", false
	}
	return u.String(), true
}
````

**Create** `core-service/internal/sosyal/gorunurluk.go`:

````go
package sosyal

import "fmt"

// Gorunurluk ve durum degerleri (sozlesme §3).
const (
	GorunurlukHerkes     = "herkes"
	GorunurlukTakipciler = "takipciler"
	GorunurlukLink       = "link"

	DurumHazirlaniyor = "hazirlaniyor"
	DurumYayinda      = "yayinda"
	DurumHata         = "hata"
)

// GorunurlukGirdisi, bir yayinin bir izleyiciye gorunup gorunmedigine
// karar vermek icin gereken her sey.
type GorunurlukGirdisi struct {
	SahipID     string
	IzleyiciID  string
	Gorunurluk  string
	Durum       string
	Silindi     bool
	TakipEdiyor bool // izleyici sahibi takip ediyor mu
}

// gorebilir, spec §4.9 tablosunun tek dogruluk kaynagidir:
//   - silinmis yayini kimse gormez (sahibi dahil; DELETE tekrari 404).
//   - sahibi kendi yayinini her durumda gorur (hazirlaniyor, hata dahil).
//   - digerleri yalnizca "yayinda" olani gorur: herkes her zaman,
//     takipciler yalnizca takip edenler, link yalnizca detayda.
//
// gorunurlukKosulu ayni kurali SQL'de kurar; ikisinin esitligi canli
// Postgres testinde tum kombinasyonlarla dogrulanir.
func gorebilir(g GorunurlukGirdisi, detay bool) bool {
	if g.Silindi {
		return false
	}
	if g.SahipID == g.IzleyiciID {
		return true
	}
	if g.Durum != DurumYayinda {
		return false
	}
	switch g.Gorunurluk {
	case GorunurlukHerkes:
		return true
	case GorunurlukTakipciler:
		return g.TakipEdiyor
	case GorunurlukLink:
		return detay
	}
	return false
}

// DetayGorebilir, id ya da slug ile acilan detay icin kuraldir.
func DetayGorebilir(g GorunurlukGirdisi) bool { return gorebilir(g, true) }

// ListedeGorebilir, profil listeleri ve sayaclar icin kuraldir; link
// yayinlari yalnizca sahibine listelenir.
func ListedeGorebilir(g GorunurlukGirdisi) bool { return gorebilir(g, false) }

// gorunurlukKosulu, gorebilir'in SQL karsiligidir. a yayin tablosunun
// takma adi, izleyici izleyici kimliginin parametresidir (orn. "$1").
// Ikisi de sabit dizgedir; kullanici girdisi buraya girmez.
func gorunurlukKosulu(a, izleyici string, detay bool) string {
	link := ""
	if detay {
		link = fmt.Sprintf(" OR %s.gorunurluk = 'link'", a)
	}
	return fmt.Sprintf(`(%[1]s.silinme IS NULL AND (%[1]s.sahip_id = %[2]s OR (%[1]s.durum = 'yayinda' AND (%[1]s.gorunurluk = 'herkes'%[3]s OR (%[1]s.gorunurluk = 'takipciler' AND EXISTS (SELECT 1 FROM sosyal.takip gt WHERE gt.takipci_id = %[2]s AND gt.takip_edilen_id = %[1]s.sahip_id))))))`,
		a, izleyici, link)
}
````

**Create** `core-service/internal/sosyal/depo.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProfilDeposu, sosyal.profil uzerindeki islemler.
type ProfilDeposu interface {
	// ProfilGetir, kullanicinin profilini doner; yoksa ErrProfilYok.
	ProfilGetir(ctx context.Context, kullaniciID string) (*ProfilKaydi, error)
	// ProfilAdlaGetir, kullanici adiyla profil doner; yoksa ErrBulunamadi.
	ProfilAdlaGetir(ctx context.Context, ad string) (*ProfilKaydi, error)
	// ProfilKaydet, profili olusturur ya da gunceller. Ad baskasindaysa
	// ErrKullaniciAdiAlinmis.
	ProfilKaydet(ctx context.Context, k ProfilKaydi) (*ProfilKaydi, error)
	// AdSahipleri, verilen adlardan alinmis olanlari ad -> kullanici id
	// eslemesi olarak doner.
	AdSahipleri(ctx context.Context, adlar []string) (map[string]string, error)
	// ProfilSayilari, sahibin sayaclarini izleyiciye gore doner.
	ProfilSayilari(ctx context.Context, sahipID, izleyiciID string) (*ProfilSayilari, error)
}

// Depo, sosyal modulunun tum kalici islemleridir.
type Depo interface {
	ProfilDeposu
}

// PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
// noktasinda uuid olarak dogrulanmis gelir (servis kimlikCoz kullanir).
type PostgresDepo struct {
	havuz *pgxpool.Pool
}

func NewPostgresDepo(havuz *pgxpool.Pool) *PostgresDepo {
	return &PostgresDepo{havuz: havuz}
}

var _ Depo = (*PostgresDepo)(nil)

// tekilIhlali, err bir UNIQUE ihlaliyse kisitin adini doner.
func tekilIhlali(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

const profilKolonlari = `kullanici_id::text, kullanici_adi, gorunen_ad, bio, olusturma`

func profilTara(satir pgx.Row) (*ProfilKaydi, error) {
	var k ProfilKaydi
	if err := satir.Scan(&k.KullaniciID, &k.KullaniciAdi, &k.GorunenAd, &k.Bio, &k.Olusturma); err != nil {
		return nil, err
	}
	return &k, nil
}

func (d *PostgresDepo) ProfilGetir(ctx context.Context, kullaniciID string) (*ProfilKaydi, error) {
	k, err := profilTara(d.havuz.QueryRow(ctx,
		`SELECT `+profilKolonlari+` FROM sosyal.profil WHERE kullanici_id = $1`, kullaniciID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProfilYok
	}
	if err != nil {
		return nil, fmt.Errorf("profil okunamadi: %w", err)
	}
	return k, nil
}

func (d *PostgresDepo) ProfilAdlaGetir(ctx context.Context, ad string) (*ProfilKaydi, error) {
	k, err := profilTara(d.havuz.QueryRow(ctx,
		`SELECT `+profilKolonlari+` FROM sosyal.profil WHERE kullanici_adi = $1`, ad))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("profil okunamadi: %w", err)
	}
	return k, nil
}

func (d *PostgresDepo) ProfilKaydet(ctx context.Context, k ProfilKaydi) (*ProfilKaydi, error) {
	kayit, err := profilTara(d.havuz.QueryRow(ctx, `
		INSERT INTO sosyal.profil (kullanici_id, kullanici_adi, gorunen_ad, bio)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (kullanici_id) DO UPDATE
		   SET kullanici_adi = EXCLUDED.kullanici_adi,
		       gorunen_ad    = EXCLUDED.gorunen_ad,
		       bio           = EXCLUDED.bio
		RETURNING `+profilKolonlari,
		k.KullaniciID, k.KullaniciAdi, k.GorunenAd, k.Bio))
	if _, ok := tekilIhlali(err); ok {
		return nil, ErrKullaniciAdiAlinmis
	}
	if err != nil {
		return nil, fmt.Errorf("profil kaydedilemedi: %w", err)
	}
	return kayit, nil
}

func (d *PostgresDepo) AdSahipleri(ctx context.Context, adlar []string) (map[string]string, error) {
	sahipler := make(map[string]string, len(adlar))
	if len(adlar) == 0 {
		return sahipler, nil
	}
	satirlar, err := d.havuz.Query(ctx,
		`SELECT kullanici_adi, kullanici_id::text FROM sosyal.profil WHERE kullanici_adi = ANY($1)`, adlar)
	if err != nil {
		return nil, fmt.Errorf("adlar okunamadi: %w", err)
	}
	defer satirlar.Close()
	for satirlar.Next() {
		var ad, sahip string
		if err := satirlar.Scan(&ad, &sahip); err != nil {
			return nil, fmt.Errorf("ad okunamadi: %w", err)
		}
		sahipler[ad] = sahip
	}
	return sahipler, satirlar.Err()
}

func (d *PostgresDepo) ProfilSayilari(ctx context.Context, sahipID, izleyiciID string) (*ProfilSayilari, error) {
	gorunur := gorunurlukKosulu("y", "$2", false)
	var s ProfilSayilari
	err := d.havuz.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM sosyal.yayin y
		    WHERE y.sahip_id = $1 AND y.kaynak_yayin_id IS NULL AND y.durum = 'yayinda' AND `+gorunur+`),
		  (SELECT count(*) FROM sosyal.yayin y
		    WHERE y.sahip_id = $1 AND y.kaynak_yayin_id IS NOT NULL AND y.durum = 'yayinda' AND `+gorunur+`),
		  (SELECT count(*) FROM sosyal.takip WHERE takip_edilen_id = $1),
		  (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1),
		  EXISTS (SELECT 1 FROM sosyal.takip WHERE takipci_id = $2 AND takip_edilen_id = $1)`,
		sahipID, izleyiciID,
	).Scan(&s.Istatistik.Sarki, &s.Istatistik.Cover, &s.Istatistik.Takipci, &s.Istatistik.Takip, &s.TakipEdiyorMuyum)
	if err != nil {
		return nil, fmt.Errorf("profil sayaclari okunamadi: %w", err)
	}
	return &s, nil
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/sosyal/ -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: All PASS, no SKIP: the visibility table, username, avatar (`fnv32a("6f1c2a9e-…") % 8 = 2`), suggestion and repository tests. Whole Go suite after this task: about 584 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/kurallar_test.go \
  core-service/internal/sosyal/gorunurluk_test.go \
  core-service/internal/sosyal/depo_yardimci_test.go \
  core-service/internal/sosyal/depo_profil_test.go \
  core-service/internal/sosyal/hatalar.go \
  core-service/internal/sosyal/model.go \
  core-service/internal/sosyal/kurallar.go \
  core-service/internal/sosyal/gorunurluk.go \
  core-service/internal/sosyal/depo.go
git commit -F - <<'EOF'
feat(sosyal): kurallar, gorunurluk ve profil deposu

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 5: Profile service and the `/api/social` profile endpoints

`Servis` with the profile use cases (R6) and the handler skeleton: the `SosyalServisi` interface, `Rotalar`, and the error mapper that turns service errors into the contract codes (R9, N10). The handler tests use a hand-written fake service; the service tests use a fake repository.

**Files:**

- Create: `core-service/internal/sosyal/sahte_depo_test.go`
- Create: `core-service/internal/sosyal/servis_profil_test.go`
- Create: `core-service/internal/handler/sosyal_test.go`
- Create: `core-service/internal/sosyal/servis.go`
- Create: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_profil.go`

**Interfaces:**
- Consumes: Everything from Task 4; `istekSahibi(c)` and `kullaniciOlarak(userID)` (existing, package `handler`); `response.Error`, `response.OK`, `response.Unauthorized`.
- Produces: `sosyal.Servis`, `sosyal.NewServis(depo Depo) *Servis` (Task 7 extends the signature), `kullaniciCoz(ham string) (string, error)`, `(*ProfilKaydi).ozet() ProfilOzeti`, methods `ProfilBen(ctx, kullaniciID)`, `ProfilKaydet(ctx, kullaniciID, ProfilGirdisi)`, `AdUygunMu(ctx, kullaniciID, ad)`, `ProfilGoster(ctx, izleyiciID, ad)`; `handler.SosyalServisi`, `handler.SosyalHandler`, `handler.NewSosyalHandler(SosyalServisi) *SosyalHandler`, `(*SosyalHandler).Rotalar(r fiber.Router, yayinLimiti fiber.Handler)`, `sosyalHata(c, err) error`, `govdeHatasi(c) error`; test helpers `sahteDepo`/`yeniSahteDepo()` (package `sosyal`), `sahteSosyal`, `sosyalUygulamasi(s, userID)`, `sosyalIstek(t, app, yontem, yol, govde) (int, map[string]any)`, `hataKodu(govde)` (package `handler`).

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/sahte_depo_test.go`:

````go
package sosyal

import (
	"context"
	"time"
)

// sahteDepo, servis testleri icin bellek ici Depo'dur. Yalnizca servisin
// kararlarini sinamaya yetecek kadar davranir; SQL davranisi canli
// Postgres testlerinde sinanir.
type sahteDepo struct {
	profiller map[string]*ProfilKaydi // kullanici id -> kayit
	sayilar   ProfilSayilari
	hata      error // dolu ise her cagri bu hatayla doner
}

func yeniSahteDepo() *sahteDepo {
	return &sahteDepo{profiller: map[string]*ProfilKaydi{}}
}

func (d *sahteDepo) ProfilGetir(_ context.Context, id string) (*ProfilKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.profiller[id]
	if !ok {
		return nil, ErrProfilYok
	}
	return k, nil
}

func (d *sahteDepo) ProfilAdlaGetir(_ context.Context, ad string) (*ProfilKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for _, k := range d.profiller {
		if k.KullaniciAdi == ad {
			return k, nil
		}
	}
	return nil, ErrBulunamadi
}

func (d *sahteDepo) ProfilKaydet(_ context.Context, k ProfilKaydi) (*ProfilKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for id, v := range d.profiller {
		if v.KullaniciAdi == k.KullaniciAdi && id != k.KullaniciID {
			return nil, ErrKullaniciAdiAlinmis
		}
	}
	k.Olusturma = time.Now()
	d.profiller[k.KullaniciID] = &k
	return &k, nil
}

func (d *sahteDepo) AdSahipleri(_ context.Context, adlar []string) (map[string]string, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	m := map[string]string{}
	for _, ad := range adlar {
		for id, k := range d.profiller {
			if k.KullaniciAdi == ad {
				m[ad] = id
			}
		}
	}
	return m, nil
}

func (d *sahteDepo) ProfilSayilari(context.Context, string, string) (*ProfilSayilari, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	s := d.sayilar
	return &s, nil
}
````

**Create** `core-service/internal/sosyal/servis_profil_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"testing"
)

const (
	kullaniciA = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	kullaniciB = "7f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func TestServis_ProfilBenYoksaProfilYok(t *testing.T) {
	s := NewServis(yeniSahteDepo())
	if _, err := s.ProfilBen(context.Background(), kullaniciA); !errors.Is(err, ErrProfilYok) {
		t.Fatalf("ErrProfilYok bekleniyordu: %v", err)
	}
}

func TestServis_GecersizKimlik(t *testing.T) {
	s := NewServis(yeniSahteDepo())
	if _, err := s.ProfilBen(context.Background(), "kullanici-a"); !errors.Is(err, ErrKimlik) {
		t.Fatalf("ErrKimlik bekleniyordu: %v", err)
	}
}

func TestServis_ProfilKaydetVeBen(t *testing.T) {
	d := yeniSahteDepo()
	d.sayilar = ProfilSayilari{Istatistik: Istatistik{Sarki: 2, Takipci: 5}}
	s := NewServis(d)
	p, err := s.ProfilKaydet(context.Background(), kullaniciA, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: " Ali ", Bio: "selam"})
	if err != nil {
		t.Fatalf("kaydet: %v", err)
	}
	if p.KullaniciAdi != "ali" || p.GorunenAd != "Ali" || !p.BenMiyim || p.TakipEdiyorMuyum || p.Istatistik.Sarki != 2 {
		t.Fatalf("profil = %+v", p)
	}
	if p.AvatarRengi != AvatarRengi(kullaniciA) {
		t.Fatalf("avatar rengi = %d", p.AvatarRengi)
	}
	ben, err := s.ProfilBen(context.Background(), kullaniciA)
	if err != nil || ben.KullaniciAdi != "ali" {
		t.Fatalf("ben = %+v, %v", ben, err)
	}
}

func TestServis_ProfilKaydetHatalari(t *testing.T) {
	d := yeniSahteDepo()
	s := NewServis(d)
	ctx := context.Background()
	if _, err := s.ProfilKaydet(ctx, kullaniciA, ProfilGirdisi{KullaniciAdi: ".ali", GorunenAd: "A"}); !errors.Is(err, ErrGecersizKullaniciAdi) {
		t.Fatalf("gecersiz ad: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, kullaniciA, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: ""}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bos gorunen ad: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, kullaniciA, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "A"}); err != nil {
		t.Fatalf("ilk kayit: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, kullaniciB, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "B"}); !errors.Is(err, ErrKullaniciAdiAlinmis) {
		t.Fatalf("alinmis ad: %v", err)
	}
}

func TestServis_AdUygunMu(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali"}
	d.profiller[kullaniciB] = &ProfilKaydi{KullaniciID: kullaniciB, KullaniciAdi: "ali2"}
	s := NewServis(d)
	ctx := context.Background()

	bos, err := s.AdUygunMu(ctx, kullaniciB, "veli")
	if err != nil || !bos.Uygun || bos.Oneri != nil {
		t.Fatalf("bos ad = %+v, %v", bos, err)
	}
	kendi, err := s.AdUygunMu(ctx, kullaniciA, "ali")
	if err != nil || !kendi.Uygun {
		t.Fatalf("kendi adi uygun olmali: %+v, %v", kendi, err)
	}
	// ali (A) ve ali2 (B) alinmis: C icin oneri ali3.
	alinmis, err := s.AdUygunMu(ctx, "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", "ali")
	if err != nil || alinmis.Uygun || alinmis.Oneri == nil || *alinmis.Oneri != "ali3" {
		t.Fatalf("alinmis ad = %+v, %v", alinmis, err)
	}
	// B icin ali2 kendi adi oldugundan oneri ali2'dir.
	bIcin, err := s.AdUygunMu(ctx, kullaniciB, "ali")
	if err != nil || bIcin.Oneri == nil || *bIcin.Oneri != "ali2" {
		t.Fatalf("B icin oneri = %+v, %v", bIcin, err)
	}
	if _, err := s.AdUygunMu(ctx, kullaniciA, "A!"); !errors.Is(err, ErrGecersizKullaniciAdi) {
		t.Fatalf("gecersiz ad: %v", err)
	}
}

func TestServis_ProfilGoster(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali", GorunenAd: "Ali"}
	d.sayilar = ProfilSayilari{TakipEdiyorMuyum: true}
	s := NewServis(d)
	ctx := context.Background()
	p, err := s.ProfilGoster(ctx, kullaniciB, "ali")
	if err != nil || p.BenMiyim || !p.TakipEdiyorMuyum {
		t.Fatalf("profil = %+v, %v", p, err)
	}
	if _, err := s.ProfilGoster(ctx, kullaniciB, "yok"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan profil: %v", err)
	}
	if _, err := s.ProfilGoster(ctx, kullaniciB, "X"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gecersiz bicimli ad: %v", err)
	}
}
````

**Create** `core-service/internal/handler/sosyal_test.go`:

````go
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
)

const sosyalKullanici = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

// sahteSosyal, SosyalServisi'nin elle yazilmis sahtesidir. Her alan
// ilgili metodun ne donecegini belirler; son cagrinin girdileri kaydedilir.
type sahteSosyal struct {
	hata error

	profil      *sosyal.Profil
	adUygunlugu *sosyal.AdUygunlugu

	sonKullanici string
	sonAd        string
	sonProfil    sosyal.ProfilGirdisi
}

func (s *sahteSosyal) ProfilBen(_ context.Context, k string) (*sosyal.Profil, error) {
	s.sonKullanici = k
	return s.profil, s.hata
}

func (s *sahteSosyal) ProfilKaydet(_ context.Context, k string, g sosyal.ProfilGirdisi) (*sosyal.Profil, error) {
	s.sonKullanici, s.sonProfil = k, g
	return s.profil, s.hata
}

func (s *sahteSosyal) AdUygunMu(_ context.Context, k, ad string) (*sosyal.AdUygunlugu, error) {
	s.sonKullanici, s.sonAd = k, ad
	return s.adUygunlugu, s.hata
}

func (s *sahteSosyal) ProfilGoster(_ context.Context, k, ad string) (*sosyal.Profil, error) {
	s.sonKullanici, s.sonAd = k, ad
	return s.profil, s.hata
}

// sosyalUygulamasi, /api/social grubunu sahte servisle kurar.
func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
	app := fiber.New()
	grup := app.Group("/api/social", kullaniciOlarak(userID))
	NewSosyalHandler(s).Rotalar(grup, func(c *fiber.Ctx) error { return c.Next() })
	return app
}

// sosyalIstek, istegi atar; durum kodunu ve cozulmus JSON govdesini doner.
func sosyalIstek(t *testing.T, app *fiber.App, yontem, yol, govde string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if govde != "" {
		r = strings.NewReader(govde)
	}
	istek := httptest.NewRequest(yontem, yol, r)
	if govde != "" {
		istek.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(istek)
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	ham, _ := io.ReadAll(resp.Body)
	var cikti map[string]any
	if len(ham) > 0 {
		if err := json.Unmarshal(ham, &cikti); err != nil {
			t.Fatalf("govde JSON degil (%d): %s", resp.StatusCode, ham)
		}
	}
	return resp.StatusCode, cikti
}

func hataKodu(govde map[string]any) string {
	e, _ := govde["error"].(map[string]any)
	kod, _ := e["code"].(string)
	return kod
}

func TestSosyal_ProfilBenYoksa404ProfilYok(t *testing.T) {
	s := &sahteSosyal{hata: sosyal.ErrProfilYok}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/ben", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "profil_yok" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
	if s.sonKullanici != sosyalKullanici {
		t.Fatalf("kullanici iletilmedi: %q", s.sonKullanici)
	}
}

func TestSosyal_ProfilBenSozlesmeAlanlari(t *testing.T) {
	s := &sahteSosyal{profil: &sosyal.Profil{
		ProfilOzeti: sosyal.ProfilOzeti{KullaniciAdi: "ali", GorunenAd: "Ali", AvatarRengi: 3},
		Bio:         "b", BenMiyim: true,
	}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/ben", "")
	if durum != http.StatusOK {
		t.Fatalf("durum=%d", durum)
	}
	for _, alan := range []string{"kullaniciAdi", "gorunenAd", "avatarRengi", "bio", "istatistik", "takipEdiyorMuyum", "benMiyim"} {
		if _, ok := govde[alan]; !ok {
			t.Errorf("%s alani yok: %v", alan, govde)
		}
	}
	ist := govde["istatistik"].(map[string]any)
	for _, alan := range []string{"sarki", "takipci", "takip", "cover"} {
		if _, ok := ist[alan]; !ok {
			t.Errorf("istatistik.%s yok", alan)
		}
	}
}

func TestSosyal_ProfilKaydetHataKodlari(t *testing.T) {
	durumlar := []struct {
		hata  error
		durum int
		kod   string
	}{
		{sosyal.ErrKullaniciAdiAlinmis, http.StatusConflict, "kullanici_adi_alinmis"},
		{sosyal.ErrGecersizKullaniciAdi, http.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
		{fmtDogrulama("gorunenAd"), http.StatusUnprocessableEntity, "dogrulama"},
		{errors.New("pg kapali"), http.StatusInternalServerError, "sunucu_hatasi"},
	}
	for _, d := range durumlar {
		s := &sahteSosyal{hata: d.hata}
		durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/profil/ben",
			`{"kullaniciAdi":"ali","gorunenAd":"Ali","bio":""}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}

func fmtDogrulama(alan string) error { return errors.Join(sosyal.ErrDogrulama, errors.New(alan)) }

func TestSosyal_ProfilKaydetGovdeyiIletir(t *testing.T) {
	s := &sahteSosyal{profil: &sosyal.Profil{}}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/profil/ben",
		`{"kullaniciAdi":"ali","gorunenAd":"Ali","bio":"selam"}`)
	if durum != http.StatusOK || s.sonProfil != (sosyal.ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "Ali", Bio: "selam"}) {
		t.Fatalf("durum=%d girdi=%+v", durum, s.sonProfil)
	}
}

func TestSosyal_BozukGovde422(t *testing.T) {
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{}, sosyalKullanici), http.MethodPut, "/api/social/profil/ben", `{"kullaniciAdi":`)
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "dogrulama" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_AdUygunOneriNullVeyaDolu(t *testing.T) {
	s := &sahteSosyal{adUygunlugu: &sosyal.AdUygunlugu{Uygun: true}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profil/kullanici-adi/uygun?ad=ali", "")
	if durum != http.StatusOK || govde["uygun"] != true || s.sonAd != "ali" {
		t.Fatalf("durum=%d govde=%v ad=%q", durum, govde, s.sonAd)
	}
	if oneri, var_ := govde["oneri"]; !var_ || oneri != nil {
		t.Fatalf("oneri alani null olarak bulunmali: %v", govde)
	}
}

func TestSosyal_ProfilYok404Bulunamadi(t *testing.T) {
	s := &sahteSosyal{hata: sosyal.ErrBulunamadi}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profiller/yok", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" || s.sonAd != "yok" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_KimliksizIstek401(t *testing.T) {
	durum, _ := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{}, ""), http.MethodGet, "/api/social/profil/ben", "")
	if durum != http.StatusUnauthorized {
		t.Fatalf("durum=%d", durum)
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors `undefined: NewServis` (sosyal) and `undefined: NewSosyalHandler` (handler).

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/servis.go`:

````go
package sosyal

import (
	"context"
	"errors"
)

// Servis, Kesfet uclarinin is kurallarini uygular. Gorunurluk detayda
// burada (DetayGorebilir), listelerde depoda (gorunurlukKosulu) uygulanir.
type Servis struct {
	depo Depo
}

func NewServis(depo Depo) *Servis {
	return &Servis{depo: depo}
}

// kullaniciCoz, istegi yapanin kimligini dogrular. Auth middleware her
// zaman uuid yazar; aksi bir yapilandirma hatasidir ve 401'e doner.
func kullaniciCoz(ham string) (string, error) {
	id, ok := kimlikCoz(ham)
	if !ok {
		return "", ErrKimlik
	}
	return id, nil
}

// ozet, kaydi ProfilOzeti'ne cevirir.
func (k *ProfilKaydi) ozet() ProfilOzeti {
	return ProfilOzeti{
		KullaniciAdi: k.KullaniciAdi,
		GorunenAd:    k.GorunenAd,
		AvatarRengi:  AvatarRengi(k.KullaniciID),
	}
}

// profilKur, kaydi izleyiciye gore tam Profil'e cevirir.
func (s *Servis) profilKur(ctx context.Context, k *ProfilKaydi, izleyiciID string) (*Profil, error) {
	sayilar, err := s.depo.ProfilSayilari(ctx, k.KullaniciID, izleyiciID)
	if err != nil {
		return nil, err
	}
	return &Profil{
		ProfilOzeti:      k.ozet(),
		Bio:              k.Bio,
		Istatistik:       sayilar.Istatistik,
		TakipEdiyorMuyum: sayilar.TakipEdiyorMuyum,
		BenMiyim:         k.KullaniciID == izleyiciID,
	}, nil
}

// ProfilBen, istegi yapanin profilini doner; yoksa ErrProfilYok.
func (s *Servis) ProfilBen(ctx context.Context, kullaniciID string) (*Profil, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.depo.ProfilGetir(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.profilKur(ctx, k, id)
}

// ProfilKaydet, profili olusturur ya da gunceller (PUT /profil/ben).
func (s *Servis) ProfilKaydet(ctx context.Context, kullaniciID string, g ProfilGirdisi) (*Profil, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	g, err = profilGirdisiniDuzenle(g)
	if err != nil {
		return nil, err
	}
	k, err := s.depo.ProfilKaydet(ctx, ProfilKaydi{
		KullaniciID: id, KullaniciAdi: g.KullaniciAdi, GorunenAd: g.GorunenAd, Bio: g.Bio,
	})
	if err != nil {
		return nil, err
	}
	return s.profilKur(ctx, k, id)
}

// AdUygunMu, adin istegi yapan icin alinabilir olup olmadigini soyler.
// Kendi adi uygundur. Alinmissa ad2..ad99 arasindan ilk bos olani onerir.
func (s *Servis) AdUygunMu(ctx context.Context, kullaniciID, ad string) (*AdUygunlugu, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if !KullaniciAdiGecerli(ad) {
		return nil, ErrGecersizKullaniciAdi
	}
	sahipler, err := s.depo.AdSahipleri(ctx, []string{ad})
	if err != nil {
		return nil, err
	}
	if sahip, alinmis := sahipler[ad]; !alinmis || sahip == id {
		return &AdUygunlugu{Uygun: true}, nil
	}
	adaylar := adAdaylari(ad)
	sahipler, err = s.depo.AdSahipleri(ctx, adaylar)
	if err != nil {
		return nil, err
	}
	for _, aday := range adaylar {
		if sahip, alinmis := sahipler[aday]; !alinmis || sahip == id {
			return &AdUygunlugu{Uygun: false, Oneri: &aday}, nil
		}
	}
	return &AdUygunlugu{Uygun: false}, nil
}

// ProfilGoster, kullanici adiyla bir profili izleyiciye gore doner.
// Gecersiz bicimli ad ErrBulunamadi'dir: boyle bir profil olamaz.
func (s *Servis) ProfilGoster(ctx context.Context, izleyiciID, ad string) (*Profil, error) {
	id, err := kullaniciCoz(izleyiciID)
	if err != nil {
		return nil, err
	}
	if !KullaniciAdiGecerli(ad) {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.ProfilAdlaGetir(ctx, ad)
	if err != nil {
		if errors.Is(err, ErrProfilYok) {
			return nil, ErrBulunamadi
		}
		return nil, err
	}
	return s.profilKur(ctx, k, id)
}
````

**Create** `core-service/internal/handler/sosyal.go`:

````go
package handler

import (
	"context"
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// SosyalServisi, Kesfet uclarinin kullandigi servis yuzudur
// (*sosyal.Servis uygular). Arayuz, handler testlerinin servisi elle
// yazilmis bir sahteyle degistirebilmesi icindir.
type SosyalServisi interface {
	ProfilBen(ctx context.Context, kullaniciID string) (*sosyal.Profil, error)
	ProfilKaydet(ctx context.Context, kullaniciID string, g sosyal.ProfilGirdisi) (*sosyal.Profil, error)
	AdUygunMu(ctx context.Context, kullaniciID, ad string) (*sosyal.AdUygunlugu, error)
	ProfilGoster(ctx context.Context, izleyiciID, ad string) (*sosyal.Profil, error)
}

var _ SosyalServisi = (*sosyal.Servis)(nil)

// SosyalHandler, /api/social altindaki uclardir.
type SosyalHandler struct {
	servis SosyalServisi
}

func NewSosyalHandler(servis SosyalServisi) *SosyalHandler {
	return &SosyalHandler{servis: servis}
}

// Rotalar, uclari verilen gruba baglar. yayinLimiti yalnizca
// POST /yayinlar'a uygulanir (kullanici basina saatlik).
func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
	r.Get("/profil/ben", h.ProfilBen)
	r.Put("/profil/ben", h.ProfilKaydet)
	r.Get("/profil/kullanici-adi/uygun", h.AdUygun)
	r.Get("/profiller/:ad", h.Profil)
}

// sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
// Sira onemlidir: ilk eslesen kazanir.
var sosyalHatalari = []struct {
	hata  error
	durum int
	kod   string
}{
	{sosyal.ErrBulunamadi, fiber.StatusNotFound, "bulunamadi"},
	{sosyal.ErrProfilYok, fiber.StatusConflict, "profil_yok"},
	{sosyal.ErrGecersizKullaniciAdi, fiber.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
	{sosyal.ErrKullaniciAdiAlinmis, fiber.StatusConflict, "kullanici_adi_alinmis"},
	{sosyal.ErrDogrulama, fiber.StatusUnprocessableEntity, "dogrulama"},
	{sosyal.ErrSahiplik, fiber.StatusForbidden, "sahiplik"},
	{sosyal.ErrKaynakBulunamadi, fiber.StatusNotFound, "kaynak_bulunamadi"},
	{sosyal.ErrAltyapiKapali, fiber.StatusForbidden, "altyapi_kapali"},
	{sosyal.ErrStemYok, fiber.StatusUnprocessableEntity, "stem_yok"},
	{sosyal.ErrDurum, fiber.StatusConflict, "durum"},
}

// sosyalHata, servis hatasini sozlesme govdesine cevirir:
// {"error":{"code","message"}}. Taninmayan hata 500 sunucu_hatasi olur
// ve ayrintisi yalnizca loga yazilir.
func sosyalHata(c *fiber.Ctx, err error) error {
	if errors.Is(err, sosyal.ErrKimlik) {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	for _, e := range sosyalHatalari {
		if errors.Is(err, e.hata) {
			return response.Error(c, e.durum, e.kod, err.Error(), nil)
		}
	}
	log.Printf("sosyal: %s %s: %v", c.Method(), c.Route().Path, err)
	return response.Error(c, fiber.StatusInternalServerError, "sunucu_hatasi", "Islem tamamlanamadi", nil)
}

// govdeHatasi, cozulemeyen JSON govdesidir: sosyal uclarda dogrulama
// hatasi 400 degil 422 dogrulama'dir.
func govdeHatasi(c *fiber.Ctx) error {
	return response.Error(c, fiber.StatusUnprocessableEntity, "dogrulama", "Gecersiz istek govdesi", nil)
}
````

**Create** `core-service/internal/handler/sosyal_profil.go`:

````go
package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// ProfilBen handles GET /api/social/profil/ben. Profil yoksa 404
// profil_yok (mobil bunu "henuz profil yok" olarak okur).
func (h *SosyalHandler) ProfilBen(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	p, err := h.servis.ProfilBen(c.Context(), userID)
	if errors.Is(err, sosyal.ErrProfilYok) {
		return response.Error(c, fiber.StatusNotFound, "profil_yok", err.Error(), nil)
	}
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, p)
}

// ProfilKaydet handles PUT /api/social/profil/ben.
func (h *SosyalHandler) ProfilKaydet(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var g sosyal.ProfilGirdisi
	if err := c.BodyParser(&g); err != nil {
		return govdeHatasi(c)
	}
	p, err := h.servis.ProfilKaydet(c.Context(), userID, g)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, p)
}

// AdUygun handles GET /api/social/profil/kullanici-adi/uygun?ad=
func (h *SosyalHandler) AdUygun(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	u, err := h.servis.AdUygunMu(c.Context(), userID, c.Query("ad"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, u)
}

// Profil handles GET /api/social/profiller/:ad
func (h *SosyalHandler) Profil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	p, err := h.servis.ProfilGoster(c.Context(), userID, c.Params("ad"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, p)
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: both packages `ok`. Whole Go suite after this task: about 598 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/sosyal/servis_profil_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/sosyal/servis.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_profil.go
git commit -F - <<'EOF'
feat(sosyal): profil servisi ve /api/social profil uclari

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 6: Publication types, slug and the publication repository

The publication half of the contract types (N2), slug generation, and the SQL for detail (with counts and the `kaynak` snapshot), insert, PATCH, soft delete, retry and the worker's hazirlik operations. `TestPostgresDepo_GorunurlukSQLVeGoAyni` proves the SQL predicate and the Go rule agree on every combination (N15). The fake repository grows the same methods for the service tests of Task 7.

**Files:**

- Create: `core-service/internal/sosyal/slug_test.go`
- Create: `core-service/internal/sosyal/depo_yayin_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go`
- Create: `core-service/internal/sosyal/model_yayin.go`
- Create: `core-service/internal/sosyal/slug.go`
- Create: `core-service/internal/sosyal/depo_yayin.go`
- Modify: `core-service/internal/sosyal/depo.go`

**Interfaces:**
- Consumes: `PostgresDepo`, `tekilIhlali`, `gorebilir`, `gorunurlukKosulu`, `AvatarRengi`, fixtures (Task 4).
- Produces: Types `Ton`, `YapiOgesi`, `SozBolumu`, `Sozler`, `KaynakOzeti`, `YayinOzeti`, `YayinDetayi`, `StemGirdisi`, `YayinIstegi`, `YayinYaniti`, `YayinYamasi`, `DosyaAdresi`, `YayinDosyalari`, `HazirlikStemi`, `Hazirlik`, `StemKaydi`, `YeniYayin`, `YayinKaydi` (embeds `YayinDetayi`; `SahipID`, `KaynakYayinID`, `StemVar`, `MasterAnahtari`, `Silindi`, `TakipEdiyor`; method `gorunurlukGirdisi(izleyiciID)`), `HazirlikKaydi`, `HazirlikSonucu`; `SlugGecerli(string) bool`, `slugUret() (string, error)`, `errSlugCakisti`; `type YayinDeposu interface` (`YayinEkle`, `YayinGetir`, `YayinSlugIleGetir`, `YayinGuncelle`, `YayinSil`, `YayinKaldir`, `TekrarDenemeyeAl`); `type HazirlikDeposu interface` (`HazirlikGetir`, `Yayinla`, `HataYap`); `Depo` now embeds both; SQL fragments `ozetSecimi`, `ozetKaynagi` and `ozetTarayici` (reused by Task 9).

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/slug_test.go`:

````go
package sosyal

import "testing"

func TestSlugUret_BicimVeCesitlilik(t *testing.T) {
	gorulen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := slugUret()
		if err != nil {
			t.Fatalf("slugUret: %v", err)
		}
		if !SlugGecerli(s) {
			t.Fatalf("gecersiz slug %q", s)
		}
		gorulen[s] = true
	}
	if len(gorulen) < 199 {
		t.Fatalf("slug'lar tekrar ediyor: %d farkli", len(gorulen))
	}
}

func TestSlugGecerli(t *testing.T) {
	for _, s := range []string{"", "abc", "abcdefg!", "abcdefghi", "abcd-efg"} {
		if SlugGecerli(s) {
			t.Errorf("%q gecersiz olmali", s)
		}
	}
	if !SlugGecerli("Ab3dEf9h") {
		t.Error("Ab3dEf9h gecerli olmali")
	}
}
````

**Create** `core-service/internal/sosyal/depo_yayin_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func ornekYeniYayin(sahipID string) YeniYayin {
	return YeniYayin{
		ID: uuid.NewString(), Slug: "Ab3dEf9h", SahipID: sahipID,
		Baslik: "Gece", Aciklama: "aciklama", Tur: "pop", Bpm: 120,
		Ton: Ton{Tonik: "A", Mod: "minor"}, SureSn: 184,
		Gorunurluk: GorunurlukHerkes, AltyapiAcik: true, YorumAcik: true,
		Sozler: Sozler{Bolumler: []SozBolumu{{BolumID: "b1", Tur: "verse", Satirlar: []string{"bir", "iki"}}}},
		Yapi:   []YapiOgesi{{ID: "s1", Tur: "verse", Olcu: 8}},
		Hazirlik: Hazirlik{MasterKey: "masters/u/p/m.wav", Stems: []HazirlikStemi{
			{ID: "s1", Enstruman: "drums", Sure: 184, Key: "stems/u/p/s1.mp3"},
		}},
	}
}

func TestPostgresDepo_YayinEkleVeGetir(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	if err := d.YayinEkle(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	k, err := d.YayinGetir(ctx, y.ID, sahip)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if k.Baslik != "Gece" || k.Durum != DurumHazirlaniyor || !k.BenimMi || k.Gorunurluk != GorunurlukHerkes {
		t.Fatalf("kayit = %+v", k)
	}
	if k.Sahip.KullaniciAdi != "sahip" || k.Sahip.AvatarRengi != AvatarRengi(sahip) {
		t.Fatalf("sahip = %+v", k.Sahip)
	}
	if k.Ton != (Ton{Tonik: "A", Mod: "minor"}) || k.SureSn != 184 || k.Bpm != 120 {
		t.Fatalf("muzik alanlari = %+v", k.YayinOzeti)
	}
	if k.DalgaFormu == nil || len(k.DalgaFormu) != 0 || k.Kaynak != nil {
		t.Fatalf("dalga formu bos dizi, kaynak nil olmali: %v %v", k.DalgaFormu, k.Kaynak)
	}
	if len(k.Sozler.Bolumler) != 1 || k.Sozler.Bolumler[0].Satirlar[1] != "iki" || len(k.Yapi) != 1 || k.Yapi[0].Olcu != 8 {
		t.Fatalf("sozler/yapi = %+v %+v", k.Sozler, k.Yapi)
	}
	if !k.StemVar || k.MasterAnahtari != "" || k.Silindi || k.Olusturma.Location().String() != "UTC" {
		t.Fatalf("ic alanlar = stem %v master %q silindi %v", k.StemVar, k.MasterAnahtari, k.Silindi)
	}
	if s, err := d.YayinSlugIleGetir(ctx, "Ab3dEf9h", uuid.NewString()); err != nil || s.ID != y.ID || s.BenimMi {
		t.Fatalf("slug ile = %+v, %v", s, err)
	}
	if _, err := d.YayinGetir(ctx, uuid.NewString(), sahip); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan yayin: %v", err)
	}
}

func TestPostgresDepo_SlugCakismasi(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	if err := d.YayinEkle(ctx, ornekYeniYayin(sahip)); err != nil {
		t.Fatalf("ilk: %v", err)
	}
	if err := d.YayinEkle(ctx, ornekYeniYayin(sahip)); !errors.Is(err, errSlugCakisti) {
		t.Fatalf("errSlugCakisti bekleniyordu: %v", err)
	}
}

// SQL kosulu ile Go kurali her kombinasyonda ayni karari vermeli.
func TestPostgresDepo_GorunurlukSQLVeGoAyni(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, takipci, yabanci := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testTakipEkle(t, havuz, takipci, sahip)

	var yayinlar []string
	for _, gr := range []string{GorunurlukHerkes, GorunurlukTakipciler, GorunurlukLink} {
		for _, durum := range []string{DurumHazirlaniyor, DurumYayinda, DurumHata} {
			for _, silindi := range []bool{false, true} {
				yayinlar = append(yayinlar, testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: gr, Durum: durum, Silindi: silindi}))
			}
		}
	}
	for _, izleyici := range []string{sahip, takipci, yabanci} {
		for _, id := range yayinlar {
			k, err := d.YayinGetir(ctx, id, izleyici)
			if err != nil {
				t.Fatalf("YayinGetir: %v", err)
			}
			for _, detay := range []bool{true, false} {
				var sqlKarari bool
				if err := havuz.QueryRow(ctx, fmt.Sprintf(
					`SELECT EXISTS (SELECT 1 FROM sosyal.yayin y WHERE y.id = $2 AND %s)`,
					gorunurlukKosulu("y", "$1", detay)), izleyici, id).Scan(&sqlKarari); err != nil {
					t.Fatalf("kosul: %v", err)
				}
				goKarari := gorebilir(k.gorunurlukGirdisi(izleyici), detay)
				if sqlKarari != goKarari {
					t.Errorf("izleyici %s yayin %s/%s silindi=%v detay=%v: SQL %v, Go %v",
						izleyici[:4], k.Gorunurluk, k.Durum, k.Silindi, detay, sqlKarari, goKarari)
				}
			}
		}
	}
}

func TestPostgresDepo_SayaclarVeKaynak(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, coverci, izleyici := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testProfilEkle(t, d, coverci, "coverci")
	orijinal := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Baslik: "Orijinal", AltyapiAcik: true})
	cover := testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Durum: DurumHata})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Silindi: true})
	for _, kisi := range []string{izleyici, coverci} {
		if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)`, kisi, orijinal); err != nil {
			t.Fatalf("begeni: %v", err)
		}
	}
	if _, err := havuz.Exec(ctx, `
		INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, silinme)
		VALUES (gen_random_uuid(), $1, $2, 'a', NULL), (gen_random_uuid(), $1, $2, 'b', now())`, orijinal, izleyici); err != nil {
		t.Fatalf("yorum: %v", err)
	}

	k, err := d.YayinGetir(ctx, orijinal, izleyici)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	// Cover sayaci: yayinda ve silinmemis (gorunurluk fark etmez) = 2.
	if k.BegeniSayisi != 2 || k.YorumSayisi != 1 || k.CoverSayisi != 2 || !k.BegendimMi {
		t.Fatalf("sayaclar = begeni %d yorum %d cover %d begendim %v", k.BegeniSayisi, k.YorumSayisi, k.CoverSayisi, k.BegendimMi)
	}

	c, err := d.YayinGetir(ctx, cover, izleyici)
	if err != nil || c.Kaynak == nil {
		t.Fatalf("cover = %+v, %v", c, err)
	}
	if *c.Kaynak != (KaynakOzeti{YayinID: orijinal, Baslik: "Orijinal", SahipKullaniciAdi: "sahip", Silindi: false}) {
		t.Fatalf("canli kaynak = %+v", *c.Kaynak)
	}
	if c.KaynakYayinID != orijinal {
		t.Fatalf("KaynakYayinID = %q", c.KaynakYayinID)
	}

	// Yumusak silinen orijinal: son degerler + silindi.
	if err := d.YayinSil(ctx, orijinal, sahip); err != nil {
		t.Fatalf("YayinSil: %v", err)
	}
	c, _ = d.YayinGetir(ctx, cover, izleyici)
	if !c.Kaynak.Silindi || c.Kaynak.Baslik != "Orijinal" {
		t.Fatalf("silinen kaynak = %+v", *c.Kaynak)
	}
	// Kalici silinen orijinal: cover'daki anlik goruntu.
	if err := d.YayinKaldir(ctx, orijinal); err != nil {
		t.Fatalf("YayinKaldir: %v", err)
	}
	c, _ = d.YayinGetir(ctx, cover, izleyici)
	if *c.Kaynak != (KaynakOzeti{YayinID: orijinal, Baslik: "Eski baslik", SahipKullaniciAdi: "eski_ad", Silindi: true}) {
		t.Fatalf("kalici silinen kaynak = %+v", *c.Kaynak)
	}
}

func TestPostgresDepo_GuncelleSilTekrarDene(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, baskasi := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	id := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHata, AltyapiAcik: true})

	aciklama, kapali := "yeni", false
	if err := d.YayinGuncelle(ctx, id, sahip, YayinYamasi{Aciklama: &aciklama, AltyapiAcik: &kapali}); err != nil {
		t.Fatalf("guncelle: %v", err)
	}
	k, _ := d.YayinGetir(ctx, id, sahip)
	if k.Aciklama != "yeni" || k.AltyapiAcik || !k.YorumAcik || k.Gorunurluk != GorunurlukHerkes {
		t.Fatalf("yalnizca gonderilen alanlar degismeli: %+v", k)
	}
	if err := d.YayinGuncelle(ctx, id, baskasi, YayinYamasi{Aciklama: &aciklama}); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasinin yamasi: %v", err)
	}

	if err := d.TekrarDenemeyeAl(ctx, id, baskasi); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasi tekrar deneyemez: %v", err)
	}
	if err := d.TekrarDenemeyeAl(ctx, id, sahip); err != nil {
		t.Fatalf("hata -> hazirlaniyor: %v", err)
	}
	if err := d.TekrarDenemeyeAl(ctx, id, sahip); !errors.Is(err, ErrDurum) {
		t.Fatalf("hazirlaniyor iken ErrDurum bekleniyordu: %v", err)
	}

	if err := d.YayinSil(ctx, id, baskasi); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasi silemez: %v", err)
	}
	if err := d.YayinSil(ctx, id, sahip); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if err := d.YayinSil(ctx, id, sahip); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("ikinci silme 404 olmali: %v", err)
	}
	if k, err := d.YayinGetir(ctx, id, sahip); err != nil || !k.Silindi {
		t.Fatalf("silinmis yayin isaretli donmeli: %+v, %v", k, err)
	}
	if err := d.YayinGuncelle(ctx, id, sahip, YayinYamasi{Aciklama: &aciklama}); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("silinmis yayin guncellenemez: %v", err)
	}
	if err := d.TekrarDenemeyeAl(ctx, id, sahip); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("silinmis yayin tekrar denenemez: %v", err)
	}
}

func TestPostgresDepo_Hazirlik(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	if err := d.YayinEkle(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	h, err := d.HazirlikGetir(ctx, y.ID)
	if err != nil || h.SahipID != sahip || h.Durum != DurumHazirlaniyor || h.Silindi {
		t.Fatalf("hazirlik = %+v, %v", h, err)
	}
	if h.Hazirlik.MasterKey != "masters/u/p/m.wav" || len(h.Hazirlik.Stems) != 1 || h.Hazirlik.Stems[0].Key != "stems/u/p/s1.mp3" {
		t.Fatalf("hazirlik girdisi = %+v", h.Hazirlik)
	}
	dalga := make([]float64, 64)
	dalga[3] = 1
	sonuc := HazirlikSonucu{
		MasterAnahtari: "sosyal/" + y.ID + "/master.wav",
		StemListesi:    []StemKaydi{{ID: "s1", Enstruman: "drums", Sure: 184, Anahtar: "sosyal/" + y.ID + "/stems/s1.mp3"}},
		DalgaFormu:     dalga,
	}
	if err := d.Yayinla(ctx, y.ID, sonuc); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	k, _ := d.YayinGetir(ctx, y.ID, sahip)
	if k.Durum != DurumYayinda || len(k.DalgaFormu) != 64 || k.DalgaFormu[3] != 1 || k.MasterAnahtari != sonuc.MasterAnahtari {
		t.Fatalf("yayinlanmis = durum %s dalga %d master %q", k.Durum, len(k.DalgaFormu), k.MasterAnahtari)
	}
	var stemler []StemKaydi
	var onizleme *string
	if err := havuz.QueryRow(ctx, `SELECT stem_listesi, onizleme_anahtari FROM sosyal.yayin WHERE id = $1`, y.ID).Scan(&stemler, &onizleme); err != nil {
		t.Fatalf("stem_listesi: %v", err)
	}
	if len(stemler) != 1 || stemler[0].Anahtar != sonuc.StemListesi[0].Anahtar || onizleme != nil {
		t.Fatalf("stemler = %+v onizleme = %v", stemler, onizleme)
	}
	// yayinda iken HataYap bir sey yapmaz.
	if err := d.HataYap(ctx, y.ID); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.YayinGetir(ctx, y.ID, sahip); k.Durum != DurumYayinda {
		t.Fatalf("yayinda yayin hataya dusmemeli: %s", k.Durum)
	}
	if _, err := d.HazirlikGetir(ctx, uuid.NewString()); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan hazirlik: %v", err)
	}
}

func TestPostgresDepo_HataYapVeSilinmisYayinYayinlanmaz(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	hatali := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor})
	if err := d.HataYap(ctx, hatali); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.YayinGetir(ctx, hatali, sahip); k.Durum != DurumHata {
		t.Fatalf("durum = %s", k.Durum)
	}
	silinen := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor, Silindi: true})
	if err := d.Yayinla(ctx, silinen, HazirlikSonucu{MasterAnahtari: "m"}); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	if k, _ := d.YayinGetir(ctx, silinen, sahip); k.Durum != DurumHazirlaniyor {
		t.Fatalf("silinmis yayin yayinlanmamali: %s", k.Durum)
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -12,10 +12,25 @@ type sahteDepo struct {
 	profiller map[string]*ProfilKaydi // kullanici id -> kayit
 	sayilar   ProfilSayilari
 	hata      error // dolu ise her cagri bu hatayla doner
+
+	yayinlar      map[string]*YayinKaydi // yayin id -> kayit
+	eklenen       []YeniYayin
+	slugCakismasi int // ilk n YayinEkle errSlugCakisti doner
+	yamalar       []YayinYamasi
+	kaldirilan    []string
+
+	hazirliklar map[string]*HazirlikKaydi
+	yayinlanan  map[string]HazirlikSonucu
+	hataYapilan []string
 }
 
 func yeniSahteDepo() *sahteDepo {
-	return &sahteDepo{profiller: map[string]*ProfilKaydi{}}
+	return &sahteDepo{
+		profiller:   map[string]*ProfilKaydi{},
+		yayinlar:    map[string]*YayinKaydi{},
+		hazirliklar: map[string]*HazirlikKaydi{},
+		yayinlanan:  map[string]HazirlikSonucu{},
+	}
 }
 
 func (d *sahteDepo) ProfilGetir(_ context.Context, id string) (*ProfilKaydi, error) {
@@ -77,3 +92,138 @@ func (d *sahteDepo) ProfilSayilari(context.Context, string, string) (*ProfilSayi
 	s := d.sayilar
 	return &s, nil
 }
+
+func (d *sahteDepo) YayinEkle(_ context.Context, y YeniYayin) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	if d.slugCakismasi > 0 {
+		d.slugCakismasi--
+		return errSlugCakisti
+	}
+	d.eklenen = append(d.eklenen, y)
+	return nil
+}
+
+// kopya, kaydin izleyiciye gore bir kopyasini doner (depo BenimMi'yi
+// doldurur).
+func (d *sahteDepo) kopya(k *YayinKaydi, izleyiciID string) *YayinKaydi {
+	c := *k
+	c.BenimMi = c.SahipID == izleyiciID
+	return &c
+}
+
+func (d *sahteDepo) YayinGetir(_ context.Context, id, izleyiciID string) (*YayinKaydi, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	k, ok := d.yayinlar[id]
+	if !ok {
+		return nil, ErrBulunamadi
+	}
+	return d.kopya(k, izleyiciID), nil
+}
+
+func (d *sahteDepo) YayinSlugIleGetir(_ context.Context, slug, izleyiciID string) (*YayinKaydi, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	for _, k := range d.yayinlar {
+		if k.Slug == slug {
+			return d.kopya(k, izleyiciID), nil
+		}
+	}
+	return nil, ErrBulunamadi
+}
+
+// sahibinCanliYayini, sahibin silinmemis yayinini bulur.
+func (d *sahteDepo) sahibinCanliYayini(id, sahipID string) (*YayinKaydi, bool) {
+	k, ok := d.yayinlar[id]
+	if !ok || k.SahipID != sahipID || k.Silindi {
+		return nil, false
+	}
+	return k, true
+}
+
+func (d *sahteDepo) YayinGuncelle(_ context.Context, id, sahipID string, yama YayinYamasi) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	k, ok := d.sahibinCanliYayini(id, sahipID)
+	if !ok {
+		return ErrBulunamadi
+	}
+	d.yamalar = append(d.yamalar, yama)
+	if yama.Aciklama != nil {
+		k.Aciklama = *yama.Aciklama
+	}
+	if yama.AltyapiAcik != nil {
+		k.AltyapiAcik = *yama.AltyapiAcik
+	}
+	if yama.Gorunurluk != nil {
+		k.Gorunurluk = *yama.Gorunurluk
+	}
+	if yama.YorumAcik != nil {
+		k.YorumAcik = *yama.YorumAcik
+	}
+	return nil
+}
+
+func (d *sahteDepo) YayinSil(_ context.Context, id, sahipID string) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	k, ok := d.sahibinCanliYayini(id, sahipID)
+	if !ok {
+		return ErrBulunamadi
+	}
+	k.Silindi = true
+	return nil
+}
+
+func (d *sahteDepo) YayinKaldir(_ context.Context, id string) error {
+	d.kaldirilan = append(d.kaldirilan, id)
+	return d.hata
+}
+
+func (d *sahteDepo) TekrarDenemeyeAl(_ context.Context, id, sahipID string) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	k, ok := d.sahibinCanliYayini(id, sahipID)
+	if !ok {
+		return ErrBulunamadi
+	}
+	if k.Durum != DurumHata {
+		return ErrDurum
+	}
+	k.Durum = DurumHazirlaniyor
+	return nil
+}
+
+func (d *sahteDepo) HazirlikGetir(_ context.Context, id string) (*HazirlikKaydi, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	k, ok := d.hazirliklar[id]
+	if !ok {
+		return nil, ErrBulunamadi
+	}
+	return k, nil
+}
+
+func (d *sahteDepo) Yayinla(_ context.Context, id string, s HazirlikSonucu) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	d.yayinlanan[id] = s
+	return nil
+}
+
+func (d *sahteDepo) HataYap(_ context.Context, id string) error {
+	d.hataYapilan = append(d.hataYapilan, id)
+	if k, ok := d.yayinlar[id]; ok && k.Durum == DurumHazirlaniyor {
+		k.Durum = DurumHata
+	}
+	return d.hata
+}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ -count=1
```

Expected: FAIL, build errors `undefined: YeniYayin`, `undefined: slugUret`, `undefined: YayinKaydi`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/model_yayin.go`:

````go
package sosyal

import "time"

// Ton, sozlesme §4.1: { tonik, mod }.
type Ton struct {
	Tonik string `json:"tonik"`
	Mod   string `json:"mod"`
}

// YapiOgesi, sarki yapisinin bir bolumu.
type YapiOgesi struct {
	ID   string  `json:"id"`
	Tur  string  `json:"tur"`
	Olcu float64 `json:"olcu"`
}

// SozBolumu ve Sozler, sozlesme §4.1 Sozler.
type SozBolumu struct {
	BolumID  string   `json:"bolumId"`
	Tur      string   `json:"tur"`
	Satirlar []string `json:"satirlar"`
}

type Sozler struct {
	Bolumler []SozBolumu `json:"bolumler"`
}

// KaynakOzeti, cover'in orijinali. Orijinal silinmisse Silindi true ve
// diger alanlar son bilinen degerlerdir.
type KaynakOzeti struct {
	YayinID           string `json:"yayinId"`
	Baslik            string `json:"baslik"`
	SahipKullaniciAdi string `json:"sahipKullaniciAdi"`
	Silindi           bool   `json:"silindi"`
}

// YayinOzeti, sozlesme §4.1. Kaynak cover olmayan yayinda null olarak
// GORUNUR (omitempty yok); DalgaFormu hicbir zaman null degildir.
type YayinOzeti struct {
	ID           string       `json:"id"`
	Slug         string       `json:"slug"`
	Baslik       string       `json:"baslik"`
	Sahip        ProfilOzeti  `json:"sahip"`
	Tur          string       `json:"tur"`
	Bpm          int          `json:"bpm"`
	Ton          Ton          `json:"ton"`
	SureSn       int          `json:"sureSn"`
	DalgaFormu   []float64    `json:"dalgaFormu"`
	BegeniSayisi int          `json:"begeniSayisi"`
	YorumSayisi  int          `json:"yorumSayisi"`
	CoverSayisi  int          `json:"coverSayisi"`
	BegendimMi   bool         `json:"begendimMi"`
	AltyapiAcik  bool         `json:"altyapiAcik"`
	Kaynak       *KaynakOzeti `json:"kaynak"`
	Olusturma    time.Time    `json:"olusturma"`
}

// YayinDetayi, sozlesme §4.1. Gorunurluk her zaman gonderilir.
type YayinDetayi struct {
	YayinOzeti
	Aciklama   string      `json:"aciklama"`
	YorumAcik  bool        `json:"yorumAcik"`
	Sozler     Sozler      `json:"sozler"`
	Yapi       []YapiOgesi `json:"yapi"`
	Durum      string      `json:"durum"`
	BenimMi    bool        `json:"benimMi"`
	Gorunurluk string      `json:"gorunurluk"`
}

// StemGirdisi, yayin isteginde bir stem. URL render sonucunun fileUrl'i
// (tam adres ya da ciplak anahtar).
type StemGirdisi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	URL       string  `json:"url"`
	Sure      float64 `json:"sure"`
}

// YayinIstegi, POST /yayinlar govdesi (mobil PublishInput).
type YayinIstegi struct {
	Baslik        string        `json:"baslik"`
	Aciklama      string        `json:"aciklama"`
	Gorunurluk    string        `json:"gorunurluk"`
	AltyapiAcik   bool          `json:"altyapiAcik"`
	YorumAcik     bool          `json:"yorumAcik"`
	MasterURL     string        `json:"masterUrl"`
	Stemler       []StemGirdisi `json:"stemler"`
	Sozler        Sozler        `json:"sozler"`
	Yapi          []YapiOgesi   `json:"yapi"`
	Tur           string        `json:"tur"`
	Bpm           int           `json:"bpm"`
	Ton           Ton           `json:"ton"`
	SureSn        int           `json:"sureSn"`
	KaynakYayinID *string       `json:"kaynakYayinId"`
}

// YayinYaniti, POST /yayinlar 202 govdesi.
type YayinYaniti struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Durum string `json:"durum"`
}

// YayinYamasi, PATCH /yayinlar/{id}; yalnizca gonderilen alanlar degisir.
type YayinYamasi struct {
	Aciklama    *string `json:"aciklama"`
	AltyapiAcik *bool   `json:"altyapiAcik"`
	Gorunurluk  *string `json:"gorunurluk"`
	YorumAcik   *bool   `json:"yorumAcik"`
}

// DosyaAdresi ve YayinDosyalari, POST /yayinlar/{id}/dosyalar yaniti.
type DosyaAdresi struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type YayinDosyalari struct {
	Master DosyaAdresi `json:"master"`
}

// HazirlikStemi ve Hazirlik, yayin.hazirlik kolonu: kopyalanacak kaynak
// anahtarlar. tekrar-dene ayni girdiyle yeniden kosabilsin diye satirda
// durur; disari verilmez.
type HazirlikStemi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	Sure      float64 `json:"sure"`
	Key       string  `json:"key"`
}

type Hazirlik struct {
	MasterKey string          `json:"masterKey"`
	Stems     []HazirlikStemi `json:"stems"`
}

// StemKaydi, yayin.stem_listesi ogesi: kopyalanmis stem. Anahtar,
// sosyal/{yayinId}/stems/{stemId}.{ext} kopyasidir.
type StemKaydi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	Sure      float64 `json:"sure"`
	Anahtar   string  `json:"anahtar"`
}

// YeniYayin, depoya yazilacak dogrulanmis yayin.
type YeniYayin struct {
	ID             string
	Slug           string
	SahipID        string
	Baslik         string
	Aciklama       string
	Tur            string
	Bpm            int
	Ton            Ton
	SureSn         int
	Gorunurluk     string
	AltyapiAcik    bool
	YorumAcik      bool
	Sozler         Sozler
	Yapi           []YapiOgesi
	Hazirlik       Hazirlik
	KaynakYayinID  string // "" orijinal
	KaynakBaslik   string
	KaynakSahipAdi string
}

// YayinKaydi, depodan okunan detay ve servisin karar icin ihtiyac
// duydugu ic alanlar. Silinmis yayinlar da doner (Silindi true);
// gorunurlugu servis DetayGorebilir ile uygular.
type YayinKaydi struct {
	YayinDetayi
	SahipID        string
	KaynakYayinID  string
	StemVar        bool // hazirlikta stem var: altyapi sonradan acilabilir
	MasterAnahtari string
	Silindi        bool
	TakipEdiyor    bool // izleyici sahibi takip ediyor
}

// gorunurlukGirdisi, kaydi izleyiciye gore gorunurluk girdisine cevirir.
func (k *YayinKaydi) gorunurlukGirdisi(izleyiciID string) GorunurlukGirdisi {
	return GorunurlukGirdisi{
		SahipID: k.SahipID, IzleyiciID: izleyiciID, Gorunurluk: k.Gorunurluk,
		Durum: k.Durum, Silindi: k.Silindi, TakipEdiyor: k.TakipEdiyor,
	}
}

// HazirlikKaydi, sosyal:hazirla isinin okudugu satir.
type HazirlikKaydi struct {
	ID       string
	SahipID  string
	Durum    string
	Silindi  bool
	Hazirlik Hazirlik
}

// HazirlikSonucu, isin basariyla bitince satira yazdiklari.
type HazirlikSonucu struct {
	MasterAnahtari   string
	StemListesi      []StemKaydi
	DalgaFormu       []float64
	OnizlemeAnahtari string // "" ise onizleme yok
}
````

**Create** `core-service/internal/sosyal/slug.go`:

````go
package sosyal

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
)

const (
	slugAlfabesi = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	slugBoyu     = 8
)

var slugDeseni = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

// errSlugCakisti, depo katmaninin slug UNIQUE ihlalidir; servis yeni slug
// ile yeniden dener. Disari sizmaz.
var errSlugCakisti = errors.New("slug cakismasi")

// SlugGecerli, slug'in 8 karakter [A-Za-z0-9] oldugunu soyler.
func SlugGecerli(slug string) bool { return slugDeseni.MatchString(slug) }

// slugUret, crypto/rand ile 8 karakterlik slug uretir (62^8 ~ 2.2e14).
// rand.Int tekdüze dagilim verir; modulo yanliligi yoktur.
func slugUret() (string, error) {
	b := make([]byte, slugBoyu)
	sinir := big.NewInt(int64(len(slugAlfabesi)))
	for i := range b {
		n, err := rand.Int(rand.Reader, sinir)
		if err != nil {
			return "", err
		}
		b[i] = slugAlfabesi[n.Int64()]
	}
	return string(b), nil
}
````

**Create** `core-service/internal/sosyal/depo_yayin.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// YayinDeposu, sosyal.yayin uzerindeki uc islemleri.
type YayinDeposu interface {
	// YayinEkle, yayini hazirlaniyor durumunda yazar. Slug baskasindaysa
	// errSlugCakisti doner; servis yeni slug ile yeniden dener.
	YayinEkle(ctx context.Context, y YeniYayin) error
	// YayinGetir ve YayinSlugIleGetir, yayini izleyiciye gore sayaclarla
	// doner. Silinmis yayin da doner (Silindi true); yoksa ErrBulunamadi.
	YayinGetir(ctx context.Context, id, izleyiciID string) (*YayinKaydi, error)
	YayinSlugIleGetir(ctx context.Context, slug, izleyiciID string) (*YayinKaydi, error)
	// YayinGuncelle, sahibin silinmemis yayininda yalnizca dolu alanlari
	// degistirir. Satir yoksa ErrBulunamadi.
	YayinGuncelle(ctx context.Context, id, sahipID string, yama YayinYamasi) error
	// YayinSil, yumusak siler (silinme = now()). Satir yoksa, sahibi
	// degilse ya da zaten silinmisse ErrBulunamadi.
	YayinSil(ctx context.Context, id, sahipID string) error
	// YayinKaldir, yayini kalici siler. Yalnizca kuyruga hic girmemis bir
	// yayini geri almak icindir.
	YayinKaldir(ctx context.Context, id string) error
	// TekrarDenemeyeAl, sahibin hata durumundaki yayinini hazirlaniyor
	// yapar. Yayin var ama hata degilse ErrDurum, yoksa ErrBulunamadi.
	TekrarDenemeyeAl(ctx context.Context, id, sahipID string) error
}

// HazirlikDeposu, sosyal:hazirla isinin kullandigi islemler.
type HazirlikDeposu interface {
	// HazirlikGetir, satiri doner; yoksa ErrBulunamadi.
	HazirlikGetir(ctx context.Context, id string) (*HazirlikKaydi, error)
	// Yayinla, hazirlaniyor durumundaki silinmemis yayini sonucla yayinda
	// yapar. Durum degismisse (silindi, zaten yayinda) hicbir sey yapmaz.
	Yayinla(ctx context.Context, id string, sonuc HazirlikSonucu) error
	// HataYap, hazirlaniyor durumundaki yayini hata yapar.
	HataYap(ctx context.Context, id string) error
}

// ozetSecimi, YayinOzeti'nin kolonlari. $1 her zaman izleyicidir. Sahip
// profili INNER JOIN'dir: profilsiz yayin olusturulamaz ve hesap silme
// ikisini birlikte siler. Kaynak LEFT JOIN'dir: orijinal kalici silinmisse
// cover'daki son bilinen degerler kullanilir.
const ozetSecimi = `
	y.id::text, y.slug, y.baslik, y.sahip_id::text, p.kullanici_adi, p.gorunen_ad,
	y.tur, y.bpm, y.ton_tonik, y.ton_mod, y.sure_sn, y.dalga_formu, y.altyapi_acik,
	y.kaynak_yayin_id::text,
	COALESCE(k.baslik, y.kaynak_baslik, ''),
	COALESCE(kp.kullanici_adi, y.kaynak_sahip_adi, ''),
	(k.id IS NULL OR k.silinme IS NOT NULL),
	y.olusturma,
	(SELECT count(*) FROM sosyal.begeni b WHERE b.yayin_id = y.id),
	(SELECT count(*) FROM sosyal.yorum c WHERE c.yayin_id = y.id AND c.silinme IS NULL),
	(SELECT count(*) FROM sosyal.yayin cv
	  WHERE cv.kaynak_yayin_id = y.id AND cv.durum = 'yayinda' AND cv.silinme IS NULL),
	EXISTS (SELECT 1 FROM sosyal.begeni b WHERE b.yayin_id = y.id AND b.kullanici_id = $1)`

const ozetKaynagi = `
	FROM sosyal.yayin y
	JOIN sosyal.profil p ON p.kullanici_id = y.sahip_id
	LEFT JOIN sosyal.yayin k ON k.id = y.kaynak_yayin_id
	LEFT JOIN sosyal.profil kp ON kp.kullanici_id = k.sahip_id`

// ozetTarayici, ozetSecimi kolonlarinin tarama hedefleridir.
type ozetTarayici struct {
	o              YayinOzeti
	sahipID        string
	kaynakID       *string
	kaynakBaslik   string
	kaynakAdi      string
	kaynakSilindi  bool
	dalgaFormu     []float64
	olusturma      time.Time
	begeni, yorum  int
	cover          int
	begendimMi     bool
	tonik, tonModu string
}

func (t *ozetTarayici) hedefler() []any {
	return []any{
		&t.o.ID, &t.o.Slug, &t.o.Baslik, &t.sahipID, &t.o.Sahip.KullaniciAdi, &t.o.Sahip.GorunenAd,
		&t.o.Tur, &t.o.Bpm, &t.tonik, &t.tonModu, &t.o.SureSn, &t.dalgaFormu, &t.o.AltyapiAcik,
		&t.kaynakID, &t.kaynakBaslik, &t.kaynakAdi, &t.kaynakSilindi,
		&t.olusturma, &t.begeni, &t.yorum, &t.cover, &t.begendimMi,
	}
}

func (t *ozetTarayici) ozet() YayinOzeti {
	o := t.o
	o.Sahip.AvatarRengi = AvatarRengi(t.sahipID)
	o.Ton = Ton{Tonik: t.tonik, Mod: t.tonModu}
	o.DalgaFormu = t.dalgaFormu
	if o.DalgaFormu == nil {
		o.DalgaFormu = []float64{}
	}
	o.BegeniSayisi, o.YorumSayisi, o.CoverSayisi, o.BegendimMi = t.begeni, t.yorum, t.cover, t.begendimMi
	o.Olusturma = t.olusturma.UTC()
	if t.kaynakID != nil {
		o.Kaynak = &KaynakOzeti{
			YayinID: *t.kaynakID, Baslik: t.kaynakBaslik,
			SahipKullaniciAdi: t.kaynakAdi, Silindi: t.kaynakSilindi,
		}
	}
	return o
}

// yayinGetir, detay sorgusunu verilen kosulla ($2) kosar.
func (d *PostgresDepo) yayinGetir(ctx context.Context, kosul, deger, izleyiciID string) (*YayinKaydi, error) {
	var (
		t       ozetTarayici
		k       YayinKaydi
		sozler  Sozler
		yapi    []YapiOgesi
		master  *string
		stemVar bool
	)
	hedefler := append(t.hedefler(),
		&k.Aciklama, &k.YorumAcik, &sozler, &yapi, &k.Durum, &k.Gorunurluk,
		&k.Silindi, &stemVar, &master, &k.TakipEdiyor)
	err := d.havuz.QueryRow(ctx, `
		SELECT `+ozetSecimi+`,
		  y.aciklama, y.yorum_acik, y.sozler, y.yapi, y.durum, y.gorunurluk,
		  y.silinme IS NOT NULL,
		  COALESCE(jsonb_array_length(y.hazirlik->'stems'), 0) > 0,
		  y.master_anahtari,
		  EXISTS (SELECT 1 FROM sosyal.takip tk WHERE tk.takipci_id = $1 AND tk.takip_edilen_id = y.sahip_id)
		`+ozetKaynagi+`
		WHERE `+kosul+` = $2`, izleyiciID, deger).Scan(hedefler...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("yayin okunamadi: %w", err)
	}
	k.YayinOzeti = t.ozet()
	k.SahipID = t.sahipID
	if t.kaynakID != nil {
		k.KaynakYayinID = *t.kaynakID
	}
	k.Sozler = sozler
	if k.Sozler.Bolumler == nil {
		k.Sozler.Bolumler = []SozBolumu{}
	}
	k.Yapi = yapi
	if k.Yapi == nil {
		k.Yapi = []YapiOgesi{}
	}
	k.StemVar = stemVar
	if master != nil {
		k.MasterAnahtari = *master
	}
	k.BenimMi = k.SahipID == izleyiciID
	return &k, nil
}

func (d *PostgresDepo) YayinGetir(ctx context.Context, id, izleyiciID string) (*YayinKaydi, error) {
	return d.yayinGetir(ctx, "y.id", id, izleyiciID)
}

func (d *PostgresDepo) YayinSlugIleGetir(ctx context.Context, slug, izleyiciID string) (*YayinKaydi, error) {
	return d.yayinGetir(ctx, "y.slug", slug, izleyiciID)
}

func (d *PostgresDepo) YayinEkle(ctx context.Context, y YeniYayin) error {
	var kaynak any
	if y.KaynakYayinID != "" {
		kaynak = y.KaynakYayinID
	}
	_, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, aciklama, tur, bpm, ton_tonik, ton_mod,
			sure_sn, gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik,
			kaynak_yayin_id, kaynak_baslik, kaynak_sahip_adi, durum)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, NULLIF($18, ''), NULLIF($19, ''), 'hazirlaniyor')`,
		y.ID, y.Slug, y.SahipID, y.Baslik, y.Aciklama, y.Tur, y.Bpm, y.Ton.Tonik, y.Ton.Mod,
		y.SureSn, y.Gorunurluk, y.AltyapiAcik, y.YorumAcik, y.Sozler, y.Yapi, y.Hazirlik,
		kaynak, y.KaynakBaslik, y.KaynakSahipAdi)
	if kisit, ok := tekilIhlali(err); ok && kisit == "yayin_slug_key" {
		return errSlugCakisti
	}
	if err != nil {
		return fmt.Errorf("yayin eklenemedi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) YayinGuncelle(ctx context.Context, id, sahipID string, yama YayinYamasi) error {
	sonuc, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin
		   SET aciklama     = COALESCE($3, aciklama),
		       altyapi_acik = COALESCE($4, altyapi_acik),
		       gorunurluk   = COALESCE($5, gorunurluk),
		       yorum_acik   = COALESCE($6, yorum_acik)
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL`,
		id, sahipID, yama.Aciklama, yama.AltyapiAcik, yama.Gorunurluk, yama.YorumAcik)
	if err != nil {
		return fmt.Errorf("yayin guncellenemedi: %w", err)
	}
	if sonuc.RowsAffected() == 0 {
		return ErrBulunamadi
	}
	return nil
}

func (d *PostgresDepo) YayinSil(ctx context.Context, id, sahipID string) error {
	sonuc, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin SET silinme = now()
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL`, id, sahipID)
	if err != nil {
		return fmt.Errorf("yayin silinemedi: %w", err)
	}
	if sonuc.RowsAffected() == 0 {
		return ErrBulunamadi
	}
	return nil
}

func (d *PostgresDepo) YayinKaldir(ctx context.Context, id string) error {
	if _, err := d.havuz.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1`, id); err != nil {
		return fmt.Errorf("yayin kaldirilamadi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) TekrarDenemeyeAl(ctx context.Context, id, sahipID string) error {
	sonuc, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin SET durum = 'hazirlaniyor'
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL AND durum = 'hata'`, id, sahipID)
	if err != nil {
		return fmt.Errorf("yayin tekrar denemeye alinamadi: %w", err)
	}
	if sonuc.RowsAffected() == 1 {
		return nil
	}
	var varMi bool
	if err := d.havuz.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM sosyal.yayin WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL)`,
		id, sahipID).Scan(&varMi); err != nil {
		return fmt.Errorf("yayin okunamadi: %w", err)
	}
	if varMi {
		return ErrDurum
	}
	return ErrBulunamadi
}

func (d *PostgresDepo) HazirlikGetir(ctx context.Context, id string) (*HazirlikKaydi, error) {
	var k HazirlikKaydi
	err := d.havuz.QueryRow(ctx, `
		SELECT id::text, sahip_id::text, durum, silinme IS NOT NULL, hazirlik
		  FROM sosyal.yayin WHERE id = $1`, id,
	).Scan(&k.ID, &k.SahipID, &k.Durum, &k.Silindi, &k.Hazirlik)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("hazirlik okunamadi: %w", err)
	}
	return &k, nil
}

func (d *PostgresDepo) Yayinla(ctx context.Context, id string, s HazirlikSonucu) error {
	stemler := s.StemListesi
	if stemler == nil {
		stemler = []StemKaydi{}
	}
	dalga := s.DalgaFormu
	if dalga == nil {
		dalga = []float64{}
	}
	_, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin
		   SET durum = 'yayinda', master_anahtari = $2, stem_listesi = $3,
		       dalga_formu = $4, onizleme_anahtari = NULLIF($5, '')
		 WHERE id = $1 AND durum = 'hazirlaniyor' AND silinme IS NULL`,
		id, s.MasterAnahtari, stemler, dalga, s.OnizlemeAnahtari)
	if err != nil {
		return fmt.Errorf("yayin yayinlanamadi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) HataYap(ctx context.Context, id string) error {
	_, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin SET durum = 'hata' WHERE id = $1 AND durum = 'hazirlaniyor'`, id)
	if err != nil {
		return fmt.Errorf("yayin hata durumuna alinamadi: %w", err)
	}
	return nil
}
````

**Modify** `core-service/internal/sosyal/depo.go`:

````diff
diff --git a/core-service/internal/sosyal/depo.go b/core-service/internal/sosyal/depo.go
--- a/core-service/internal/sosyal/depo.go
+++ b/core-service/internal/sosyal/depo.go
@@ -29,6 +29,8 @@ type ProfilDeposu interface {
 // Depo, sosyal modulunun tum kalici islemleridir.
 type Depo interface {
 	ProfilDeposu
+	YayinDeposu
+	HazirlikDeposu
 }
 
 // PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/sosyal/ -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: All PASS, no SKIP (including `TestPostgresDepo_GorunurlukSQLVeGoAyni` and `TestPostgresDepo_SayaclarVeKaynak`). Whole Go suite after this task: about 607 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/slug_test.go \
  core-service/internal/sosyal/depo_yayin_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/sosyal/model_yayin.go \
  core-service/internal/sosyal/slug.go \
  core-service/internal/sosyal/depo_yayin.go \
  core-service/internal/sosyal/depo.go
git commit -F - <<'EOF'
feat(sosyal): yayin deposu ve hazirlik islemleri

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 7: Publish, detail, slug, PATCH, DELETE, retry and listen URL

The publication use cases (R3, R5, R9, N3–N10) and their handlers. The constructor gains the queue, the storage client and the R2 config, so Task 5's test helper and profile tests change with it. `POST /yayinlar` gets the per-user publish limiter passed into `Rotalar`.

**Files:**

- Create: `core-service/internal/sosyal/anahtarlar_test.go`
- Create: `core-service/internal/sosyal/yayin_kurallari_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go`
- Modify: `core-service/internal/sosyal/servis_profil_test.go`
- Create: `core-service/internal/sosyal/servis_yayin_test.go`
- Modify: `core-service/internal/handler/sosyal_test.go`
- Create: `core-service/internal/handler/sosyal_yayin_test.go`
- Create: `core-service/internal/sosyal/anahtarlar.go`
- Create: `core-service/internal/sosyal/yayin_kurallari.go`
- Modify: `core-service/internal/sosyal/servis.go`
- Create: `core-service/internal/sosyal/servis_yayin.go`
- Modify: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_yayin.go`

**Interfaces:**
- Consumes: `client.SosyalAnahtari`, `client.SahipliAnahtarVeyaURLCoz`, `client.IsPublicKey`, `client.ResolveURL`, `client.ErrYabanciAnahtar` (Task 2 and existing); repository and types (Tasks 4, 6); `response.Accepted`, `response.NoContent`.
- Produces: `type sosyal.Kuyruk interface { Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) }`; `sosyal.NewServis(depo Depo, kuyruk Kuyruk, depolama client.StorageClient, r2 *config.R2Config) *Servis`; constants `TaskTypeHazirla = "sosyal:hazirla"`, `TaskTypeDosyaSil = "sosyal:dosya-sil"`, `KuyrukSosyal = "sosyal"`; `GorevYuku{YayinID string}` (JSON key `yayinId`); `MasterHedefi(yayinID, kaynak string)`, `StemHedefi(yayinID, stemID, kaynak string)`, `OnizlemeHedefi(yayinID string)` (all `(string, error)`); `GorunurlukGecerli(string) bool`; service methods `YayinOlustur`, `YayinGoster`, `SlugGoster`, `YayinGuncelle`, `YayinSil`, `TekrarDene`, `Dosyalar`; `SosyalServisi` gains the same seven methods; routes `POST /yayinlar` (limited), `GET|PATCH|DELETE /yayinlar/:id`, `POST /yayinlar/:id/tekrar-dene`, `POST /yayinlar/:id/dosyalar`, `GET /s/:slug`; test helpers `sahteKuyruk`, `testR2()`, `testServisi(d) (*Servis, *sahteKuyruk)`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/anahtarlar_test.go`:

````go
package sosyal

import "testing"

func TestHedefAnahtarlari(t *testing.T) {
	id := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	if got, err := MasterHedefi(id, "masters/u/p/m1.wav"); err != nil || got != "sosyal/"+id+"/master.wav" {
		t.Fatalf("master = %q, %v", got, err)
	}
	if got, err := MasterHedefi(id, "masters/u/p/m1"); err != nil || got != "sosyal/"+id+"/master" {
		t.Fatalf("uzantisiz master = %q, %v", got, err)
	}
	if got, err := StemHedefi(id, "drums", "stems/u/p/drums.mp3"); err != nil || got != "sosyal/"+id+"/stems/drums.mp3" {
		t.Fatalf("stem = %q, %v", got, err)
	}
	if got, err := OnizlemeHedefi(id); err != nil || got != "sosyal/"+id+"/onizleme.mp3" {
		t.Fatalf("onizleme = %q, %v", got, err)
	}
	if _, err := StemHedefi(id, "a.b", "stems/u/p/x.mp3"); err == nil {
		t.Fatal("noktali stem id reddedilmeli")
	}
	if _, err := StemHedefi(id, "../x", "stems/u/p/x.mp3"); err == nil {
		t.Fatal("tirmanan stem id reddedilmeli")
	}
	if got := uzanti("stems/u/p/x.WAV"); got != "" {
		t.Fatalf("buyuk harfli uzanti segment desenine uymaz, bos olmali: %q", got)
	}
}
````

**Create** `core-service/internal/sosyal/yayin_kurallari_test.go`:

````go
package sosyal

import (
	"errors"
	"strings"
	"testing"
)

func gecerliIstek() YayinIstegi {
	return YayinIstegi{
		Baslik: "Gece", Aciklama: "", Gorunurluk: GorunurlukHerkes, AltyapiAcik: true, YorumAcik: true,
		MasterURL: "masters/u/p/m.wav",
		Stemler:   []StemGirdisi{{ID: "drums", Enstruman: "drums", URL: "stems/u/p/drums.mp3", Sure: 184}},
		Tur:       "pop", Bpm: 120, Ton: Ton{Tonik: "A", Mod: "minor"}, SureSn: 184,
	}
}

func TestYayinIsteginiDuzenle_Gecerli(t *testing.T) {
	ist := gecerliIstek()
	ist.Baslik = "  Gece  "
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("gecerli istek reddedildi: %v", err)
	}
	if ist.Baslik != "Gece" || ist.Yapi == nil || ist.Sozler.Bolumler == nil {
		t.Fatalf("duzenleme = %+v", ist)
	}
}

func TestYayinIsteginiDuzenle_Hatalar(t *testing.T) {
	durumlar := map[string]func(*YayinIstegi){
		"bos baslik":       func(i *YayinIstegi) { i.Baslik = "  " },
		"uzun baslik":      func(i *YayinIstegi) { i.Baslik = strings.Repeat("ş", 81) },
		"uzun aciklama":    func(i *YayinIstegi) { i.Aciklama = strings.Repeat("a", 301) },
		"gorunurluk":       func(i *YayinIstegi) { i.Gorunurluk = "gizli" },
		"ton mod":          func(i *YayinIstegi) { i.Ton.Mod = "dorian" },
		"ton tonik":        func(i *YayinIstegi) { i.Ton.Tonik = "" },
		"tur":              func(i *YayinIstegi) { i.Tur = "" },
		"sure":             func(i *YayinIstegi) { i.SureSn = 0 },
		"bpm dusuk":        func(i *YayinIstegi) { i.Bpm = 19 },
		"bpm yuksek":       func(i *YayinIstegi) { i.Bpm = 301 },
		"master yok":       func(i *YayinIstegi) { i.MasterURL = "" },
		"stem yok":         func(i *YayinIstegi) { i.Stemler = nil },
		"stem id noktali":  func(i *YayinIstegi) { i.Stemler[0].ID = "a.b" },
		"stem id tirmanma": func(i *YayinIstegi) { i.Stemler[0].ID = "../x" },
		"stem sure":        func(i *YayinIstegi) { i.Stemler[0].Sure = 0 },
		"stem url":         func(i *YayinIstegi) { i.Stemler[0].URL = "" },
		"stem enstruman":   func(i *YayinIstegi) { i.Stemler[0].Enstruman = "" },
		"stem tekrar":      func(i *YayinIstegi) { i.Stemler = append(i.Stemler, i.Stemler[0]) },
		"yapi ogesi":       func(i *YayinIstegi) { i.Yapi = []YapiOgesi{{ID: "", Olcu: 4}} },
		"cok stem":         func(i *YayinIstegi) { i.Stemler = make([]StemGirdisi, 33) },
		"cok satir":        func(i *YayinIstegi) { i.Sozler.Bolumler = []SozBolumu{{BolumID: "b", Satirlar: make([]string, 201)}} },
	}
	for ad, boz := range durumlar {
		ist := gecerliIstek()
		boz(&ist)
		if err := yayinIsteginiDuzenle(&ist); !errors.Is(err, ErrDogrulama) {
			t.Errorf("%s: ErrDogrulama bekleniyordu, gelen %v", ad, err)
		}
	}
}

func TestYayinIsteginiDuzenle_SinirDegerler(t *testing.T) {
	ist := gecerliIstek()
	ist.Baslik, ist.Aciklama, ist.Bpm = strings.Repeat("ş", 80), strings.Repeat("ğ", 300), 300
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("sinir degerler kabul edilmeli: %v", err)
	}
	ist = gecerliIstek()
	ist.Bpm = 20
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("bpm 20 kabul edilmeli: %v", err)
	}
}

func TestYayinIsteginiDuzenle_CoverAltyapiKapanirStemlerAtilir(t *testing.T) {
	ist := gecerliIstek()
	kaynak := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	ist.KaynakYayinID = &kaynak
	ist.Stemler[0].ID = "gecersiz.id" // yok sayilir, dogrulanmaz
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("cover: %v", err)
	}
	if ist.AltyapiAcik || ist.Stemler != nil {
		t.Fatalf("cover altyapisi kapali ve stemsiz olmali: %+v", ist)
	}
}

func TestYayinIsteginiDuzenle_BosKaynakOrijinaldir(t *testing.T) {
	ist := gecerliIstek()
	bos := ""
	ist.KaynakYayinID = &bos
	if err := yayinIsteginiDuzenle(&ist); err != nil || ist.KaynakYayinID != nil || !ist.AltyapiAcik {
		t.Fatalf("bos kaynak orijinal sayilmali: %v %+v", err, ist)
	}
}

func TestYayinIsteginiDuzenle_KapaliAltyapiStemleriYokSayar(t *testing.T) {
	ist := gecerliIstek()
	ist.AltyapiAcik = false
	ist.Stemler = []StemGirdisi{{ID: "x.y"}}
	if err := yayinIsteginiDuzenle(&ist); err != nil || ist.Stemler != nil {
		t.Fatalf("kapali altyapi: %v %+v", err, ist.Stemler)
	}
}

func TestYamayiDuzenle(t *testing.T) {
	a, g := "  merhaba ", "link"
	y := YayinYamasi{Aciklama: &a, Gorunurluk: &g}
	if err := yamayiDuzenle(&y); err != nil || *y.Aciklama != "merhaba" {
		t.Fatalf("yama = %v %v", err, *y.Aciklama)
	}
	kotu := "gizli"
	if err := yamayiDuzenle(&YayinYamasi{Gorunurluk: &kotu}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("gecersiz gorunurluk: %v", err)
	}
	uzun := strings.Repeat("a", 301)
	if err := yamayiDuzenle(&YayinYamasi{Aciklama: &uzun}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("uzun aciklama: %v", err)
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -3,8 +3,41 @@ package sosyal
 import (
 	"context"
 	"time"
+
+	"github.com/hibiken/asynq"
+	"github.com/makeasinger/api/internal/config"
 )
 
+// sahteKuyruk, kuyruga konan gorevleri ve seceneklerini kaydeder.
+type sahteKuyruk struct {
+	gorevler   []*asynq.Task
+	secenekler [][]asynq.Option
+	hata       error
+}
+
+func (q *sahteKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
+	if q.hata != nil {
+		return nil, q.hata
+	}
+	q.gorevler = append(q.gorevler, t)
+	q.secenekler = append(q.secenekler, opts)
+	return &asynq.TaskInfo{}, nil
+}
+
+// testR2, client testlerindeki yapilandirmanin aynisidir.
+func testR2() *config.R2Config {
+	return &config.R2Config{
+		AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
+		PublicURL: "https://makesinger-cdn.celalettindemir.dev", PresignTTL: time.Hour,
+	}
+}
+
+// testServisi, sahte depo ve kuyrukla, R2 istemcisi olmadan servis kurar.
+func testServisi(d *sahteDepo) (*Servis, *sahteKuyruk) {
+	q := &sahteKuyruk{}
+	return NewServis(d, q, nil, testR2()), q
+}
+
 // sahteDepo, servis testleri icin bellek ici Depo'dur. Yalnizca servisin
 // kararlarini sinamaya yetecek kadar davranir; SQL davranisi canli
 // Postgres testlerinde sinanir.
````

**Modify** `core-service/internal/sosyal/servis_profil_test.go`:

````diff
diff --git a/core-service/internal/sosyal/servis_profil_test.go b/core-service/internal/sosyal/servis_profil_test.go
--- a/core-service/internal/sosyal/servis_profil_test.go
+++ b/core-service/internal/sosyal/servis_profil_test.go
@@ -12,14 +12,14 @@ const (
 )
 
 func TestServis_ProfilBenYoksaProfilYok(t *testing.T) {
-	s := NewServis(yeniSahteDepo())
+	s, _ := testServisi(yeniSahteDepo())
 	if _, err := s.ProfilBen(context.Background(), kullaniciA); !errors.Is(err, ErrProfilYok) {
 		t.Fatalf("ErrProfilYok bekleniyordu: %v", err)
 	}
 }
 
 func TestServis_GecersizKimlik(t *testing.T) {
-	s := NewServis(yeniSahteDepo())
+	s, _ := testServisi(yeniSahteDepo())
 	if _, err := s.ProfilBen(context.Background(), "kullanici-a"); !errors.Is(err, ErrKimlik) {
 		t.Fatalf("ErrKimlik bekleniyordu: %v", err)
 	}
@@ -28,7 +28,7 @@ func TestServis_GecersizKimlik(t *testing.T) {
 func TestServis_ProfilKaydetVeBen(t *testing.T) {
 	d := yeniSahteDepo()
 	d.sayilar = ProfilSayilari{Istatistik: Istatistik{Sarki: 2, Takipci: 5}}
-	s := NewServis(d)
+	s, _ := testServisi(d)
 	p, err := s.ProfilKaydet(context.Background(), kullaniciA, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: " Ali ", Bio: "selam"})
 	if err != nil {
 		t.Fatalf("kaydet: %v", err)
@@ -47,7 +47,7 @@ func TestServis_ProfilKaydetVeBen(t *testing.T) {
 
 func TestServis_ProfilKaydetHatalari(t *testing.T) {
 	d := yeniSahteDepo()
-	s := NewServis(d)
+	s, _ := testServisi(d)
 	ctx := context.Background()
 	if _, err := s.ProfilKaydet(ctx, kullaniciA, ProfilGirdisi{KullaniciAdi: ".ali", GorunenAd: "A"}); !errors.Is(err, ErrGecersizKullaniciAdi) {
 		t.Fatalf("gecersiz ad: %v", err)
@@ -67,7 +67,7 @@ func TestServis_AdUygunMu(t *testing.T) {
 	d := yeniSahteDepo()
 	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali"}
 	d.profiller[kullaniciB] = &ProfilKaydi{KullaniciID: kullaniciB, KullaniciAdi: "ali2"}
-	s := NewServis(d)
+	s, _ := testServisi(d)
 	ctx := context.Background()
 
 	bos, err := s.AdUygunMu(ctx, kullaniciB, "veli")
@@ -97,7 +97,7 @@ func TestServis_ProfilGoster(t *testing.T) {
 	d := yeniSahteDepo()
 	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali", GorunenAd: "Ali"}
 	d.sayilar = ProfilSayilari{TakipEdiyorMuyum: true}
-	s := NewServis(d)
+	s, _ := testServisi(d)
 	ctx := context.Background()
 	p, err := s.ProfilGoster(ctx, kullaniciB, "ali")
 	if err != nil || p.BenMiyim || !p.TakipEdiyorMuyum {
````

**Create** `core-service/internal/sosyal/servis_yayin_test.go`:

````go
package sosyal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

const (
	yayinX = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yayinY = "9b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func profilliDepo() *sahteDepo {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali", GorunenAd: "Ali"}
	return d
}

func sahteYayin(id, sahip, gorunurluk, durum string) *YayinKaydi {
	k := &YayinKaydi{SahipID: sahip}
	k.ID, k.Slug, k.Baslik = id, "Ab3dEf9h", "Orijinal"
	k.Sahip = ProfilOzeti{KullaniciAdi: "sahip"}
	k.Gorunurluk, k.Durum, k.AltyapiAcik, k.StemVar = gorunurluk, durum, true, true
	return k
}

// yayinIstegiA, kullaniciA'nin kendi dosyalariyla gecerli bir istek.
func yayinIstegiA() YayinIstegi {
	ist := gecerliIstek()
	ist.MasterURL = "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/" + kullaniciA + "/p1/m1.wav?X-Amz-Signature=x"
	ist.Stemler[0].URL = "stems/" + kullaniciA + "/p1/drums.mp3"
	return ist
}

func secenek(opts []asynq.Option, tur asynq.OptionType) any {
	for _, o := range opts {
		if o.Type() == tur {
			return o.Value()
		}
	}
	return nil
}

func TestServis_YayinOlustur_ProfilYok(t *testing.T) {
	s, q := testServisi(yeniSahteDepo())
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); !errors.Is(err, ErrProfilYok) {
		t.Fatalf("ErrProfilYok bekleniyordu: %v", err)
	}
	if len(q.gorevler) != 0 {
		t.Fatal("profil yokken is kurulmamali")
	}
}

func TestServis_YayinOlustur_KaydederVeKuyrugaKoyar(t *testing.T) {
	d := profilliDepo()
	s, q := testServisi(d)
	yanit, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA())
	if err != nil {
		t.Fatalf("YayinOlustur: %v", err)
	}
	if yanit.Durum != DurumHazirlaniyor || !SlugGecerli(yanit.Slug) {
		t.Fatalf("yanit = %+v", yanit)
	}
	if len(d.eklenen) != 1 {
		t.Fatalf("tek yayin eklenmeli: %d", len(d.eklenen))
	}
	y := d.eklenen[0]
	if y.ID != yanit.ID || y.Slug != yanit.Slug || y.SahipID != kullaniciA || !y.AltyapiAcik || y.KaynakYayinID != "" {
		t.Fatalf("yeni yayin = %+v", y)
	}
	if y.Hazirlik.MasterKey != "masters/"+kullaniciA+"/p1/m1.wav" {
		t.Fatalf("master anahtari = %q", y.Hazirlik.MasterKey)
	}
	if len(y.Hazirlik.Stems) != 1 || y.Hazirlik.Stems[0] != (HazirlikStemi{ID: "drums", Enstruman: "drums", Sure: 184, Key: "stems/" + kullaniciA + "/p1/drums.mp3"}) {
		t.Fatalf("stemler = %+v", y.Hazirlik.Stems)
	}
	if len(q.gorevler) != 1 || q.gorevler[0].Type() != TaskTypeHazirla {
		t.Fatalf("hazirla isi kurulmali: %v", q.gorevler)
	}
	var yuk GorevYuku
	if err := json.Unmarshal(q.gorevler[0].Payload(), &yuk); err != nil || yuk.YayinID != yanit.ID {
		t.Fatalf("yuk = %s", q.gorevler[0].Payload())
	}
	opts := q.secenekler[0]
	if secenek(opts, asynq.QueueOpt) != KuyrukSosyal || secenek(opts, asynq.MaxRetryOpt) != 3 || secenek(opts, asynq.RetentionOpt) != 24*time.Hour {
		t.Fatalf("secenekler = %v", opts)
	}
}

func TestServis_YayinOlustur_AdresHatalari(t *testing.T) {
	durumlar := []struct {
		ad   string
		boz  func(*YayinIstegi)
		hata error
	}{
		{"yabanci master", func(i *YayinIstegi) { i.MasterURL = "masters/" + kullaniciB + "/p/m.wav" }, ErrSahiplik},
		{"yabanci stem", func(i *YayinIstegi) { i.Stemler[0].URL = "stems/" + kullaniciB + "/p/s.mp3" }, ErrSahiplik},
		{"bozuk adres", func(i *YayinIstegi) { i.MasterURL = "https://kotu.example.com/masters/x/m.wav" }, ErrDogrulama},
		{"ciplak anlamsiz", func(i *YayinIstegi) { i.MasterURL = "merhaba" }, ErrDogrulama},
		{"public dosya", func(i *YayinIstegi) { i.MasterURL = "exports/" + kullaniciA + "/e1.mp3" }, ErrDogrulama},
	}
	for _, dr := range durumlar {
		d := profilliDepo()
		s, q := testServisi(d)
		ist := yayinIstegiA()
		dr.boz(&ist)
		if _, err := s.YayinOlustur(context.Background(), kullaniciA, ist); !errors.Is(err, dr.hata) {
			t.Errorf("%s: %v bekleniyordu, gelen %v", dr.ad, dr.hata, err)
		}
		if len(d.eklenen) != 0 || len(q.gorevler) != 0 {
			t.Errorf("%s: hatali istek kaydedilmemeli", dr.ad)
		}
	}
}

func TestServis_YayinOlustur_SlugCakismasiYenidenDenenir(t *testing.T) {
	d := profilliDepo()
	d.slugCakismasi = 2
	s, _ := testServisi(d)
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); err != nil {
		t.Fatalf("iki cakismadan sonra basarmali: %v", err)
	}
	d = profilliDepo()
	d.slugCakismasi = 5
	s, _ = testServisi(d)
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); err == nil {
		t.Fatal("bes cakismada vazgecmeli")
	}
}

func TestServis_YayinOlustur_KuyrukHatasindaGeriAlinir(t *testing.T) {
	d := profilliDepo()
	s, q := testServisi(d)
	q.hata = errors.New("redis kapali")
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, yayinIstegiA()); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if len(d.kaldirilan) != 1 || d.kaldirilan[0] != d.eklenen[0].ID {
		t.Fatalf("eklenen yayin geri alinmali: %v", d.kaldirilan)
	}
}

func TestServis_YayinOlustur_Cover(t *testing.T) {
	coverIstegi := func(kaynak string) YayinIstegi {
		ist := yayinIstegiA()
		ist.KaynakYayinID = &kaynak
		return ist
	}
	durumlar := []struct {
		ad     string
		kaynak *YayinKaydi
		kimlik string
		hata   error
	}{
		{"yok", nil, yayinX, ErrKaynakBulunamadi},
		{"gecersiz kimlik", nil, "x", ErrKaynakBulunamadi},
		{"gizli", sahteYayin(yayinX, kullaniciB, GorunurlukTakipciler, DurumYayinda), yayinX, ErrKaynakBulunamadi},
		{"hazirlaniyor", sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumHazirlaniyor), yayinX, ErrKaynakBulunamadi},
		{"cover'in cover'i", func() *YayinKaydi {
			k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
			k.KaynakYayinID = yayinY
			return k
		}(), yayinX, ErrKaynakBulunamadi},
		{"altyapi kapali", func() *YayinKaydi {
			k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
			k.AltyapiAcik = false
			return k
		}(), yayinX, ErrAltyapiKapali},
	}
	for _, dr := range durumlar {
		d := profilliDepo()
		if dr.kaynak != nil {
			d.yayinlar[dr.kaynak.ID] = dr.kaynak
		}
		s, _ := testServisi(d)
		if _, err := s.YayinOlustur(context.Background(), kullaniciA, coverIstegi(dr.kimlik)); !errors.Is(err, dr.hata) {
			t.Errorf("%s: %v bekleniyordu, gelen %v", dr.ad, dr.hata, err)
		}
	}

	d := profilliDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciB, GorunurlukLink, DurumYayinda)
	s, _ := testServisi(d)
	if _, err := s.YayinOlustur(context.Background(), kullaniciA, coverIstegi(yayinX)); err != nil {
		t.Fatalf("link yayini cover'lanabilir: %v", err)
	}
	y := d.eklenen[0]
	if y.AltyapiAcik || len(y.Hazirlik.Stems) != 0 || y.KaynakYayinID != yayinX || y.KaynakBaslik != "Orijinal" || y.KaynakSahipAdi != "sahip" {
		t.Fatalf("cover = %+v", y)
	}
}

func TestServis_YayinGosterGorunurluk(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumHata)
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukLink, DurumYayinda)
	d.yayinlar[yayinY].Slug = "Zz9yYx8w"
	s, _ := testServisi(d)
	ctx := context.Background()

	if y, err := s.YayinGoster(ctx, kullaniciA, yayinX); err != nil || !y.BenimMi || y.Durum != DurumHata {
		t.Fatalf("sahip hata yayinini gormeli: %+v, %v", y, err)
	}
	if _, err := s.YayinGoster(ctx, kullaniciB, yayinX); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("yabanci hata yayinini gormemeli: %v", err)
	}
	if y, err := s.YayinGoster(ctx, kullaniciB, yayinY); err != nil || y.BenimMi {
		t.Fatalf("link detayda gorunur: %+v, %v", y, err)
	}
	if _, err := s.YayinGoster(ctx, kullaniciB, "x"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gecersiz id: %v", err)
	}
	if y, err := s.SlugGoster(ctx, kullaniciB, "Zz9yYx8w"); err != nil || y.ID != yayinY {
		t.Fatalf("slug: %+v, %v", y, err)
	}
	if _, err := s.SlugGoster(ctx, kullaniciB, "kisa"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gecersiz slug: %v", err)
	}
}

func TestServis_YayinGuncelle(t *testing.T) {
	d := yeniSahteDepo()
	kapali := sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumYayinda)
	kapali.AltyapiAcik = false
	d.yayinlar[yayinX] = kapali
	s, _ := testServisi(d)
	ctx := context.Background()
	ac, link := true, GorunurlukLink

	if _, err := s.YayinGuncelle(ctx, kullaniciB, yayinX, YayinYamasi{Gorunurluk: &link}); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasi guncelleyemez: %v", err)
	}
	// Stem kopyasi var: altyapi yeniden acilabilir.
	y, err := s.YayinGuncelle(ctx, kullaniciA, yayinX, YayinYamasi{AltyapiAcik: &ac, Gorunurluk: &link})
	if err != nil || !y.AltyapiAcik || y.Gorunurluk != GorunurlukLink {
		t.Fatalf("guncelle = %+v, %v", y, err)
	}

	stemsiz := sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumYayinda)
	stemsiz.AltyapiAcik, stemsiz.StemVar = false, false
	d.yayinlar[yayinY] = stemsiz
	if _, err := s.YayinGuncelle(ctx, kullaniciA, yayinY, YayinYamasi{AltyapiAcik: &ac}); !errors.Is(err, ErrStemYok) {
		t.Fatalf("stemsiz yayin: %v", err)
	}
	stemsiz.StemVar, stemsiz.KaynakYayinID = true, yayinX
	if _, err := s.YayinGuncelle(ctx, kullaniciA, yayinY, YayinYamasi{AltyapiAcik: &ac}); !errors.Is(err, ErrStemYok) {
		t.Fatalf("cover'in altyapisi acilamaz: %v", err)
	}
	kotu := "gizli"
	if _, err := s.YayinGuncelle(ctx, kullaniciA, yayinX, YayinYamasi{Gorunurluk: &kotu}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("gecersiz gorunurluk: %v", err)
	}
}

func TestServis_YayinSil(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumYayinda)
	s, q := testServisi(d)
	ctx := context.Background()
	if err := s.YayinSil(ctx, kullaniciB, yayinX); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasi silemez: %v", err)
	}
	if err := s.YayinSil(ctx, kullaniciA, yayinX); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if len(q.gorevler) != 1 || q.gorevler[0].Type() != TaskTypeDosyaSil {
		t.Fatalf("dosya silme isi kurulmali: %v", q.gorevler)
	}
	opts := q.secenekler[0]
	if secenek(opts, asynq.QueueOpt) != "bakim" || secenek(opts, asynq.ProcessInOpt) != 24*time.Hour {
		t.Fatalf("secenekler = %v", opts)
	}
	if err := s.YayinSil(ctx, kullaniciA, yayinX); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("ikinci silme 404: %v", err)
	}
	if _, err := s.YayinGoster(ctx, kullaniciA, yayinX); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("silinen yayin sahibine de gorunmez: %v", err)
	}

	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumYayinda)
	q.hata = errors.New("redis kapali")
	if err := s.YayinSil(ctx, kullaniciA, yayinY); err != nil {
		t.Fatalf("kuyruk hatasi silmeyi bozmamali: %v", err)
	}
}

func TestServis_TekrarDene(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumHata)
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumYayinda)
	s, q := testServisi(d)
	ctx := context.Background()
	if _, err := s.TekrarDene(ctx, kullaniciB, yayinX); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasi: %v", err)
	}
	if _, err := s.TekrarDene(ctx, kullaniciA, yayinY); !errors.Is(err, ErrDurum) {
		t.Fatalf("yayindaki yayin: %v", err)
	}
	y, err := s.TekrarDene(ctx, kullaniciA, yayinX)
	if err != nil || y.Durum != DurumHazirlaniyor || y.ID != yayinX {
		t.Fatalf("tekrar dene = %+v, %v", y, err)
	}
	if len(q.gorevler) != 1 || q.gorevler[0].Type() != TaskTypeHazirla {
		t.Fatalf("hazirla isi yeniden kurulmali: %v", q.gorevler)
	}

	d.yayinlar[yayinX].Durum = DurumHata
	q.hata = errors.New("redis kapali")
	if _, err := s.TekrarDene(ctx, kullaniciA, yayinX); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if d.yayinlar[yayinX].Durum != DurumHata {
		t.Fatalf("kuyruga girmeyen yayin hataya donmeli: %s", d.yayinlar[yayinX].Durum)
	}
}

type imzaliDepolama struct{}

func (imzaliDepolama) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (imzaliDepolama) Delete(context.Context, string) error { return nil }
func (imzaliDepolama) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (imzaliDepolama) URLFor(_ context.Context, key string) (string, time.Time, error) {
	return "https://imzali.example/" + key, time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC), nil
}

func TestServis_Dosyalar(t *testing.T) {
	d := yeniSahteDepo()
	hazir := sahteYayin(yayinX, kullaniciA, GorunurlukHerkes, DurumYayinda)
	hazir.MasterAnahtari = "sosyal/" + yayinX + "/master.wav"
	d.yayinlar[yayinX] = hazir
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciA, GorunurlukHerkes, DurumHazirlaniyor)
	ctx := context.Background()

	s, _ := testServisi(d)
	dosya, err := s.Dosyalar(ctx, kullaniciB, yayinX)
	if err != nil || !strings.HasSuffix(dosya.Master.URL, "/sosyal/"+yayinX+"/master.wav") || dosya.Master.ExpiresAt != nil {
		t.Fatalf("R2'siz adres = %+v, %v", dosya, err)
	}
	imzali := NewServis(d, &sahteKuyruk{}, imzaliDepolama{}, testR2())
	dosya, err = imzali.Dosyalar(ctx, kullaniciB, yayinX)
	if err != nil || dosya.Master.URL != "https://imzali.example/sosyal/"+yayinX+"/master.wav" || dosya.Master.ExpiresAt == nil {
		t.Fatalf("imzali adres = %+v, %v", dosya, err)
	}
	if _, err := s.Dosyalar(ctx, kullaniciA, yayinY); !errors.Is(err, ErrDurum) {
		t.Fatalf("hazirlanan yayinin dosyasi yok: %v", err)
	}
	if _, err := s.Dosyalar(ctx, kullaniciB, yayinY); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("yabanci hazirlanan yayini gormez: %v", err)
	}
}
````

**Modify** `core-service/internal/handler/sosyal_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_test.go b/core-service/internal/handler/sosyal_test.go
--- a/core-service/internal/handler/sosyal_test.go
+++ b/core-service/internal/handler/sosyal_test.go
@@ -23,10 +23,16 @@ type sahteSosyal struct {
 
 	profil      *sosyal.Profil
 	adUygunlugu *sosyal.AdUygunlugu
+	yayinYaniti *sosyal.YayinYaniti
+	detay       *sosyal.YayinDetayi
+	dosyalar    *sosyal.YayinDosyalari
 
 	sonKullanici string
 	sonAd        string
 	sonProfil    sosyal.ProfilGirdisi
+	sonYayin     string
+	sonIstek     sosyal.YayinIstegi
+	sonYama      sosyal.YayinYamasi
 }
 
 func (s *sahteSosyal) ProfilBen(_ context.Context, k string) (*sosyal.Profil, error) {
@@ -49,6 +55,41 @@ func (s *sahteSosyal) ProfilGoster(_ context.Context, k, ad string) (*sosyal.Pro
 	return s.profil, s.hata
 }
 
+func (s *sahteSosyal) YayinOlustur(_ context.Context, k string, ist sosyal.YayinIstegi) (*sosyal.YayinYaniti, error) {
+	s.sonKullanici, s.sonIstek = k, ist
+	return s.yayinYaniti, s.hata
+}
+
+func (s *sahteSosyal) YayinGoster(_ context.Context, k, id string) (*sosyal.YayinDetayi, error) {
+	s.sonKullanici, s.sonYayin = k, id
+	return s.detay, s.hata
+}
+
+func (s *sahteSosyal) SlugGoster(_ context.Context, k, slug string) (*sosyal.YayinDetayi, error) {
+	s.sonKullanici, s.sonYayin = k, slug
+	return s.detay, s.hata
+}
+
+func (s *sahteSosyal) YayinGuncelle(_ context.Context, k, id string, y sosyal.YayinYamasi) (*sosyal.YayinDetayi, error) {
+	s.sonKullanici, s.sonYayin, s.sonYama = k, id, y
+	return s.detay, s.hata
+}
+
+func (s *sahteSosyal) YayinSil(_ context.Context, k, id string) error {
+	s.sonKullanici, s.sonYayin = k, id
+	return s.hata
+}
+
+func (s *sahteSosyal) TekrarDene(_ context.Context, k, id string) (*sosyal.YayinYaniti, error) {
+	s.sonKullanici, s.sonYayin = k, id
+	return s.yayinYaniti, s.hata
+}
+
+func (s *sahteSosyal) Dosyalar(_ context.Context, k, id string) (*sosyal.YayinDosyalari, error) {
+	s.sonKullanici, s.sonYayin = k, id
+	return s.dosyalar, s.hata
+}
+
 // sosyalUygulamasi, /api/social grubunu sahte servisle kurar.
 func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
 	app := fiber.New()
````

**Create** `core-service/internal/handler/sosyal_yayin_test.go`:

````go
package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
)

func ornekDetay() *sosyal.YayinDetayi {
	d := &sosyal.YayinDetayi{Durum: sosyal.DurumYayinda, Gorunurluk: sosyal.GorunurlukHerkes}
	d.ID, d.Slug, d.Baslik = "y1", "Ab3dEf9h", "Gece"
	d.DalgaFormu = []float64{}
	d.Yapi = []sosyal.YapiOgesi{}
	d.Sozler = sosyal.Sozler{Bolumler: []sosyal.SozBolumu{}}
	d.Olusturma = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	return d
}

func TestSosyal_YayinOlustur202(t *testing.T) {
	s := &sahteSosyal{yayinYaniti: &sosyal.YayinYaniti{ID: "y1", Slug: "Ab3dEf9h", Durum: sosyal.DurumHazirlaniyor}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar",
		`{"baslik":"Gece","gorunurluk":"herkes","altyapiAcik":true,"masterUrl":"masters/u/p/m.wav","stemler":[{"id":"d","enstruman":"drums","url":"stems/u/p/d.mp3","sure":12.5}],"bpm":120,"sureSn":184,"ton":{"tonik":"A","mod":"minor"},"kaynakYayinId":null}`)
	if durum != http.StatusAccepted || govde["durum"] != "hazirlaniyor" || govde["id"] != "y1" || govde["slug"] != "Ab3dEf9h" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
	if s.sonIstek.Baslik != "Gece" || len(s.sonIstek.Stemler) != 1 || s.sonIstek.Stemler[0].Sure != 12.5 || s.sonIstek.KaynakYayinID != nil {
		t.Fatalf("istek = %+v", s.sonIstek)
	}
}

func TestSosyal_YayinOlusturHataKodlari(t *testing.T) {
	durumlar := []struct {
		hata  error
		durum int
		kod   string
	}{
		{sosyal.ErrProfilYok, http.StatusConflict, "profil_yok"},
		{sosyal.ErrSahiplik, http.StatusForbidden, "sahiplik"},
		{sosyal.ErrKaynakBulunamadi, http.StatusNotFound, "kaynak_bulunamadi"},
		{sosyal.ErrAltyapiKapali, http.StatusForbidden, "altyapi_kapali"},
		{sosyal.ErrDogrulama, http.StatusUnprocessableEntity, "dogrulama"},
	}
	for _, d := range durumlar {
		durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: d.hata}, sosyalKullanici), http.MethodPost, "/api/social/yayinlar", `{}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}

func TestSosyal_YayinOlusturHizSiniri(t *testing.T) {
	app := fiber.New()
	grup := app.Group("/api/social", kullaniciOlarak(sosyalKullanici))
	NewSosyalHandler(&sahteSosyal{}).Rotalar(grup, func(c *fiber.Ctx) error {
		return c.Status(http.StatusTooManyRequests).JSON(fiber.Map{"error": fiber.Map{"code": "RATE_LIMITED", "message": "x"}})
	})
	if durum, _ := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); durum != http.StatusTooManyRequests {
		t.Fatalf("yayin limiti POST /yayinlar'a uygulanmali: %d", durum)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodGet, "/api/social/yayinlar/y1", ""); durum == http.StatusTooManyRequests {
		t.Fatal("yayin limiti okuma uclarina uygulanmamali")
	}
}

// YayinDetayi sozlesmesi: gorunurluk her zaman, kaynak null olarak,
// dalgaFormu dizi olarak bulunmali.
func TestSosyal_YayinDetaySozlesmeAlanlari(t *testing.T) {
	s := &sahteSosyal{detay: ornekDetay()}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/yayinlar/y1", "")
	if durum != http.StatusOK || s.sonYayin != "y1" {
		t.Fatalf("durum=%d", durum)
	}
	for _, alan := range []string{"id", "slug", "baslik", "sahip", "tur", "bpm", "ton", "sureSn", "dalgaFormu",
		"begeniSayisi", "yorumSayisi", "coverSayisi", "begendimMi", "altyapiAcik", "kaynak", "olusturma",
		"aciklama", "yorumAcik", "sozler", "yapi", "durum", "benimMi", "gorunurluk"} {
		if _, ok := govde[alan]; !ok {
			t.Errorf("%s alani yok", alan)
		}
	}
	if govde["kaynak"] != nil {
		t.Errorf("kaynak null olmali: %v", govde["kaynak"])
	}
	if _, dizi := govde["dalgaFormu"].([]any); !dizi {
		t.Errorf("dalgaFormu dizi olmali: %v", govde["dalgaFormu"])
	}
	if govde["olusturma"] != "2026-10-12T10:00:00Z" {
		t.Errorf("olusturma RFC3339 olmali: %v", govde["olusturma"])
	}
	ton := govde["ton"].(map[string]any)
	if _, ok := ton["tonik"]; !ok {
		t.Errorf("ton.tonik yok: %v", ton)
	}
}

func TestSosyal_SlugVeYok404(t *testing.T) {
	s := &sahteSosyal{detay: ornekDetay()}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/s/Ab3dEf9h", ""); durum != http.StatusOK || s.sonYayin != "Ab3dEf9h" {
		t.Fatalf("slug durum=%d son=%q", durum, s.sonYayin)
	}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: sosyal.ErrBulunamadi}, sosyalKullanici), http.MethodGet, "/api/social/s/Ab3dEf9h", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_YayinGuncelleYalnizcaGonderilenAlanlar(t *testing.T) {
	s := &sahteSosyal{detay: ornekDetay()}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPatch, "/api/social/yayinlar/y1", `{"gorunurluk":"link"}`)
	if durum != http.StatusOK {
		t.Fatalf("durum=%d", durum)
	}
	if s.sonYama.Gorunurluk == nil || *s.sonYama.Gorunurluk != "link" || s.sonYama.Aciklama != nil || s.sonYama.AltyapiAcik != nil || s.sonYama.YorumAcik != nil {
		t.Fatalf("yama = %+v", s.sonYama)
	}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: sosyal.ErrStemYok}, sosyalKullanici), http.MethodPatch, "/api/social/yayinlar/y1", `{"altyapiAcik":true}`)
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "stem_yok" {
		t.Fatalf("stem_yok: durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_YayinSil204Ve404(t *testing.T) {
	s := &sahteSosyal{}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yayinlar/y1", ""); durum != http.StatusNoContent || s.sonYayin != "y1" {
		t.Fatalf("durum=%d", durum)
	}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: sosyal.ErrBulunamadi}, sosyalKullanici), http.MethodDelete, "/api/social/yayinlar/y1", ""); durum != http.StatusNotFound {
		t.Fatalf("durum=%d", durum)
	}
}

func TestSosyal_TekrarDene202Ve409(t *testing.T) {
	s := &sahteSosyal{yayinYaniti: &sosyal.YayinYaniti{ID: "y1", Slug: "Ab3dEf9h", Durum: sosyal.DurumHazirlaniyor}}
	if durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/tekrar-dene", ""); durum != http.StatusAccepted {
		t.Fatalf("durum=%d", durum)
	}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: sosyal.ErrDurum}, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/tekrar-dene", "")
	if durum != http.StatusConflict || hataKodu(govde) != "durum" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_DosyalarMasterURL(t *testing.T) {
	bitis := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	s := &sahteSosyal{dosyalar: &sosyal.YayinDosyalari{Master: sosyal.DosyaAdresi{URL: "https://imzali/x", ExpiresAt: &bitis}}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/dosyalar", "")
	master, _ := govde["master"].(map[string]any)
	if durum != http.StatusOK || master["url"] != "https://imzali/x" || master["expiresAt"] != "2026-10-12T11:00:00Z" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors such as `undefined: yayinIsteginiDuzenle`, `undefined: MasterHedefi`, `too many arguments in call to NewServis`, `s.YayinOlustur undefined`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/anahtarlar.go`:

````go
package sosyal

import (
	"path"
	"regexp"

	"github.com/makeasinger/api/internal/client"
)

// uzantiDeseni, segment desenindeki uzanti kuraliyla aynidir.
var uzantiDeseni = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// uzanti, kaynak anahtarin uzantisini doner; desene uymuyorsa bos.
func uzanti(kaynak string) string {
	u := path.Ext(kaynak)
	if !uzantiDeseni.MatchString(u) {
		return ""
	}
	return u
}

// MasterHedefi, master kopyasinin anahtaridir: sosyal/{id}/master.{ext}.
func MasterHedefi(yayinID, kaynak string) (string, error) {
	return client.SosyalAnahtari(yayinID, "master"+uzanti(kaynak))
}

// StemHedefi, stem kopyasinin anahtaridir: sosyal/{id}/stems/{stemId}.{ext}.
func StemHedefi(yayinID, stemID, kaynak string) (string, error) {
	return client.SosyalAnahtari(yayinID, "stems", stemID+uzanti(kaynak))
}

// OnizlemeHedefi, 30 sn onizlemenin anahtaridir: sosyal/{id}/onizleme.mp3.
func OnizlemeHedefi(yayinID string) (string, error) {
	return client.SosyalAnahtari(yayinID, "onizleme.mp3")
}
````

**Create** `core-service/internal/sosyal/yayin_kurallari.go`:

````go
package sosyal

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// stemIDDeseni: stem id bir anahtar segmenti olur (stems/{stemId}.{ext});
// segment deseninin uzantisiz halidir.
var stemIDDeseni = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const (
	baslikEnUzun      = 80
	aciklamaEnUzun    = 300
	turEnUzun         = 40
	tonikEnUzun       = 16
	enstrumanEnUzun   = 40
	bpmEnAz, bpmEnCok = 20, 300
	stemEnCok         = 32
	yapiEnCok         = 200
	bolumEnCok        = 200
	satirEnCok        = 200
)

// GorunurlukGecerli, degerin uc gorunurlukten biri oldugunu soyler.
func GorunurlukGecerli(g string) bool {
	return g == GorunurlukHerkes || g == GorunurlukTakipciler || g == GorunurlukLink
}

func runeAraliginda(s string, enAz, enCok int) bool {
	n := utf8.RuneCountInString(s)
	return n >= enAz && n <= enCok
}

// yayinIsteginiDuzenle, istegi kirpar, bos dizileri doldurur ve sozlesme
// kurallarini uygular. Kaynak (cover) ve URL sahipligi servis tarafinda
// denetlenir; burada yalnizca bicim vardir.
func yayinIsteginiDuzenle(ist *YayinIstegi) error {
	ist.Baslik = strings.TrimSpace(ist.Baslik)
	ist.Aciklama = strings.TrimSpace(ist.Aciklama)
	switch {
	case !runeAraliginda(ist.Baslik, 1, baslikEnUzun):
		return dogrulamaHatasi("baslik 1-80 karakter olmali")
	case !runeAraliginda(ist.Aciklama, 0, aciklamaEnUzun):
		return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
	case !GorunurlukGecerli(ist.Gorunurluk):
		return dogrulamaHatasi("gorunurluk herkes, takipciler ya da link olmali")
	case ist.Ton.Mod != "major" && ist.Ton.Mod != "minor":
		return dogrulamaHatasi("ton.mod major ya da minor olmali")
	case !runeAraliginda(ist.Ton.Tonik, 1, tonikEnUzun):
		return dogrulamaHatasi("ton.tonik bos olamaz")
	case !runeAraliginda(ist.Tur, 1, turEnUzun):
		return dogrulamaHatasi("tur 1-40 karakter olmali")
	case ist.SureSn <= 0:
		return dogrulamaHatasi("sureSn sifirdan buyuk olmali")
	case ist.Bpm < bpmEnAz || ist.Bpm > bpmEnCok:
		return dogrulamaHatasi("bpm 20-300 araliginda olmali")
	case ist.MasterURL == "":
		return dogrulamaHatasi("masterUrl zorunlu")
	case len(ist.Yapi) > yapiEnCok:
		return dogrulamaHatasi("yapi cok uzun")
	case len(ist.Sozler.Bolumler) > bolumEnCok:
		return dogrulamaHatasi("sozler cok uzun")
	}
	if ist.Yapi == nil {
		ist.Yapi = []YapiOgesi{}
	}
	for _, o := range ist.Yapi {
		if o.ID == "" || o.Olcu < 0 {
			return dogrulamaHatasi("yapi ogesi gecersiz")
		}
	}
	if ist.Sozler.Bolumler == nil {
		ist.Sozler.Bolumler = []SozBolumu{}
	}
	for i := range ist.Sozler.Bolumler {
		b := &ist.Sozler.Bolumler[i]
		if len(b.Satirlar) > satirEnCok {
			return dogrulamaHatasi("sozler cok uzun")
		}
		if b.Satirlar == nil {
			b.Satirlar = []string{}
		}
	}

	if ist.KaynakYayinID != nil && *ist.KaynakYayinID == "" {
		ist.KaynakYayinID = nil
	}
	if ist.KaynakYayinID != nil {
		// Cover'in altyapisi her zaman kapalidir; stem listesi yok sayilir.
		ist.AltyapiAcik = false
		ist.Stemler = nil
		return nil
	}
	if !ist.AltyapiAcik {
		// Altyapi kapaliysa stem kopyalanmaz; liste yok sayilir.
		ist.Stemler = nil
		return nil
	}
	if len(ist.Stemler) == 0 {
		return dogrulamaHatasi("altyapi acikken en az bir stem gerekli")
	}
	if len(ist.Stemler) > stemEnCok {
		return dogrulamaHatasi("cok fazla stem")
	}
	gorulen := make(map[string]bool, len(ist.Stemler))
	for _, st := range ist.Stemler {
		if !stemIDDeseni.MatchString(st.ID) {
			return dogrulamaHatasi("stem id gecersiz")
		}
		if gorulen[st.ID] {
			return dogrulamaHatasi("stem id tekrar ediyor")
		}
		gorulen[st.ID] = true
		if st.URL == "" || st.Sure <= 0 || !runeAraliginda(st.Enstruman, 1, enstrumanEnUzun) {
			return dogrulamaHatasi("stem alanlari gecersiz")
		}
	}
	return nil
}

// yamayiDuzenle, PATCH govdesini dogrular.
func yamayiDuzenle(y *YayinYamasi) error {
	if y.Aciklama != nil {
		a := strings.TrimSpace(*y.Aciklama)
		if !runeAraliginda(a, 0, aciklamaEnUzun) {
			return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
		}
		y.Aciklama = &a
	}
	if y.Gorunurluk != nil && !GorunurlukGecerli(*y.Gorunurluk) {
		return dogrulamaHatasi("gorunurluk herkes, takipciler ya da link olmali")
	}
	return nil
}
````

**Modify** `core-service/internal/sosyal/servis.go`:

````diff
diff --git a/core-service/internal/sosyal/servis.go b/core-service/internal/sosyal/servis.go
--- a/core-service/internal/sosyal/servis.go
+++ b/core-service/internal/sosyal/servis.go
@@ -3,16 +3,30 @@ package sosyal
 import (
 	"context"
 	"errors"
+
+	"github.com/hibiken/asynq"
+	"github.com/makeasinger/api/internal/client"
+	"github.com/makeasinger/api/internal/config"
 )
 
+// Kuyruk, *asynq.Client'in burada kullanilan yuzu.
+type Kuyruk interface {
+	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
+}
+
 // Servis, Kesfet uclarinin is kurallarini uygular. Gorunurluk detayda
 // burada (DetayGorebilir), listelerde depoda (gorunurlukKosulu) uygulanir.
 type Servis struct {
-	depo Depo
+	depo     Depo
+	kuyruk   Kuyruk
+	depolama client.StorageClient // nil: R2 yok, imzasiz adres uretilir
+	r2       *config.R2Config
 }
 
-func NewServis(depo Depo) *Servis {
-	return &Servis{depo: depo}
+// NewServis kurar. depolama nil olabilir (R2 yapilandirilmamis); main
+// tipli nil tuzagina dusmemek icin yalnizca gercek istemciyi gecirir.
+func NewServis(depo Depo, kuyruk Kuyruk, depolama client.StorageClient, r2 *config.R2Config) *Servis {
+	return &Servis{depo: depo, kuyruk: kuyruk, depolama: depolama, r2: r2}
 }
 
 // kullaniciCoz, istegi yapanin kimligini dogrular. Auth middleware her
````

**Create** `core-service/internal/sosyal/servis_yayin.go`:

````go
package sosyal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
)

// Kuyruk gorevleri. Sabitler is turu adlaridir; main bunlari isleyicilere
// baglar.
const (
	// TaskTypeHazirla, yayinin dosyalarini kopyalar, analiz eder ve
	// yayinda yapar (sosyal kuyrugu).
	TaskTypeHazirla = "sosyal:hazirla"
	// TaskTypeDosyaSil, silinen yayinin sosyal/{id}/ onekini 24 saat
	// sonra temizler (bakim kuyrugu).
	TaskTypeDosyaSil = "sosyal:dosya-sil"

	// KuyrukSosyal, hazirlama islerinin asynq kuyrugu.
	KuyrukSosyal = "sosyal"

	hazirlaDenemesi     = 3
	hazirlaSaklama      = 24 * time.Hour
	dosyaSilmeGecikmesi = 24 * time.Hour
	dosyaSilDenemesi    = 10
	slugDenemesi        = 5
)

// GorevYuku, iki sosyal gorevinin yukudur.
type GorevYuku struct {
	YayinID string `json:"yayinId"`
}

func gorevYuku(yayinID string) []byte {
	b, _ := json.Marshal(GorevYuku{YayinID: yayinID})
	return b
}

func (s *Servis) hazirlamayaKoy(yayinID string) error {
	_, err := s.kuyruk.Enqueue(asynq.NewTask(TaskTypeHazirla, gorevYuku(yayinID)),
		asynq.Queue(KuyrukSosyal),
		asynq.MaxRetry(hazirlaDenemesi),
		asynq.Retention(hazirlaSaklama),
	)
	return err
}

// anahtarCoz, istemcinin gonderdigi adresi (tam URL ya da ciplak anahtar)
// sahiplik denetimiyle anahtara cevirir. Public bucket'taki (exports/)
// dosya reddedilir: kopya ayni bucket icinde yapilir.
func (s *Servis) anahtarCoz(ham, kullaniciID, alan string) (string, error) {
	key, err := client.SahipliAnahtarVeyaURLCoz(ham, kullaniciID, s.r2)
	switch {
	case errors.Is(err, client.ErrYabanciAnahtar):
		return "", ErrSahiplik
	case err != nil:
		return "", dogrulamaHatasi(alan + " gecersiz dosya adresi")
	case client.IsPublicKey(key):
		return "", dogrulamaHatasi(alan + " ozel bir dosya olmali")
	}
	return key, nil
}

// kaynakCoz, cover'in orijinalini dogrular: izleyiciye gorunur, yayinda,
// kendisi cover degil ve altyapisi acik olmali.
func (s *Servis) kaynakCoz(ctx context.Context, hamID, izleyiciID string) (*YayinKaydi, error) {
	id, ok := kimlikCoz(hamID)
	if !ok {
		return nil, ErrKaynakBulunamadi
	}
	k, err := s.depo.YayinGetir(ctx, id, izleyiciID)
	if errors.Is(err, ErrBulunamadi) {
		return nil, ErrKaynakBulunamadi
	}
	if err != nil {
		return nil, err
	}
	if !DetayGorebilir(k.gorunurlukGirdisi(izleyiciID)) || k.Durum != DurumYayinda || k.KaynakYayinID != "" {
		return nil, ErrKaynakBulunamadi
	}
	if !k.AltyapiAcik {
		return nil, ErrAltyapiKapali
	}
	return k, nil
}

// YayinOlustur, yayini hazirlaniyor durumunda kaydeder ve sosyal:hazirla
// isini kuyruga koyar (POST /yayinlar, 202).
func (s *Servis) YayinOlustur(ctx context.Context, kullaniciID string, ist YayinIstegi) (*YayinYaniti, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if _, err := s.depo.ProfilGetir(ctx, id); err != nil {
		return nil, err
	}
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		return nil, err
	}

	yeni := YeniYayin{
		ID: uuid.NewString(), SahipID: id,
		Baslik: ist.Baslik, Aciklama: ist.Aciklama, Tur: ist.Tur, Bpm: ist.Bpm, Ton: ist.Ton,
		SureSn: ist.SureSn, Gorunurluk: ist.Gorunurluk, AltyapiAcik: ist.AltyapiAcik,
		YorumAcik: ist.YorumAcik, Sozler: ist.Sozler, Yapi: ist.Yapi,
		Hazirlik: Hazirlik{Stems: []HazirlikStemi{}},
	}
	if ist.KaynakYayinID != nil {
		kaynak, err := s.kaynakCoz(ctx, *ist.KaynakYayinID, id)
		if err != nil {
			return nil, err
		}
		yeni.KaynakYayinID = kaynak.ID
		yeni.KaynakBaslik = kaynak.Baslik
		yeni.KaynakSahipAdi = kaynak.Sahip.KullaniciAdi
	}

	if yeni.Hazirlik.MasterKey, err = s.anahtarCoz(ist.MasterURL, id, "masterUrl"); err != nil {
		return nil, err
	}
	for _, st := range ist.Stemler {
		key, err := s.anahtarCoz(st.URL, id, "stem url")
		if err != nil {
			return nil, err
		}
		yeni.Hazirlik.Stems = append(yeni.Hazirlik.Stems, HazirlikStemi{
			ID: st.ID, Enstruman: st.Enstruman, Sure: st.Sure, Key: key,
		})
	}

	for deneme := 1; ; deneme++ {
		if yeni.Slug, err = slugUret(); err != nil {
			return nil, fmt.Errorf("slug uretilemedi: %w", err)
		}
		err = s.depo.YayinEkle(ctx, yeni)
		if !errors.Is(err, errSlugCakisti) || deneme == slugDenemesi {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	if err := s.hazirlamayaKoy(yeni.ID); err != nil {
		// Kuyruga girmeyen yayin hic hazirlanmaz; sahipsiz bir
		// "hazirlaniyor" satiri birakmak yerine geri alinir.
		if kerr := s.depo.YayinKaldir(ctx, yeni.ID); kerr != nil {
			log.Printf("Uyari: kuyruga girmeyen yayin geri alinamadi (%s): %v", yeni.ID, kerr)
		}
		return nil, fmt.Errorf("hazirlama isi kurulamadi: %w", err)
	}
	return &YayinYaniti{ID: yeni.ID, Slug: yeni.Slug, Durum: DurumHazirlaniyor}, nil
}

// gorunurYayin, yayini yukler ve detay gorunurlugunu uygular.
func (s *Servis) gorunurYayin(ctx context.Context, izleyiciID, hamYayinID string) (*YayinKaydi, error) {
	yayinID, ok := kimlikCoz(hamYayinID)
	if !ok {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.YayinGetir(ctx, yayinID, izleyiciID)
	if err != nil {
		return nil, err
	}
	if !DetayGorebilir(k.gorunurlukGirdisi(izleyiciID)) {
		return nil, ErrBulunamadi
	}
	return k, nil
}

// sahibinYayini, yayini yukler ve istegi yapanin sahibi oldugunu, yayinin
// silinmedigini dogrular. Aksi ErrBulunamadi'dir: baskasinin yayininin
// varligi da sizdirilmaz.
func (s *Servis) sahibinYayini(ctx context.Context, sahipID, hamYayinID string) (*YayinKaydi, error) {
	yayinID, ok := kimlikCoz(hamYayinID)
	if !ok {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.YayinGetir(ctx, yayinID, sahipID)
	if err != nil {
		return nil, err
	}
	if k.SahipID != sahipID || k.Silindi {
		return nil, ErrBulunamadi
	}
	return k, nil
}

// YayinGoster, GET /yayinlar/{id}.
func (s *Servis) YayinGoster(ctx context.Context, kullaniciID, yayinID string) (*YayinDetayi, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	return &k.YayinDetayi, nil
}

// SlugGoster, GET /s/{slug}.
func (s *Servis) SlugGoster(ctx context.Context, kullaniciID, slug string) (*YayinDetayi, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if !SlugGecerli(slug) {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.YayinSlugIleGetir(ctx, slug, id)
	if err != nil {
		return nil, err
	}
	if !DetayGorebilir(k.gorunurlukGirdisi(id)) {
		return nil, ErrBulunamadi
	}
	return &k.YayinDetayi, nil
}

// YayinGuncelle, PATCH /yayinlar/{id}. Altyapi yalnizca stem kopyasi olan
// (yayinlanirken altyapisi acik) orijinallerde yeniden acilabilir.
func (s *Servis) YayinGuncelle(ctx context.Context, kullaniciID, yayinID string, yama YayinYamasi) (*YayinDetayi, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if err := yamayiDuzenle(&yama); err != nil {
		return nil, err
	}
	k, err := s.sahibinYayini(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if yama.AltyapiAcik != nil && *yama.AltyapiAcik && !k.AltyapiAcik && (!k.StemVar || k.KaynakYayinID != "") {
		return nil, ErrStemYok
	}
	if err := s.depo.YayinGuncelle(ctx, k.ID, id, yama); err != nil {
		return nil, err
	}
	return s.YayinGoster(ctx, id, k.ID)
}

// YayinSil, DELETE /yayinlar/{id}: yumusak siler ve dosya temizligini 24
// saat sonraya kurar. Kuyruk hatasi loglanir; hesap silme ayni oneki
// yeniden temizler.
func (s *Servis) YayinSil(ctx context.Context, kullaniciID, yayinID string) error {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return err
	}
	yid, ok := kimlikCoz(yayinID)
	if !ok {
		return ErrBulunamadi
	}
	if err := s.depo.YayinSil(ctx, yid, id); err != nil {
		return err
	}
	if _, err := s.kuyruk.Enqueue(asynq.NewTask(TaskTypeDosyaSil, gorevYuku(yid)),
		asynq.Queue("bakim"),
		asynq.ProcessIn(dosyaSilmeGecikmesi),
		asynq.MaxRetry(dosyaSilDenemesi),
	); err != nil {
		log.Printf("Uyari: yayin dosya temizligi kurulamadi (%s): %v", yid, err)
	}
	return nil
}

// TekrarDene, POST /yayinlar/{id}/tekrar-dene: hata durumundaki yayini
// yeniden hazirlamaya alir.
func (s *Servis) TekrarDene(ctx context.Context, kullaniciID, yayinID string) (*YayinYaniti, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.sahibinYayini(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if err := s.depo.TekrarDenemeyeAl(ctx, k.ID, id); err != nil {
		return nil, err
	}
	if err := s.hazirlamayaKoy(k.ID); err != nil {
		if herr := s.depo.HataYap(ctx, k.ID); herr != nil {
			log.Printf("Uyari: kuyruga girmeyen yayin hataya alinamadi (%s): %v", k.ID, herr)
		}
		return nil, fmt.Errorf("hazirlama isi kurulamadi: %w", err)
	}
	return &YayinYaniti{ID: k.ID, Slug: k.Slug, Durum: DurumHazirlaniyor}, nil
}

// Dosyalar, POST /yayinlar/{id}/dosyalar: master kopyasi icin imzali
// dinleme adresi (R2_PRESIGN_TTL, varsayilan 1 saat).
func (s *Servis) Dosyalar(ctx context.Context, kullaniciID, yayinID string) (*YayinDosyalari, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if k.Durum != DurumYayinda || k.MasterAnahtari == "" {
		return nil, ErrDurum
	}
	url, bitis, err := client.ResolveURL(ctx, s.depolama, s.r2, k.MasterAnahtari)
	if err != nil {
		return nil, fmt.Errorf("dinleme adresi uretilemedi: %w", err)
	}
	adres := DosyaAdresi{URL: url}
	if !bitis.IsZero() {
		adres.ExpiresAt = &bitis
	}
	return &YayinDosyalari{Master: adres}, nil
}
````

**Modify** `core-service/internal/handler/sosyal.go`:

````diff
diff --git a/core-service/internal/handler/sosyal.go b/core-service/internal/handler/sosyal.go
--- a/core-service/internal/handler/sosyal.go
+++ b/core-service/internal/handler/sosyal.go
@@ -18,6 +18,14 @@ type SosyalServisi interface {
 	ProfilKaydet(ctx context.Context, kullaniciID string, g sosyal.ProfilGirdisi) (*sosyal.Profil, error)
 	AdUygunMu(ctx context.Context, kullaniciID, ad string) (*sosyal.AdUygunlugu, error)
 	ProfilGoster(ctx context.Context, izleyiciID, ad string) (*sosyal.Profil, error)
+
+	YayinOlustur(ctx context.Context, kullaniciID string, ist sosyal.YayinIstegi) (*sosyal.YayinYaniti, error)
+	YayinGoster(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinDetayi, error)
+	SlugGoster(ctx context.Context, kullaniciID, slug string) (*sosyal.YayinDetayi, error)
+	YayinGuncelle(ctx context.Context, kullaniciID, yayinID string, yama sosyal.YayinYamasi) (*sosyal.YayinDetayi, error)
+	YayinSil(ctx context.Context, kullaniciID, yayinID string) error
+	TekrarDene(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinYaniti, error)
+	Dosyalar(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinDosyalari, error)
 }
 
 var _ SosyalServisi = (*sosyal.Servis)(nil)
@@ -38,6 +46,14 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
 	r.Put("/profil/ben", h.ProfilKaydet)
 	r.Get("/profil/kullanici-adi/uygun", h.AdUygun)
 	r.Get("/profiller/:ad", h.Profil)
+
+	r.Post("/yayinlar", yayinLimiti, h.YayinOlustur)
+	r.Get("/yayinlar/:id", h.Yayin)
+	r.Patch("/yayinlar/:id", h.YayinGuncelle)
+	r.Delete("/yayinlar/:id", h.YayinSil)
+	r.Post("/yayinlar/:id/tekrar-dene", h.TekrarDene)
+	r.Post("/yayinlar/:id/dosyalar", h.Dosyalar)
+	r.Get("/s/:slug", h.Slug)
 }
 
 // sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
````

**Create** `core-service/internal/handler/sosyal_yayin.go`:

````go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// YayinOlustur handles POST /api/social/yayinlar (202). Profil yoksa 409
// profil_yok.
func (h *SosyalHandler) YayinOlustur(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var ist sosyal.YayinIstegi
	if err := c.BodyParser(&ist); err != nil {
		return govdeHatasi(c)
	}
	y, err := h.servis.YayinOlustur(c.Context(), userID, ist)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.Accepted(c, y)
}

// Yayin handles GET /api/social/yayinlar/:id
func (h *SosyalHandler) Yayin(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	y, err := h.servis.YayinGoster(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, y)
}

// Slug handles GET /api/social/s/:slug
func (h *SosyalHandler) Slug(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	y, err := h.servis.SlugGoster(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, y)
}

// YayinGuncelle handles PATCH /api/social/yayinlar/:id
func (h *SosyalHandler) YayinGuncelle(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var yama sosyal.YayinYamasi
	if err := c.BodyParser(&yama); err != nil {
		return govdeHatasi(c)
	}
	y, err := h.servis.YayinGuncelle(c.Context(), userID, c.Params("id"), yama)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, y)
}

// YayinSil handles DELETE /api/social/yayinlar/:id (204). Govde yok sayilir.
func (h *SosyalHandler) YayinSil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	if err := h.servis.YayinSil(c.Context(), userID, c.Params("id")); err != nil {
		return sosyalHata(c, err)
	}
	return response.NoContent(c)
}

// TekrarDene handles POST /api/social/yayinlar/:id/tekrar-dene (202).
func (h *SosyalHandler) TekrarDene(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	y, err := h.servis.TekrarDene(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.Accepted(c, y)
}

// Dosyalar handles POST /api/social/yayinlar/:id/dosyalar
func (h *SosyalHandler) Dosyalar(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	d, err := h.servis.Dosyalar(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, d)
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1 && golangci-lint run ./...
```

Expected: both packages `ok`; `0 issues.`. Whole Go suite after this task: about 635 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/anahtarlar_test.go \
  core-service/internal/sosyal/yayin_kurallari_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/sosyal/servis_profil_test.go \
  core-service/internal/sosyal/servis_yayin_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/handler/sosyal_yayin_test.go \
  core-service/internal/sosyal/anahtarlar.go \
  core-service/internal/sosyal/yayin_kurallari.go \
  core-service/internal/sosyal/servis.go \
  core-service/internal/sosyal/servis_yayin.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_yayin.go
git commit -F - <<'EOF'
feat(sosyal): yayinlama, detay, yama, silme, tekrar deneme ve dosya uclari

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 8: Worker: `sosyal:hazirla` and `sosyal:dosya-sil`

The job of R5 with the hardening of N13. The worker depends only on interfaces (`sosyal.HazirlikDeposu`, `client.Kopyalayici`, `client.AudioAnalizci`, `client.OnekSilici`, `hesapisaret.Denetci`), so the tests use fakes; `sahteDenetci` and `hepSilinmis` already exist in `worker/render_worker_test.go`.

**Files:**

- Create: `core-service/internal/worker/sosyal_worker_test.go`
- Create: `core-service/internal/worker/sosyal_worker.go`

**Interfaces:**
- Consumes: `sahipYazabilir`, `errSahipSilindi` (existing, `worker/render_worker.go`); `sosyal.HazirlikDeposu`, `HazirlikKaydi`, `HazirlikSonucu`, `StemKaydi`, `GorevYuku`, `MasterHedefi`, `StemHedefi`, `OnizlemeHedefi`, `ErrBulunamadi`, `DurumHazirlaniyor` (Tasks 4, 6, 7); `client.Kopyalayici`, `client.AudioAnalizci`, `client.AnalizRequest`, `client.SosyalOneki`, `client.OnekSilici` (Task 2, existing).
- Produces: `worker.NewSosyalWorker(depo sosyal.HazirlikDeposu, kopyalayici client.Kopyalayici, analizci client.AudioAnalizci, silici client.OnekSilici, hesap hesapisaret.Denetci) *SosyalWorker`; asynq handlers `(*SosyalWorker).Hazirla(ctx, *asynq.Task) error` and `(*SosyalWorker).DosyaSil(ctx, *asynq.Task) error`; field `sonDeneme func(context.Context) bool` (tests override it).

- [ ] **Step 1: Write the failing test**

**Create** `core-service/internal/worker/sosyal_worker_test.go`:

````go
package worker

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/hesapisaret"
	"github.com/makeasinger/api/internal/sosyal"
)

const (
	sYayin = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	sSahip = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

type sahteHazirlikDeposu struct {
	kayit       *sosyal.HazirlikKaydi
	okumaHata   error
	yayinlanan  *sosyal.HazirlikSonucu
	hataYapildi bool
}

func (d *sahteHazirlikDeposu) HazirlikGetir(context.Context, string) (*sosyal.HazirlikKaydi, error) {
	if d.okumaHata != nil {
		return nil, d.okumaHata
	}
	if d.kayit == nil {
		return nil, sosyal.ErrBulunamadi
	}
	return d.kayit, nil
}

func (d *sahteHazirlikDeposu) Yayinla(_ context.Context, _ string, s sosyal.HazirlikSonucu) error {
	d.yayinlanan = &s
	return nil
}

func (d *sahteHazirlikDeposu) HataYap(context.Context, string) error {
	d.hataYapildi = true
	return nil
}

type kopya struct{ kaynak, hedef string }

type sahteKopyalayici struct {
	kopyalar []kopya
	hata     error
}

func (k *sahteKopyalayici) Kopyala(_ context.Context, kaynak, hedef string) error {
	if k.hata != nil {
		return k.hata
	}
	k.kopyalar = append(k.kopyalar, kopya{kaynak, hedef})
	return nil
}

type sahteAnalizci struct {
	istek *client.AnalizRequest
	yanit *client.AnalizResponse
	hata  error
}

func (a *sahteAnalizci) Analiz(_ context.Context, r *client.AnalizRequest) (*client.AnalizResponse, error) {
	a.istek = r
	return a.yanit, a.hata
}

func tepeler(n int, deger float64) []float64 {
	p := make([]float64, n)
	for i := range p {
		p[i] = deger
	}
	return p
}

func hazirKayit() *sosyal.HazirlikKaydi {
	return &sosyal.HazirlikKaydi{
		ID: sYayin, SahipID: sSahip, Durum: sosyal.DurumHazirlaniyor,
		Hazirlik: sosyal.Hazirlik{
			MasterKey: "masters/" + sSahip + "/p1/m1.wav",
			Stems: []sosyal.HazirlikStemi{
				{ID: "drums", Enstruman: "drums", Sure: 184, Key: "stems/" + sSahip + "/p1/drums.mp3"},
				{ID: "bass", Enstruman: "bass", Sure: 184, Key: "stems/" + sSahip + "/p1/bass.mp3"},
			},
		},
	}
}

type sosyalKurulum struct {
	depo   *sahteHazirlikDeposu
	kopya  *sahteKopyalayici
	analiz *sahteAnalizci
	w      *SosyalWorker
}

func sosyalTestWorker(kayit *sosyal.HazirlikKaydi, denetci hesapisaret.Denetci) *sosyalKurulum {
	k := &sosyalKurulum{
		depo:   &sahteHazirlikDeposu{kayit: kayit},
		kopya:  &sahteKopyalayici{},
		analiz: &sahteAnalizci{yanit: &client.AnalizResponse{Peaks: tepeler(64, 0.5), Duration: 184}},
	}
	k.w = NewSosyalWorker(k.depo, k.kopya, k.analiz, nil, denetci)
	return k
}

func hazirlaGorevi() *asynq.Task {
	return asynq.NewTask(sosyal.TaskTypeHazirla, []byte(`{"yayinId":"`+sYayin+`"}`))
}

func TestSosyalWorker_KopyalarAnalizEderYayinlar(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	beklenen := []kopya{
		{"masters/" + sSahip + "/p1/m1.wav", "sosyal/" + sYayin + "/master.wav"},
		{"stems/" + sSahip + "/p1/drums.mp3", "sosyal/" + sYayin + "/stems/drums.mp3"},
		{"stems/" + sSahip + "/p1/bass.mp3", "sosyal/" + sYayin + "/stems/bass.mp3"},
	}
	if len(k.kopya.kopyalar) != len(beklenen) {
		t.Fatalf("kopyalar = %v", k.kopya.kopyalar)
	}
	for i, b := range beklenen {
		if k.kopya.kopyalar[i] != b {
			t.Errorf("kopya %d = %v, beklenen %v", i, k.kopya.kopyalar[i], b)
		}
	}
	if *k.analiz.istek != (client.AnalizRequest{InputKey: "sosyal/" + sYayin + "/master.wav", PreviewKey: "sosyal/" + sYayin + "/onizleme.mp3", PeakCount: 64, PreviewSeconds: 30}) {
		t.Fatalf("analiz istegi = %+v", *k.analiz.istek)
	}
	s := k.depo.yayinlanan
	if s == nil || s.MasterAnahtari != "sosyal/"+sYayin+"/master.wav" || len(s.DalgaFormu) != 64 || s.OnizlemeAnahtari != "sosyal/"+sYayin+"/onizleme.mp3" {
		t.Fatalf("sonuc = %+v", s)
	}
	if len(s.StemListesi) != 2 || s.StemListesi[1] != (sosyal.StemKaydi{ID: "bass", Enstruman: "bass", Sure: 184, Anahtar: "sosyal/" + sYayin + "/stems/bass.mp3"}) {
		t.Fatalf("stem listesi = %+v", s.StemListesi)
	}
	if k.depo.hataYapildi {
		t.Fatal("basarili is hata yapmamali")
	}
}

func TestSosyalWorker_YokSilinmisYaDaHazirOlanAtlanir(t *testing.T) {
	silinmis := hazirKayit()
	silinmis.Silindi = true
	yayinda := hazirKayit()
	yayinda.Durum = sosyal.DurumYayinda
	for ad, kayit := range map[string]*sosyal.HazirlikKaydi{"yok": nil, "silinmis": silinmis, "yayinda": yayinda} {
		k := sosyalTestWorker(kayit, nil)
		if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
			t.Errorf("%s: hata donmemeli: %v", ad, err)
		}
		if len(k.kopya.kopyalar) != 0 || k.depo.yayinlanan != nil {
			t.Errorf("%s: hicbir sey yapilmamali", ad)
		}
	}
}

func TestSosyalWorker_SilinmisSahipKopyalanmazHataOlur(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), &sahteDenetci{yanit: hepSilinmis})
	err := k.w.Hazirla(context.Background(), hazirlaGorevi())
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu: %v", err)
	}
	if len(k.kopya.kopyalar) != 0 || !k.depo.hataYapildi || k.depo.yayinlanan != nil {
		t.Fatalf("kopya=%d hata=%v", len(k.kopya.kopyalar), k.depo.hataYapildi)
	}
}

// Hesap kopyalarin ortasinda silinirse kalan kopyalar yapilmaz.
func TestSosyalWorker_SahipIsinOrtasindaSilinirse(t *testing.T) {
	ikinciDenetimdeSilinmis := func(n int) (bool, error) { return n >= 1, nil }
	k := sosyalTestWorker(hazirKayit(), &sahteDenetci{yanit: ikinciDenetimdeSilinmis})
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu: %v", err)
	}
	if len(k.kopya.kopyalar) != 1 || !k.depo.hataYapildi {
		t.Fatalf("yalnizca master kopyalanmali: %v", k.kopya.kopyalar)
	}
}

func TestSosyalWorker_DenetimHatasiTekrarDenenir(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), &sahteDenetci{yanit: func(int) (bool, error) { return false, errors.New("redis kapali") }})
	err := k.w.Hazirla(context.Background(), hazirlaGorevi())
	if err == nil || errors.Is(err, asynq.SkipRetry) || k.depo.hataYapildi {
		t.Fatalf("tekrar denenebilir hata bekleniyordu: %v hata=%v", err, k.depo.hataYapildi)
	}
}

func TestSosyalWorker_KopyaHatasiSonDenemedeHataOlur(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.kopya.hata = errors.New("r2 kapali")
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("tekrar denenebilir hata bekleniyordu: %v", err)
	}
	if k.depo.hataYapildi || k.depo.yayinlanan != nil {
		t.Fatal("son deneme degilken hata durumuna alinmamali")
	}
	k.w.sonDeneme = func(context.Context) bool { return true }
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err == nil {
		t.Fatal("hata donmeli")
	}
	if !k.depo.hataYapildi {
		t.Fatal("son denemede yayin hata olmali")
	}
}

func TestSosyalWorker_AnalizHatasiYayiniDurdurmaz(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.analiz.hata = errors.New("audio-service kapali")
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("analiz hatasi isi bozmamali: %v", err)
	}
	s := k.depo.yayinlanan
	if s == nil || s.DalgaFormu == nil || len(s.DalgaFormu) != 0 || s.OnizlemeAnahtari != "" {
		t.Fatalf("bos dalga formu, onizlemesiz yayinlanmali: %+v", s)
	}
}

func TestSosyalWorker_AnalizCiktisiDenetlenir(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	k.analiz.yanit = &client.AnalizResponse{Peaks: tepeler(10, 0.5)}
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	if len(k.depo.yayinlanan.DalgaFormu) != 0 {
		t.Fatalf("64 olmayan tepe dizisi atilmali: %v", k.depo.yayinlanan.DalgaFormu)
	}

	k = sosyalTestWorker(hazirKayit(), nil)
	p := tepeler(64, 0.5)
	p[0], p[1], p[2] = -0.2, 1.7, math.NaN()
	k.analiz.yanit = &client.AnalizResponse{Peaks: p}
	if err := k.w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	d := k.depo.yayinlanan.DalgaFormu
	if d[0] != 0 || d[1] != 1 || d[2] != 0 || d[3] != 0.5 {
		t.Fatalf("degerler 0-1'e kenetlenmeli: %v", d[:4])
	}
}

func TestSosyalWorker_R2VeAnalizYokken(t *testing.T) {
	d := &sahteHazirlikDeposu{kayit: hazirKayit()}
	w := NewSosyalWorker(d, nil, nil, nil, nil)
	if err := w.Hazirla(context.Background(), hazirlaGorevi()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	if d.yayinlanan == nil || d.yayinlanan.MasterAnahtari != "sosyal/"+sYayin+"/master.wav" || len(d.yayinlanan.DalgaFormu) != 0 {
		t.Fatalf("sonuc = %+v", d.yayinlanan)
	}
}

func TestSosyalWorker_BozukYukTekrarlanmaz(t *testing.T) {
	k := sosyalTestWorker(hazirKayit(), nil)
	for _, yuk := range []string{"{", `{"yayinId":"../x"}`} {
		if err := k.w.Hazirla(context.Background(), asynq.NewTask(sosyal.TaskTypeHazirla, []byte(yuk))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: SkipRetry bekleniyordu: %v", yuk, err)
		}
		if err := k.w.DosyaSil(context.Background(), asynq.NewTask(sosyal.TaskTypeDosyaSil, []byte(yuk))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: DosyaSil SkipRetry bekleniyordu: %v", yuk, err)
		}
	}
}

type sosyalSilici struct {
	onekler []string
	hata    error
}

func (s *sosyalSilici) DeletePrefix(_ context.Context, onek string) (int, error) {
	s.onekler = append(s.onekler, onek)
	return 1, s.hata
}

func TestSosyalWorker_DosyaSil(t *testing.T) {
	gorev := asynq.NewTask(sosyal.TaskTypeDosyaSil, []byte(`{"yayinId":"`+sYayin+`"}`))
	silinmis := hazirKayit()
	silinmis.Silindi = true

	durumlar := []struct {
		ad      string
		kayit   *sosyal.HazirlikKaydi
		silinir bool
	}{
		{"silinmis", silinmis, true},
		{"satir yok", nil, true},
		{"canli", hazirKayit(), false},
	}
	for _, d := range durumlar {
		s := &sosyalSilici{}
		w := NewSosyalWorker(&sahteHazirlikDeposu{kayit: d.kayit}, nil, nil, s, nil)
		if err := w.DosyaSil(context.Background(), gorev); err != nil {
			t.Fatalf("%s: %v", d.ad, err)
		}
		if d.silinir != (len(s.onekler) == 1 && s.onekler[0] == "sosyal/"+sYayin+"/") {
			t.Errorf("%s: onekler = %v", d.ad, s.onekler)
		}
	}

	s := &sosyalSilici{hata: errors.New("r2 kapali")}
	w := NewSosyalWorker(&sahteHazirlikDeposu{kayit: silinmis}, nil, nil, s, nil)
	if err := w.DosyaSil(context.Background(), gorev); err == nil {
		t.Fatal("R2 hatasi tekrar denensin diye donmeli")
	}
	w = NewSosyalWorker(&sahteHazirlikDeposu{okumaHata: errors.New("pg kapali")}, nil, nil, &sosyalSilici{}, nil)
	if err := w.DosyaSil(context.Background(), gorev); err == nil {
		t.Fatal("okuma hatasi donmeli")
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/worker/ -count=1
```

Expected: FAIL, build error `undefined: NewSosyalWorker`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/worker/sosyal_worker.go`:

````go
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/hesapisaret"
	"github.com/makeasinger/api/internal/sosyal"
)

const (
	// dalgaTepeSayisi ve onizlemeSaniyesi, sozlesmedeki 64 tepe ve 30 sn.
	dalgaTepeSayisi  = 64
	onizlemeSaniyesi = 30
)

// SosyalWorker, sosyal:hazirla ve sosyal:dosya-sil islerini yapar.
type SosyalWorker struct {
	depo        sosyal.HazirlikDeposu
	kopyalayici client.Kopyalayici   // nil: R2 yok, kopya atlanir
	analizci    client.AudioAnalizci // nil: dalga formu ve onizleme yok
	silici      client.OnekSilici    // nil: R2 yok
	hesap       hesapisaret.Denetci  // nil: denetim yok
	sonDeneme   func(context.Context) bool
}

// NewSosyalWorker kurar. Arayuz parametreleri nil olabilir; main tipli nil
// tuzagina dusmemek icin yalnizca gercek istemcileri gecirir.
func NewSosyalWorker(depo sosyal.HazirlikDeposu, kopyalayici client.Kopyalayici, analizci client.AudioAnalizci,
	silici client.OnekSilici, hesap hesapisaret.Denetci) *SosyalWorker {
	return &SosyalWorker{
		depo: depo, kopyalayici: kopyalayici, analizci: analizci, silici: silici, hesap: hesap,
		sonDeneme: asynqSonDeneme,
	}
}

// asynqSonDeneme, bu denemenin isin son denemesi olup olmadigini soyler.
// Baglamda asynq bilgisi yoksa (test, elle cagri) false doner.
func asynqSonDeneme(ctx context.Context) bool {
	n, ok1 := asynq.GetRetryCount(ctx)
	enCok, ok2 := asynq.GetMaxRetry(ctx)
	return ok1 && ok2 && n >= enCok
}

func gorevYukunuCoz(t *asynq.Task) (string, error) {
	var yuk sosyal.GorevYuku
	if err := json.Unmarshal(t.Payload(), &yuk); err != nil {
		return "", fmt.Errorf("sosyal gorev yuku cozulemedi: %v: %w", err, asynq.SkipRetry)
	}
	id, err := uuid.Parse(yuk.YayinID)
	if err != nil {
		return "", fmt.Errorf("sosyal gorev yukunde gecersiz yayin kimligi: %w", asynq.SkipRetry)
	}
	return id.String(), nil
}

// Hazirla, yayinin dosyalarini sosyal/{id}/ altina kopyalar, analiz eder
// ve yayinda yapar. Kopya hatalari tekrar denenir; son denemede yayin
// hata olur. Analiz hatasi yayini durdurmaz: dalga formu bos kalir.
func (w *SosyalWorker) Hazirla(ctx context.Context, t *asynq.Task) error {
	id, err := gorevYukunuCoz(t)
	if err != nil {
		return err
	}
	k, err := w.depo.HazirlikGetir(ctx, id)
	if errors.Is(err, sosyal.ErrBulunamadi) {
		return nil
	}
	if err != nil {
		return err
	}
	if k.Silindi || k.Durum != sosyal.DurumHazirlaniyor {
		return nil
	}
	sonuc, err := w.kopyala(ctx, k)
	if err != nil {
		return w.basarisiz(ctx, id, err)
	}
	if err := w.sahipDenetle(ctx, k.SahipID); err != nil {
		return w.basarisiz(ctx, id, err)
	}
	w.analizEt(ctx, id, &sonuc)
	if err := w.depo.Yayinla(ctx, id, sonuc); err != nil {
		return w.basarisiz(ctx, id, err)
	}
	return nil
}

// sahipDenetle, her R2 yazimindan once is sahibinin silinmedigini dogrular.
// Silinmisse kalici hatadir (SkipRetry); denetim hatasi tekrar denenir.
func (w *SosyalWorker) sahipDenetle(ctx context.Context, sahipID string) error {
	if err := sahipYazabilir(ctx, w.hesap, sahipID); err != nil {
		if errors.Is(err, errSahipSilindi) {
			return fmt.Errorf("yayin hazirlanmadi: %w: %w", err, asynq.SkipRetry)
		}
		return err
	}
	return nil
}

// basarisiz, hatayi doner; kalici hatada ya da son denemede yayini hata
// durumuna alir ki sahibi "Tekrar dene" gorebilsin.
func (w *SosyalWorker) basarisiz(ctx context.Context, id string, err error) error {
	if errors.Is(err, asynq.SkipRetry) || w.sonDeneme(ctx) {
		if herr := w.depo.HataYap(ctx, id); herr != nil {
			log.Printf("Uyari: yayin hata durumuna alinamadi (%s): %v", id, herr)
		}
	}
	return err
}

// kopyala, master ve stem kopyalarini kurar ve sonucun anahtar alanlarini
// doldurur. Ayni hedefe tekrar kopya zararsizdir; is tekrar denenebilir.
func (w *SosyalWorker) kopyala(ctx context.Context, k *sosyal.HazirlikKaydi) (sosyal.HazirlikSonucu, error) {
	var sonuc sosyal.HazirlikSonucu
	master, err := sosyal.MasterHedefi(k.ID, k.Hazirlik.MasterKey)
	if err != nil {
		return sonuc, fmt.Errorf("master hedefi kurulamadi: %v: %w", err, asynq.SkipRetry)
	}
	if err := w.kopyalaBir(ctx, k.SahipID, k.Hazirlik.MasterKey, master); err != nil {
		return sonuc, fmt.Errorf("master kopyalanamadi: %w", err)
	}
	sonuc.MasterAnahtari = master
	sonuc.StemListesi = make([]sosyal.StemKaydi, 0, len(k.Hazirlik.Stems))
	for _, st := range k.Hazirlik.Stems {
		hedef, err := sosyal.StemHedefi(k.ID, st.ID, st.Key)
		if err != nil {
			return sonuc, fmt.Errorf("stem hedefi kurulamadi: %v: %w", err, asynq.SkipRetry)
		}
		if err := w.kopyalaBir(ctx, k.SahipID, st.Key, hedef); err != nil {
			return sonuc, fmt.Errorf("stem kopyalanamadi: %w", err)
		}
		sonuc.StemListesi = append(sonuc.StemListesi, sosyal.StemKaydi{
			ID: st.ID, Enstruman: st.Enstruman, Sure: st.Sure, Anahtar: hedef,
		})
	}
	return sonuc, nil
}

func (w *SosyalWorker) kopyalaBir(ctx context.Context, sahipID, kaynak, hedef string) error {
	if err := w.sahipDenetle(ctx, sahipID); err != nil {
		return err
	}
	if w.kopyalayici == nil {
		return nil
	}
	return w.kopyalayici.Kopyala(ctx, kaynak, hedef)
}

// analizEt, dalga formu ve onizlemeyi uretir. Her hata loglanir ve
// sonuc bos dalga formuyla, onizlemesiz devam eder (spec §4.3).
func (w *SosyalWorker) analizEt(ctx context.Context, id string, sonuc *sosyal.HazirlikSonucu) {
	sonuc.DalgaFormu = []float64{}
	if w.analizci == nil {
		return
	}
	onizleme, err := sosyal.OnizlemeHedefi(id)
	if err != nil {
		log.Printf("Uyari: onizleme hedefi kurulamadi (%s): %v", id, err)
		return
	}
	yanit, err := w.analizci.Analiz(ctx, &client.AnalizRequest{
		InputKey: sonuc.MasterAnahtari, PreviewKey: onizleme,
		PeakCount: dalgaTepeSayisi, PreviewSeconds: onizlemeSaniyesi,
	})
	if err != nil {
		log.Printf("Uyari: yayin analiz edilemedi, dalga formu bos kalacak (%s): %v", id, err)
		return
	}
	if len(yanit.Peaks) != dalgaTepeSayisi {
		log.Printf("Uyari: analiz %d tepe dondu, %d bekleniyordu (%s)", len(yanit.Peaks), dalgaTepeSayisi, id)
		return
	}
	dalga := make([]float64, dalgaTepeSayisi)
	for i, p := range yanit.Peaks {
		if math.IsNaN(p) {
			p = 0
		}
		dalga[i] = math.Min(1, math.Max(0, p))
	}
	sonuc.DalgaFormu = dalga
	sonuc.OnizlemeAnahtari = onizleme
}

// DosyaSil, silinen yayinin sosyal/{id}/ onekini temizler. Satir hala
// canliysa (silinmemis) hicbir sey silinmez.
func (w *SosyalWorker) DosyaSil(ctx context.Context, t *asynq.Task) error {
	id, err := gorevYukunuCoz(t)
	if err != nil {
		return err
	}
	onek, err := client.SosyalOneki(id)
	if err != nil {
		return fmt.Errorf("sosyal onek kurulamadi: %v: %w", err, asynq.SkipRetry)
	}
	k, err := w.depo.HazirlikGetir(ctx, id)
	switch {
	case errors.Is(err, sosyal.ErrBulunamadi):
		// Satir yok (hesap silindi): dosyalar yine de temizlenir.
	case err != nil:
		return err
	case !k.Silindi:
		return nil
	}
	if w.silici == nil {
		return nil
	}
	if _, err := w.silici.DeletePrefix(ctx, onek); err != nil {
		return fmt.Errorf("sosyal dosyalari silinemedi: %w", err)
	}
	return nil
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/worker/ -count=1
```

Expected: `ok  github.com/makeasinger/api/internal/worker`. Whole Go suite after this task: about 646 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/worker/sosyal_worker_test.go \
  core-service/internal/worker/sosyal_worker.go
git commit -F - <<'EOF'
feat(worker): sosyal:hazirla ve sosyal:dosya-sil isleri

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 9: Feeds, profile publication lists and likes

R8 and N11–N12: cursors, the three feed tabs, the profile lists and likes, from SQL to routes. `butunlesik_test.go` then drives the real `Servis` on the real `PostgresDepo` through a full publication life cycle (profile → publish → hazirlik result → slug detail JSON → feed → like → listen URL → PATCH visibility → counters → delete) and the suggestion flow.

**Files:**

- Create: `core-service/internal/sosyal/imlec_test.go`
- Create: `core-service/internal/sosyal/depo_akis_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go`
- Create: `core-service/internal/sosyal/servis_akis_test.go`
- Create: `core-service/internal/sosyal/butunlesik_test.go`
- Modify: `core-service/internal/handler/sosyal_test.go`
- Create: `core-service/internal/handler/sosyal_akis_test.go`
- Create: `core-service/internal/sosyal/imlec.go`
- Create: `core-service/internal/sosyal/depo_akis.go`
- Modify: `core-service/internal/sosyal/depo.go`
- Modify: `core-service/internal/sosyal/servis.go`
- Create: `core-service/internal/sosyal/servis_akis.go`
- Modify: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_akis.go`

**Interfaces:**
- Consumes: `ozetSecimi`, `ozetKaynagi`, `ozetTarayici`, `gorunurlukKosulu` (Tasks 4, 6); `gorunurYayin` (Task 7).
- Produces: `type Sayfa[T any] struct { Ogeler []T; SonrakiImlec *string }`; `SayfaBoyutu(ham string) int`; `ZamanImleci{T time.Time; ID string}`, `PuanImleci{P float64; ID string; N int64}`; `type AkisDeposu interface` (`ProfilYayinlari`, `AkisYeni`, `AkisTakip`, `AkisSeninIcin`), `type BegeniDeposu interface` (`Begen`, `BegeniyiKaldir`), `PuanliOzet`; `Depo` embeds both; `Servis.simdi func() time.Time`; constants `SekmeSeninIcin`, `SekmeTakip`, `SekmeYeni`; `BegeniDurumu{BegendimMi bool; BegeniSayisi int}`; service methods `Akis(ctx, kullaniciID, sekme, imlec string, limit int)`, `ProfilYayinlari(ctx, izleyiciID, ad, tur, imlec string, limit int)`, `Begeni(ctx, kullaniciID, yayinID string, begen bool)`; routes `GET /akis`, `GET /profiller/:ad/yayinlar`, `PUT|DELETE /yayinlar/:id/begeni`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/imlec_test.go`:

````go
package sosyal

import (
	"errors"
	"testing"
	"time"
)

func TestSayfaBoyutu(t *testing.T) {
	durumlar := map[string]int{"": 20, "x": 20, "0": 20, "-3": 20, "1": 1, "35": 35, "50": 50, "51": 50, "1000": 50}
	for ham, beklenen := range durumlar {
		if got := SayfaBoyutu(ham); got != beklenen {
			t.Errorf("SayfaBoyutu(%q) = %d, beklenen %d", ham, got, beklenen)
		}
	}
}

func TestZamanImleci_GidisDonus(t *testing.T) {
	an := time.Date(2026, 10, 12, 9, 30, 15, 123456000, time.UTC)
	ham := imlecKodla(ZamanImleci{T: an, ID: yayinX})
	im, err := zamanImleciCoz(*ham)
	if err != nil || !im.T.Equal(an) || im.ID != yayinX {
		t.Fatalf("imlec = %+v, %v", im, err)
	}
	if im, err := zamanImleciCoz(""); im != nil || err != nil {
		t.Fatalf("bos imlec nil olmali: %+v %v", im, err)
	}
}

func TestPuanImleci_GidisDonus(t *testing.T) {
	ham := imlecKodla(PuanImleci{P: 0.1234567890123, ID: yayinX, N: 1760000000000})
	im, err := puanImleciCoz(*ham)
	if err != nil || im.P != 0.1234567890123 || im.N != 1760000000000 {
		t.Fatalf("imlec = %+v, %v", im, err)
	}
}

func TestImlec_BozukReddedilir(t *testing.T) {
	bozuklar := []string{"!!!", "e30", *imlecKodla(map[string]string{"id": "x"}), *imlecKodla(ZamanImleci{ID: yayinX})}
	for _, ham := range bozuklar {
		if _, err := zamanImleciCoz(ham); !errors.Is(err, ErrDogrulama) {
			t.Errorf("zaman %q: %v", ham, err)
		}
	}
	for _, ham := range []string{"!!!", *imlecKodla(PuanImleci{ID: yayinX})} {
		if _, err := puanImleciCoz(ham); !errors.Is(err, ErrDogrulama) {
			t.Errorf("puan %q: %v", ham, err)
		}
	}
}
````

**Create** `core-service/internal/sosyal/depo_akis_test.go`:

````go
package sosyal

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tumSayfalar, bir zaman listesini limit'lik sayfalarla sonuna kadar
// gezer ve id'leri sirayla doner.
func tumSayfalar(t *testing.T, cek func(*ZamanImleci) ([]YayinOzeti, error), limit int) []string {
	t.Helper()
	var (
		idler []string
		imlec *ZamanImleci
	)
	for sayfa := 0; sayfa < 20; sayfa++ {
		ogeler, err := cek(imlec)
		if err != nil {
			t.Fatalf("sayfa %d: %v", sayfa, err)
		}
		bitti := len(ogeler) <= limit
		if !bitti {
			ogeler = ogeler[:limit]
		}
		for _, o := range ogeler {
			idler = append(idler, o.ID)
		}
		if bitti {
			return idler
		}
		son := ogeler[len(ogeler)-1]
		imlec = &ZamanImleci{T: son.Olusturma, ID: son.ID}
	}
	t.Fatal("sayfalama bitmedi")
	return nil
}

func esitSira(t *testing.T, ad string, got, beklenen []string) {
	t.Helper()
	if len(got) != len(beklenen) {
		t.Fatalf("%s: %d oge, beklenen %d (%v)", ad, len(got), len(beklenen), got)
	}
	for i := range got {
		if got[i] != beklenen[i] {
			t.Fatalf("%s: sira %d = %s, beklenen %s", ad, i, got[i], beklenen[i])
		}
	}
}

func akisKurulumu(t *testing.T) (*pgxpool.Pool, *PostgresDepo, string, string, string) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ben, takipEdilen, yabanci := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, ben, "ben")
	testProfilEkle(t, d, takipEdilen, "takip_edilen")
	testProfilEkle(t, d, yabanci, "yabanci")
	testTakipEkle(t, havuz, ben, takipEdilen)
	return havuz, d, ben, takipEdilen, yabanci
}

func TestPostgresDepo_AkisYeniVeSayfalama(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	ayniAn := t0.Add(10 * time.Minute)
	// Beklenen sira: yeniden eskiye; ayni anda olusanlar id'ye gore azalan.
	a := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: t0.Add(30 * time.Minute)})
	b := testYayinEkle(t, havuz, testYayin{SahipID: ben, Olusturma: t0.Add(20 * time.Minute)})
	c1 := testYayinEkle(t, havuz, testYayin{ID: "ffffffff-0000-4000-8000-000000000001", SahipID: takipEdilen, Olusturma: ayniAn})
	c2 := testYayinEkle(t, havuz, testYayin{ID: "00000000-0000-4000-8000-000000000001", SahipID: takipEdilen, Olusturma: ayniAn})
	e := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: t0})
	// Akisa girmeyenler:
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Gorunurluk: GorunurlukTakipciler})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Durum: DurumHazirlaniyor})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Silindi: true})

	beklenen := []string{a, b, c1, c2, e}
	for _, limit := range []int{1, 2, 5, 50} {
		got := tumSayfalar(t, func(im *ZamanImleci) ([]YayinOzeti, error) { return d.AkisYeni(ctx, ben, im, limit) }, limit)
		esitSira(t, "yeni", got, beklenen)
	}
}

func TestPostgresDepo_AkisTakip(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	herkes := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Olusturma: t0.Add(2 * time.Minute)})
	takipcilere := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukTakipciler, Olusturma: t0.Add(time.Minute)})
	testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Durum: DurumHata})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci})
	testYayinEkle(t, havuz, testYayin{SahipID: ben})

	got := tumSayfalar(t, func(im *ZamanImleci) ([]YayinOzeti, error) { return d.AkisTakip(ctx, ben, im, 20) }, 20)
	esitSira(t, "takip", got, []string{herkes, takipcilere})
}

func TestPostgresDepo_ProfilYayinlari(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	herkes := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Olusturma: t0.Add(5 * time.Minute)})
	takipcilere := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukTakipciler, Olusturma: t0.Add(4 * time.Minute)})
	link := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukLink, Olusturma: t0.Add(3 * time.Minute)})
	hazirlanan := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Durum: DurumHazirlaniyor, Olusturma: t0.Add(2 * time.Minute)})
	testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Silindi: true})
	cover := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, KaynakID: herkes, Olusturma: t0.Add(time.Minute)})

	liste := func(izleyici string, coverlar bool) []string {
		return tumSayfalar(t, func(im *ZamanImleci) ([]YayinOzeti, error) {
			return d.ProfilYayinlari(ctx, takipEdilen, izleyici, coverlar, im, 2)
		}, 2)
	}
	esitSira(t, "sahip", liste(takipEdilen, false), []string{herkes, takipcilere, link, hazirlanan})
	esitSira(t, "takipci", liste(ben, false), []string{herkes, takipcilere})
	esitSira(t, "yabanci", liste(yabanci, false), []string{herkes})
	esitSira(t, "coverlar", liste(yabanci, true), []string{cover})

	ogeler, _ := d.ProfilYayinlari(ctx, takipEdilen, yabanci, true, nil, 20)
	if ogeler[0].Kaynak == nil || ogeler[0].Kaynak.YayinID != herkes || ogeler[0].Sahip.KullaniciAdi != "takip_edilen" {
		t.Fatalf("cover ozeti = %+v", ogeler[0])
	}
}

func begeniEkle(t *testing.T, havuz *pgxpool.Pool, yayinID string, adet int, zaman time.Time) {
	t.Helper()
	for i := 0; i < adet; i++ {
		if _, err := havuz.Exec(context.Background(),
			`INSERT INTO sosyal.begeni (kullanici_id, yayin_id, olusturma) VALUES ($1, $2, $3)`,
			uuid.NewString(), yayinID, zaman); err != nil {
			t.Fatalf("begeni: %v", err)
		}
	}
}

func TestPostgresDepo_AkisSeninIcinPuanVeSayfalama(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	referans := time.Now().Truncate(time.Millisecond)

	// Ayni yasta: daha cok begeni yukarida. Eski yayin asagida.
	populer := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: referans.Add(-2 * time.Hour)})
	begeniEkle(t, havuz, populer, 5, referans.Add(-time.Hour))
	sakin := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Olusturma: referans.Add(-2 * time.Hour)})
	eski := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: referans.Add(-72 * time.Hour)})
	// 7 gunden eski begeniler ve referanstan sonraki begeniler sayilmaz.
	begeniEkle(t, havuz, eski, 50, referans.Add(-8*24*time.Hour))
	begeniEkle(t, havuz, sakin, 50, referans.Add(time.Hour))
	// Akisa girmeyenler: kendi yayinim, referanstan sonra olusan, herkes olmayan.
	testYayinEkle(t, havuz, testYayin{SahipID: ben, Olusturma: referans.Add(-time.Hour)})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: referans.Add(time.Minute)})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Gorunurluk: GorunurlukLink, Olusturma: referans.Add(-time.Hour)})

	hepsi, err := d.AkisSeninIcin(ctx, ben, referans, nil, 50)
	if err != nil {
		t.Fatalf("AkisSeninIcin: %v", err)
	}
	var idler []string
	for _, o := range hepsi {
		idler = append(idler, o.ID)
	}
	esitSira(t, "senin icin", idler, []string{populer, sakin, eski})
	// (1+5)/(2+2)^1.5 = 0.75
	if p := hepsi[0].Puan; p < 0.7499 || p > 0.7501 {
		t.Fatalf("populer puani = %v, beklenen 0.75", p)
	}

	// Tek ogelik sayfalarla ayni sira, tekrar yok.
	var (
		gezilen []string
		imlec   *PuanImleci
	)
	for i := 0; i < 5; i++ {
		ogeler, err := d.AkisSeninIcin(ctx, ben, referans, imlec, 1)
		if err != nil {
			t.Fatalf("sayfa: %v", err)
		}
		gezilen = append(gezilen, ogeler[0].ID)
		if len(ogeler) == 1 {
			break
		}
		imlec = &PuanImleci{P: ogeler[0].Puan, ID: ogeler[0].ID, N: referans.UnixMilli()}
	}
	esitSira(t, "senin icin sayfalari", gezilen, []string{populer, sakin, eski})
}

func TestPostgresDepo_Begeni(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, ben := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	id := testYayinEkle(t, havuz, testYayin{SahipID: sahip})
	for i := 0; i < 2; i++ {
		if err := d.Begen(ctx, ben, id); err != nil {
			t.Fatalf("Begen %d: %v", i, err)
		}
	}
	k, _ := d.YayinGetir(ctx, id, ben)
	if k.BegeniSayisi != 1 || !k.BegendimMi {
		t.Fatalf("begeni tekrari guvenli olmali: %d %v", k.BegeniSayisi, k.BegendimMi)
	}
	for i := 0; i < 2; i++ {
		if err := d.BegeniyiKaldir(ctx, ben, id); err != nil {
			t.Fatalf("Kaldir %d: %v", i, err)
		}
	}
	k, _ = d.YayinGetir(ctx, id, ben)
	if k.BegeniSayisi != 0 || k.BegendimMi {
		t.Fatalf("begeni kaldirilmali: %d %v", k.BegeniSayisi, k.BegendimMi)
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -55,6 +55,9 @@ type sahteDepo struct {
 	hazirliklar map[string]*HazirlikKaydi
 	yayinlanan  map[string]HazirlikSonucu
 	hataYapilan []string
+
+	akisOgeleri []YayinOzeti
+	sonAkis     akisCagrisi
 }
 
 func yeniSahteDepo() *sahteDepo {
@@ -260,3 +263,76 @@ func (d *sahteDepo) HataYap(_ context.Context, id string) error {
 	}
 	return d.hata
 }
+
+// akisCagrisi, sahte akis yontemlerine son gelen girdilerdir.
+type akisCagrisi struct {
+	tur      string
+	izleyici string
+	sahip    string
+	coverlar bool
+	zaman    *ZamanImleci
+	puan     *PuanImleci
+	referans time.Time
+	limit    int
+}
+
+func (d *sahteDepo) zamanListesi(c akisCagrisi) ([]YayinOzeti, error) {
+	d.sonAkis = c
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	n := len(d.akisOgeleri)
+	if n > c.limit+1 {
+		n = c.limit + 1
+	}
+	return d.akisOgeleri[:n], nil
+}
+
+func (d *sahteDepo) ProfilYayinlari(_ context.Context, sahipID, izleyiciID string, coverlar bool, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
+	return d.zamanListesi(akisCagrisi{tur: "profil", izleyici: izleyiciID, sahip: sahipID, coverlar: coverlar, zaman: imlec, limit: limit})
+}
+
+func (d *sahteDepo) AkisYeni(_ context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
+	return d.zamanListesi(akisCagrisi{tur: "yeni", izleyici: izleyiciID, zaman: imlec, limit: limit})
+}
+
+func (d *sahteDepo) AkisTakip(_ context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
+	return d.zamanListesi(akisCagrisi{tur: "takip", izleyici: izleyiciID, zaman: imlec, limit: limit})
+}
+
+func (d *sahteDepo) AkisSeninIcin(_ context.Context, izleyiciID string, referans time.Time, imlec *PuanImleci, limit int) ([]PuanliOzet, error) {
+	d.sonAkis = akisCagrisi{tur: "senin-icin", izleyici: izleyiciID, puan: imlec, referans: referans, limit: limit}
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	var ogeler []PuanliOzet
+	for i, o := range d.akisOgeleri {
+		if i > limit {
+			break
+		}
+		ogeler = append(ogeler, PuanliOzet{YayinOzeti: o, Puan: float64(100 - i)})
+	}
+	return ogeler, nil
+}
+
+func (d *sahteDepo) Begen(_ context.Context, kullaniciID, yayinID string) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	if k, ok := d.yayinlar[yayinID]; ok && !k.BegendimMi {
+		k.BegendimMi = true
+		k.BegeniSayisi++
+	}
+	return nil
+}
+
+func (d *sahteDepo) BegeniyiKaldir(_ context.Context, kullaniciID, yayinID string) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	if k, ok := d.yayinlar[yayinID]; ok && k.BegendimMi {
+		k.BegendimMi = false
+		k.BegeniSayisi--
+	}
+	return nil
+}
````

**Create** `core-service/internal/sosyal/servis_akis_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func ozetler(n int) []YayinOzeti {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	o := make([]YayinOzeti, n)
	for i := range o {
		o[i].ID = fmt.Sprintf("%08d-1b2c-4d3e-8f9a-0b1c2d3e4f5a", i)
		o[i].Olusturma = t0.Add(-time.Duration(i) * time.Minute)
	}
	return o
}

func TestServis_AkisYeniSayfalari(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Akis(ctx, kullaniciA, SekmeYeni, "", 2)
	if err != nil || len(sayfa.Ogeler) != 2 || sayfa.SonrakiImlec == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.tur != "yeni" || d.sonAkis.limit != 2 || d.sonAkis.zaman != nil || d.sonAkis.izleyici != kullaniciA {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}
	if _, err := s.Akis(ctx, kullaniciA, SekmeYeni, *sayfa.SonrakiImlec, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if d.sonAkis.zaman == nil || d.sonAkis.zaman.ID != d.akisOgeleri[1].ID || !d.sonAkis.zaman.T.Equal(d.akisOgeleri[1].Olusturma) {
		t.Fatalf("imlec son ogeyi gostermeli: %+v", d.sonAkis.zaman)
	}

	d.akisOgeleri = ozetler(2)
	son, err := s.Akis(ctx, kullaniciA, SekmeTakip, "", 2)
	if err != nil || len(son.Ogeler) != 2 || son.SonrakiImlec != nil || d.sonAkis.tur != "takip" {
		t.Fatalf("son sayfa = %+v, %v", son, err)
	}
	d.akisOgeleri = nil
	bos, err := s.Akis(ctx, kullaniciA, SekmeYeni, "", 20)
	if err != nil || bos.Ogeler == nil || len(bos.Ogeler) != 0 {
		t.Fatalf("bos sayfa ogeler [] olmali: %+v, %v", bos, err)
	}
}

func TestServis_AkisSeninIcinReferansSabit(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	an := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	s.simdi = func() time.Time { return an }
	ctx := context.Background()

	sayfa, err := s.Akis(ctx, kullaniciA, SekmeSeninIcin, "", 2)
	if err != nil || len(sayfa.Ogeler) != 2 || sayfa.SonrakiImlec == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if !d.sonAkis.referans.Equal(an) || d.sonAkis.puan != nil {
		t.Fatalf("ilk sayfa referansi simdi olmali: %+v", d.sonAkis)
	}
	s.simdi = func() time.Time { return an.Add(time.Hour) }
	if _, err := s.Akis(ctx, kullaniciA, SekmeSeninIcin, *sayfa.SonrakiImlec, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if !d.sonAkis.referans.Equal(an) {
		t.Fatalf("sonraki sayfa ayni referansi kullanmali: %v", d.sonAkis.referans)
	}
	if d.sonAkis.puan == nil || d.sonAkis.puan.P != 99 || d.sonAkis.puan.ID != d.akisOgeleri[1].ID {
		t.Fatalf("puan imleci = %+v", d.sonAkis.puan)
	}
}

func TestServis_AkisHatalari(t *testing.T) {
	s, _ := testServisi(yeniSahteDepo())
	ctx := context.Background()
	if _, err := s.Akis(ctx, kullaniciA, "populer", "", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("gecersiz sekme: %v", err)
	}
	if _, err := s.Akis(ctx, kullaniciA, SekmeYeni, "bozuk!", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bozuk imlec: %v", err)
	}
	if _, err := s.Akis(ctx, kullaniciA, SekmeSeninIcin, "bozuk!", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bozuk puan imleci: %v", err)
	}
}

func TestServis_ProfilYayinlari(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciB] = &ProfilKaydi{KullaniciID: kullaniciB, KullaniciAdi: "veli"}
	d.akisOgeleri = ozetler(1)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.ProfilYayinlari(ctx, kullaniciA, "veli", "coverlar", "", 20)
	if err != nil || len(sayfa.Ogeler) != 1 || sayfa.SonrakiImlec != nil {
		t.Fatalf("sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.sahip != kullaniciB || d.sonAkis.izleyici != kullaniciA || !d.sonAkis.coverlar {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}
	if _, err := s.ProfilYayinlari(ctx, kullaniciA, "veli", "", "", 20); err != nil || d.sonAkis.coverlar {
		t.Fatalf("bos tur sarkilar olmali: %v %+v", err, d.sonAkis)
	}
	if _, err := s.ProfilYayinlari(ctx, kullaniciA, "veli", "videolar", "", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("gecersiz tur: %v", err)
	}
	if _, err := s.ProfilYayinlari(ctx, kullaniciA, "yok", "sarkilar", "", 20); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan profil: %v", err)
	}
}

func TestServis_Begeni(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciB, GorunurlukTakipciler, DurumYayinda)
	s, _ := testServisi(d)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		b, err := s.Begeni(ctx, kullaniciA, yayinX, true)
		if err != nil || !b.BegendimMi || b.BegeniSayisi != 1 {
			t.Fatalf("begen %d = %+v, %v", i, b, err)
		}
	}
	b, err := s.Begeni(ctx, kullaniciA, yayinX, false)
	if err != nil || b.BegendimMi || b.BegeniSayisi != 0 {
		t.Fatalf("kaldir = %+v, %v", b, err)
	}
	if _, err := s.Begeni(ctx, kullaniciA, yayinY, true); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorulmeyen yayin begenilemez: %v", err)
	}
}
````

**Create** `core-service/internal/sosyal/butunlesik_test.go`:

````go
package sosyal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// jsonAlanlari, degeri JSON'a cevirip ust duzey alanlarini doner.
func jsonAlanlari(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json: %v", err)
	}
	return m
}

// Gercek Servis + PostgresDepo ile yayin yasam dongusu: profil, yayinla,
// hazirlik sonucu, detay, akis, begeni, yama, silme. Is (worker) yerine
// depo.Yayinla dogrudan cagrilir.
func TestButunlesik_YayinYasamDongusu(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	q := &sahteKuyruk{}
	s := NewServis(d, q, nil, testR2())
	ctx := context.Background()
	sahip, dinleyici := uuid.NewString(), uuid.NewString()

	if _, err := s.YayinOlustur(ctx, sahip, YayinIstegi{}); !errors.Is(err, ErrProfilYok) {
		t.Fatalf("profilsiz yayin: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, sahip, ProfilGirdisi{KullaniciAdi: "sahip", GorunenAd: "Sahip"}); err != nil {
		t.Fatalf("profil: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, dinleyici, ProfilGirdisi{KullaniciAdi: "dinleyici", GorunenAd: "D"}); err != nil {
		t.Fatalf("profil: %v", err)
	}

	ist := gecerliIstek()
	ist.MasterURL = "masters/" + sahip + "/p1/m1.wav"
	ist.Stemler[0].URL = "stems/" + sahip + "/p1/drums.mp3"
	yanit, err := s.YayinOlustur(ctx, sahip, ist)
	if err != nil {
		t.Fatalf("YayinOlustur: %v", err)
	}
	if len(q.gorevler) != 1 {
		t.Fatalf("hazirla isi kurulmali")
	}

	// Hazirlanirken yalnizca sahibine gorunur.
	if y, err := s.YayinGoster(ctx, sahip, yanit.ID); err != nil || y.Durum != DurumHazirlaniyor {
		t.Fatalf("sahip hazirlanani gormeli: %+v, %v", y, err)
	}
	if _, err := s.YayinGoster(ctx, dinleyici, yanit.ID); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("dinleyici hazirlanani gormemeli: %v", err)
	}

	h, err := d.HazirlikGetir(ctx, yanit.ID)
	if err != nil || h.Hazirlik.MasterKey != ist.MasterURL {
		t.Fatalf("hazirlik = %+v, %v", h, err)
	}
	master, _ := MasterHedefi(yanit.ID, h.Hazirlik.MasterKey)
	dalga := make([]float64, 64)
	if err := d.Yayinla(ctx, yanit.ID, HazirlikSonucu{MasterAnahtari: master, DalgaFormu: dalga}); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}

	detay, err := s.SlugGoster(ctx, dinleyici, yanit.Slug)
	if err != nil {
		t.Fatalf("SlugGoster: %v", err)
	}
	m := jsonAlanlari(t, detay)
	for _, alan := range []string{"id", "slug", "baslik", "sahip", "tur", "bpm", "ton", "sureSn", "dalgaFormu",
		"begeniSayisi", "yorumSayisi", "coverSayisi", "begendimMi", "altyapiAcik", "kaynak", "olusturma",
		"aciklama", "yorumAcik", "sozler", "yapi", "durum", "benimMi", "gorunurluk"} {
		if _, ok := m[alan]; !ok {
			t.Errorf("YayinDetayi.%s yok", alan)
		}
	}
	if m["kaynak"] != nil || m["durum"] != "yayinda" || m["benimMi"] != false || len(m["dalgaFormu"].([]any)) != 64 {
		t.Fatalf("detay = %v", m)
	}
	if sahipJSON := m["sahip"].(map[string]any); sahipJSON["kullaniciAdi"] != "sahip" {
		t.Fatalf("sahip = %v", sahipJSON)
	}

	akis, err := s.Akis(ctx, dinleyici, SekmeSeninIcin, "", 20)
	if err != nil || len(akis.Ogeler) != 1 || akis.SonrakiImlec != nil {
		t.Fatalf("senin icin = %+v, %v", akis, err)
	}
	if kendi, _ := s.Akis(ctx, sahip, SekmeSeninIcin, "", 20); len(kendi.Ogeler) != 0 {
		t.Fatalf("kendi yayinim senin icin'de olmamali: %+v", kendi.Ogeler)
	}
	if b, err := s.Begeni(ctx, dinleyici, yanit.ID, true); err != nil || b.BegeniSayisi != 1 || !b.BegendimMi {
		t.Fatalf("begeni = %+v, %v", b, err)
	}

	dosya, err := s.Dosyalar(ctx, dinleyici, yanit.ID)
	if err != nil || dosya.Master.URL == "" {
		t.Fatalf("dosyalar = %+v, %v", dosya, err)
	}

	takipcilere := GorunurlukTakipciler
	if _, err := s.YayinGuncelle(ctx, sahip, yanit.ID, YayinYamasi{Gorunurluk: &takipcilere}); err != nil {
		t.Fatalf("yama: %v", err)
	}
	if _, err := s.YayinGoster(ctx, dinleyici, yanit.ID); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("takipciler yayini takip etmeyene gorunmemeli: %v", err)
	}
	p, err := s.ProfilGoster(ctx, dinleyici, "sahip")
	if err != nil || p.Istatistik.Sarki != 0 {
		t.Fatalf("takip etmeyen icin sarki sayaci 0 olmali: %+v, %v", p, err)
	}
	ben, _ := s.ProfilBen(ctx, sahip)
	if ben.Istatistik.Sarki != 1 || !ben.BenMiyim {
		t.Fatalf("sahibin sayaci = %+v", ben)
	}

	if err := s.YayinSil(ctx, sahip, yanit.ID); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if err := s.YayinSil(ctx, sahip, yanit.ID); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("ikinci silme: %v", err)
	}
	if len(q.gorevler) != 2 || q.gorevler[1].Type() != TaskTypeDosyaSil {
		t.Fatalf("dosya silme isi kurulmali: %d", len(q.gorevler))
	}
}

func TestButunlesik_AdUygunlugu(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	s := NewServis(d, &sahteKuyruk{}, nil, testR2())
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	for i, ad := range []string{"ali", "ali2"} {
		id := []string{a, b}[i]
		if _, err := s.ProfilKaydet(ctx, id, ProfilGirdisi{KullaniciAdi: ad, GorunenAd: "x"}); err != nil {
			t.Fatalf("profil: %v", err)
		}
	}
	u, err := s.AdUygunMu(ctx, uuid.NewString(), "ali")
	if err != nil || u.Uygun || u.Oneri == nil || *u.Oneri != "ali3" {
		t.Fatalf("uygunluk = %+v, %v", u, err)
	}
	if _, err := s.ProfilKaydet(ctx, b, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "x"}); !errors.Is(err, ErrKullaniciAdiAlinmis) {
		t.Fatalf("alinmis ad: %v", err)
	}
}
````

**Modify** `core-service/internal/handler/sosyal_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_test.go b/core-service/internal/handler/sosyal_test.go
--- a/core-service/internal/handler/sosyal_test.go
+++ b/core-service/internal/handler/sosyal_test.go
@@ -26,6 +26,8 @@ type sahteSosyal struct {
 	yayinYaniti *sosyal.YayinYaniti
 	detay       *sosyal.YayinDetayi
 	dosyalar    *sosyal.YayinDosyalari
+	sayfa       *sosyal.Sayfa[sosyal.YayinOzeti]
+	begeni      *sosyal.BegeniDurumu
 
 	sonKullanici string
 	sonAd        string
@@ -33,6 +35,11 @@ type sahteSosyal struct {
 	sonYayin     string
 	sonIstek     sosyal.YayinIstegi
 	sonYama      sosyal.YayinYamasi
+	sonSekme     string
+	sonTur       string
+	sonImlec     string
+	sonLimit     int
+	sonBegen     bool
 }
 
 func (s *sahteSosyal) ProfilBen(_ context.Context, k string) (*sosyal.Profil, error) {
@@ -90,6 +97,21 @@ func (s *sahteSosyal) Dosyalar(_ context.Context, k, id string) (*sosyal.YayinDo
 	return s.dosyalar, s.hata
 }
 
+func (s *sahteSosyal) Akis(_ context.Context, k, sekme, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error) {
+	s.sonKullanici, s.sonSekme, s.sonImlec, s.sonLimit = k, sekme, imlec, limit
+	return s.sayfa, s.hata
+}
+
+func (s *sahteSosyal) ProfilYayinlari(_ context.Context, k, ad, tur, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error) {
+	s.sonKullanici, s.sonAd, s.sonTur, s.sonImlec, s.sonLimit = k, ad, tur, imlec, limit
+	return s.sayfa, s.hata
+}
+
+func (s *sahteSosyal) Begeni(_ context.Context, k, id string, begen bool) (*sosyal.BegeniDurumu, error) {
+	s.sonKullanici, s.sonYayin, s.sonBegen = k, id, begen
+	return s.begeni, s.hata
+}
+
 // sosyalUygulamasi, /api/social grubunu sahte servisle kurar.
 func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
 	app := fiber.New()
````

**Create** `core-service/internal/handler/sosyal_akis_test.go`:

````go
package handler

import (
	"net/http"
	"testing"

	"github.com/makeasinger/api/internal/sosyal"
)

func TestSosyal_AkisSayfaZarfi(t *testing.T) {
	imlec := "sonraki"
	s := &sahteSosyal{sayfa: &sosyal.Sayfa[sosyal.YayinOzeti]{Ogeler: []sosyal.YayinOzeti{}, SonrakiImlec: &imlec}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/akis?sekme=yeni&imlec=abc", "")
	if durum != http.StatusOK || govde["sonrakiImlec"] != "sonraki" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
	if _, dizi := govde["ogeler"].([]any); !dizi {
		t.Fatalf("ogeler dizi olmali: %v", govde)
	}
	if s.sonSekme != "yeni" || s.sonImlec != "abc" || s.sonLimit != 20 {
		t.Fatalf("iletilen = sekme %q imlec %q limit %d", s.sonSekme, s.sonImlec, s.sonLimit)
	}
}

func TestSosyal_AkisSonSayfaImlecNull(t *testing.T) {
	s := &sahteSosyal{sayfa: &sosyal.Sayfa[sosyal.YayinOzeti]{Ogeler: []sosyal.YayinOzeti{}}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/akis?sekme=senin-icin&limit=999", "")
	if durum != http.StatusOK {
		t.Fatalf("durum=%d", durum)
	}
	if v, var_ := govde["sonrakiImlec"]; !var_ || v != nil {
		t.Fatalf("sonrakiImlec null olarak bulunmali: %v", govde)
	}
	if s.sonLimit != 50 {
		t.Fatalf("limit 50 ile sinirlanmali: %d", s.sonLimit)
	}
}

func TestSosyal_AkisGecersizSekme422(t *testing.T) {
	durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: sosyal.ErrDogrulama}, sosyalKullanici), http.MethodGet, "/api/social/akis?sekme=x", "")
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "dogrulama" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_ProfilYayinlari(t *testing.T) {
	s := &sahteSosyal{sayfa: &sosyal.Sayfa[sosyal.YayinOzeti]{Ogeler: []sosyal.YayinOzeti{}}}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/profiller/veli/yayinlar?tur=coverlar&limit=5", "")
	if durum != http.StatusOK || s.sonAd != "veli" || s.sonTur != "coverlar" || s.sonLimit != 5 {
		t.Fatalf("durum=%d ad=%q tur=%q limit=%d", durum, s.sonAd, s.sonTur, s.sonLimit)
	}
}

func TestSosyal_BegeniPutVeDelete(t *testing.T) {
	s := &sahteSosyal{begeni: &sosyal.BegeniDurumu{BegendimMi: true, BegeniSayisi: 3}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/yayinlar/y1/begeni", "")
	if durum != http.StatusOK || govde["begendimMi"] != true || govde["begeniSayisi"] != float64(3) || !s.sonBegen || s.sonYayin != "y1" {
		t.Fatalf("PUT durum=%d govde=%v", durum, govde)
	}
	s.begeni = &sosyal.BegeniDurumu{BegeniSayisi: 2}
	durum, govde = sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yayinlar/y1/begeni", "")
	if durum != http.StatusOK || govde["begendimMi"] != false || s.sonBegen {
		t.Fatalf("DELETE durum=%d govde=%v", durum, govde)
	}
	durum, _ = sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: sosyal.ErrBulunamadi}, sosyalKullanici), http.MethodPut, "/api/social/yayinlar/y1/begeni", "")
	if durum != http.StatusNotFound {
		t.Fatalf("gorulmeyen yayin 404: %d", durum)
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors such as `undefined: SayfaBoyutu`, `undefined: ZamanImleci`, `d.AkisYeni undefined`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/imlec.go`:

````go
package sosyal

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"
)

const (
	varsayilanSayfa = 20
	enBuyukSayfa    = 50
)

// Sayfa, sozlesmenin liste zarfidir. SonrakiImlec son sayfada null
// olarak GORUNUR; Ogeler hicbir zaman null degildir.
type Sayfa[T any] struct {
	Ogeler       []T     `json:"ogeler"`
	SonrakiImlec *string `json:"sonrakiImlec"`
}

// SayfaBoyutu, ?limit= degerini yorumlar: bos ya da gecersizse 20, en
// cok 50. Mobil limit gondermez; web gonderebilir.
func SayfaBoyutu(ham string) int {
	n, err := strconv.Atoi(ham)
	if err != nil || n < 1 {
		return varsayilanSayfa
	}
	if n > enBuyukSayfa {
		return enBuyukSayfa
	}
	return n
}

// ZamanImleci, zamana gore azalan listelerin konumudur.
type ZamanImleci struct {
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

// PuanImleci, "senin icin" akisinin konumudur. N, ilk sayfanin referans
// zamanidir (Unix ms); sonraki sayfalar ayni N ile hesaplanir ki puanlar
// sayfalar arasinda kaymasin.
type PuanImleci struct {
	P  float64 `json:"p"`
	ID string  `json:"id"`
	N  int64   `json:"n"`
}

func imlecKodla(v any) *string {
	b, _ := json.Marshal(v)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func imlecCoz(ham string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(ham)
	if err != nil || json.Unmarshal(b, v) != nil {
		return dogrulamaHatasi("imlec gecersiz")
	}
	return nil
}

// zamanImleciCoz, bos dizgede nil doner.
func zamanImleciCoz(ham string) (*ZamanImleci, error) {
	if ham == "" {
		return nil, nil
	}
	var im ZamanImleci
	if err := imlecCoz(ham, &im); err != nil {
		return nil, err
	}
	id, ok := kimlikCoz(im.ID)
	if !ok || im.T.IsZero() {
		return nil, dogrulamaHatasi("imlec gecersiz")
	}
	im.ID = id
	return &im, nil
}

// puanImleciCoz, bos dizgede nil doner.
func puanImleciCoz(ham string) (*PuanImleci, error) {
	if ham == "" {
		return nil, nil
	}
	var im PuanImleci
	if err := imlecCoz(ham, &im); err != nil {
		return nil, err
	}
	id, ok := kimlikCoz(im.ID)
	if !ok || im.N <= 0 {
		return nil, dogrulamaHatasi("imlec gecersiz")
	}
	im.ID = id
	return &im, nil
}
````

**Create** `core-service/internal/sosyal/depo_akis.go`:

````go
package sosyal

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AkisDeposu, liste uclarinin sorgulari. Her yontem en cok limit+1 oge
// doner; fazlalik servisin "sonraki sayfa var" isaretidir.
type AkisDeposu interface {
	// ProfilYayinlari, sahibin orijinallerini ya da cover'larini
	// izleyicinin gorebildigi kadariyla yeniden eskiye listeler.
	ProfilYayinlari(ctx context.Context, sahipID, izleyiciID string, coverlar bool, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// AkisYeni: herkes, yayinda, silinmemis; yeniden eskiye.
	AkisYeni(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// AkisTakip: takip edilenlerin herkes ve takipciler yayinlari.
	AkisTakip(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// AkisSeninIcin: izleyicinin olmayan herkes yayinlari, referans
	// zamana gore puanla azalan.
	AkisSeninIcin(ctx context.Context, izleyiciID string, referans time.Time, imlec *PuanImleci, limit int) ([]PuanliOzet, error)
}

// BegeniDeposu, sosyal.begeni islemleri. Ikisi de tekrari guvenlidir.
type BegeniDeposu interface {
	Begen(ctx context.Context, kullaniciID, yayinID string) error
	BegeniyiKaldir(ctx context.Context, kullaniciID, yayinID string) error
}

// PuanliOzet, puanla siralanan bir akis ogesidir; puan imlece girer.
type PuanliOzet struct {
	YayinOzeti
	Puan float64
}

// zamanSayfasi, $1 izleyici olmak uzere verilen kosul ve parametrelerle
// zamana gore azalan bir sayfa ceker. Imlec parametreleri sona eklenir.
func (d *PostgresDepo) zamanSayfasi(ctx context.Context, kosul string, imlec *ZamanImleci, limit int, args ...any) ([]YayinOzeti, error) {
	var imlecT, imlecID any
	if imlec != nil {
		imlecT, imlecID = imlec.T, imlec.ID
	}
	n := len(args)
	args = append(args, imlecT, imlecID, limit+1)
	sorgu := fmt.Sprintf(`SELECT %s %s
		WHERE %s
		  AND ($%d::timestamptz IS NULL OR (y.olusturma, y.id) < ($%d::timestamptz, $%d::uuid))
		ORDER BY y.olusturma DESC, y.id DESC
		LIMIT $%d`, ozetSecimi, ozetKaynagi, kosul, n+1, n+1, n+2, n+3)
	satirlar, err := d.havuz.Query(ctx, sorgu, args...)
	if err != nil {
		return nil, fmt.Errorf("liste okunamadi: %w", err)
	}
	return ozetleriTara(satirlar)
}

func ozetleriTara(satirlar pgx.Rows) ([]YayinOzeti, error) {
	defer satirlar.Close()
	ogeler := []YayinOzeti{}
	for satirlar.Next() {
		var t ozetTarayici
		if err := satirlar.Scan(t.hedefler()...); err != nil {
			return nil, fmt.Errorf("liste ogesi okunamadi: %w", err)
		}
		ogeler = append(ogeler, t.ozet())
	}
	return ogeler, satirlar.Err()
}

func (d *PostgresDepo) ProfilYayinlari(ctx context.Context, sahipID, izleyiciID string, coverlar bool, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.sahip_id = $2 AND (y.kaynak_yayin_id IS NOT NULL) = $3 AND ` + gorunurlukKosulu("y", "$1", false)
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID, sahipID, coverlar)
}

func (d *PostgresDepo) AkisYeni(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.gorunurluk = 'herkes' AND y.durum = 'yayinda' AND y.silinme IS NULL`
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID)
}

func (d *PostgresDepo) AkisTakip(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.gorunurluk IN ('herkes', 'takipciler') AND y.durum = 'yayinda' AND y.silinme IS NULL
		AND EXISTS (SELECT 1 FROM sosyal.takip tk WHERE tk.takipci_id = $1 AND tk.takip_edilen_id = y.sahip_id)`
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID)
}

// puanIfadesi, spec §5.1: (1 + b + 2y + 3c) / (saat + 2)^1.5. b, y, c
// referanstan onceki 7 gunun begeni, yorum ve cover sayilaridir; saat
// yayindan referansa gecen suredir. Referanstan sonraki etkilesim sayilmaz
// ki ayni referansla cekilen sayfalarda puan sabit kalsin.
const puanIfadesi = `
	(1
	 + (SELECT count(*) FROM sosyal.begeni b
	     WHERE b.yayin_id = y.id AND b.olusturma > $2::timestamptz - interval '7 days' AND b.olusturma <= $2::timestamptz)
	 + 2 * (SELECT count(*) FROM sosyal.yorum c
	     WHERE c.yayin_id = y.id AND c.silinme IS NULL
	       AND c.olusturma > $2::timestamptz - interval '7 days' AND c.olusturma <= $2::timestamptz)
	 + 3 * (SELECT count(*) FROM sosyal.yayin cv
	     WHERE cv.kaynak_yayin_id = y.id AND cv.durum = 'yayinda' AND cv.silinme IS NULL
	       AND cv.olusturma > $2::timestamptz - interval '7 days' AND cv.olusturma <= $2::timestamptz)
	)::float8
	/ power(extract(epoch FROM ($2::timestamptz - y.olusturma))::float8 / 3600.0 + 2, 1.5)`

func (d *PostgresDepo) AkisSeninIcin(ctx context.Context, izleyiciID string, referans time.Time, imlec *PuanImleci, limit int) ([]PuanliOzet, error) {
	var imlecP, imlecID any
	if imlec != nil {
		imlecP, imlecID = imlec.P, imlec.ID
	}
	// Referanstan sonra olusan yayin bu akis oturumuna girmez: hem puani
	// tanimsiz olur (negatif saat) hem de sayfalar kayar.
	satirlar, err := d.havuz.Query(ctx, `
		SELECT s.* FROM (
		  SELECT `+ozetSecimi+`, `+puanIfadesi+` AS puan, y.id AS sira_id
		  `+ozetKaynagi+`
		  WHERE y.gorunurluk = 'herkes' AND y.durum = 'yayinda' AND y.silinme IS NULL
		    AND y.sahip_id <> $1 AND y.olusturma <= $2::timestamptz
		) s
		WHERE ($3::float8 IS NULL OR (s.puan, s.sira_id) < ($3::float8, $4::uuid))
		ORDER BY s.puan DESC, s.sira_id DESC
		LIMIT $5`, izleyiciID, referans, imlecP, imlecID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("akis okunamadi: %w", err)
	}
	defer satirlar.Close()
	ogeler := []PuanliOzet{}
	for satirlar.Next() {
		var (
			t      ozetTarayici
			puan   float64
			siraID string
		)
		if err := satirlar.Scan(append(t.hedefler(), &puan, &siraID)...); err != nil {
			return nil, fmt.Errorf("akis ogesi okunamadi: %w", err)
		}
		ogeler = append(ogeler, PuanliOzet{YayinOzeti: t.ozet(), Puan: puan})
	}
	return ogeler, satirlar.Err()
}

func (d *PostgresDepo) Begen(ctx context.Context, kullaniciID, yayinID string) error {
	if _, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, kullaniciID, yayinID); err != nil {
		return fmt.Errorf("begeni eklenemedi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) BegeniyiKaldir(ctx context.Context, kullaniciID, yayinID string) error {
	if _, err := d.havuz.Exec(ctx, `
		DELETE FROM sosyal.begeni WHERE kullanici_id = $1 AND yayin_id = $2`, kullaniciID, yayinID); err != nil {
		return fmt.Errorf("begeni kaldirilamadi: %w", err)
	}
	return nil
}
````

**Modify** `core-service/internal/sosyal/depo.go`:

````diff
diff --git a/core-service/internal/sosyal/depo.go b/core-service/internal/sosyal/depo.go
--- a/core-service/internal/sosyal/depo.go
+++ b/core-service/internal/sosyal/depo.go
@@ -31,6 +31,8 @@ type Depo interface {
 	ProfilDeposu
 	YayinDeposu
 	HazirlikDeposu
+	AkisDeposu
+	BegeniDeposu
 }
 
 // PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
````

**Modify** `core-service/internal/sosyal/servis.go`:

````diff
diff --git a/core-service/internal/sosyal/servis.go b/core-service/internal/sosyal/servis.go
--- a/core-service/internal/sosyal/servis.go
+++ b/core-service/internal/sosyal/servis.go
@@ -3,6 +3,7 @@ package sosyal
 import (
 	"context"
 	"errors"
+	"time"
 
 	"github.com/hibiken/asynq"
 	"github.com/makeasinger/api/internal/client"
@@ -21,12 +22,13 @@ type Servis struct {
 	kuyruk   Kuyruk
 	depolama client.StorageClient // nil: R2 yok, imzasiz adres uretilir
 	r2       *config.R2Config
+	simdi    func() time.Time
 }
 
 // NewServis kurar. depolama nil olabilir (R2 yapilandirilmamis); main
 // tipli nil tuzagina dusmemek icin yalnizca gercek istemciyi gecirir.
 func NewServis(depo Depo, kuyruk Kuyruk, depolama client.StorageClient, r2 *config.R2Config) *Servis {
-	return &Servis{depo: depo, kuyruk: kuyruk, depolama: depolama, r2: r2}
+	return &Servis{depo: depo, kuyruk: kuyruk, depolama: depolama, r2: r2, simdi: time.Now}
 }
 
 // kullaniciCoz, istegi yapanin kimligini dogrular. Auth middleware her
````

**Create** `core-service/internal/sosyal/servis_akis.go`:

````go
package sosyal

import (
	"context"
	"time"
)

// Akis sekmeleri (GET /akis?sekme=).
const (
	SekmeSeninIcin = "senin-icin"
	SekmeTakip     = "takip"
	SekmeYeni      = "yeni"
)

// BegeniDurumu, PUT/DELETE /yayinlar/{id}/begeni yaniti.
type BegeniDurumu struct {
	BegendimMi   bool `json:"begendimMi"`
	BegeniSayisi int  `json:"begeniSayisi"`
}

// zamanSayfasiKur, limit+1 ogeden sayfayi ve sonraki imleci kurar.
func zamanSayfasiKur(ogeler []YayinOzeti, limit int) *Sayfa[YayinOzeti] {
	sayfa := &Sayfa[YayinOzeti]{Ogeler: ogeler}
	if len(ogeler) > limit {
		sayfa.Ogeler = ogeler[:limit]
		son := sayfa.Ogeler[limit-1]
		sayfa.SonrakiImlec = imlecKodla(ZamanImleci{T: son.Olusturma, ID: son.ID})
	}
	if sayfa.Ogeler == nil {
		sayfa.Ogeler = []YayinOzeti{}
	}
	return sayfa
}

// Akis, GET /akis. Gecersiz sekme ya da imlec ErrDogrulama'dir.
func (s *Servis) Akis(ctx context.Context, kullaniciID, sekme, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	switch sekme {
	case SekmeYeni, SekmeTakip:
		imlec, err := zamanImleciCoz(hamImlec)
		if err != nil {
			return nil, err
		}
		cek := s.depo.AkisYeni
		if sekme == SekmeTakip {
			cek = s.depo.AkisTakip
		}
		ogeler, err := cek(ctx, id, imlec, limit)
		if err != nil {
			return nil, err
		}
		return zamanSayfasiKur(ogeler, limit), nil
	case SekmeSeninIcin:
		return s.seninIcin(ctx, id, hamImlec, limit)
	}
	return nil, dogrulamaHatasi("sekme senin-icin, takip ya da yeni olmali")
}

// seninIcin, puanli akisi ceker. Ilk sayfa referans zamanini "simdi"
// alir; sonraki sayfalar imlecteki ayni referansi kullanir.
func (s *Servis) seninIcin(ctx context.Context, id, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	imlec, err := puanImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	referans := s.simdi().Truncate(time.Millisecond)
	if imlec != nil {
		referans = time.UnixMilli(imlec.N)
	}
	ogeler, err := s.depo.AkisSeninIcin(ctx, id, referans, imlec, limit)
	if err != nil {
		return nil, err
	}
	sayfa := &Sayfa[YayinOzeti]{Ogeler: make([]YayinOzeti, 0, len(ogeler))}
	for i, o := range ogeler {
		if i == limit {
			son := ogeler[limit-1]
			sayfa.SonrakiImlec = imlecKodla(PuanImleci{P: son.Puan, ID: son.ID, N: referans.UnixMilli()})
			break
		}
		sayfa.Ogeler = append(sayfa.Ogeler, o.YayinOzeti)
	}
	return sayfa, nil
}

// ProfilYayinlari, GET /profiller/{ad}/yayinlar?tur=sarkilar|coverlar.
// tur bossa sarkilar'dir.
func (s *Servis) ProfilYayinlari(ctx context.Context, izleyiciID, ad, tur, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	id, err := kullaniciCoz(izleyiciID)
	if err != nil {
		return nil, err
	}
	var coverlar bool
	switch tur {
	case "", "sarkilar":
	case "coverlar":
		coverlar = true
	default:
		return nil, dogrulamaHatasi("tur sarkilar ya da coverlar olmali")
	}
	imlec, err := zamanImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	if !KullaniciAdiGecerli(ad) {
		return nil, ErrBulunamadi
	}
	p, err := s.depo.ProfilAdlaGetir(ctx, ad)
	if err != nil {
		return nil, err
	}
	ogeler, err := s.depo.ProfilYayinlari(ctx, p.KullaniciID, id, coverlar, imlec, limit)
	if err != nil {
		return nil, err
	}
	return zamanSayfasiKur(ogeler, limit), nil
}

// Begeni, PUT (begen=true) ve DELETE (false) /yayinlar/{id}/begeni.
// Tekrari guvenlidir; yalnizca gorulebilen yayin begenilir.
func (s *Servis) Begeni(ctx context.Context, kullaniciID, yayinID string, begen bool) (*BegeniDurumu, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if begen {
		err = s.depo.Begen(ctx, id, k.ID)
	} else {
		err = s.depo.BegeniyiKaldir(ctx, id, k.ID)
	}
	if err != nil {
		return nil, err
	}
	guncel, err := s.depo.YayinGetir(ctx, k.ID, id)
	if err != nil {
		return nil, err
	}
	return &BegeniDurumu{BegendimMi: guncel.BegendimMi, BegeniSayisi: guncel.BegeniSayisi}, nil
}
````

**Modify** `core-service/internal/handler/sosyal.go`:

````diff
diff --git a/core-service/internal/handler/sosyal.go b/core-service/internal/handler/sosyal.go
--- a/core-service/internal/handler/sosyal.go
+++ b/core-service/internal/handler/sosyal.go
@@ -26,6 +26,10 @@ type SosyalServisi interface {
 	YayinSil(ctx context.Context, kullaniciID, yayinID string) error
 	TekrarDene(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinYaniti, error)
 	Dosyalar(ctx context.Context, kullaniciID, yayinID string) (*sosyal.YayinDosyalari, error)
+
+	Akis(ctx context.Context, kullaniciID, sekme, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
+	ProfilYayinlari(ctx context.Context, izleyiciID, ad, tur, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
+	Begeni(ctx context.Context, kullaniciID, yayinID string, begen bool) (*sosyal.BegeniDurumu, error)
 }
 
 var _ SosyalServisi = (*sosyal.Servis)(nil)
@@ -46,6 +50,8 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
 	r.Put("/profil/ben", h.ProfilKaydet)
 	r.Get("/profil/kullanici-adi/uygun", h.AdUygun)
 	r.Get("/profiller/:ad", h.Profil)
+	r.Get("/profiller/:ad/yayinlar", h.ProfilYayinlari)
+	r.Get("/akis", h.Akis)
 
 	r.Post("/yayinlar", yayinLimiti, h.YayinOlustur)
 	r.Get("/yayinlar/:id", h.Yayin)
@@ -54,6 +60,8 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
 	r.Post("/yayinlar/:id/tekrar-dene", h.TekrarDene)
 	r.Post("/yayinlar/:id/dosyalar", h.Dosyalar)
 	r.Get("/s/:slug", h.Slug)
+	r.Put("/yayinlar/:id/begeni", h.Begen)
+	r.Delete("/yayinlar/:id/begeni", h.BegeniyiKaldir)
 }
 
 // sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
````

**Create** `core-service/internal/handler/sosyal_akis.go`:

````go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// Akis handles GET /api/social/akis?sekme=senin-icin|takip|yeni&imlec=&limit=
func (h *SosyalHandler) Akis(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.Akis(c.Context(), userID, c.Query("sekme"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// ProfilYayinlari handles GET /api/social/profiller/:ad/yayinlar?tur=sarkilar|coverlar
func (h *SosyalHandler) ProfilYayinlari(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.ProfilYayinlari(c.Context(), userID, c.Params("ad"), c.Query("tur"),
		c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// Begen handles PUT /api/social/yayinlar/:id/begeni
func (h *SosyalHandler) Begen(c *fiber.Ctx) error {
	return h.begeni(c, true)
}

// BegeniyiKaldir handles DELETE /api/social/yayinlar/:id/begeni
func (h *SosyalHandler) BegeniyiKaldir(c *fiber.Ctx) error {
	return h.begeni(c, false)
}

func (h *SosyalHandler) begeni(c *fiber.Ctx, begen bool) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	b, err := h.servis.Begeni(c.Context(), userID, c.Params("id"), begen)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, b)
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1 -v 2>&1 | grep -E '^(--- FAIL|--- SKIP|ok|FAIL)'
```

Expected: only the two `ok` lines (no FAIL, no SKIP). Whole Go suite after this task: about 667 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/imlec_test.go \
  core-service/internal/sosyal/depo_akis_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/sosyal/servis_akis_test.go \
  core-service/internal/sosyal/butunlesik_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/handler/sosyal_akis_test.go \
  core-service/internal/sosyal/imlec.go \
  core-service/internal/sosyal/depo_akis.go \
  core-service/internal/sosyal/depo.go \
  core-service/internal/sosyal/servis.go \
  core-service/internal/sosyal/servis_akis.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_akis.go
git commit -F - <<'EOF'
feat(sosyal): akis, profil listeleri ve begeni

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 10: Account deletion removes Keşfet data and files

R2: `HesapService` gets the optional `SosyalTemizleyici` as a fifth constructor argument. Every caller changes; `main.go` passes `nil` for now and Task 11 passes the real repository. The existing assertions that exactly five user prefixes are deleted stay unchanged.

**Files:**

- Create: `core-service/internal/sosyal/depo_temizlik_test.go`
- Create: `core-service/internal/service/hesap_sosyal_test.go`
- Modify: `core-service/internal/service/hesap_service_test.go`
- Modify: `core-service/internal/worker/hesap_worker_test.go`
- Modify: `core-service/internal/handler/hesap_test.go`
- Create: `core-service/internal/sosyal/depo_temizlik.go`
- Modify: `core-service/internal/service/hesap_service.go`
- Modify: `core-service/cmd/server/main.go`

**Interfaces:**
- Consumes: `client.KullaniciOnekleri`, `client.OnekSilici` (existing); `client.SosyalOneki` (Task 2); `PostgresDepo`, fixtures (Tasks 4, 6).
- Produces: `(*sosyal.PostgresDepo).YayinlariListele(ctx, kullaniciID string) ([]string, error)` and `KullaniciVerisiniSil(ctx, kullaniciID string) error`; `type service.SosyalTemizleyici interface` with the same two methods; `service.NewHesapService(kimlikSilici KimlikSilici, silici client.OnekSilici, rdb *redis.Client, kuyruk Kuyruk, sosyal SosyalTemizleyici) *HesapService`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/depo_temizlik_test.go`:

````go
package sosyal

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresDepo_KullaniciVerisiniSil(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	giden, kalan := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, giden, "giden")
	testProfilEkle(t, d, kalan, "kalan")

	canli := testYayinEkle(t, havuz, testYayin{SahipID: giden, Baslik: "Orijinal"})
	silinmis := testYayinEkle(t, havuz, testYayin{SahipID: giden, Silindi: true})
	kalaninYayini := testYayinEkle(t, havuz, testYayin{SahipID: kalan})
	cover := testYayinEkle(t, havuz, testYayin{SahipID: kalan, KaynakID: canli})
	testTakipEkle(t, havuz, giden, kalan)
	testTakipEkle(t, havuz, kalan, giden)
	for _, sorgu := range []string{
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + giden + `', '` + kalaninYayini + `')`,
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + kalan + `', '` + canli + `')`,
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + kalan + `', '` + kalaninYayini + `')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), '` + kalaninYayini + `', '` + giden + `', 'g')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), '` + kalaninYayini + `', '` + kalan + `', 'k')`,
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), '` + giden + `', 'yayin', '` + kalaninYayini + `', '` + kalaninYayini + `', 'spam')`,
	} {
		if _, err := havuz.Exec(ctx, sorgu); err != nil {
			t.Fatalf("kurulum: %v", err)
		}
	}

	idler, err := d.YayinlariListele(ctx, giden)
	if err != nil {
		t.Fatalf("YayinlariListele: %v", err)
	}
	sort.Strings(idler)
	beklenen := []string{canli, silinmis}
	sort.Strings(beklenen)
	if len(idler) != 2 || idler[0] != beklenen[0] || idler[1] != beklenen[1] {
		t.Fatalf("silinmisler dahil iki yayin bekleniyordu: %v", idler)
	}

	if err := d.KullaniciVerisiniSil(ctx, giden); err != nil {
		t.Fatalf("KullaniciVerisiniSil: %v", err)
	}
	if err := d.KullaniciVerisiniSil(ctx, giden); err != nil {
		t.Fatalf("tekrar silme hatasiz olmali: %v", err)
	}

	var gidenKalan int
	if err := havuz.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.profil WHERE kullanici_id = $1)
		     + (SELECT count(*) FROM sosyal.yayin WHERE sahip_id = $1)
		     + (SELECT count(*) FROM sosyal.begeni WHERE kullanici_id = $1 OR yayin_id = $2)
		     + (SELECT count(*) FROM sosyal.yorum WHERE yazar_id = $1)
		     + (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1)
		     + (SELECT count(*) FROM sosyal.sikayet WHERE bildiren_id = $1)`, giden, canli).Scan(&gidenKalan); err != nil {
		t.Fatalf("sayim: %v", err)
	}
	if gidenKalan != 0 {
		t.Fatalf("silinen kullanicinin %d kaydi kaldi", gidenKalan)
	}

	// Baskalarinin verisi yerinde; cover kalir ve kaynagi silindi gorunur.
	if _, err := d.ProfilGetir(ctx, kalan); err != nil {
		t.Fatalf("kalan profil silinmemeli: %v", err)
	}
	k, err := d.YayinGetir(ctx, kalaninYayini, kalan)
	if err != nil || k.BegeniSayisi != 1 || k.YorumSayisi != 1 {
		t.Fatalf("kalanin yayini = %+v, %v", k, err)
	}
	c, err := d.YayinGetir(ctx, cover, kalan)
	if err != nil || c.Kaynak == nil || !c.Kaynak.Silindi || c.Kaynak.YayinID != canli {
		t.Fatalf("cover kalmali ve kaynagi silindi olmali: %+v, %v", c, err)
	}
	if bos, err := d.YayinlariListele(ctx, giden); err != nil || len(bos) != 0 {
		t.Fatalf("silinen kullanicinin yayini kalmamali: %v %v", bos, err)
	}
}
````

**Create** `core-service/internal/service/hesap_sosyal_test.go`:

````go
package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type sahteSosyalTemizleyici struct {
	idler     []string
	listeHata error
	silinen   []string
}

func (s *sahteSosyalTemizleyici) YayinlariListele(context.Context, string) ([]string, error) {
	return s.idler, s.listeHata
}

func (s *sahteSosyalTemizleyici) KullaniciVerisiniSil(_ context.Context, userID string) error {
	s.silinen = append(s.silinen, userID)
	return nil
}

// seciciSilici, yalnizca verilen onekte hata doner.
type seciciSilici struct {
	onekler  []string
	hataliOn string
}

func (s *seciciSilici) DeletePrefix(_ context.Context, prefix string) (int, error) {
	s.onekler = append(s.onekler, prefix)
	if s.hataliOn != "" && strings.HasPrefix(prefix, s.hataliOn) {
		return 0, errors.New("r2 kapali")
	}
	return 1, nil
}

const (
	sosyalY1 = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	sosyalY2 = "9b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func TestHesapSil_SosyalOnekleriVeSatirlariSiler(t *testing.T) {
	sil := &seciciSilici{}
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1, sosyalY2}}
	svc := NewHesapService(&sahteKimlik{}, sil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(sil.onekler) != 7 {
		t.Fatalf("5 kullanici + 2 sosyal onek bekleniyordu: %v", sil.onekler)
	}
	if !slices.Equal(sil.onekler[5:], []string{"sosyal/" + sosyalY1 + "/", "sosyal/" + sosyalY2 + "/"}) {
		t.Fatalf("sosyal onekler = %v", sil.onekler[5:])
	}
	if !slices.Equal(sos.silinen, []string{hesapID}) {
		t.Fatalf("sosyal satirlar silinmeli: %v", sos.silinen)
	}
}

// R2 hatasinda satirlar kalir: gecikmeli gorev kimlikleri yeniden bulur.
func TestHesapTemizle_SosyalR2HatasindaSatirlarKalir(t *testing.T) {
	sil := &seciciSilici{hataliOn: "sosyal/" + sosyalY2}
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1, sosyalY2}}
	svc := NewHesapService(&sahteKimlik{}, sil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); err == nil {
		t.Fatal("R2 hatasi donmeli")
	}
	if len(sos.silinen) != 0 {
		t.Fatal("R2 temizligi eksikken satirlar silinmemeli")
	}
	if len(sil.onekler) != 7 {
		t.Fatalf("hata olsa da tum onekler denenmeli: %v", sil.onekler)
	}
}

func TestHesapTemizle_SosyalListeHatasi(t *testing.T) {
	sos := &sahteSosyalTemizleyici{listeHata: errors.New("pg kapali")}
	svc := NewHesapService(&sahteKimlik{}, &seciciSilici{}, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); err == nil {
		t.Fatal("liste hatasi donmeli")
	}
	if len(sos.silinen) != 0 {
		t.Fatal("liste okunamadan satir silinmemeli")
	}
}

// R2 yoksa dosya yoktur; satirlar yine silinir.
func TestHesapTemizle_R2YokkenSosyalSatirlarSilinir(t *testing.T) {
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1}}
	svc := NewHesapService(&sahteKimlik{}, nil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if !slices.Equal(sos.silinen, []string{hesapID}) {
		t.Fatalf("satirlar silinmeli: %v", sos.silinen)
	}
}
````

**Modify** `core-service/internal/service/hesap_service_test.go`:

````diff
diff --git a/core-service/internal/service/hesap_service_test.go b/core-service/internal/service/hesap_service_test.go
--- a/core-service/internal/service/hesap_service_test.go
+++ b/core-service/internal/service/hesap_service_test.go
@@ -55,7 +55,7 @@ const hesapID = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
 
 func TestHesapSil_HerSeyiSilerVeIkinciTemizligiKurar(t *testing.T) {
 	k, s, q := &sahteKimlik{}, &sahteSilici{}, &sahteKuyruk{}
-	svc := NewHesapService(k, s, ulasilamazRedis(), q)
+	svc := NewHesapService(k, s, ulasilamazRedis(), q, nil)
 	if err := svc.Sil(context.Background(), hesapID); err != nil {
 		t.Fatalf("beklenmeyen hata: %v", err)
 	}
@@ -78,7 +78,7 @@ func TestHesapSil_HerSeyiSilerVeIkinciTemizligiKurar(t *testing.T) {
 
 func TestHesapSil_KimlikYoksaErrHesapYok(t *testing.T) {
 	k, s, q := &sahteKimlik{hata: kimlik.ErrKullaniciYok}, &sahteSilici{}, &sahteKuyruk{}
-	err := NewHesapService(k, s, ulasilamazRedis(), q).Sil(context.Background(), hesapID)
+	err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID)
 	if !errors.Is(err, ErrHesapYok) {
 		t.Fatalf("ErrHesapYok bekleniyordu, gelen %v", err)
 	}
@@ -89,7 +89,7 @@ func TestHesapSil_KimlikYoksaErrHesapYok(t *testing.T) {
 
 func TestHesapSil_KimlikHatasindaR2yeDokunulmaz(t *testing.T) {
 	k, s, q := &sahteKimlik{hata: errors.New("pg kapali")}, &sahteSilici{}, &sahteKuyruk{}
-	err := NewHesapService(k, s, ulasilamazRedis(), q).Sil(context.Background(), hesapID)
+	err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID)
 	if err == nil || errors.Is(err, ErrHesapYok) {
 		t.Fatalf("genel hata bekleniyordu, gelen %v", err)
 	}
@@ -100,7 +100,7 @@ func TestHesapSil_KimlikHatasindaR2yeDokunulmaz(t *testing.T) {
 
 func TestHesapSil_R2HatasiIstegiBozmaz(t *testing.T) {
 	k, s, q := &sahteKimlik{}, &sahteSilici{hata: errors.New("r2 kapali")}, &sahteKuyruk{}
-	if err := NewHesapService(k, s, ulasilamazRedis(), q).Sil(context.Background(), hesapID); err != nil {
+	if err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID); err != nil {
 		t.Fatalf("hesap silindikten sonra hata donmemeli: %v", err)
 	}
 	if len(q.gorevler) != 1 {
@@ -110,14 +110,14 @@ func TestHesapSil_R2HatasiIstegiBozmaz(t *testing.T) {
 
 func TestHesapSil_GecersizIDHicbirSeySilmez(t *testing.T) {
 	k, s, q := &sahteKimlik{}, &sahteSilici{}, &sahteKuyruk{}
-	err := NewHesapService(k, s, ulasilamazRedis(), q).Sil(context.Background(), "../x")
+	err := NewHesapService(k, s, ulasilamazRedis(), q, nil).Sil(context.Background(), "../x")
 	if !errors.Is(err, ErrHesapYok) || len(k.silinen) != 0 {
 		t.Fatalf("gecersiz id silinmemeli: err=%v silinen=%v", err, k.silinen)
 	}
 }
 
 func TestHesapTemizle_R2YoksaHataYok(t *testing.T) {
-	svc := NewHesapService(&sahteKimlik{}, nil, ulasilamazRedis(), &sahteKuyruk{})
+	svc := NewHesapService(&sahteKimlik{}, nil, ulasilamazRedis(), &sahteKuyruk{}, nil)
 	if err := svc.Temizle(context.Background(), hesapID); err != nil {
 		t.Fatalf("R2 yokken temizlik no-op olmali: %v", err)
 	}
@@ -127,7 +127,7 @@ func TestHesapTemizle_R2YoksaHataYok(t *testing.T) {
 // kosan isler sahibin silindigini bu sure boyunca gorebilmeli.
 func TestHesapSil_Isaret24SaatYasar(t *testing.T) {
 	rdb := canliRedis(t)
-	svc := NewHesapService(&sahteKimlik{}, &sahteSilici{}, rdb, &sahteKuyruk{})
+	svc := NewHesapService(&sahteKimlik{}, &sahteSilici{}, rdb, &sahteKuyruk{}, nil)
 	if err := svc.Sil(context.Background(), hesapID); err != nil {
 		t.Fatalf("beklenmeyen hata: %v", err)
 	}
@@ -141,7 +141,7 @@ func TestHesapSil_Isaret24SaatYasar(t *testing.T) {
 // (render varsayilan 30dk zaman asimi) fazlasiyla sonra.
 func TestHesapSil_GecikmeliTemizlikBirSaatSonra(t *testing.T) {
 	q := &sahteKuyruk{}
-	if err := NewHesapService(&sahteKimlik{}, &sahteSilici{}, ulasilamazRedis(), q).Sil(context.Background(), hesapID); err != nil {
+	if err := NewHesapService(&sahteKimlik{}, &sahteSilici{}, ulasilamazRedis(), q, nil).Sil(context.Background(), hesapID); err != nil {
 		t.Fatalf("beklenmeyen hata: %v", err)
 	}
 	if len(q.secenekler) != 1 {
````

**Modify** `core-service/internal/worker/hesap_worker_test.go`:

````diff
diff --git a/core-service/internal/worker/hesap_worker_test.go b/core-service/internal/worker/hesap_worker_test.go
--- a/core-service/internal/worker/hesap_worker_test.go
+++ b/core-service/internal/worker/hesap_worker_test.go
@@ -22,7 +22,7 @@ func (bosKimlik) HesapSil(ctx context.Context, userID string) error { return nil
 
 func TestHesapWorker_OnekleriTemizler(t *testing.T) {
 	s := &kayitSilici{}
-	svc := service.NewHesapService(bosKimlik{}, s, nil, nil)
+	svc := service.NewHesapService(bosKimlik{}, s, nil, nil, nil)
 	gorev := asynq.NewTask(service.TaskTypeHesapTemizle, []byte(`{"userId":"6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"}`))
 	if err := NewHesapWorker(svc).ProcessTask(context.Background(), gorev); err != nil {
 		t.Fatalf("beklenmeyen hata: %v", err)
@@ -33,7 +33,7 @@ func TestHesapWorker_OnekleriTemizler(t *testing.T) {
 }
 
 func TestHesapWorker_BozukYukTekrarlanmaz(t *testing.T) {
-	svc := service.NewHesapService(bosKimlik{}, &kayitSilici{}, nil, nil)
+	svc := service.NewHesapService(bosKimlik{}, &kayitSilici{}, nil, nil, nil)
 	err := NewHesapWorker(svc).ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeHesapTemizle, []byte("{")))
 	if !errors.Is(err, asynq.SkipRetry) {
 		t.Fatalf("SkipRetry bekleniyordu, gelen %v", err)
````

**Modify** `core-service/internal/handler/hesap_test.go`:

````diff
diff --git a/core-service/internal/handler/hesap_test.go b/core-service/internal/handler/hesap_test.go
--- a/core-service/internal/handler/hesap_test.go
+++ b/core-service/internal/handler/hesap_test.go
@@ -27,7 +27,7 @@ func (hBosKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo,
 
 func hesapUygulamasi(kimlikHata error, userID string) *fiber.App {
 	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
-	svc := service.NewHesapService(hSahteKimlik{hata: kimlikHata}, nil, rdb, hBosKuyruk{})
+	svc := service.NewHesapService(hSahteKimlik{hata: kimlikHata}, nil, rdb, hBosKuyruk{}, nil)
 	app := fiber.New()
 	app.Delete("/api/account", kullaniciOlarak(userID), NewHesapHandler(svc).Sil)
 	return app
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/service/ ./internal/worker/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors `d.YayinlariListele undefined` and `too many arguments in call to NewHesapService`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/depo_temizlik.go`:

````go
package sosyal

import (
	"context"
	"fmt"
)

// YayinlariListele, kullanicinin TUM yayin kimliklerini (yumusak
// silinmisler dahil) doner. Hesap silme her biri icin sosyal/{id}/ onekini
// temizler; satirlar ancak R2 temizligi basarili olunca silinir ki
// gecikmeli tekrar ayni kimlikleri yeniden bulabilsin.
func (d *PostgresDepo) YayinlariListele(ctx context.Context, kullaniciID string) ([]string, error) {
	satirlar, err := d.havuz.Query(ctx,
		`SELECT id::text FROM sosyal.yayin WHERE sahip_id = $1 ORDER BY olusturma`, kullaniciID)
	if err != nil {
		return nil, fmt.Errorf("yayinlar listelenemedi: %w", err)
	}
	defer satirlar.Close()
	idler := []string{}
	for satirlar.Next() {
		var id string
		if err := satirlar.Scan(&id); err != nil {
			return nil, fmt.Errorf("yayin kimligi okunamadi: %w", err)
		}
		idler = append(idler, id)
	}
	return idler, satirlar.Err()
}

// KullaniciVerisiniSil, kullanicinin sosyal kayitlarini tek islemde siler:
// begenileri, yorumlari, takipleri (iki yon), sikayetleri, yayinlari
// (onlara bagli begeni, yorum ve sikayetler cascade ile) ve profili.
// Baskalarinin cover'lari kalir; kaynaklari silindi olarak gorunur.
func (d *PostgresDepo) KullaniciVerisiniSil(ctx context.Context, kullaniciID string) error {
	islem, err := d.havuz.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sosyal silme islemi baslatilamadi: %w", err)
	}
	defer func() { _ = islem.Rollback(ctx) }()
	for _, sorgu := range []string{
		`DELETE FROM sosyal.begeni WHERE kullanici_id = $1`,
		`DELETE FROM sosyal.yorum WHERE yazar_id = $1`,
		`DELETE FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1`,
		`DELETE FROM sosyal.sikayet WHERE bildiren_id = $1`,
		`DELETE FROM sosyal.yayin WHERE sahip_id = $1`,
		`DELETE FROM sosyal.profil WHERE kullanici_id = $1`,
	} {
		if _, err := islem.Exec(ctx, sorgu, kullaniciID); err != nil {
			return fmt.Errorf("sosyal veri silinemedi: %w", err)
		}
	}
	if err := islem.Commit(ctx); err != nil {
		return fmt.Errorf("sosyal silme commit edilemedi: %w", err)
	}
	return nil
}
````

**Modify** `core-service/internal/service/hesap_service.go`:

````diff
diff --git a/core-service/internal/service/hesap_service.go b/core-service/internal/service/hesap_service.go
--- a/core-service/internal/service/hesap_service.go
+++ b/core-service/internal/service/hesap_service.go
@@ -40,15 +40,26 @@ type Kuyruk interface {
 	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
 }
 
+// SosyalTemizleyici, Kesfet verisinin hesap silmede temizlenen yuzu
+// (*sosyal.PostgresDepo uygular).
+type SosyalTemizleyici interface {
+	// YayinlariListele, yumusak silinmisler dahil tum yayin kimlikleri.
+	YayinlariListele(ctx context.Context, userID string) ([]string, error)
+	// KullaniciVerisiniSil, profil, yayin, begeni, yorum, takip ve
+	// sikayetleri tek islemde siler.
+	KullaniciVerisiniSil(ctx context.Context, userID string) error
+}
+
 type HesapService struct {
 	kimlik KimlikSilici
 	silici client.OnekSilici // nil: R2 yapilandirilmamis
 	redis  *redis.Client
 	kuyruk Kuyruk
+	sosyal SosyalTemizleyici // nil: sosyal modul yok
 }
 
-func NewHesapService(kimlikSilici KimlikSilici, silici client.OnekSilici, rdb *redis.Client, kuyruk Kuyruk) *HesapService {
-	return &HesapService{kimlik: kimlikSilici, silici: silici, redis: rdb, kuyruk: kuyruk}
+func NewHesapService(kimlikSilici KimlikSilici, silici client.OnekSilici, rdb *redis.Client, kuyruk Kuyruk, sosyal SosyalTemizleyici) *HesapService {
+	return &HesapService{kimlik: kimlikSilici, silici: silici, redis: rdb, kuyruk: kuyruk, sosyal: sosyal}
 }
 
 // Sil, hesabi ve tum dosyalarini siler. Kimlik kaydi silindikten sonra
@@ -88,21 +99,55 @@ func (s *HesapService) Sil(ctx context.Context, userID string) error {
 	return nil
 }
 
-// Temizle, kullanicinin tum R2 oneklerini siler. R2 yoksa bir sey
-// yapmaz. Bir onek basarisiz olsa da digerleri denenir.
+// Temizle, kullanicinin tum R2 oneklerini ve Kesfet verisini siler. R2
+// yoksa onekler atlanir. Bir onek basarisiz olsa da digerleri denenir.
 func (s *HesapService) Temizle(ctx context.Context, userID string) error {
-	if s.silici == nil {
-		return nil
-	}
 	onekler, err := client.KullaniciOnekleri(userID)
 	if err != nil {
 		return err
 	}
 	var hatalar []error
-	for _, onek := range onekler {
-		if _, err := s.silici.DeletePrefix(ctx, onek); err != nil {
-			hatalar = append(hatalar, fmt.Errorf("%s: %w", onek, err))
+	if s.silici != nil {
+		for _, onek := range onekler {
+			if _, err := s.silici.DeletePrefix(ctx, onek); err != nil {
+				hatalar = append(hatalar, fmt.Errorf("%s: %w", onek, err))
+			}
+		}
+	}
+	if s.sosyal != nil {
+		if err := s.sosyalTemizle(ctx, userID); err != nil {
+			hatalar = append(hatalar, err)
 		}
 	}
 	return errors.Join(hatalar...)
 }
+
+// sosyalTemizle, once her yayinin sosyal/{id}/ onekini, sonra satirlari
+// siler. Bir onek bile basarisizsa satirlar KALIR: gecikmeli gorev ayni
+// kimlikleri yeniden listeleyip temizligi tamamlar (idempotent).
+func (s *HesapService) sosyalTemizle(ctx context.Context, userID string) error {
+	idler, err := s.sosyal.YayinlariListele(ctx, userID)
+	if err != nil {
+		return fmt.Errorf("sosyal yayinlar listelenemedi: %w", err)
+	}
+	if s.silici != nil {
+		var hatalar []error
+		for _, id := range idler {
+			onek, err := client.SosyalOneki(id)
+			if err != nil {
+				hatalar = append(hatalar, fmt.Errorf("sosyal onek %s: %w", id, err))
+				continue
+			}
+			if _, err := s.silici.DeletePrefix(ctx, onek); err != nil {
+				hatalar = append(hatalar, fmt.Errorf("%s: %w", onek, err))
+			}
+		}
+		if len(hatalar) > 0 {
+			return errors.Join(hatalar...)
+		}
+	}
+	if err := s.sosyal.KullaniciVerisiniSil(ctx, userID); err != nil {
+		return fmt.Errorf("sosyal veri silinemedi: %w", err)
+	}
+	return nil
+}
````

**Modify** `core-service/cmd/server/main.go`:

````diff
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -150,7 +150,7 @@ func main() {
 	}
 
 	// Hesap silme: kullanici kaydi kendi OP'mizde tutulur.
-	hesapService := service.NewHesapService(kimlikSunucu, silici, redisClient, asynqClient)
+	hesapService := service.NewHesapService(kimlikSunucu, silici, redisClient, asynqClient, nil)
 	hesapHandler := handler.NewHesapHandler(hesapService)
 
 	// Initialize handlers
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go build ./... && go test ./internal/sosyal/ ./internal/service/ ./internal/worker/ ./internal/handler/ -count=1
```

Expected: build clean, four packages `ok`. Whole Go suite after this task: about 672 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/depo_temizlik_test.go \
  core-service/internal/service/hesap_sosyal_test.go \
  core-service/internal/service/hesap_service_test.go \
  core-service/internal/worker/hesap_worker_test.go \
  core-service/internal/handler/hesap_test.go \
  core-service/internal/sosyal/depo_temizlik.go \
  core-service/internal/service/hesap_service.go \
  core-service/cmd/server/main.go
git commit -F - <<'EOF'
feat(hesap): hesap silmede Kesfet verisini ve dosyalarini temizle

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 11: Configuration and server wiring

R9's rate-limit key and everything `main.go` needs: the sosyal pool and migration (fatal on failure, N14), the service and handler, `api.Group("/social")` with the publish limiter, PATCH in CORS, the `sosyal` queue (weight 2), the two task handlers and the real `SosyalTemizleyici` for account deletion. `kopyalayici` is assigned only when R2 is configured, to avoid the typed-nil trap (same pattern as `storage` and `silici`).

**Files:**

- Modify: `core-service/internal/config/config_test.go`
- Modify: `core-service/internal/config/config.go`
- Modify: `core-service/config.yaml`
- Modify: `core-service/.env.example`
- Modify: `core-service/cmd/server/main.go`

**Interfaces:**
- Consumes: Everything above.
- Produces: `config.RateLimitConfig.SosyalYayinPerHour int` (`ratelimit.sosyal_yayin_per_hour`, env `RATELIMIT_SOSYAL_YAYIN_PER_HOUR`, default 30); the running endpoints and workers.

- [ ] **Step 1: Write the failing test**

**Modify** `core-service/internal/config/config_test.go`:

````diff
diff --git a/core-service/internal/config/config_test.go b/core-service/internal/config/config_test.go
--- a/core-service/internal/config/config_test.go
+++ b/core-service/internal/config/config_test.go
@@ -336,3 +336,23 @@ func TestLoad_SunoModelEnvIleDegisir(t *testing.T) {
 		t.Errorf("Suno.Model = %q, beklenen V6_MINI", cfg.Suno.Model)
 	}
 }
+
+func TestLoad_SosyalYayinLimiti(t *testing.T) {
+	os.Unsetenv("RATELIMIT_SOSYAL_YAYIN_PER_HOUR")
+	cfg, err := Load()
+	if err != nil {
+		t.Fatalf("Load hata verdi: %v", err)
+	}
+	if cfg.RateLimit.SosyalYayinPerHour != 30 {
+		t.Errorf("varsayilan SosyalYayinPerHour = %d, beklenen 30", cfg.RateLimit.SosyalYayinPerHour)
+	}
+
+	t.Setenv("RATELIMIT_SOSYAL_YAYIN_PER_HOUR", "7")
+	cfg, err = Load()
+	if err != nil {
+		t.Fatalf("Load hata verdi: %v", err)
+	}
+	if cfg.RateLimit.SosyalYayinPerHour != 7 {
+		t.Errorf("SosyalYayinPerHour = %d, ortamdan 7 bekleniyordu", cfg.RateLimit.SosyalYayinPerHour)
+	}
+}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/config/ -count=1 -run Sosyal
```

Expected: FAIL, build error `cfg.RateLimit.SosyalYayinPerHour undefined`.

- [ ] **Step 3: Implement**

There is no test harness for `main.go`; the compiler checks the wiring (`*sosyal.PostgresDepo` must satisfy `service.SosyalTemizleyici` and `sosyal.HazirlikDeposu`, `*client.AudioClient` must satisfy `client.AudioAnalizci`), and Step 5 runs the binary.

**Modify** `core-service/internal/config/config.go`:

````diff
diff --git a/core-service/internal/config/config.go b/core-service/internal/config/config.go
--- a/core-service/internal/config/config.go
+++ b/core-service/internal/config/config.go
@@ -60,6 +60,9 @@ type RateLimitConfig struct {
 	UploadPerHour  int
 	WSTicketPerMin int
 	SignPerMin     int
+	// SosyalYayinPerHour, POST /api/social/yayinlar icin kullanici basina
+	// saatlik limit.
+	SosyalYayinPerHour int
 }
 
 type GroqConfig struct {
@@ -228,6 +231,7 @@ func Load() (*Config, error) {
 	_ = viper.BindEnv("auth.fed_apple_team_id", "AUTH_FED_APPLE_TEAM_ID")
 	_ = viper.BindEnv("auth.fed_apple_key_id", "AUTH_FED_APPLE_KEY_ID")
 	_ = viper.BindEnv("auth.fed_apple_key_p8", "AUTH_FED_APPLE_KEY_P8")
+	_ = viper.BindEnv("ratelimit.sosyal_yayin_per_hour", "RATELIMIT_SOSYAL_YAYIN_PER_HOUR")
 
 	// Defaults
 	viper.SetDefault("server.port", "8000")
@@ -243,6 +247,7 @@ func Load() (*Config, error) {
 	viper.SetDefault("ratelimit.upload_per_hour", 50)
 	viper.SetDefault("ratelimit.ws_ticket_per_min", 60)
 	viper.SetDefault("ratelimit.sign_per_min", 60)
+	viper.SetDefault("ratelimit.sosyal_yayin_per_hour", 30)
 
 	// Groq defaults
 	viper.SetDefault("groq.base_url", "https://api.groq.com/openai/v1")
@@ -330,6 +335,8 @@ func Load() (*Config, error) {
 			UploadPerHour:  viper.GetInt("ratelimit.upload_per_hour"),
 			WSTicketPerMin: viper.GetInt("ratelimit.ws_ticket_per_min"),
 			SignPerMin:     viper.GetInt("ratelimit.sign_per_min"),
+
+			SosyalYayinPerHour: viper.GetInt("ratelimit.sosyal_yayin_per_hour"),
 		},
 		Groq: GroqConfig{
 			APIKey:  viper.GetString("groq.api_key"),
````

**Modify** `core-service/config.yaml`:

````diff
diff --git a/core-service/config.yaml b/core-service/config.yaml
--- a/core-service/config.yaml
+++ b/core-service/config.yaml
@@ -16,6 +16,7 @@ ratelimit:
   upload_per_hour: 50
   ws_ticket_per_min: 60
   sign_per_min: 60
+  sosyal_yayin_per_hour: 30
 
 # Groq AI Configuration
 groq:
````

**Modify** `core-service/.env.example`:

````diff
diff --git a/core-service/.env.example b/core-service/.env.example
--- a/core-service/.env.example
+++ b/core-service/.env.example
@@ -13,6 +13,8 @@ RATELIMIT_RENDER_PER_HOUR=5
 RATELIMIT_MASTER_PER_HOUR=10
 RATELIMIT_EXPORT_PER_HOUR=20
 RATELIMIT_UPLOAD_PER_HOUR=50
+# POST /api/social/yayinlar, kullanici basina saatlik
+RATELIMIT_SOSYAL_YAYIN_PER_HOUR=30
 
 # Groq AI Configuration
 GROQ_API_KEY=gsk_...
````

**Modify** `core-service/cmd/server/main.go`:

````diff
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -27,6 +27,7 @@ import (
 	"github.com/makeasinger/api/internal/kimlik"
 	"github.com/makeasinger/api/internal/middleware"
 	"github.com/makeasinger/api/internal/service"
+	"github.com/makeasinger/api/internal/sosyal"
 	ws "github.com/makeasinger/api/internal/websocket"
 	"github.com/makeasinger/api/internal/worker"
 )
@@ -93,6 +94,19 @@ func main() {
 		_ = kimlikSunucu.Kapat(kapatCtx)
 	}()
 
+	// Kesfet (sosyal): kimlikle ayni veritabani, ama kendi havuzu, kendi
+	// semasi (sosyal) ve kendi migration tablosu. Kimlik gibi FATAL:
+	// sosyal tablolari olmayan bir pod her /api/social isteginde 500 doner.
+	sosyalHavuz, err := kimlik.Baglan(ctx, cfg.Auth.DBURL)
+	if err != nil {
+		log.Fatalf("Sosyal veritabanina baglanilamadi: %v", err)
+	}
+	defer sosyalHavuz.Close()
+	if err := sosyal.Migrate(ctx, sosyalHavuz); err != nil {
+		log.Fatalf("Sosyal migration basarisiz: %v", err)
+	}
+	sosyalDepo := sosyal.NewPostgresDepo(sosyalHavuz)
+
 	// Initialize Asynq client
 	asynqClient := asynq.NewClient(asynq.RedisClientOpt{
 		Addr:     cfg.Redis.Addr,
@@ -150,9 +164,19 @@ func main() {
 	}
 
 	// Hesap silme: kullanici kaydi kendi OP'mizde tutulur.
-	hesapService := service.NewHesapService(kimlikSunucu, silici, redisClient, asynqClient, nil)
+	hesapService := service.NewHesapService(kimlikSunucu, silici, redisClient, asynqClient, sosyalDepo)
 	hesapHandler := handler.NewHesapHandler(hesapService)
 
+	sosyalHandler := handler.NewSosyalHandler(sosyal.NewServis(sosyalDepo, asynqClient, storage, &cfg.R2))
+
+	// kopyalayici: arayuz degiskeni; r2Client nil iken dogrudan atamak
+	// tipli nil tuzagini kurar (bkz. storage).
+	var kopyalayici client.Kopyalayici
+	if r2Client != nil {
+		kopyalayici = r2Client
+	}
+	sosyalWorker := worker.NewSosyalWorker(sosyalDepo, kopyalayici, audioClient, silici, hesapisaret.NewRedisDenetci(redisClient))
+
 	// Initialize handlers
 	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
 	renderHandler := handler.NewRenderHandler(renderService, validate)
@@ -190,7 +214,7 @@ func main() {
 	}))
 	app.Use(cors.New(cors.Config{
 		AllowOrigins: "*",
-		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
+		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
 		AllowHeaders: "Origin,Content-Type,Accept,Authorization",
 	}))
 
@@ -275,6 +299,10 @@ func main() {
 
 	api.Delete("/account", hesapHandler.Sil)
 
+	// Kesfet (sosyal) uclari. Yayin limiti yalnizca POST /yayinlar'dadir.
+	sosyalHandler.Rotalar(api.Group("/social"),
+		rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SosyalYayinPerHour, time.Hour))
+
 	// WebSocket routes
 	app.Use("/ws", func(c *fiber.Ctx) error {
 		if websocket.IsWebSocketUpgrade(c) {
@@ -290,7 +318,7 @@ func main() {
 	}))
 
 	// Start Asynq worker server
-	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub, hesapService)
+	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub, hesapService, sosyalWorker)
 
 	// Graceful shutdown
 	quit := make(chan os.Signal, 1)
@@ -322,6 +350,7 @@ func startWorkerServer(
 	r2Client client.StorageClient,
 	hub *ws.Hub,
 	hesapService *service.HesapService,
+	sosyalWorker *worker.SosyalWorker,
 ) {
 	asynqLogLevel := asynq.InfoLevel
 	if strings.EqualFold(cfg.Server.LogLevel, "debug") {
@@ -343,6 +372,7 @@ func startWorkerServer(
 			Queues: map[string]int{
 				"render": 6,
 				"master": 4,
+				"sosyal": 2,
 				"bakim":  1,
 			},
 			LogLevel: asynqLogLevel,
@@ -360,6 +390,8 @@ func startWorkerServer(
 	mux.HandleFunc(service.TaskTypeRender, renderWorker.ProcessTask)
 	mux.HandleFunc(service.TaskTypeMaster, masterWorker.ProcessTask)
 	mux.HandleFunc(service.TaskTypeHesapTemizle, worker.NewHesapWorker(hesapService).ProcessTask)
+	mux.HandleFunc(sosyal.TaskTypeHazirla, sosyalWorker.Hazirla)
+	mux.HandleFunc(sosyal.TaskTypeDosyaSil, sosyalWorker.DosyaSil)
 
 	if err := srv.Run(mux); err != nil {
 		log.Printf("Asynq worker error: %v", err)
````

- [ ] **Step 4: Run every check, then smoke-run the server**

```bash
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... \
  && for i in 1 2 3; do go test ./internal/... -count=1 || break; done
openssl genrsa 2048 > /tmp/k.pem && go build -o /tmp/server ./cmd/server && \
  SERVER_PORT=18000 AUTH_PORT=18001 AUTH_ISSUER=http://localhost:18001 AUTH_SIGNING_KEY="$(cat /tmp/k.pem)" \
  AUTH_REDIRECT_URIS=com.makesinger.app://cb AUTH_CLIENT_ID=mobil AUTH_CRYPTO_KEY=0123456789abcdef0123456789abcdef \
  AUTH_DB_URL='postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable' REDIS_DB=14 /tmp/server &
curl -s -i localhost:18000/api/social/akis | head -1
curl -s -i -X OPTIONS -H 'Origin: http://x' -H 'Access-Control-Request-Method: PATCH' localhost:18000/api/social/yayinlar/x | grep -i allow-methods
kill %1
```

Expected: no gofmt output, vet/build clean, `0 issues.`, all packages `ok` three times in a row; the server starts (no `Sosyal migration basarisiz`), `/api/social/akis` answers `HTTP/1.1 401 Unauthorized`, and the preflight shows `Access-Control-Allow-Methods: GET,POST,PUT,PATCH,DELETE,OPTIONS`. Whole Go suite after this task: about 673 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/config/config_test.go \
  core-service/internal/config/config.go \
  core-service/config.yaml \
  core-service/.env.example \
  core-service/cmd/server/main.go
git commit -F - <<'EOF'
feat(server): sosyal modulu baglantisi, yayin limiti ve PATCH CORS

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 12: Document the module in `CLAUDE.md`

Brings the repository guide up to date: the new package, handlers and worker, the fourth queue, the account-deletion extension and the `sosyal/` key rules.

**Files:**

- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: Nothing.
- Produces: Documentation only.

- [ ] **Step 1: Update `CLAUDE.md`**

**Modify** `CLAUDE.md`:

````diff
diff --git a/CLAUDE.md b/CLAUDE.md
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -6,8 +6,8 @@ This file provides guidance to Claude Code (claude.ai/code) when working with co
 
 Monorepo with two services plus reverse proxy configuration:
 
-- **core-service/** — Go API backend (Fiber v2 + Asynq workers). Includes `internal/kimlik/` for custom OpenID Provider.
-- **audio-service/** — Python audio processing (FFmpeg-based mastering)
+- **core-service/** — Go API backend (Fiber v2 + Asynq workers). Includes `internal/kimlik/` for custom OpenID Provider and `internal/sosyal/` for Keşfet (social).
+- **audio-service/** — Python audio processing (FFmpeg-based mastering, Keşfet waveform and preview via `/analiz`)
 - **traefik/** — Reverse proxy configuration
 
 Notes: Zitadel was removed in Phase 5; authentication is the custom OpenID Provider in core-service (internal/kimlik/).
@@ -51,12 +51,13 @@ HTTP Request → Fiber middleware (auth, rate-limit) → Handler → Service →
 
 ### Key layers (`core-service/internal/`)
 
-- **handler/** — Fiber route handlers. One file per domain: `lyrics`, `render`, `master`, `export`, `upload`, `hesap` (`DELETE /api/account`), `files` (`POST /api/files/sign`).
+- **handler/** — Fiber route handlers. One file per domain: `lyrics`, `render`, `master`, `export`, `upload`, `hesap` (`DELETE /api/account`), `files` (`POST /api/files/sign`), `sosyal*` (`/api/social/*`, routes bound by `SosyalHandler.Rotalar`).
 - **service/** — Business logic. Mirrors handler structure. Services enqueue Asynq jobs for render/master operations.
-- **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate`, then by stemMode: none / `split_stem_advanced` / `split_stem`; stems downloaded and uploaded to R2 as `stems/{userId}/{projectId}/{stemId}.mp3` via `client.KullaniciAnahtari`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline), `hesap_worker.go` (delayed R2 cleanup `hesap:temizle` after account deletion). Render and master workers refuse paid calls and R2 writes for deleted owners (`hesapisaret.Denetci`).
+- **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate`, then by stemMode: none / `split_stem_advanced` / `split_stem`; stems downloaded and uploaded to R2 as `stems/{userId}/{projectId}/{stemId}.mp3` via `client.KullaniciAnahtari`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline), `hesap_worker.go` (delayed R2 cleanup `hesap:temizle` after account deletion), `sosyal_worker.go` (`sosyal:hazirla`: copies a publication's master and stems to `sosyal/{yayinId}/`, asks audio-service `/analiz` for 64 peaks and a 30 s preview, sets `yayinda`; `sosyal:dosya-sil`: deletes `sosyal/{yayinId}/` 24 h after a publication is deleted). Render and master workers refuse paid calls and R2 writes for deleted owners (`hesapisaret.Denetci`).
 - **client/** — HTTP clients for external services: Groq (AI lyrics), Suno (music generation), R2 (Cloudflare S3-compatible storage), audio-service (local mastering).
 - **model/** — Request/response structs and enums. All enum values defined in `enums.go`.
 - **middleware/** — `auth.go` (RS256 access tokens from `internal/kimlik` — the only auth path), `ratelimit.go` (Redis fixed window per user; limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute), `silinmis.go` (`SilinmisKullaniciEngeli`: deleted-user gate on `/api`, 401 for deleted accounts, 503 + `Retry-After: 5` when Redis is unreachable).
+- **sosyal/** — Keşfet (spec: `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md`). Own Postgres pool on `AUTH_DB_URL`, own schema `sosyal` and migration table `sosyal.schema_migrations` (embedded `migrations/*.sql`, advisory lock `0x736F7379616C31`, run at startup, additive only). No FK to the kimlik `users` table. `Servis` holds the rules (username, publish validation, detail visibility via `DetayGorebilir`), `PostgresDepo` the SQL (list visibility via `gorunurlukKosulu`; a live test proves the two agree). Errors map to lowercase contract codes in `handler/sosyal.go` (`bulunamadi`, `profil_yok`, `dogrulama`, `sahiplik`, `stem_yok`, `durum`, ...); social validation failures are 422, not 400. Lists are cursor-paged (`imlec`, `limit` 1-50, `{ogeler, sonrakiImlec}`).
 - **hesapisaret/** — Deleted-account marker shared by middleware, `HesapService` and workers: `Anahtar(userID)` (`kimlik:silindi:<userId>`), `Omur` (24 h), `Denetci` interface and `RedisDenetci` (EXISTS).
 - **auth/** — `jwks_verifier.go`: token verifiers. `YukariAkisDogrulayici` validates **upstream** (Apple/Google) `id_token`s for the federation flow — RS256 pinned, `iss`/`aud`/`exp` enforced, `nonce` required and compared in constant time.
 - **websocket/** — Hub for broadcasting real-time job progress to clients via `GET /ws/jobs/:jobId?ticket=` (ticket from `POST /api/ws/ticket`).
@@ -78,7 +79,7 @@ Config priority: environment variables > `config.yaml` > Viper defaults. See `co
 
 ### Job lifecycle
 
-Jobs are stored in Redis as JSON with key `job:{jobId}` and 24-hour TTL. States: `queued` → `running` → `succeeded` / `failed` / `canceled`. Progress updates (0-100%) are pushed to WebSocket subscribers. Asynq runs three queues: `render` (weight 6), `master` (weight 4) and `bakim` (weight 1, delayed account cleanup).
+Jobs are stored in Redis as JSON with key `job:{jobId}` and 24-hour TTL. States: `queued` → `running` → `succeeded` / `failed` / `canceled`. Progress updates (0-100%) are pushed to WebSocket subscribers. Asynq runs four queues: `render` (weight 6), `master` (weight 4), `sosyal` (weight 2, publication preparation) and `bakim` (weight 1, delayed account and publication file cleanup).
 Each job record carries `ownerId` (the `sub` of the user who started it). `status`, `result` and `cancel` load the job through `sahipliJobOku` (`internal/service/job_kayit.go`) and answer **404** — not 403 — when the caller is not the owner; records without `ownerId` are visible to nobody.
 
 ### API response conventions
@@ -105,11 +106,11 @@ Bearer token required on all `/api/*` routes.
 
 If `AUTH_ISSUER` is empty or the OpenID Provider fails to start, the service exits with a fatal error (fail-closed design).
 
-**Account deletion (`DELETE /api/account`):** `service.HesapService.Sil` validates the id, deletes the user via `kimlik.Sunucu.HesapSil` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`hesapisaret.Omur` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.KullaniciOnekleri`) and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.SilinmisKullaniciEngeli` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `hesapisaret.Denetci`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.
+**Account deletion (`DELETE /api/account`):** `service.HesapService.Sil` validates the id, deletes the user via `kimlik.Sunucu.HesapSil` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`hesapisaret.Omur` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.KullaniciOnekleri`), then the Keşfet data through the optional `SosyalTemizleyici` (every publication id, soft-deleted included, gets `sosyal/{id}/` deleted; the rows are deleted in one transaction only if all those prefixes succeeded, so the retry can list the ids again; other users' covers stay and show the source as deleted), and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.SilinmisKullaniciEngeli` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `hesapisaret.Denetci`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.
 
 Extracted claims (`userId`, `email`, `name`) are stored in Fiber context locals.
 
-**Ownership (Phase 3):** R2 keys are prefixed with the owner: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3`, `exports/<userId>/<exportId>.<ext>`, `previews/<userId>/<id>.mp3`. Keys are only built with `client.KullaniciAnahtari`, which rejects any client-supplied segment containing `/` or `..`. Every URL a client sends back (master stems/vocals, export inputs) is resolved with `client.SahipliAnahtarCoz`; a key outside the caller's prefix answers 404. `DELETE /api/upload/vocal/:takeId` requires `?projectId=&sectionId=`. `POST /api/files/sign` re-signs stored URLs (1-50, same order) after resolving each with `client.SahipliAnahtarCoz`; a single foreign key fails the whole request with 404 and nothing is signed.
+**Ownership (Phase 3):** R2 keys are prefixed with the owner: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3`, `exports/<userId>/<exportId>.<ext>`, `previews/<userId>/<id>.mp3`. Keys are only built with `client.KullaniciAnahtari`, which rejects any client-supplied segment containing `/` or `..`. Every URL a client sends back (master stems/vocals, export inputs) is resolved with `client.SahipliAnahtarCoz`; a key outside the caller's prefix answers 404. `DELETE /api/upload/vocal/:takeId` requires `?projectId=&sectionId=`. `POST /api/files/sign` re-signs stored URLs (1-50, same order) after resolving each with `client.SahipliAnahtarCoz`; a single foreign key fails the whole request with 404 and nothing is signed. Publication copies live under `sosyal/{yayinId}/` (`client.SosyalAnahtari`); they belong to a publication, not a user prefix, so `files/sign` never signs them and only `POST /api/social/yayinlar/{id}/dosyalar` hands out a signed listen URL after the visibility check. Publishing accepts `masterUrl` and stem `url` as a full URL or a bare key (`client.SahipliAnahtarVeyaURLCoz`); a foreign key is 403 `sahiplik`.
 
 **WebSocket:** `GET /ws/jobs/:jobId` requires `?ticket=` from `POST /api/ws/ticket` (body `{"jobId"}`, owner only). Tickets live 30 s in Redis (`wsbilet:<ticket>`) and are consumed with `GETDEL` before the upgrade.
 
````

- [ ] **Step 2: Check the text**

```bash
grep -n 'sosyal' CLAUDE.md | head -20
```

Expected: the new lines for `internal/sosyal/`, `sosyal*` handlers, `sosyal_worker.go`, the four queues, account deletion and `sosyal/{yayinId}/` keys. Whole Go suite after this task: about 673 passing tests (`go test ./internal/... -json`, counting subtests); Python: 36.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -F - <<'EOF'
docs: CLAUDE.md'ye Kesfet (sosyal) modulunu ekle

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

## Self-Review

**1. Spec coverage**

| Spec item | Task |
|---|---|
| §3 data model, all tables (incl. `begeni`, `takip`, `yorum`, `sikayet` for 5b–5d) | 1 (schema), N1, N2 |
| §3.1 files `sosyal/{yayinId}/…` | 2 (keys, copy), 7 (target keys), 8 (worker) |
| §4.1 common types | 4 (profile types), 6 (publication types), 9 (pages) |
| §4.2 `GET/PUT /profil/ben`, `uygun`, `GET /profiller/{ad}` | 5 |
| §4.2 `GET /profiller/{ad}/yayinlar` | 9 |
| §4.3 `POST /yayinlar`, `GET /yayinlar/{id}`, `GET /s/{slug}`, `tekrar-dene`, `PATCH`, `DELETE`, `dosyalar` | 6 (SQL), 7 (service, handlers) |
| §4.3 `sosyal:hazirla` (copy, 64 peaks, 30 s preview, `yayinda`/`hata`) | 2 (Go client), 3 (Python), 8 (worker) |
| §4.3 `GET /akis` (three tabs) and §5.1 scoring | 9 |
| §4.4 likes | 9 |
| §4.4 follow, suggestions; §4.5 comments; §4.6 covers list/start; §4.7 reports; §4.8 public preview | Plan B (5b–5d), tables already exist |
| §4.9 visibility | 4 (rule + predicate), 6 (SQL = Go test), 7 (detail), 9 (lists) |
| §5.2 "cover of a cover does not exist" | 7 (`kaynak_bulunamadi`, `stem_yok`) |
| §5.3 account deletion | 10 |
| Publish rate limit, PATCH in CORS, queue, wiring | 11 |
| Documentation | 12 |

**2. Placeholder scan.** Every step carries the exact file content or diff, the exact command and the expected outcome. No "TBD", "similar to", or "add validation" steps. The plan file was re-applied from scratch onto `main` with an extractor and produced a tree byte-identical to the tested scratch tree (see "Deployment and manual verification").

**3. Type consistency.** Names used across tasks were taken from the compiled code: `sosyal.NewServis(depo, kuyruk, depolama, r2)` (Task 7 changes Task 5's one-argument constructor and its test helper), `sosyal.HazirlikDeposu` (Task 6) consumed by `worker.NewSosyalWorker` (Task 8), `sosyal.GorevYuku`/`TaskTypeHazirla`/`TaskTypeDosyaSil`/`KuyrukSosyal` (Task 7) consumed by the worker and `main.go`, `service.SosyalTemizleyici` (Task 10) satisfied by `*sosyal.PostgresDepo` (checked by the compiler in `main.go`, Task 11), `handler.SosyalServisi` grown in Tasks 5, 7 and 9 and asserted against `*sosyal.Servis` with `var _ SosyalServisi = (*sosyal.Servis)(nil)`.

---

## Deployment and manual verification

**Order.** Deploy audio-service and core-service from the same `v*` tag (both CI workflows build on tags). If core-service runs before the new audio-service is live, `/analiz` answers 404, the worker logs it and publications go `yayinda` with an empty waveform and no preview; nothing breaks, but those publications keep an empty waveform. So roll audio-service first.

1. Tag and push (`git tag vX.Y.Z && git push origin vX.Y.Z`), wait for both images.
2. Portainer: bump the audio-service image, then `API_VERSION` for core-service. No new required env. Optional: `RATELIMIT_SOSYAL_YAYIN_PER_HOUR` (default 30).
3. Startup log must not contain `Sosyal migration basarisiz`. The migration is additive; rolling back to the previous image is safe (it ignores the `sosyal` schema).
4. Database check (same database as `AUTH_DB_URL`):
   ```sql
   SELECT version FROM sosyal.schema_migrations;          -- 001_sosyal.sql
   SELECT table_name FROM information_schema.tables WHERE table_schema = 'sosyal' ORDER BY 1;
   ```
5. API smoke test with a real access token (`T`) and a master produced by the app (`M` = its `fileUrl`):
   ```bash
   A=https://makesinger-api.celalettindemir.dev/api/social
   curl -s -H "Authorization: Bearer $T" $A/profil/ben                          # 404 profil_yok
   curl -s -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
     -d '{"kullaniciAdi":"deneme_1","gorunenAd":"Deneme","bio":""}' $A/profil/ben
   curl -s -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
     -d "{\"baslik\":\"Deneme\",\"aciklama\":\"\",\"gorunurluk\":\"link\",\"altyapiAcik\":false,\"yorumAcik\":true,\"masterUrl\":\"$M\",\"stemler\":[],\"sozler\":{\"bolumler\":[]},\"yapi\":[],\"tur\":\"pop\",\"bpm\":120,\"ton\":{\"tonik\":\"A\",\"mod\":\"minor\"},\"sureSn\":180,\"kaynakYayinId\":null}" \
     $A/yayinlar                                                                 # 202 {id, slug, durum}
   curl -s -H "Authorization: Bearer $T" $A/yayinlar/$ID                       # poll until "durum":"yayinda", 64 dalgaFormu values
   curl -s -X POST -H "Authorization: Bearer $T" $A/yayinlar/$ID/dosyalar        # signed master URL, playable
   curl -s -H "Authorization: Bearer $T" "$A/akis?sekme=yeni"                    # link publication must NOT appear
   curl -s -X PUT -H "Authorization: Bearer $T" $A/yayinlar/$ID/begeni           # {"begendimMi":true,"begeniSayisi":1}
   curl -s -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"gorunurluk":"herkes"}' $A/yayinlar/$ID
   curl -s -i -X DELETE -H "Authorization: Bearer $T" $A/yayinlar/$ID           # 204, a repeat is 404
   ```
6. R2 check: `sosyal/$ID/master.<ext>` and `sosyal/$ID/onizleme.mp3` exist in the private bucket after step 5's poll; the prefix disappears about 24 h after the DELETE (asynq `bakim` queue, `sosyal:dosya-sil`).
7. Account deletion: delete a test account that has publications; its `sosyal.*` rows disappear (or, if R2 failed, they disappear with the 1 h `hesap:temizle` retry) and `sosyal/{id}/` prefixes are gone; another user's cover of it now shows `kaynak.silindi: true`.
8. Mobile: switch the 5a social service from the fake to the real base URL and run the publish → poll → listen → like flow on a device.

**Verification done while writing this plan.** All code was implemented and tested in a scratch worktree from `main` (one commit per task); each commit built, vetted and linted clean (golangci-lint v2.14.0: 0 issues) and its tests passed. The full Go suite passed three times in a row against the live Postgres and Redis (673 passing tests, up from 550; 0 skipped), Python 36 passed, `ruff check` and `ruff format --check` clean. A server binary was started against the local Postgres and Redis: the sosyal migration ran, `GET /api/social/akis` answered 401 without a token, and the CORS preflight listed `PATCH`. Finally, every code block of this file was extracted and re-applied, task by task, onto a fresh worktree from `main`; the result was byte-identical to the scratch tree and the suites passed again.

---

## Follow-ups

- **Plan B (5b–5d):** comments (§4.5) and reports (§4.7) on the existing tables (reports fill `sikayet.yayin_id`, N1); follow/unfollow, suggestions and the Takip tab's empty state (§4.4); cover list and cover start (§4.6, copy `stem_listesi[].anahtar` to `stems/{coverOwner}/{uuid}/…`); public preview `GET /api/public/s/{slug}` with the IP limiter and `onizleme_anahtari` (§4.8).
- **Contract copy:** add `durum` (409), `sunucu_hatasi` (500), `stem_yok` details and the 202 body of `tekrar-dene` to `docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md` and the mobile spec, so the app can show "still preparing" on 409 `durum` from `dosyalar`.
- **Swagger:** the sosyal handlers carry no `@Router` annotations yet; add them and regenerate with `swag init -g cmd/server/main.go`.
- **Orphan sweep:** a crash between "copy finished" and "row deleted by account deletion" (or a hard delete of a row whose files were copied) can leave `sosyal/{id}/` objects without a row; a periodic sweep comparing `sosyal/` prefixes with `sosyal.yayin` would close it.
- **Soft-deleted rows** are kept forever (needed only for covers' live `kaynak` titles); a retention job could hard-delete them after the files are gone.
- **Feed performance:** `senin-icin` scores every `herkes` publication per page. Fine for launch volumes; add a 7-day window on `y.olusturma` or a materialised score when the table grows.
- **Health:** `/health` does not report the sosyal pool; add a `sosyal` flag with a ping if operations wants it.
- **Mobile-contract item, not backend:** the app treats any 404 on `DELETE /yayinlar/{id}` as success; keep that, since the backend answers 404 for a repeat.
