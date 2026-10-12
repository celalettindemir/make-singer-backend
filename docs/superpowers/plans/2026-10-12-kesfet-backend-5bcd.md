# Keşfet Backend 5b–5d Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the Keşfet backend for mobile phases 5b–5d: comments (§4.5), reports (§4.7), follow and suggestions (§4.4), the cover list and cover start (§4.6), and the unauthenticated web preview `GET /api/public/s/{slug}` with a per-IP limiter and a web-server key (§4.8). Account deletion (§5.3) is extended to match. A last task, in the `make-singer-frontend` repository, makes the song page send the web key.

**Architecture:** Everything builds on the 5a module `core-service/internal/sosyal` (schema `sosyal`, `PostgresDepo`, `Servis`) and its handlers. No migration is added: 5a created every table (`takip`, `yorum`, `sikayet` included). Each feature adds one repository file (`depo_<x>.go`, embedded in the `Depo` interface), one service file (`servis_<x>.go`) and one handler file (`handler/sosyal_<x>.go`); the `SosyalServisi` interface grows accordingly. The per-endpoint limiters move into a `handler.SosyalLimitleri` struct. `middleware.RateLimiter` gains `LimitKod` (a custom 429 code, used for `yavasla`) and `LimitIP` (per client IP from Traefik's `X-Real-Ip`, with a shared counter for requests that carry the configured `X-Web-Anahtari`). The public route is bound by `SosyalHandler.PublicRotalar` on `app.Group("/api/public")`, registered in `main.go` before the authenticated `/api` group.

**Tech Stack:** Go 1.25, Fiber v2, pgx v5, go-redis v9, AWS SDK v2 (S3 CopyObject on R2, through the 5a `client.Kopyalayici`); Next.js 15 + vitest 3 for the frontend task. No new dependencies.

**Spec:** `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md` (§4.4–4.9, §5.2, §5.3). The mobile copy wins over the backend copy (`docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md`), and the mobile app's parser (reproduced under Global Constraints) wins over spec text.

**Prerequisite:** the 5a plan, `docs/superpowers/plans/2026-10-12-kesfet-backend-5a.md`, applied exactly as written (all 12 tasks). Every **Modify** diff below was generated against that end state. If the 5a execution changed one of the touched 5a files (`handler/sosyal.go`, `handler/sosyal_test.go`, `handler/sosyal_yayin_test.go`, `sosyal/depo.go`, `sosyal/hatalar.go`, `sosyal/imlec.go`, `sosyal/servis.go`, `sosyal/sahte_depo_test.go`, `sosyal/depo_temizlik.go`, `sosyal/depo_temizlik_test.go`, `middleware/ratelimit.go`, `config/*`, `cmd/server/main.go`, `CLAUDE.md`, the contract copy), `git apply` reports the failing hunk; re-create that hunk by hand from the shown `+` lines.

## Global Constraints

**Process**
- Every code block in this plan was compiled and tested before the plan was written. Apply each block exactly. **Create** blocks are whole files; **Modify** blocks are unified diffs (`git apply` accepts them from the repository root).
- Module `github.com/makeasinger/api`; Go commands run in `core-service/`. Plain `testing`, hand-written fakes, Turkish test names and messages; no testify.
- After every Go task: `gofmt -l .` prints nothing, `go vet ./...` and `go build ./...` are clean, golangci-lint v2.14.0 (CI version, config `core-service/.golangci.yml`) reports `0 issues`.
- Task 10 runs in the sibling repository `make-singer-frontend`; it uses `npx vitest run` and `npx tsc --noEmit` and does not touch `package.json` or any lockfile.
- Commit messages are Turkish and end exactly with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
  ```

**Test database and Redis**
- Live Postgres DSN: `postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable`, overridable with `KIMLIK_TEST_DB_URL`. Redis: `localhost:6379`.
- Repository tests use the 5a helper `testHavuz(t)` (skip when Postgres is unreachable, `TRUNCATE` of `sosyal.*` only). The new middleware tests use the existing `canliRedis(t)` helper (Redis DB 14, skipped when Redis is down); kimlik keeps DB 15. No new test touches `public.*` or another package's Redis DB, so the 5a and kimlik tests are unaffected.
- Two checkouts must not run the suite at the same moment against the same database: both truncate `sosyal.*`, and both kimlik runs flush Redis DB 15. When another checkout is busy, point `KIMLIK_TEST_DB_URL` at a separate database (`CREATE DATABASE kesfet5bcd;` on the same server), as was done while writing this plan.

**HTTP**
- Base path `/api/social` (Bearer auth, deleted-user gate) as in 5a, plus the one unauthenticated route `GET /api/public/s/{slug}`.
- Every error body is `{"error":{"code","message"}}` via `response.Error`. New codes:

| Code | Status | When |
|---|---|---|
| `kendini_takip` | 422 | `PUT` or `DELETE /profiller/{ad}/takip` on your own name |
| `yorum_kapali` | 403 | `GET` or `POST /yayinlar/{id}/yorumlar` while `yorumAcik` is false (owner included) |
| `profil_yok` | 409 | `POST /yayinlar/{id}/yorumlar` without a profile (same code and status as publish) |
| `yetki` | 403 | `DELETE /yorumlar/{id}` by someone who is neither author nor publication owner but can see the publication |
| `yavasla` | 429 | comment rate limit (`sosyal-yorum`, default 10/min/user) |
| `altyapi_kapali` | 403 | `cover-baslat` on a closed backing, on a cover, or on a publication without stem copies |
| `durum` | 409 | `cover-baslat` on the owner's publication that is not `yayinda` yet |
| `bulunamadi` | 404 | as in 5a, plus: unknown or hidden preview slug, report target not visible, comment missing or deleted |
| `RATE_LIMITED` | 429 | `cover-baslat` (`sosyal-cover`, default 20/hour/user); public preview (default 30/min/IP, 600/min shared for the web key) |

**JSON contract (what the mobile Zod schemas and the web page parse)**
- `Yorum {id, yazar: ProfilOzeti, metin, zamanMs: int >= 0 | null (key always present), olusturma: RFC3339 UTC, silebilirMiyim}`. `POST /yayinlar/{id}/yorumlar` body `{metin, zamanMs: number|null}` → 201 `Yorum`. `DELETE /yorumlar/{id}` → 204.
- Comment pages use the 5a envelope `{ogeler, sonrakiImlec}`; order: timed comments by `zamanMs` ascending, then untimed ones by `olusturma` descending.
- `PUT|DELETE /profiller/{ad}/takip` → `{takipci, takipEdiyorMuyum}`. `GET /oneriler` → `ProfilOzeti[]` (at most 10, `[]` when empty, never null).
- `POST /sikayetler` body `{hedefTuru: 'yayin'|'yorum', hedefId, neden: spam|taciz|telif|uygunsuz|diger, aciklama?}` → 201 with an empty body, also on a repeat.
- `GET /yayinlar/{id}/coverlar` → page of `YayinOzeti` (empty page when the backing is closed). `POST /yayinlar/{id}/cover-baslat` → `CoverBaslangici {kaynak: KaynakOzeti, tur, bpm, ton, sureSn, yapi, sozler, stemler: [{id, enstruman, fileUrl, sure}]}`.
- `GET /api/public/s/{slug}` → `{baslik, sahip: ProfilOzeti, sureSn, dalgaFormu: number[] (never null), onizlemeUrl: string|null (key always present), tur}` with `Cache-Control: no-store`.

**Limits and configuration**

| Limit | Redis key | Config key / env | Default |
|---|---|---|---|
| comment write, per user | `ratelimit:sosyal-yorum:<userId>` | `ratelimit.sosyal_yorum_per_min` / `RATELIMIT_SOSYAL_YORUM_PER_MIN` | 10 per minute, 429 `yavasla` |
| cover start, per user | `ratelimit:sosyal-cover:<userId>` | `ratelimit.sosyal_cover_per_hour` / `RATELIMIT_SOSYAL_COVER_PER_HOUR` | 20 per hour |
| public preview, per IP | `ratelimit:public:ip:<ip>` | `ratelimit.public_per_min` / `RATELIMIT_PUBLIC_PER_MIN` | 30 per minute |
| public preview, web key | `ratelimit:public:web` (shared) | `ratelimit.public_web_per_min` / `RATELIMIT_PUBLIC_WEB_PER_MIN` | 600 per minute |
| web key | — | `sosyal.web_anahtari` / `SOSYAL_WEB_ANAHTARI` (or `SOSYAL_WEB_ANAHTARI_FILE`) | empty = bypass disabled |

---

## Rulings

These are binding for every task. The 5a rulings R1–R10 and N1–N16 still apply. C1–C5 reproduce the controller rulings (`backend-rulings.md`, 2026-10-12) and the task brief for Plan B. P1–P15 are **NEW rulings made while writing and testing the code**; they are flagged so a reviewer can challenge them.

### Controller rulings that apply to 5b–5d

**C1. Scope (Plan B).** §4.5 comments, §4.7 reports, §4.4 follow and suggestions, §4.6 cover list and cover start, §4.8 public preview with the IP limiter and the web key, and §5.3 consistency. The tables exist since 5a; `sikayet.yayin_id` (N1) is filled by reports; cover start copies from `stem_listesi[].anahtar` (N2).

**C2. Comments.** Timed comments by `zaman_ms` ascending, then untimed by `olusturma` descending, with one cursor that crosses both groups. Only the author or the publication owner deletes; a soft delete drops the comment from lists and from `yorumSayisi`. Limit `sosyal-yorum` 10/min/user answering 429 **`yavasla`** (not `RATE_LIMITED`). 409 `profil_yok` without a profile; 403 `yorum_kapali` on GET and POST.

**C3. Follow, suggestions, reports.** 422 `kendini_takip`; follow responses `{takipci, takipEdiyorMuyum}`; suggestions are up to 10 `ProfilOzeti` of owners the viewer does not follow, excluding the viewer, ranked by likes received in the last 30 days. A repeat report is 201.

**C4. Covers (B3).** Cover start copies the publication's stems server-side to `stems/{caller}/{newUUID}/{stemId}.{ext}` with `client.KullaniciAnahtari` and the 5a `client.Kopyalayici`, and returns `fileUrl` from `client.ResolveURL`, so `/api/files/sign` can re-sign them. 403 `altyapi_kapali` for a closed backing or a cover; 404 when not visible. Per-user limit `sosyal-cover`, 20/hour, configurable. The cover list is empty when the backing is closed.

**C5. Public preview (B11).** `GET /api/public/s/{slug}` is registered before the `/api` auth group. Only `herkes`/`link` + `yayinda` + not deleted; everything else 404 `bulunamadi` as JSON. `onizlemeUrl` is a 1 h signed URL for `sosyal/{id}/onizleme.mp3`, or null. Per-IP limit 30/min with `middleware.LimitIP`, key `ratelimit:<prefix>:ip:<ip>`, IP from `X-Real-Ip` with `c.IP()` as fallback, Fiber's global `ProxyHeader` untouched. A request whose `X-Web-Anahtari` equals `SOSYAL_WEB_ANAHTARI` (constant-time compare; empty config disables it) is counted under the shared key `web` at 600/min. Both limits configurable.

### NEW rulings made while coding (challenge these first)

**P1 (NEW). Route limiters as a struct.** 5a's `Rotalar(r, yayinLimiti)` becomes `Rotalar(r, SosyalLimitleri{Yayin, Yorum, Cover})`. Every field is required (no nil fallback, so a forgotten limiter fails loudly at start-up). The two 5a test call sites are updated.

**P2 (NEW). `LimitKod` and what it counts.** `RateLimiter.LimitKod(prefix, max, window, kod, mesaj)` is `Limit` with a custom 429 body; the comment limiter uses `kod = "yavasla"`, `mesaj = "Biraz yavasla"`. Like every limiter it runs before the handler, so invalid comment attempts count too. `Limit`'s behaviour (401 without user, 503 fail-closed, `Retry-After`) is unchanged; the counting core moved into `say` and `Limit` now delegates.

**P3 (NEW). `LimitIP` details.** `X-Real-Ip` is used only if `net.ParseIP` accepts it (normalised with `ip.String()`); anything else falls back to `c.IP()`, so a garbage header cannot mint keys. A wrong or empty web key is not an error: the request is simply counted per IP. Both values are hashed with SHA-256 before `subtle.ConstantTimeCompare`, so the key length does not leak either. Redis failure is 503 fail-closed, as for `Limit`. The prefix is `public`.

**P4 (NEW). Configuration shape.** New fields `RateLimit.SosyalYorumPerMin`, `SosyalCoverPerHour`, `PublicPerMin`, `PublicWebPerMin` and a new `Config.Sosyal.WebAnahtari`, read through `readSecret("SOSYAL_WEB_ANAHTARI")` (so `_FILE` works). The backend `docker-stack.yml` passes `SOSYAL_WEB_ANAHTARI: ${SOSYAL_WEB_ANAHTARI:-}` as a plain stack variable (like `AUTH_SIGNING_KEY`) rather than a swarm secret, so a deploy without the value still starts (bypass off).

**P5 (NEW). Follow.** No profile is needed to follow (as for likes, N11). `DELETE` on yourself is also 422 `kendini_takip`. A malformed or unknown name is 404 `bulunamadi`. The response is re-read from the database after the write.

**P6 (NEW). Suggestion ranking.** A candidate must own at least one `yayinda`, non-deleted publication with visibility `herkes` or `takipciler` (a `link`-only owner would leak that the account publishes). Score = likes from other users in the last 30 days (database `now()`) on those publications; the owner's own likes and likes on `link` publications do not count. Ties: the owner with the newest such publication first, then `kullanici_id`. Zero-like publishers are still suggested (after the liked ones) so the Takip tab's empty state is never empty while anyone publishes.

**P7 (NEW). Comment checks and bounds.** Order: body (422) → visibility (404) → `yorumAcik` (403, owner included, GET and POST) → profile (409) → `zamanMs` range (422). `metin` is trimmed, 1–500 runes. `zamanMs` is `null` or `0 ≤ zamanMs ≤ sureSn × 1000`; a fractional `zamanMs` fails body parsing (422). Comments are allowed on any publication the writer can see (the owner's own `hazirlaniyor` one included).

**P8 (NEW). Comment order tie-breaks and cursor.** Timed: `(zaman_ms, olusturma, id)` ascending; untimed: `(olusturma, id)` descending. Cursor = base64url JSON `{g, z?, t, id}`: `g = 0` (timed, `z ≥ 0` required) or `g = 1` (untimed, `z` forbidden); anything else is 422 `dogrulama`. A `g = 0` cursor returns the remaining timed comments followed by all untimed ones.

**P9 (NEW). Comment deletion.** Author or publication owner only. Anyone else gets 403 **`yetki`** (a new code) when they can see the publication and 404 when they cannot, so hidden content is not revealed. A missing or already deleted comment is 404 (a repeat delete is 404). The author can delete their comment even if the publication has since become invisible to them.

**P10 (NEW). Reports.** The target must be visible to the reporter: a publication by the detail rule; a comment must be undeleted and on a visible publication. Otherwise, and for a malformed `hedefId`, 404 `bulunamadi`. Enum and length errors are 422 (`aciklama` trimmed, ≤ 300 runes). No profile is needed; reporting your own content is allowed. The response is 201 with an empty body; a repeat keeps the first row unchanged (its `neden` is not updated).

**P11 (NEW). Cover list visibility.** The original must be visible (else 404). A closed backing (a cover always has one) gives an empty page without querying. Listed covers must be `yayinda` and pass the list rule (`gorunurlukKosulu`, link excluded), so unlike N5 the owner does not see their own `hazirlaniyor`/`hata` covers in this strip. Newest first, 5a time cursor.

**P12 (NEW). Cover start details.** 403 `altyapi_kapali` also when `stem_listesi` is empty. 409 `durum` for the owner's publication that is not `yayinda`. Every call copies into a fresh uuid folder (no de-duplication; the 20/hour limit bounds it). A copy failure part-way returns 500; the copies already made stay under the caller's `stems/` prefix and are removed only by account deletion (`KullaniciOnekleri`). No profile is needed. R2 not configured → copies are skipped and URLs are still built (dev). The copier is attached with `Servis.KopyalayiciAyarla` instead of a new `NewServis` argument, so the 5a test call sites stay untouched.

**P13 (NEW). Public preview details.** The rule is one SQL predicate (`onizlemeKosulu`), tested on all six visibility/state variants. The URL is signed with `GetSignedURL(key, 1h)`, independent of `R2_PRESIGN_TTL`; without R2 it is the unsigned URL (dev). The response carries `Cache-Control: no-store`. An invalid slug is 404 without a query. CORS `AllowHeaders` is unchanged: the web key is a server-to-server header and must never come from a browser.

**P14 (NEW). Account deletion and reports.** Besides 5a's list, the transaction deletes reports whose target is one of the user's comments (soft-deleted ones included). They belong to other users and live on other users' publications, so neither `bildiren_id` nor the `yayin_id` cascade removes them, and they would point at nothing.

**P15 (NEW). Web page header.** The frontend sends `X-Web-Anahtari` only when the trimmed server-only env `SOSYAL_WEB_ANAHTARI` is non-empty. The env is read per call (a default parameter), so tests can stub it; a `webKey` option allows injection like `fetchImpl`.

---

## File Structure

| File | Responsibility |
|---|---|
| `core-service/internal/middleware/ratelimit.go` | `say` (counting core), `LimitKod`, `LimitIP`, `istemciIP`, `WebAnahtariBasligi` |
| `core-service/internal/config/config.go`, `config.yaml`, `.env.example` | Four new limits, `SosyalConfig.WebAnahtari` |
| `core-service/internal/sosyal/depo_takip.go`, `servis_takip.go` | Follow, follower count, suggestions |
| `core-service/internal/sosyal/model_yorum.go`, `depo_yorum.go`, `servis_yorum.go` | Comment types, SQL (two-group order), rules |
| `core-service/internal/sosyal/imlec.go` | `YorumImleci` added next to the 5a cursors |
| `core-service/internal/sosyal/depo_sikayet.go`, `servis_sikayet.go` | Reports |
| `core-service/internal/sosyal/depo_cover.go`, `servis_cover.go` | Cover list, stem list, cover start and its key |
| `core-service/internal/sosyal/depo_onizleme.go`, `servis_onizleme.go` | Public preview query, signed URL |
| `core-service/internal/sosyal/depo_temizlik.go` | Account deletion also removes reports on the user's comments |
| `core-service/internal/sosyal/hatalar.go`, `depo.go`, `servis.go` | New errors; `Depo` embeds the new interfaces; `kopyalayici` field |
| `core-service/internal/handler/sosyal.go` | `SosyalServisi` grows, `SosyalLimitleri`, routes, error codes |
| `core-service/internal/handler/sosyal_takip.go`, `sosyal_yorum.go`, `sosyal_sikayet.go`, `sosyal_cover.go`, `sosyal_onizleme.go` | Endpoint handlers; `PublicRotalar` |
| `core-service/cmd/server/main.go` | Limiters, copier, public route before `/api` |
| `CLAUDE.md`, `docker-stack.yml`, `docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md` | Documentation, stack env, contract copy |
| `make-singer-frontend/src/lib/songPreview.ts`, `docker-stack.yml`, `README.md` | Web key header (different repository) |

Test files sit next to the code (`*_test.go`, `songPreview.test.ts`). `sosyal/sahte_depo_test.go` (fake repository) and `handler/sosyal_test.go` (fake service, request helpers) grow in every feature task.

---

## Tasks

### Task 1: Rate limiter: custom 429 code and per-IP limit with the web key

The two middleware pieces Plan B needs (C2, C5, P2, P3). The existing counting body moves unchanged into `say`; `Limit`, `LimitKod` and `LimitIP` only choose the key, the maximum and the 429 body. The existing `ratelimit_test.go` and `ratelimit_canli_test.go` stay untouched and keep passing.

**Files:**

- Create: `core-service/internal/middleware/ratelimit_ip_test.go`
- Modify: `core-service/internal/middleware/ratelimit.go`

**Interfaces:**
- Consumes: `RateLimiter`, `GetUserID`, `response.RateLimited`, `response.Error`, `canliRedis` (existing).
- Produces: `(*RateLimiter).LimitKod(keyPrefix string, maxRequests int, window time.Duration, kod, mesaj string) fiber.Handler`; `(*RateLimiter).LimitIP(keyPrefix string, maxRequests int, window time.Duration, webAnahtari string, webMax int) fiber.Handler`; `const middleware.WebAnahtariBasligi = "X-Web-Anahtari"`; unexported `say`, `kullaniciBasina`, `istemciIP`.

- [ ] **Step 1: Write the failing test**

The live tests reuse `canliRedis(t)` from `ratelimit_canli_test.go` (Redis DB 14, skipped when Redis is down). `app.Test` connections come from `0.0.0.0`, which is what `c.IP()` returns when `X-Real-Ip` is missing or invalid.

**Create** `core-service/internal/middleware/ratelimit_ip_test.go`:

````go
package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

const testWebAnahtari = "web-anahtari-en-az-otuz-iki-karakter-uzun"

// ipUygulamasi, kimliksiz bir uca LimitIP baglar.
func ipUygulamasi(rdb *redis.Client, ipBasina int, webAnahtari string, webMax int) *fiber.App {
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb).LimitIP("onizleme", ipBasina, time.Minute, webAnahtari, webMax),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	return app
}

// ipIstek, istegi verilen basliklarla atar; durumu ve hata kodunu doner.
func ipIstek(t *testing.T, app *fiber.App, basliklar map[string]string) (int, string) {
	t.Helper()
	istek := httptest.NewRequest("GET", "/uc", nil)
	for k, v := range basliklar {
		istek.Header.Set(k, v)
	}
	y, err := app.Test(istek, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	ham, _ := io.ReadAll(y.Body)
	var govde struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(ham, &govde)
	return y.StatusCode, govde.Error.Code
}

func TestLimitIP_RedisYoksaFailClosed(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	durum, _ := ipIstek(t, ipUygulamasi(rdb, 5, "", 0), nil)
	if durum != fiber.StatusServiceUnavailable {
		t.Fatalf("durum = %d, beklenen 503 (kimliksiz uc 401 vermemeli, Redis yoksa gecirmemeli)", durum)
	}
}

func TestCanliLimitIP_IPBasinaSayar(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	app := ipUygulamasi(rdb, 2, "", 0)
	a := map[string]string{"X-Real-Ip": "203.0.113.5"}

	for i := 1; i <= 2; i++ {
		if durum, _ := ipIstek(t, app, a); durum != fiber.StatusOK {
			t.Fatalf("istek %d: durum = %d, beklenen 200", i, durum)
		}
	}
	if durum, kod := ipIstek(t, app, a); durum != fiber.StatusTooManyRequests || kod != "RATE_LIMITED" {
		t.Fatalf("3. istek: durum = %d kod = %q, beklenen 429 RATE_LIMITED", durum, kod)
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "203.0.113.6"}); durum != fiber.StatusOK {
		t.Fatalf("baska IP etkilenmemeli: durum = %d", durum)
	}
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:ip:203.0.113.5").Int(); err != nil || n != 3 {
		t.Fatalf("IP anahtari = %d (hata %v), beklenen 3", n, err)
	}
	if ttl, err := rdb.TTL(ctx, "ratelimit:onizleme:ip:203.0.113.5").Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("TTL = %v (hata %v)", ttl, err)
	}
}

func TestCanliLimitIP_GecersizBaslikBaglantiAdresineDuser(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	app := ipUygulamasi(rdb, 5, "", 0)
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "1.2.3.4:80 kotu"}); durum != fiber.StatusOK {
		t.Fatalf("durum = %d", durum)
	}
	if durum, _ := ipIstek(t, app, nil); durum != fiber.StatusOK {
		t.Fatalf("durum = %d", durum)
	}
	// app.Test'in baglanti adresi 0.0.0.0'dir; iki istek de onda sayilir.
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:ip:0.0.0.0").Int(); err != nil || n != 2 {
		t.Fatalf("baglanti adresi anahtari = %d (hata %v), beklenen 2", n, err)
	}
	if anahtarlar, _ := rdb.Keys(ctx, "ratelimit:onizleme:ip:1.2.3.4*").Result(); len(anahtarlar) != 0 {
		t.Fatalf("gecersiz baslik anahtar olmamali: %v", anahtarlar)
	}
}

func TestCanliLimitIP_WebAnahtariOrtakSinir(t *testing.T) {
	rdb := canliRedis(t)
	ctx := context.Background()
	app := ipUygulamasi(rdb, 1, testWebAnahtari, 3)

	// Dogru anahtar: IP'den bagimsiz, ortak "web" sayacinda 3 hak.
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.1"} {
		durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": ip, WebAnahtariBasligi: testWebAnahtari})
		if durum != fiber.StatusOK {
			t.Fatalf("web istegi %d: durum = %d, beklenen 200", i+1, durum)
		}
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "198.51.100.3", WebAnahtariBasligi: testWebAnahtari}); durum != fiber.StatusTooManyRequests {
		t.Fatalf("web siniri asilinca 429 beklenir: %d", durum)
	}
	if n, err := rdb.Get(ctx, "ratelimit:onizleme:web").Int(); err != nil || n != 4 {
		t.Fatalf("web anahtari = %d (hata %v), beklenen 4", n, err)
	}
	if anahtarlar, _ := rdb.Keys(ctx, "ratelimit:onizleme:ip:*").Result(); len(anahtarlar) != 0 {
		t.Fatalf("dogru web anahtari IP sayacina dokunmamali: %v", anahtarlar)
	}

	// Yanlis anahtar: olagan IP siniri (1).
	yanlis := map[string]string{"X-Real-Ip": "198.51.100.9", WebAnahtariBasligi: testWebAnahtari + "x"}
	if durum, _ := ipIstek(t, app, yanlis); durum != fiber.StatusOK {
		t.Fatalf("yanlis anahtar ilk istek: %d", durum)
	}
	if durum, _ := ipIstek(t, app, yanlis); durum != fiber.StatusTooManyRequests {
		t.Fatalf("yanlis anahtar IP sinirina takilmali: %d", durum)
	}
}

func TestCanliLimitIP_BosYapilandirmaWebYolunuKapatir(t *testing.T) {
	rdb := canliRedis(t)
	app := ipUygulamasi(rdb, 1, "", 100)
	// Yapilandirma bosken bos baslik da, herhangi bir baslik da IP sinirindadir.
	for _, anahtar := range []string{"", "herhangi"} {
		b := map[string]string{"X-Real-Ip": "192.0.2.10", WebAnahtariBasligi: anahtar}
		_, _ = ipIstek(t, app, b)
	}
	if durum, _ := ipIstek(t, app, map[string]string{"X-Real-Ip": "192.0.2.10", WebAnahtariBasligi: ""}); durum != fiber.StatusTooManyRequests {
		t.Fatalf("durum = %d, beklenen 429", durum)
	}
	if n, _ := rdb.Exists(context.Background(), "ratelimit:onizleme:web").Result(); n != 0 {
		t.Fatal("web anahtari bosken web sayaci olusmamali")
	}
}

func TestCanliLimitKod_OzelKodla429(t *testing.T) {
	rdb := canliRedis(t)
	app := fiber.New()
	app.Get("/uc", func(c *fiber.Ctx) error {
		c.Locals("userId", "kullanici-yorum")
		return c.Next()
	}, NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", "Biraz yavasla"),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })

	if durum, _ := ipIstek(t, app, nil); durum != fiber.StatusOK {
		t.Fatalf("ilk istek: %d", durum)
	}
	if durum, kod := ipIstek(t, app, nil); durum != fiber.StatusTooManyRequests || kod != "yavasla" {
		t.Fatalf("durum = %d kod = %q, beklenen 429 yavasla", durum, kod)
	}
	if n, err := rdb.Get(context.Background(), "ratelimit:yorum:kullanici-yorum").Int(); err != nil || n != 2 {
		t.Fatalf("kullanici anahtari = %d (hata %v)", n, err)
	}
}

func TestLimitKod_KimliksizIstek401(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	app := fiber.New()
	app.Get("/uc", NewRateLimiter(rdb).LimitKod("yorum", 1, time.Minute, "yavasla", "x"),
		func(c *fiber.Ctx) error { return c.SendString("tamam") })
	if durum, _ := ipIstek(t, app, nil); durum != fiber.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", durum)
	}
}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/middleware/ -count=1
```

Expected: FAIL, build errors `NewRateLimiter(rdb).LimitIP undefined`, `undefined: WebAnahtariBasligi`, `NewRateLimiter(rdb).LimitKod undefined`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/middleware/ratelimit.go`:

````diff
diff --git a/core-service/internal/middleware/ratelimit.go b/core-service/internal/middleware/ratelimit.go
--- a/core-service/internal/middleware/ratelimit.go
+++ b/core-service/internal/middleware/ratelimit.go
@@ -2,8 +2,12 @@ package middleware
 
 import (
 	"context"
+	"crypto/sha256"
+	"crypto/subtle"
 	"errors"
 	"fmt"
+	"net"
+	"strings"
 	"time"
 
 	"github.com/gofiber/fiber/v2"
@@ -21,6 +25,19 @@ func NewRateLimiter(redisClient *redis.Client) *RateLimiter {
 
 // Limit creates a rate limiting middleware
 func (rl *RateLimiter) Limit(keyPrefix string, maxRequests int, window time.Duration) fiber.Handler {
+	return rl.kullaniciBasina(keyPrefix, maxRequests, window, response.RateLimited)
+}
+
+// LimitKod, Limit'in asimda genel RATE_LIMITED yerine verilen kodla 429
+// donen bicimidir. Sozlesmesi kendi kodunu isteyen uclar icindir (orn.
+// Kesfet yorumlari: 429 yavasla).
+func (rl *RateLimiter) LimitKod(keyPrefix string, maxRequests int, window time.Duration, kod, mesaj string) fiber.Handler {
+	return rl.kullaniciBasina(keyPrefix, maxRequests, window, func(c *fiber.Ctx) error {
+		return response.Error(c, fiber.StatusTooManyRequests, kod, mesaj, nil)
+	})
+}
+
+func (rl *RateLimiter) kullaniciBasina(keyPrefix string, maxRequests int, window time.Duration, asim fiber.Handler) fiber.Handler {
 	return func(c *fiber.Ctx) error {
 		userID := GetUserID(c)
 		if userID == "" {
@@ -29,71 +46,117 @@ func (rl *RateLimiter) Limit(keyPrefix string, maxRequests int, window time.Dura
 			// c.Next()), bu da kota bypass'i demekti.
 			return response.Unauthorized(c, "Kimlik dogrulanamadi")
 		}
+		return rl.say(c, fmt.Sprintf("ratelimit:%s:%s", keyPrefix, userID), maxRequests, window, asim)
+	}
+}
 
-		key := fmt.Sprintf("ratelimit:%s:%s", keyPrefix, userID)
-		ctx := context.Background()
-
-		// Sayaci artirma ve TTL atama TEK islemde (MULTI/EXEC) yapilir.
-		// Eskiden Incr basarili olup Expire hata verdiginde hata
-		// yutuluyordu: anahtar TTL'siz kaliyor, sonraki her istekte
-		// count > maxRequests oluyor ve TTL -1 dondugu icin
-		// Retry-After: -1 ile kullanici KALICI kilitleniyordu.
-		// SET key 0 EX window NX + INCR sirasi, anahtari yalnizca yokken
-		// olusturur ve TTL'i olusturma aninda kenetler.
-		boru := rl.redis.TxPipeline()
-		boru.SetArgs(ctx, key, 0, redis.SetArgs{Mode: "NX", TTL: window})
-		artir := boru.Incr(ctx, key)
-		// redis.Nil = SET NX uygulanmadi (anahtar zaten vardi); bu
-		// normal akistir, hata degil.
-		if _, err := boru.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
-			// Fail-closed: sayaci artiramadiysak kotanin asilip
-			// asilmadigini BILEMEYIZ. Eskiden istek serbest gecirilirdi;
-			// bu, Redis'i dusurebilen birine sinirsiz kota veriyordu.
-			// Hata metni loglanmaz (baglanti dizgesi sir tasiyabilir).
-			c.Set("Retry-After", "5")
-			return response.Error(c, fiber.StatusServiceUnavailable,
-				response.CodeServiceError, "Rate limit denetlenemedi", nil)
-		}
-		count, err := artir.Result()
-		if err != nil {
-			c.Set("Retry-After", "5")
-			return response.Error(c, fiber.StatusServiceUnavailable,
-				response.CodeServiceError, "Rate limit denetlenemedi", nil)
-		}
-
-		if count > int64(maxRequests) {
-			// Retry-After en az 1 saniye: TTL okunamazsa veya anahtar
-			// bir sekilde TTL'siz kaldiysa (-1) negatif/sifir deger
-			// donmemeli.
-			bekle := window
-			ttl, ttlErr := rl.redis.TTL(ctx, key).Result()
-			switch {
-			case ttlErr == nil && ttl > 0:
-				bekle = ttl
-			case ttlErr == nil && ttl < 0:
-				// MIRAS ANAHTAR IYILESTIRMESI: TTL -1 demek anahtarin
-				// suresi yok. Yeni kod boyle bir anahtar URETEMEZ
-				// (MULTI/EXEC atomik), ama eski kodun uretimde biraktigi
-				// anahtarlar olabilir: SET NX uygulanmadigi icin TTL -1
-				// kalir, sayac sonsuza buyur ve kullanici KALICI kilitli
-				// kalir — tek cikis manuel DEL. Burada pencereyi geri
-				// veriyoruz ki anahtar kendiliginden sifirlansin.
-				rl.redis.Expire(ctx, key, window)
-			}
-			saniye := int(bekle.Seconds())
-			if saniye < 1 {
-				saniye = 1
+// WebAnahtariBasligi, guvenilen web sunucusunun (make-singer-frontend)
+// IP basina siniri atlamak icin gonderdigi basliktir. Web sunucusu tum
+// ziyaretcilerin isteklerini TEK IP'den yapar; bu baslik olmadan o IP
+// dakikada birkac ziyaretciden sonra 429 alirdi.
+const WebAnahtariBasligi = "X-Web-Anahtari"
+
+// LimitIP, kimliksiz uclar icin istemci IP'si basina sabit pencereli
+// sinirdir; anahtar ratelimit:<onek>:ip:<ip>. webAnahtari doluysa ve
+// istek X-Web-Anahtari basligiyla ayni degeri tasiyorsa istek IP yerine
+// ortak ratelimit:<onek>:web anahtarinda webMax ile sayilir. Yanlis ya da
+// bos anahtar 401 degil, olagan IP siniridir. Karsilastirma sabit
+// zamanlidir (once SHA-256, boy da sizmaz). webAnahtari bossa web yolu
+// tamamen kapalidir.
+func (rl *RateLimiter) LimitIP(keyPrefix string, maxRequests int, window time.Duration, webAnahtari string, webMax int) fiber.Handler {
+	webOzeti := sha256.Sum256([]byte(webAnahtari))
+	return func(c *fiber.Ctx) error {
+		if webAnahtari != "" {
+			if gelen := c.Get(WebAnahtariBasligi); gelen != "" {
+				gelenOzet := sha256.Sum256([]byte(gelen))
+				if subtle.ConstantTimeCompare(gelenOzet[:], webOzeti[:]) == 1 {
+					return rl.say(c, fmt.Sprintf("ratelimit:%s:web", keyPrefix), webMax, window, response.RateLimited)
+				}
 			}
-			c.Set("Retry-After", fmt.Sprintf("%d", saniye))
-			return response.RateLimited(c)
 		}
+		return rl.say(c, fmt.Sprintf("ratelimit:%s:ip:%s", keyPrefix, istemciIP(c)), maxRequests, window, response.RateLimited)
+	}
+}
 
-		// Add rate limit headers
-		c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", maxRequests))
-		c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", maxRequests-int(count)))
+// istemciIP, Traefik'in yazdigi X-Real-Ip basligini kullanir; Traefik
+// istemcinin gonderdigi X-Real-Ip'yi (guvenilmeyen kaynaktan) siler ve
+// kendi gordugu adresi yazar. Baslik yoksa ya da gecerli bir IP degilse
+// baglantinin adresi (c.IP()) kullanilir. Fiber'in genel ProxyHeader
+// ayari BILEREK degistirilmez: diger uclar kullanici basina sayar.
+// Not: API Traefik'i atlayarak dogrudan erisilebilir olursa bu baslik
+// uydurulabilir; bu yuzden API portu disari acilmamalidir.
+func istemciIP(c *fiber.Ctx) string {
+	if ip := net.ParseIP(strings.TrimSpace(c.Get("X-Real-Ip"))); ip != nil {
+		return ip.String()
+	}
+	return c.IP()
+}
 
-		return c.Next()
+// say, key sayacini pencere icinde artirir; maxRequests asilinca asim
+// yanitini doner, aksi halde zinciri surdurur.
+func (rl *RateLimiter) say(c *fiber.Ctx, key string, maxRequests int, window time.Duration, asim fiber.Handler) error {
+	ctx := context.Background()
+
+	// Sayaci artirma ve TTL atama TEK islemde (MULTI/EXEC) yapilir.
+	// Eskiden Incr basarili olup Expire hata verdiginde hata
+	// yutuluyordu: anahtar TTL'siz kaliyor, sonraki her istekte
+	// count > maxRequests oluyor ve TTL -1 dondugu icin
+	// Retry-After: -1 ile kullanici KALICI kilitleniyordu.
+	// SET key 0 EX window NX + INCR sirasi, anahtari yalnizca yokken
+	// olusturur ve TTL'i olusturma aninda kenetler.
+	boru := rl.redis.TxPipeline()
+	boru.SetArgs(ctx, key, 0, redis.SetArgs{Mode: "NX", TTL: window})
+	artir := boru.Incr(ctx, key)
+	// redis.Nil = SET NX uygulanmadi (anahtar zaten vardi); bu
+	// normal akistir, hata degil.
+	if _, err := boru.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
+		// Fail-closed: sayaci artiramadiysak kotanin asilip
+		// asilmadigini BILEMEYIZ. Eskiden istek serbest gecirilirdi;
+		// bu, Redis'i dusurebilen birine sinirsiz kota veriyordu.
+		// Hata metni loglanmaz (baglanti dizgesi sir tasiyabilir).
+		c.Set("Retry-After", "5")
+		return response.Error(c, fiber.StatusServiceUnavailable,
+			response.CodeServiceError, "Rate limit denetlenemedi", nil)
+	}
+	count, err := artir.Result()
+	if err != nil {
+		c.Set("Retry-After", "5")
+		return response.Error(c, fiber.StatusServiceUnavailable,
+			response.CodeServiceError, "Rate limit denetlenemedi", nil)
 	}
+
+	if count > int64(maxRequests) {
+		// Retry-After en az 1 saniye: TTL okunamazsa veya anahtar
+		// bir sekilde TTL'siz kaldiysa (-1) negatif/sifir deger
+		// donmemeli.
+		bekle := window
+		ttl, ttlErr := rl.redis.TTL(ctx, key).Result()
+		switch {
+		case ttlErr == nil && ttl > 0:
+			bekle = ttl
+		case ttlErr == nil && ttl < 0:
+			// MIRAS ANAHTAR IYILESTIRMESI: TTL -1 demek anahtarin
+			// suresi yok. Yeni kod boyle bir anahtar URETEMEZ
+			// (MULTI/EXEC atomik), ama eski kodun uretimde biraktigi
+			// anahtarlar olabilir: SET NX uygulanmadigi icin TTL -1
+			// kalir, sayac sonsuza buyur ve kullanici KALICI kilitli
+			// kalir — tek cikis manuel DEL. Burada pencereyi geri
+			// veriyoruz ki anahtar kendiliginden sifirlansin.
+			rl.redis.Expire(ctx, key, window)
+		}
+		saniye := int(bekle.Seconds())
+		if saniye < 1 {
+			saniye = 1
+		}
+		c.Set("Retry-After", fmt.Sprintf("%d", saniye))
+		return asim(c)
+	}
+
+	// Add rate limit headers
+	c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", maxRequests))
+	c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", maxRequests-int(count)))
+
+	return c.Next()
 }
 
 // LyricsLimit returns a rate limiter for lyrics endpoints (30 req/min)
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/middleware/ -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)' && gofmt -l . && go vet ./... && golangci-lint run ./...
```

Expected: the 7 new tests and all existing middleware tests PASS (none SKIP while Redis is up), no gofmt output, `0 issues.`. Whole Go suite after this task: 680 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/middleware/ratelimit_ip_test.go \
  core-service/internal/middleware/ratelimit.go
git commit -F - <<'EOF'
feat(middleware): IP basina limit, web anahtari ve ozel 429 kodu

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 2: Configuration: Keşfet limits and the web key

P4: four limits and the web key, each with a struct field, a `viper.SetDefault`, an explicit `BindEnv`, and entries in `config.yaml` and `.env.example`. `readSecret` gives the key `_FILE` support for Docker secrets.

**Files:**

- Modify: `core-service/internal/config/config_test.go`
- Modify: `core-service/internal/config/config.go`
- Modify: `core-service/config.yaml`
- Modify: `core-service/.env.example`

**Interfaces:**
- Consumes: `readSecret`, viper setup (existing).
- Produces: `RateLimitConfig.SosyalYorumPerMin`, `SosyalCoverPerHour`, `PublicPerMin`, `PublicWebPerMin int`; `type SosyalConfig struct { WebAnahtari string }`; `Config.Sosyal SosyalConfig`.

- [ ] **Step 1: Write the failing test**

**Modify** `core-service/internal/config/config_test.go`:

````diff
diff --git a/core-service/internal/config/config_test.go b/core-service/internal/config/config_test.go
--- a/core-service/internal/config/config_test.go
+++ b/core-service/internal/config/config_test.go
@@ -356,3 +356,62 @@ func TestLoad_SosyalYayinLimiti(t *testing.T) {
 		t.Errorf("SosyalYayinPerHour = %d, ortamdan 7 bekleniyordu", cfg.RateLimit.SosyalYayinPerHour)
 	}
 }
+
+func TestLoad_KesfetLimitleriVarsayilan(t *testing.T) {
+	for _, ad := range []string{
+		"RATELIMIT_SOSYAL_YORUM_PER_MIN", "RATELIMIT_SOSYAL_COVER_PER_HOUR",
+		"RATELIMIT_PUBLIC_PER_MIN", "RATELIMIT_PUBLIC_WEB_PER_MIN",
+		"SOSYAL_WEB_ANAHTARI", "SOSYAL_WEB_ANAHTARI_FILE",
+	} {
+		os.Unsetenv(ad)
+	}
+	cfg, err := Load()
+	if err != nil {
+		t.Fatalf("Load hata verdi: %v", err)
+	}
+	r := cfg.RateLimit
+	if r.SosyalYorumPerMin != 10 || r.SosyalCoverPerHour != 20 || r.PublicPerMin != 30 || r.PublicWebPerMin != 600 {
+		t.Errorf("varsayilanlar = yorum %d, cover %d, public %d, web %d; beklenen 10, 20, 30, 600",
+			r.SosyalYorumPerMin, r.SosyalCoverPerHour, r.PublicPerMin, r.PublicWebPerMin)
+	}
+	if cfg.Sosyal.WebAnahtari != "" {
+		t.Errorf("WebAnahtari varsayilani bos olmali: %q", cfg.Sosyal.WebAnahtari)
+	}
+}
+
+func TestLoad_KesfetLimitleriOrtamdan(t *testing.T) {
+	t.Setenv("RATELIMIT_SOSYAL_YORUM_PER_MIN", "3")
+	t.Setenv("RATELIMIT_SOSYAL_COVER_PER_HOUR", "4")
+	t.Setenv("RATELIMIT_PUBLIC_PER_MIN", "5")
+	t.Setenv("RATELIMIT_PUBLIC_WEB_PER_MIN", "6")
+	t.Setenv("SOSYAL_WEB_ANAHTARI", "dogrudan-deger")
+	cfg, err := Load()
+	if err != nil {
+		t.Fatalf("Load hata verdi: %v", err)
+	}
+	r := cfg.RateLimit
+	if r.SosyalYorumPerMin != 3 || r.SosyalCoverPerHour != 4 || r.PublicPerMin != 5 || r.PublicWebPerMin != 6 {
+		t.Errorf("ortam degerleri okunmadi: %+v", r)
+	}
+	if cfg.Sosyal.WebAnahtari != "dogrudan-deger" {
+		t.Errorf("WebAnahtari = %q", cfg.Sosyal.WebAnahtari)
+	}
+}
+
+// Docker secret yolu: SOSYAL_WEB_ANAHTARI bos, _FILE dolu.
+func TestLoad_WebAnahtariDosyadan(t *testing.T) {
+	dosya := t.TempDir() + "/web_anahtari"
+	if err := os.WriteFile(dosya, []byte("  dosyadaki-sir\n"), 0o600); err != nil {
+		t.Fatal(err)
+	}
+	t.Setenv("SOSYAL_WEB_ANAHTARI", "")
+	t.Setenv("SOSYAL_WEB_ANAHTARI_FILE", dosya)
+	// readSecret degeri ortama yazar; t.Setenv'in temizligi geri alir.
+	cfg, err := Load()
+	if err != nil {
+		t.Fatalf("Load hata verdi: %v", err)
+	}
+	if cfg.Sosyal.WebAnahtari != "dosyadaki-sir" {
+		t.Errorf("WebAnahtari = %q, beklenen dosyadaki-sir (kirpilmis)", cfg.Sosyal.WebAnahtari)
+	}
+}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/config/ -count=1 -run 'Kesfet|WebAnahtari'
```

Expected: FAIL, build errors `r.SosyalYorumPerMin undefined` and `cfg.Sosyal undefined`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/config/config.go`:

````diff
diff --git a/core-service/internal/config/config.go b/core-service/internal/config/config.go
--- a/core-service/internal/config/config.go
+++ b/core-service/internal/config/config.go
@@ -37,6 +37,7 @@ type Config struct {
 	Suno      SunoConfig
 	Audio     AudioConfig
 	Auth      AuthConfig
+	Sosyal    SosyalConfig
 }
 
 type ServerConfig struct {
@@ -63,6 +64,27 @@ type RateLimitConfig struct {
 	// SosyalYayinPerHour, POST /api/social/yayinlar icin kullanici basina
 	// saatlik limit.
 	SosyalYayinPerHour int
+	// SosyalYorumPerMin, POST /api/social/yayinlar/{id}/yorumlar icin
+	// kullanici basina dakikalik limit (asimda 429 yavasla).
+	SosyalYorumPerMin int
+	// SosyalCoverPerHour, POST /api/social/yayinlar/{id}/cover-baslat
+	// icin kullanici basina saatlik limit (her cagri stem kopyalar).
+	SosyalCoverPerHour int
+	// PublicPerMin, kimliksiz GET /api/public/s/{slug} icin istemci IP'si
+	// basina dakikalik limit.
+	PublicPerMin int
+	// PublicWebPerMin, ayni ucun X-Web-Anahtari ile gelen (web sunucusu)
+	// istekleri icin TUM web istekleri ortak dakikalik limit.
+	PublicWebPerMin int
+}
+
+// SosyalConfig, Kesfet'in yapilandirmasi.
+type SosyalConfig struct {
+	// WebAnahtari, make-singer-frontend'in X-Web-Anahtari basligiyla
+	// gonderdigi paylasilan sir. Bos ise web yolu kapalidir ve web
+	// sunucusu da IP basina sinira tabidir. Sir: SOSYAL_WEB_ANAHTARI ya
+	// da SOSYAL_WEB_ANAHTARI_FILE.
+	WebAnahtari string
 }
 
 type GroqConfig struct {
@@ -176,6 +198,7 @@ func Load() (*Config, error) {
 	readSecret("AUTH_DB_URL")
 	readSecret("AUTH_SIGNING_KEY")
 	readSecret("AUTH_CRYPTO_KEY")
+	readSecret("SOSYAL_WEB_ANAHTARI")
 
 	viper.SetConfigName("config")
 	viper.SetConfigType("yaml")
@@ -232,6 +255,11 @@ func Load() (*Config, error) {
 	_ = viper.BindEnv("auth.fed_apple_key_id", "AUTH_FED_APPLE_KEY_ID")
 	_ = viper.BindEnv("auth.fed_apple_key_p8", "AUTH_FED_APPLE_KEY_P8")
 	_ = viper.BindEnv("ratelimit.sosyal_yayin_per_hour", "RATELIMIT_SOSYAL_YAYIN_PER_HOUR")
+	_ = viper.BindEnv("ratelimit.sosyal_yorum_per_min", "RATELIMIT_SOSYAL_YORUM_PER_MIN")
+	_ = viper.BindEnv("ratelimit.sosyal_cover_per_hour", "RATELIMIT_SOSYAL_COVER_PER_HOUR")
+	_ = viper.BindEnv("ratelimit.public_per_min", "RATELIMIT_PUBLIC_PER_MIN")
+	_ = viper.BindEnv("ratelimit.public_web_per_min", "RATELIMIT_PUBLIC_WEB_PER_MIN")
+	_ = viper.BindEnv("sosyal.web_anahtari", "SOSYAL_WEB_ANAHTARI")
 
 	// Defaults
 	viper.SetDefault("server.port", "8000")
@@ -248,6 +276,10 @@ func Load() (*Config, error) {
 	viper.SetDefault("ratelimit.ws_ticket_per_min", 60)
 	viper.SetDefault("ratelimit.sign_per_min", 60)
 	viper.SetDefault("ratelimit.sosyal_yayin_per_hour", 30)
+	viper.SetDefault("ratelimit.sosyal_yorum_per_min", 10)
+	viper.SetDefault("ratelimit.sosyal_cover_per_hour", 20)
+	viper.SetDefault("ratelimit.public_per_min", 30)
+	viper.SetDefault("ratelimit.public_web_per_min", 600)
 
 	// Groq defaults
 	viper.SetDefault("groq.base_url", "https://api.groq.com/openai/v1")
@@ -337,6 +369,10 @@ func Load() (*Config, error) {
 			SignPerMin:     viper.GetInt("ratelimit.sign_per_min"),
 
 			SosyalYayinPerHour: viper.GetInt("ratelimit.sosyal_yayin_per_hour"),
+			SosyalYorumPerMin:  viper.GetInt("ratelimit.sosyal_yorum_per_min"),
+			SosyalCoverPerHour: viper.GetInt("ratelimit.sosyal_cover_per_hour"),
+			PublicPerMin:       viper.GetInt("ratelimit.public_per_min"),
+			PublicWebPerMin:    viper.GetInt("ratelimit.public_web_per_min"),
 		},
 		Groq: GroqConfig{
 			APIKey:  viper.GetString("groq.api_key"),
@@ -384,6 +420,9 @@ func Load() (*Config, error) {
 
 			Federasyon: federasyonIstemcileriOlustur(),
 		},
+		Sosyal: SosyalConfig{
+			WebAnahtari: viper.GetString("sosyal.web_anahtari"),
+		},
 	}
 
 	return cfg, nil
````

**Modify** `core-service/config.yaml`:

````diff
diff --git a/core-service/config.yaml b/core-service/config.yaml
--- a/core-service/config.yaml
+++ b/core-service/config.yaml
@@ -17,6 +17,14 @@ ratelimit:
   ws_ticket_per_min: 60
   sign_per_min: 60
   sosyal_yayin_per_hour: 30
+  sosyal_yorum_per_min: 10 # asimda 429 yavasla
+  sosyal_cover_per_hour: 20
+  public_per_min: 30 # GET /api/public/s/{slug}, istemci IP'si basina
+  public_web_per_min: 600 # ayni uc, X-Web-Anahtari ile gelen tum web istekleri ortak
+
+# Kesfet (sosyal)
+sosyal:
+  web_anahtari: "" # Set via SOSYAL_WEB_ANAHTARI (or _FILE). Empty = web bypass disabled.
 
 # Groq AI Configuration
 groq:
````

**Modify** `core-service/.env.example`:

````diff
diff --git a/core-service/.env.example b/core-service/.env.example
--- a/core-service/.env.example
+++ b/core-service/.env.example
@@ -15,6 +15,20 @@ RATELIMIT_EXPORT_PER_HOUR=20
 RATELIMIT_UPLOAD_PER_HOUR=50
 # POST /api/social/yayinlar, kullanici basina saatlik
 RATELIMIT_SOSYAL_YAYIN_PER_HOUR=30
+# POST /api/social/yayinlar/{id}/yorumlar, kullanici basina dakikalik (429 yavasla)
+RATELIMIT_SOSYAL_YORUM_PER_MIN=10
+# POST /api/social/yayinlar/{id}/cover-baslat, kullanici basina saatlik
+RATELIMIT_SOSYAL_COVER_PER_HOUR=20
+# GET /api/public/s/{slug} (oturumsuz): istemci IP'si basina dakikalik
+RATELIMIT_PUBLIC_PER_MIN=30
+# Ayni uc, X-Web-Anahtari dogru olan istekler (web sunucusu) icin ortak dakikalik
+RATELIMIT_PUBLIC_WEB_PER_MIN=600
+
+# Kesfet web anahtari: make-singer-frontend'in X-Web-Anahtari basligi.
+# Bos ise web sunucusu da IP basina sinira tabidir. Sirdir: uretimde
+# SOSYAL_WEB_ANAHTARI_FILE ile Docker secret'tan okunur. En az 32 karakter
+# rastgele bir deger kullanin (orn. openssl rand -hex 32).
+SOSYAL_WEB_ANAHTARI=
 
 # Groq AI Configuration
 GROQ_API_KEY=gsk_...
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && go test ./internal/config/ -count=3 -shuffle=on && go build ./...
```

Expected: `ok  github.com/makeasinger/api/internal/config` (order-independent), build clean. Whole Go suite after this task: 683 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/config/config_test.go \
  core-service/internal/config/config.go \
  core-service/config.yaml \
  core-service/.env.example
git commit -F - <<'EOF'
feat(config): Kesfet yorum, cover ve onizleme limitleri, web anahtari

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 3: Follow, unfollow and suggestions (§4.4)

C3, P5, P6. `TakipDeposu` joins `Depo`; the fake repository gets the four methods. `TakipDurumu` lives next to `BegeniDurumu`'s pattern in the service file. `profilKolonlariP` is the `p.`-qualified variant of 5a's `profilKolonlari`, so `profilTara` can be reused.

**Files:**

- Create: `core-service/internal/sosyal/depo_takip_test.go`
- Create: `core-service/internal/sosyal/servis_takip_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go` (fake `TakipDeposu`)
- Create: `core-service/internal/handler/sosyal_takip_test.go`
- Modify: `core-service/internal/handler/sosyal_test.go` (fake service methods)
- Modify: `core-service/internal/sosyal/hatalar.go`
- Modify: `core-service/internal/sosyal/depo.go`
- Create: `core-service/internal/sosyal/depo_takip.go`
- Create: `core-service/internal/sosyal/servis_takip.go`
- Modify: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_takip.go`

**Interfaces:**
- Consumes: `ProfilAdlaGetir`, `profilTara`, `profilKolonlari`, `KullaniciAdiGecerli`, `kullaniciCoz`, `(*ProfilKaydi).ozet`, fixtures `testProfilEkle`, `testYayinEkle`, `testTakipEkle`, `esitSira` (5a).
- Produces: `ErrKendiniTakip`; `type TakipDeposu interface { TakipEt, TakibiBirak(ctx, takipciID, takipEdilenID string) error; TakipDurumu(ctx, takipciID, takipEdilenID string) (*TakipDurumu, error); Oneriler(ctx, izleyiciID string, limit int) ([]ProfilKaydi, error) }`; `type TakipDurumu struct { Takipci int; TakipEdiyorMuyum bool }`; `(*Servis).Takip(ctx, kullaniciID, ad string, takip bool) (*TakipDurumu, error)`, `(*Servis).Oneriler(ctx, kullaniciID string) ([]ProfilOzeti, error)`; handlers `TakipEt`, `TakibiBirak`, `Oneriler`; routes `PUT|DELETE /profiller/:ad/takip`, `GET /oneriler`; code `kendini_takip` (422).

- [ ] **Step 1: Write the failing tests**

`testBegeniEkle` (in `depo_takip_test.go`) writes likes at a chosen time; later tasks reuse it. `sosyalHamIstek` (in `sosyal_takip_test.go`) returns the raw body for endpoints that answer with a JSON array.

**Create** `core-service/internal/sosyal/depo_takip_test.go`:

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

// testBegeniEkle, verilen zamanda bir begeni yazar.
func testBegeniEkle(t *testing.T, havuz *pgxpool.Pool, kullanici, yayin string, zaman time.Time) {
	t.Helper()
	if _, err := havuz.Exec(context.Background(),
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id, olusturma) VALUES ($1, $2, $3)`,
		kullanici, yayin, zaman); err != nil {
		t.Fatalf("begeni eklenemedi: %v", err)
	}
}

func TestPostgresDepo_TakipTekrariGuvenli(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()

	for i := 0; i < 2; i++ {
		if err := d.TakipEt(ctx, a, b); err != nil {
			t.Fatalf("TakipEt %d: %v", i, err)
		}
	}
	if err := d.TakipEt(ctx, c, b); err != nil {
		t.Fatal(err)
	}
	durum, err := d.TakipDurumu(ctx, a, b)
	if err != nil || durum.Takipci != 2 || !durum.TakipEdiyorMuyum {
		t.Fatalf("durum = %+v, %v", durum, err)
	}
	ters, err := d.TakipDurumu(ctx, b, a)
	if err != nil || ters.Takipci != 0 || ters.TakipEdiyorMuyum {
		t.Fatalf("ters yon = %+v, %v", ters, err)
	}
	for i := 0; i < 2; i++ {
		if err := d.TakibiBirak(ctx, a, b); err != nil {
			t.Fatalf("TakibiBirak %d: %v", i, err)
		}
	}
	durum, err = d.TakipDurumu(ctx, a, b)
	if err != nil || durum.Takipci != 1 || durum.TakipEdiyorMuyum {
		t.Fatalf("birakmadan sonra = %+v, %v", durum, err)
	}
	if err := d.TakipEt(ctx, a, a); err == nil {
		t.Fatal("kendini takip CHECK kisitina takilmali")
	}
}

func TestPostgresDepo_Oneriler(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	simdi := time.Now()
	gun := 24 * time.Hour

	profil := func(ad string) string {
		id := uuid.NewString()
		testProfilEkle(t, d, id, ad)
		return id
	}
	begen := func(yayin string, n int, zaman time.Time) {
		for i := 0; i < n; i++ {
			testBegeniEkle(t, havuz, uuid.NewString(), yayin, zaman)
		}
	}

	ben := profil("ben")
	takipEdilen := profil("takip_edilen")
	cok := profil("cok_begenilen")
	eski := profil("eski_begeniler")
	sifir := profil("begenisiz")
	takipcili := profil("takipcili")
	linkci := profil("yalniz_link")
	silinmis := profil("silinmis_yayin")
	hazirlanan := profil("hazirlanan")
	profil("yayinsiz")

	testTakipEkle(t, havuz, ben, takipEdilen)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen}), 9, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: ben}), 9, simdi)

	y := testYayinEkle(t, havuz, testYayin{SahipID: cok, Olusturma: simdi.Add(-40 * gun)})
	begen(y, 5, simdi.Add(-29*gun))
	testBegeniEkle(t, havuz, cok, y, simdi) // kendi begenisi sayilmaz

	y = testYayinEkle(t, havuz, testYayin{SahipID: eski, Olusturma: simdi.Add(-40 * gun)})
	begen(y, 10, simdi.Add(-31*gun))
	begen(y, 1, simdi)

	testYayinEkle(t, havuz, testYayin{SahipID: sifir, Olusturma: simdi.Add(-time.Hour)})
	begen(testYayinEkle(t, havuz, testYayin{SahipID: takipcili, Gorunurluk: GorunurlukTakipciler}), 3, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: linkci, Gorunurluk: GorunurlukLink}), 20, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: silinmis, Silindi: true}), 20, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: hazirlanan, Durum: DurumHazirlaniyor}), 20, simdi)

	oneriler, err := d.Oneriler(ctx, ben, 10)
	if err != nil {
		t.Fatalf("Oneriler: %v", err)
	}
	var adlar []string
	for _, o := range oneriler {
		adlar = append(adlar, o.KullaniciAdi)
	}
	esitSira(t, "oneriler", adlar, []string{"cok_begenilen", "takipcili", "eski_begeniler", "begenisiz"})

	ilk, err := d.Oneriler(ctx, ben, 2)
	if err != nil || len(ilk) != 2 || ilk[0].KullaniciAdi != "cok_begenilen" {
		t.Fatalf("limit 2 = %+v, %v", ilk, err)
	}
}

func TestPostgresDepo_OnerilerEsitlikteYeniYayinOnde(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	simdi := time.Now()
	ben := uuid.NewString()
	var beklenen []string
	for i := 0; i < 12; i++ {
		id, ad := uuid.NewString(), fmt.Sprintf("yayinci_%02d", i)
		testProfilEkle(t, d, id, ad)
		testYayinEkle(t, havuz, testYayin{SahipID: id, Olusturma: simdi.Add(-time.Duration(i) * time.Minute)})
		if i < 10 {
			beklenen = append(beklenen, ad)
		}
	}
	oneriler, err := d.Oneriler(context.Background(), ben, 10)
	if err != nil {
		t.Fatal(err)
	}
	var adlar []string
	for _, o := range oneriler {
		adlar = append(adlar, o.KullaniciAdi)
	}
	esitSira(t, "esitlik", adlar, beklenen)
}
````

**Create** `core-service/internal/sosyal/servis_takip_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"testing"
)

func takipDeposu() *sahteDepo {
	d := profilliDepo()
	d.profiller[kullaniciB] = &ProfilKaydi{KullaniciID: kullaniciB, KullaniciAdi: "berk", GorunenAd: "Berk"}
	return d
}

func TestServis_TakipEtVeBirak(t *testing.T) {
	d := takipDeposu()
	s, _ := testServisi(d)
	ctx := context.Background()

	for i := 0; i < 2; i++ { // tekrari guvenli
		durum, err := s.Takip(ctx, kullaniciA, "berk", true)
		if err != nil || durum.Takipci != 1 || !durum.TakipEdiyorMuyum {
			t.Fatalf("takip %d = %+v, %v", i, durum, err)
		}
	}
	if !d.takipler[[2]string{kullaniciA, kullaniciB}] {
		t.Fatal("takip A -> B yazilmali")
	}
	for i := 0; i < 2; i++ {
		durum, err := s.Takip(ctx, kullaniciA, "berk", false)
		if err != nil || durum.Takipci != 0 || durum.TakipEdiyorMuyum {
			t.Fatalf("birakma %d = %+v, %v", i, durum, err)
		}
	}
}

func TestServis_TakipHatalari(t *testing.T) {
	d := takipDeposu()
	s, _ := testServisi(d)
	ctx := context.Background()
	durumlar := []struct {
		ad, kullanici string
		takip         bool
		beklenen      error
	}{
		{"ali", kullaniciA, true, ErrKendiniTakip},
		{"ali", kullaniciA, false, ErrKendiniTakip},
		{"yok_biri", kullaniciA, true, ErrBulunamadi},
		{".kotu", kullaniciA, true, ErrBulunamadi},
		{"berk", "uuid-degil", true, ErrKimlik},
	}
	for _, c := range durumlar {
		if _, err := s.Takip(ctx, c.kullanici, c.ad, c.takip); !errors.Is(err, c.beklenen) {
			t.Errorf("%s/%v: %v, beklenen %v", c.ad, c.takip, err, c.beklenen)
		}
	}
	if len(d.takipler) != 0 {
		t.Fatalf("hatali isteklerde takip yazilmamali: %v", d.takipler)
	}
}

func TestServis_OnerilerOzetVeSinir(t *testing.T) {
	d := takipDeposu()
	s, _ := testServisi(d)
	ctx := context.Background()

	bos, err := s.Oneriler(ctx, kullaniciA)
	if err != nil || bos == nil || len(bos) != 0 {
		t.Fatalf("bos oneriler [] olmali: %#v, %v", bos, err)
	}
	if d.sonOneri != [2]any{kullaniciA, 10} {
		t.Fatalf("depo cagrisi = %v, beklenen {A, 10}", d.sonOneri)
	}

	d.oneriKayitlar = []ProfilKaydi{*d.profiller[kullaniciB]}
	o, err := s.Oneriler(ctx, kullaniciA)
	if err != nil || len(o) != 1 {
		t.Fatalf("oneriler = %+v, %v", o, err)
	}
	if o[0] != (ProfilOzeti{KullaniciAdi: "berk", GorunenAd: "Berk", AvatarRengi: AvatarRengi(kullaniciB)}) {
		t.Fatalf("ozet = %+v", o[0])
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -58,6 +58,10 @@ type sahteDepo struct {
 
 	akisOgeleri []YayinOzeti
 	sonAkis     akisCagrisi
+
+	takipler      map[[2]string]bool // {takipci, takip edilen}
+	oneriKayitlar []ProfilKaydi
+	sonOneri      [2]any // {izleyici, limit}
 }
 
 func yeniSahteDepo() *sahteDepo {
@@ -66,6 +70,7 @@ func yeniSahteDepo() *sahteDepo {
 		yayinlar:    map[string]*YayinKaydi{},
 		hazirliklar: map[string]*HazirlikKaydi{},
 		yayinlanan:  map[string]HazirlikSonucu{},
+		takipler:    map[[2]string]bool{},
 	}
 }
 
@@ -336,3 +341,40 @@ func (d *sahteDepo) BegeniyiKaldir(_ context.Context, kullaniciID, yayinID strin
 	}
 	return nil
 }
+
+func (d *sahteDepo) TakipEt(_ context.Context, takipci, edilen string) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	d.takipler[[2]string{takipci, edilen}] = true
+	return nil
+}
+
+func (d *sahteDepo) TakibiBirak(_ context.Context, takipci, edilen string) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	delete(d.takipler, [2]string{takipci, edilen})
+	return nil
+}
+
+func (d *sahteDepo) TakipDurumu(_ context.Context, takipci, edilen string) (*TakipDurumu, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	t := &TakipDurumu{TakipEdiyorMuyum: d.takipler[[2]string{takipci, edilen}]}
+	for k := range d.takipler {
+		if k[1] == edilen {
+			t.Takipci++
+		}
+	}
+	return t, nil
+}
+
+func (d *sahteDepo) Oneriler(_ context.Context, izleyiciID string, limit int) ([]ProfilKaydi, error) {
+	d.sonOneri = [2]any{izleyiciID, limit}
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	return d.oneriKayitlar, nil
+}
````

**Create** `core-service/internal/handler/sosyal_takip_test.go`:

````go
package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
)

func TestSosyal_TakipYaniti(t *testing.T) {
	for _, d := range []struct {
		yontem string
		takip  bool
	}{{http.MethodPut, true}, {http.MethodDelete, false}} {
		s := &sahteSosyal{takip: &sosyal.TakipDurumu{Takipci: 4, TakipEdiyorMuyum: d.takip}}
		durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), d.yontem, "/api/social/profiller/ayse/takip", "")
		if durum != http.StatusOK || govde["takipci"] != float64(4) || govde["takipEdiyorMuyum"] != d.takip {
			t.Fatalf("%s: durum=%d govde=%v", d.yontem, durum, govde)
		}
		if s.sonAd != "ayse" || s.sonTakip != d.takip || s.sonKullanici != sosyalKullanici {
			t.Fatalf("%s: iletilen ad=%q takip=%v", d.yontem, s.sonAd, s.sonTakip)
		}
	}
}

func TestSosyal_KendiniTakip422(t *testing.T) {
	s := &sahteSosyal{hata: sosyal.ErrKendiniTakip}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPut, "/api/social/profiller/ben/takip", "")
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "kendini_takip" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_TakipProfilYok404(t *testing.T) {
	s := &sahteSosyal{hata: sosyal.ErrBulunamadi}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/profiller/yok/takip", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_OnerilerDizi(t *testing.T) {
	s := &sahteSosyal{oneriler: []sosyal.ProfilOzeti{{KullaniciAdi: "ayse", GorunenAd: "Ayse", AvatarRengi: 2}}}
	app := sosyalUygulamasi(s, sosyalKullanici)
	durum, ham := sosyalHamIstek(t, app, http.MethodGet, "/api/social/oneriler", "")
	if durum != http.StatusOK || ham != `[{"kullaniciAdi":"ayse","gorunenAd":"Ayse","avatarRengi":2}]` {
		t.Fatalf("durum=%d govde=%s", durum, ham)
	}
	s.oneriler = []sosyal.ProfilOzeti{}
	if _, ham := sosyalHamIstek(t, app, http.MethodGet, "/api/social/oneriler", ""); ham != `[]` {
		t.Fatalf("bos oneri [] olmali: %s", ham)
	}
}

// sosyalHamIstek, istegi atar; durum kodunu ve ham govdeyi doner (dizi
// donen uclar icin).
func sosyalHamIstek(t *testing.T, app *fiber.App, yontem, yol, govde string) (int, string) {
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
	return resp.StatusCode, string(ham)
}
````

**Modify** `core-service/internal/handler/sosyal_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_test.go b/core-service/internal/handler/sosyal_test.go
--- a/core-service/internal/handler/sosyal_test.go
+++ b/core-service/internal/handler/sosyal_test.go
@@ -28,6 +28,8 @@ type sahteSosyal struct {
 	dosyalar    *sosyal.YayinDosyalari
 	sayfa       *sosyal.Sayfa[sosyal.YayinOzeti]
 	begeni      *sosyal.BegeniDurumu
+	takip       *sosyal.TakipDurumu
+	oneriler    []sosyal.ProfilOzeti
 
 	sonKullanici string
 	sonAd        string
@@ -40,6 +42,7 @@ type sahteSosyal struct {
 	sonImlec     string
 	sonLimit     int
 	sonBegen     bool
+	sonTakip     bool
 }
 
 func (s *sahteSosyal) ProfilBen(_ context.Context, k string) (*sosyal.Profil, error) {
@@ -112,6 +115,16 @@ func (s *sahteSosyal) Begeni(_ context.Context, k, id string, begen bool) (*sosy
 	return s.begeni, s.hata
 }
 
+func (s *sahteSosyal) Takip(_ context.Context, k, ad string, takip bool) (*sosyal.TakipDurumu, error) {
+	s.sonKullanici, s.sonAd, s.sonTakip = k, ad, takip
+	return s.takip, s.hata
+}
+
+func (s *sahteSosyal) Oneriler(_ context.Context, k string) ([]sosyal.ProfilOzeti, error) {
+	s.sonKullanici = k
+	return s.oneriler, s.hata
+}
+
 // sosyalUygulamasi, /api/social grubunu sahte servisle kurar.
 func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
 	app := fiber.New()
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors `undefined: TakipDurumu`, `d.TakipEt undefined`, `undefined: sosyal.TakipDurumu`, `undefined: sosyal.ErrKendiniTakip`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/sosyal/hatalar.go`:

````diff
diff --git a/core-service/internal/sosyal/hatalar.go b/core-service/internal/sosyal/hatalar.go
--- a/core-service/internal/sosyal/hatalar.go
+++ b/core-service/internal/sosyal/hatalar.go
@@ -21,6 +21,7 @@ var (
 	ErrStemYok              = errors.New("yayinin stem kopyasi yok")
 	ErrDurum                = errors.New("yayin bu islem icin uygun durumda degil")
 	ErrKimlik               = errors.New("kimlik gecersiz")
+	ErrKendiniTakip         = errors.New("kendini takip edemezsin")
 )
 
 // dogrulamaHatasi, ErrDogrulama'yi alan bazli bir aciklamayla sarar.
````

**Modify** `core-service/internal/sosyal/depo.go`:

````diff
diff --git a/core-service/internal/sosyal/depo.go b/core-service/internal/sosyal/depo.go
--- a/core-service/internal/sosyal/depo.go
+++ b/core-service/internal/sosyal/depo.go
@@ -33,6 +33,7 @@ type Depo interface {
 	HazirlikDeposu
 	AkisDeposu
 	BegeniDeposu
+	TakipDeposu
 }
 
 // PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
@@ -58,6 +59,9 @@ func tekilIhlali(err error) (string, bool) {
 
 const profilKolonlari = `kullanici_id::text, kullanici_adi, gorunen_ad, bio, olusturma`
 
+// profilKolonlariP, profilKolonlari'nin "p" takma adli bicimidir.
+const profilKolonlariP = `p.kullanici_id::text, p.kullanici_adi, p.gorunen_ad, p.bio, p.olusturma`
+
 func profilTara(satir pgx.Row) (*ProfilKaydi, error) {
 	var k ProfilKaydi
 	if err := satir.Scan(&k.KullaniciID, &k.KullaniciAdi, &k.GorunenAd, &k.Bio, &k.Olusturma); err != nil {
````

**Create** `core-service/internal/sosyal/depo_takip.go`:

````go
package sosyal

import (
	"context"
	"fmt"
)

// TakipDeposu, sosyal.takip islemleri ve oneriler (spec §4.4).
type TakipDeposu interface {
	// TakipEt ve TakibiBirak, tekrari guvenlidir.
	TakipEt(ctx context.Context, takipciID, takipEdilenID string) error
	TakibiBirak(ctx context.Context, takipciID, takipEdilenID string) error
	// TakipDurumu, takip edilenin takipci sayisini ve takipcinin onu
	// takip edip etmedigini doner.
	TakipDurumu(ctx context.Context, takipciID, takipEdilenID string) (*TakipDurumu, error)
	// Oneriler, izleyicinin takip etmedigi (kendisi haric) yayincilari son
	// 30 gunde aldiklari begeniye gore en cok limit tane doner.
	Oneriler(ctx context.Context, izleyiciID string, limit int) ([]ProfilKaydi, error)
}

func (d *PostgresDepo) TakipEt(ctx context.Context, takipciID, takipEdilenID string) error {
	if _, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.takip (takipci_id, takip_edilen_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, takipciID, takipEdilenID); err != nil {
		return fmt.Errorf("takip eklenemedi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) TakibiBirak(ctx context.Context, takipciID, takipEdilenID string) error {
	if _, err := d.havuz.Exec(ctx, `
		DELETE FROM sosyal.takip WHERE takipci_id = $1 AND takip_edilen_id = $2`,
		takipciID, takipEdilenID); err != nil {
		return fmt.Errorf("takip kaldirilamadi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) TakipDurumu(ctx context.Context, takipciID, takipEdilenID string) (*TakipDurumu, error) {
	var t TakipDurumu
	if err := d.havuz.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.takip WHERE takip_edilen_id = $2),
		       EXISTS (SELECT 1 FROM sosyal.takip WHERE takipci_id = $1 AND takip_edilen_id = $2)`,
		takipciID, takipEdilenID).Scan(&t.Takipci, &t.TakipEdiyorMuyum); err != nil {
		return nil, fmt.Errorf("takip durumu okunamadi: %w", err)
	}
	return &t, nil
}

// Oneriler: yayinci, herkes ya da takipciler gorunurlugunde en az bir
// yayinda ve silinmemis yayini olan profildir (link yayinlari onu
// onerilere sokmaz: varligini sizdirirdi). Puan, bu yayinlarin son 30
// gunde baskalarindan aldigi begenilerdir (sahibin kendi begenisi
// sayilmaz). Esitlikte son yayini yeni olan, sonra kimlik one gecer.
func (d *PostgresDepo) Oneriler(ctx context.Context, izleyiciID string, limit int) ([]ProfilKaydi, error) {
	satirlar, err := d.havuz.Query(ctx, `
		SELECT `+profilKolonlariP+`
		  FROM sosyal.profil p
		  JOIN LATERAL (
		    SELECT count(b.yayin_id) AS begeni, max(y.olusturma) AS son
		      FROM sosyal.yayin y
		      LEFT JOIN sosyal.begeni b
		        ON b.yayin_id = y.id AND b.kullanici_id <> y.sahip_id
		       AND b.olusturma > now() - interval '30 days'
		     WHERE y.sahip_id = p.kullanici_id AND y.durum = 'yayinda' AND y.silinme IS NULL
		       AND y.gorunurluk IN ('herkes', 'takipciler')
		  ) s ON s.son IS NOT NULL
		 WHERE p.kullanici_id <> $1
		   AND NOT EXISTS (SELECT 1 FROM sosyal.takip t WHERE t.takipci_id = $1 AND t.takip_edilen_id = p.kullanici_id)
		 ORDER BY s.begeni DESC, s.son DESC, p.kullanici_id
		 LIMIT $2`, izleyiciID, limit)
	if err != nil {
		return nil, fmt.Errorf("oneriler okunamadi: %w", err)
	}
	defer satirlar.Close()
	kayitlar := []ProfilKaydi{}
	for satirlar.Next() {
		k, err := profilTara(satirlar)
		if err != nil {
			return nil, fmt.Errorf("oneri okunamadi: %w", err)
		}
		kayitlar = append(kayitlar, *k)
	}
	return kayitlar, satirlar.Err()
}
````

**Create** `core-service/internal/sosyal/servis_takip.go`:

````go
package sosyal

import "context"

// oneriSayisi, GET /oneriler'in en cok kac profil dondurdugu (spec §4.4).
const oneriSayisi = 10

// TakipDurumu, PUT/DELETE /profiller/{ad}/takip yaniti.
type TakipDurumu struct {
	Takipci          int  `json:"takipci"`
	TakipEdiyorMuyum bool `json:"takipEdiyorMuyum"`
}

// Takip, PUT (takip=true) ve DELETE (false) /profiller/{ad}/takip.
// Tekrari guvenlidir. Kendini takip (ya da birakma) ErrKendiniTakip,
// olmayan ya da gecersiz bicimli ad ErrBulunamadi'dir. Takip eden icin
// profil gerekmez (begeni gibi).
func (s *Servis) Takip(ctx context.Context, kullaniciID, ad string, takip bool) (*TakipDurumu, error) {
	id, err := kullaniciCoz(kullaniciID)
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
	if p.KullaniciID == id {
		return nil, ErrKendiniTakip
	}
	if takip {
		err = s.depo.TakipEt(ctx, id, p.KullaniciID)
	} else {
		err = s.depo.TakibiBirak(ctx, id, p.KullaniciID)
	}
	if err != nil {
		return nil, err
	}
	return s.depo.TakipDurumu(ctx, id, p.KullaniciID)
}

// Oneriler, GET /oneriler: en cok 10 ProfilOzeti, sayfasiz. Bos liste
// null degil [] olarak doner.
func (s *Servis) Oneriler(ctx context.Context, kullaniciID string) ([]ProfilOzeti, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	kayitlar, err := s.depo.Oneriler(ctx, id, oneriSayisi)
	if err != nil {
		return nil, err
	}
	ozetler := make([]ProfilOzeti, 0, len(kayitlar))
	for i := range kayitlar {
		ozetler = append(ozetler, kayitlar[i].ozet())
	}
	return ozetler, nil
}
````

**Modify** `core-service/internal/handler/sosyal.go`:

````diff
diff --git a/core-service/internal/handler/sosyal.go b/core-service/internal/handler/sosyal.go
--- a/core-service/internal/handler/sosyal.go
+++ b/core-service/internal/handler/sosyal.go
@@ -30,6 +30,9 @@ type SosyalServisi interface {
 	Akis(ctx context.Context, kullaniciID, sekme, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
 	ProfilYayinlari(ctx context.Context, izleyiciID, ad, tur, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
 	Begeni(ctx context.Context, kullaniciID, yayinID string, begen bool) (*sosyal.BegeniDurumu, error)
+
+	Takip(ctx context.Context, kullaniciID, ad string, takip bool) (*sosyal.TakipDurumu, error)
+	Oneriler(ctx context.Context, kullaniciID string) ([]sosyal.ProfilOzeti, error)
 }
 
 var _ SosyalServisi = (*sosyal.Servis)(nil)
@@ -62,6 +65,10 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
 	r.Get("/s/:slug", h.Slug)
 	r.Put("/yayinlar/:id/begeni", h.Begen)
 	r.Delete("/yayinlar/:id/begeni", h.BegeniyiKaldir)
+
+	r.Put("/profiller/:ad/takip", h.TakipEt)
+	r.Delete("/profiller/:ad/takip", h.TakibiBirak)
+	r.Get("/oneriler", h.Oneriler)
 }
 
 // sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
@@ -81,6 +88,7 @@ var sosyalHatalari = []struct {
 	{sosyal.ErrAltyapiKapali, fiber.StatusForbidden, "altyapi_kapali"},
 	{sosyal.ErrStemYok, fiber.StatusUnprocessableEntity, "stem_yok"},
 	{sosyal.ErrDurum, fiber.StatusConflict, "durum"},
+	{sosyal.ErrKendiniTakip, fiber.StatusUnprocessableEntity, "kendini_takip"},
 }
 
 // sosyalHata, servis hatasini sozlesme govdesine cevirir:
````

**Create** `core-service/internal/handler/sosyal_takip.go`:

````go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// TakipEt handles PUT /api/social/profiller/:ad/takip
func (h *SosyalHandler) TakipEt(c *fiber.Ctx) error {
	return h.takip(c, true)
}

// TakibiBirak handles DELETE /api/social/profiller/:ad/takip
func (h *SosyalHandler) TakibiBirak(c *fiber.Ctx) error {
	return h.takip(c, false)
}

func (h *SosyalHandler) takip(c *fiber.Ctx, takip bool) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	d, err := h.servis.Takip(c.Context(), userID, c.Params("ad"), takip)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, d)
}

// Oneriler handles GET /api/social/oneriler (en cok 10 ProfilOzeti, sayfasiz).
func (h *SosyalHandler) Oneriler(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	o, err := h.servis.Oneriler(c.Context(), userID)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, o)
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... && go test ./internal/... -count=1
```

Expected: no gofmt output, `0 issues.`, every package `ok`. Whole Go suite after this task: 693 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/depo_takip_test.go \
  core-service/internal/sosyal/servis_takip_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/handler/sosyal_takip_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/sosyal/hatalar.go \
  core-service/internal/sosyal/depo.go \
  core-service/internal/sosyal/depo_takip.go \
  core-service/internal/sosyal/servis_takip.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_takip.go
git commit -F - <<'EOF'
feat(sosyal): takip, takibi birakma ve oneriler

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 4: Comments (§4.5) and the route-limiter struct

C2, P1, P2, P7, P8, P9. One SQL query orders both groups (`ORDER BY (zaman_ms IS NULL), zaman_ms, CASE … DESC, …`) and one cursor crosses the boundary; the live test pages through nine comments with limits 1–4 and 50 and gets the same order every time. `Rotalar` now takes `SosyalLimitleri`; `main.go` wires the comment limiter with `LimitKod(…, "yavasla", …)`, which is why `main.go` changes in this task.

**Files:**

- Create: `core-service/internal/sosyal/depo_yorum_test.go`
- Create: `core-service/internal/sosyal/servis_yorum_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go` (fake `YorumDeposu`)
- Create: `core-service/internal/handler/sosyal_yorum_test.go`
- Modify: `core-service/internal/handler/sosyal_test.go` (fake service methods, `gecir`, `sosyalUygulamasiLimitli`)
- Modify: `core-service/internal/handler/sosyal_yayin_test.go` (5a test, new `Rotalar` signature)
- Create: `core-service/internal/sosyal/model_yorum.go`
- Modify: `core-service/internal/sosyal/hatalar.go`
- Modify: `core-service/internal/sosyal/imlec.go`
- Modify: `core-service/internal/sosyal/depo.go`
- Create: `core-service/internal/sosyal/depo_yorum.go`
- Create: `core-service/internal/sosyal/servis_yorum.go`
- Modify: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_yorum.go`
- Modify: `core-service/cmd/server/main.go`

**Interfaces:**
- Consumes: `gorunurYayin`, `ProfilGetir`, `imlecKodla`, `imlecCoz`, `kimlikCoz`, `Sayfa`, `SayfaBoyutu`, `AvatarRengi`, `govdeHatasi`, `sosyalHata` (5a); `RateLimiter.LimitKod` (Task 1); `RateLimit.SosyalYorumPerMin` (Task 2).
- Produces: `type Yorum`, `YorumGirdisi`, `YeniYorum`, `YorumKaydi`; `type YorumImleci struct { G int; Z *int; T time.Time; ID string }`, `yorumImleciCoz`, `yorumImleci`; `ErrYorumKapali`, `ErrYetki`; `type YorumDeposu interface { YorumEkle(ctx, YeniYorum) (*Yorum, error); Yorumlar(ctx, yayinID, izleyiciID string, imlec *YorumImleci, limit int) ([]Yorum, error); YorumGetir(ctx, id string) (*YorumKaydi, error); YorumSil(ctx, id string) error }`; `(*Servis).Yorumlar(ctx, kullaniciID, yayinID, imlec string, limit int) (*Sayfa[Yorum], error)`, `YorumYaz(ctx, kullaniciID, yayinID string, g YorumGirdisi) (*Yorum, error)`, `YorumSil(ctx, kullaniciID, yorumID string) error`; `type handler.SosyalLimitleri struct { Yayin, Yorum fiber.Handler }`; `(*SosyalHandler).Rotalar(r fiber.Router, l SosyalLimitleri)`; routes `GET|POST /yayinlar/:id/yorumlar`, `DELETE /yorumlar/:id`; codes `yorum_kapali`, `yetki` (403).

- [ ] **Step 1: Write the failing tests**

`testYorumEkle` (in `depo_yorum_test.go`) inserts a comment at a chosen time and is reused by Tasks 5 and 8. `kullaniciC` and `intp` (in `servis_yorum_test.go`) are reused by later service tests.

**Create** `core-service/internal/sosyal/depo_yorum_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testYorumEkle, verilen zamanda bir yorum yazar ve kimligini doner.
func testYorumEkle(t *testing.T, havuz *pgxpool.Pool, id, yayin, yazar string, zamanMs *int, olusturma time.Time) string {
	t.Helper()
	if id == "" {
		id = uuid.NewString()
	}
	if _, err := havuz.Exec(context.Background(), `
		INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, zaman_ms, olusturma)
		VALUES ($1, $2, $3, 'metin', $4, $5)`, id, yayin, yazar, zamanMs, olusturma); err != nil {
		t.Fatalf("yorum eklenemedi: %v", err)
	}
	return id
}

// tumYorumSayfalari, yorum listesini limit'lik sayfalarla gezer; her
// sayfanin imlecini servisin kurdugu gibi (son ogeden) kurar.
func tumYorumSayfalari(t *testing.T, d *PostgresDepo, yayin, izleyici string, limit int) []Yorum {
	t.Helper()
	var (
		hepsi []Yorum
		imlec *YorumImleci
	)
	for sayfa := 0; sayfa < 20; sayfa++ {
		ogeler, err := d.Yorumlar(context.Background(), yayin, izleyici, imlec, limit)
		if err != nil {
			t.Fatalf("sayfa %d: %v", sayfa, err)
		}
		s := yorumSayfasiKur(ogeler, limit)
		hepsi = append(hepsi, s.Ogeler...)
		if s.SonrakiImlec == nil {
			return hepsi
		}
		if imlec, err = yorumImleciCoz(*s.SonrakiImlec); err != nil {
			t.Fatalf("imlec cozulemedi: %v", err)
		}
	}
	t.Fatal("sayfalama bitmedi")
	return nil
}

func TestPostgresDepo_YorumSirasiVeSayfalama(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	sahip, yazar, izleyici := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testProfilEkle(t, d, yazar, "yazar")
	yayin := testYayinEkle(t, havuz, testYayin{SahipID: sahip})
	baska := testYayinEkle(t, havuz, testYayin{SahipID: sahip})
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)

	// Beklenen sira: zamanlilar zaman_ms artan (esitlikte olusturma, id
	// artan), sonra damgasizlar olusturma azalan (esitlikte id azalan).
	z100a := testYorumEkle(t, havuz, "00000000-0000-4000-8000-0000000000a1", yayin, yazar, intp(100), t0)
	z100b := testYorumEkle(t, havuz, "00000000-0000-4000-8000-0000000000a2", yayin, yazar, intp(100), t0)
	z100c := testYorumEkle(t, havuz, "", yayin, sahip, intp(100), t0.Add(time.Second))
	z0 := testYorumEkle(t, havuz, "", yayin, yazar, intp(0), t0.Add(5*time.Minute))
	z900 := testYorumEkle(t, havuz, "", yayin, yazar, intp(900), t0.Add(-time.Minute))
	yeni := testYorumEkle(t, havuz, "", yayin, yazar, nil, t0.Add(10*time.Minute))
	ayniB := testYorumEkle(t, havuz, "ffffffff-0000-4000-8000-0000000000b1", yayin, yazar, nil, t0.Add(5*time.Minute))
	ayniA := testYorumEkle(t, havuz, "00000000-0000-4000-8000-0000000000b1", yayin, sahip, nil, t0.Add(5*time.Minute))
	eski := testYorumEkle(t, havuz, "", yayin, yazar, nil, t0)
	// Listeye girmeyenler: silinmis yorum, baska yayinin yorumu.
	silinen := testYorumEkle(t, havuz, "", yayin, yazar, intp(50), t0)
	if err := d.YorumSil(context.Background(), silinen); err != nil {
		t.Fatal(err)
	}
	testYorumEkle(t, havuz, "", baska, yazar, nil, t0)

	beklenen := []string{z0, z100a, z100b, z100c, z900, yeni, ayniB, ayniA, eski}
	for _, limit := range []int{1, 2, 3, 4, 50} {
		var idler []string
		for _, y := range tumYorumSayfalari(t, d, yayin, izleyici, limit) {
			idler = append(idler, y.ID)
		}
		esitSira(t, "yorum sirasi", idler, beklenen)
	}

	// silebilirMiyim izleyiciye goredir: yazar kendi yorumunu, sahip hepsini.
	for izleyiciID, beklenenSilebilir := range map[string]int{izleyici: 0, yazar: 7, sahip: 9} {
		n := 0
		for _, y := range tumYorumSayfalari(t, d, yayin, izleyiciID, 50) {
			if y.SilebilirMiyim {
				n++
			}
		}
		if n != beklenenSilebilir {
			t.Errorf("izleyici %s: silinebilir %d, beklenen %d", izleyiciID, n, beklenenSilebilir)
		}
	}

	// yorumSayisi silinmisi saymaz.
	k, err := d.YayinGetir(context.Background(), yayin, izleyici)
	if err != nil || k.YorumSayisi != 9 {
		t.Fatalf("yorumSayisi = %d, %v; beklenen 9", k.YorumSayisi, err)
	}
}

func TestPostgresDepo_YorumEkleGetirSil(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, yazar := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testProfilEkle(t, d, yazar, "yazar")
	yayin := testYayinEkle(t, havuz, testYayin{SahipID: sahip})

	id := uuid.NewString()
	y, err := d.YorumEkle(ctx, YeniYorum{ID: id, YayinID: yayin, YazarID: yazar, Metin: "selam", ZamanMs: intp(1200)})
	if err != nil {
		t.Fatalf("YorumEkle: %v", err)
	}
	if y.ID != id || y.Metin != "selam" || *y.ZamanMs != 1200 || y.Yazar.KullaniciAdi != "yazar" ||
		y.Yazar.AvatarRengi != AvatarRengi(yazar) || y.Olusturma.IsZero() || y.Olusturma.Location() != time.UTC {
		t.Fatalf("yorum = %+v", y)
	}
	damgasiz, err := d.YorumEkle(ctx, YeniYorum{ID: uuid.NewString(), YayinID: yayin, YazarID: yazar, Metin: "a"})
	if err != nil || damgasiz.ZamanMs != nil {
		t.Fatalf("damgasiz = %+v, %v", damgasiz, err)
	}

	k, err := d.YorumGetir(ctx, id)
	if err != nil || k.YayinID != yayin || k.YazarID != yazar || k.YayinSahibiID != sahip || k.Silindi {
		t.Fatalf("kayit = %+v, %v", k, err)
	}
	if err := d.YorumSil(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := d.YorumSil(ctx, id); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("tekrar silme: %v", err)
	}
	if k, err := d.YorumGetir(ctx, id); err != nil || !k.Silindi {
		t.Fatalf("silinmis kayit = %+v, %v", k, err)
	}
	if _, err := d.YorumGetir(ctx, uuid.NewString()); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan yorum: %v", err)
	}
	if _, err := d.YorumEkle(ctx, YeniYorum{ID: uuid.NewString(), YayinID: yayin, YazarID: yazar, Metin: "x", ZamanMs: intp(-1)}); err == nil {
		t.Fatal("negatif zaman_ms CHECK kisitina takilmali")
	}
}
````

**Create** `core-service/internal/sosyal/servis_yorum_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	kullaniciC = "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yorum1     = "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yorumC     = "c21c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

// yorumDeposu: kullaniciA'nin profili var; yayinX kullaniciB'nin herkes
// yayinidir (180 sn, yorumlar acik).
func yorumDeposu() *sahteDepo {
	d := profilliDepo()
	k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
	k.YorumAcik, k.SureSn = true, 180
	d.yayinlar[yayinX] = k
	return d
}

func intp(n int) *int { return &n }

func TestServis_YorumYaz(t *testing.T) {
	d := yorumDeposu()
	s, _ := testServisi(d)
	y, err := s.YorumYaz(context.Background(), kullaniciA, yayinX, YorumGirdisi{Metin: "  harika  ", ZamanMs: intp(180000)})
	if err != nil {
		t.Fatalf("YorumYaz: %v", err)
	}
	if !y.SilebilirMiyim || y.Metin != "harika" || y.Yazar.KullaniciAdi != "ali" || *y.ZamanMs != 180000 {
		t.Fatalf("yorum = %+v", y)
	}
	if len(d.eklenenYorumlar) != 1 || d.eklenenYorumlar[0].YayinID != yayinX || d.eklenenYorumlar[0].YazarID != kullaniciA {
		t.Fatalf("eklenen = %+v", d.eklenenYorumlar)
	}
	if _, err := s.YorumYaz(context.Background(), kullaniciA, yayinX, YorumGirdisi{Metin: "damgasiz"}); err != nil {
		t.Fatalf("damgasiz yorum: %v", err)
	}
	if d.eklenenYorumlar[1].ZamanMs != nil {
		t.Fatal("damgasiz yorumun zamani nil kalmali")
	}
}

func TestServis_YorumYazHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad        string
		hazirla   func(d *sahteDepo)
		kullanici string
		yayin     string
		girdi     YorumGirdisi
		beklenen  error
	}{
		{"bos metin", nil, kullaniciA, yayinX, YorumGirdisi{Metin: "   "}, ErrDogrulama},
		{"uzun metin", nil, kullaniciA, yayinX, YorumGirdisi{Metin: strings.Repeat("ş", 501)}, ErrDogrulama},
		{"negatif zaman", nil, kullaniciA, yayinX, YorumGirdisi{Metin: "a", ZamanMs: intp(-1)}, ErrDogrulama},
		{"sureyi asan zaman", nil, kullaniciA, yayinX, YorumGirdisi{Metin: "a", ZamanMs: intp(180001)}, ErrDogrulama},
		{"gorunmeyen yayin", func(d *sahteDepo) { d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler }, kullaniciA, yayinX, YorumGirdisi{Metin: "a"}, ErrBulunamadi},
		{"olmayan yayin", nil, kullaniciA, yayinY, YorumGirdisi{Metin: "a"}, ErrBulunamadi},
		{"bozuk yayin id", nil, kullaniciA, "x", YorumGirdisi{Metin: "a"}, ErrBulunamadi},
		{"yorum kapali", func(d *sahteDepo) { d.yayinlar[yayinX].YorumAcik = false }, kullaniciA, yayinX, YorumGirdisi{Metin: "a"}, ErrYorumKapali},
		{"profil yok", nil, kullaniciC, yayinX, YorumGirdisi{Metin: "a"}, ErrProfilYok},
	}
	for _, c := range durumlar {
		d := yorumDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		if _, err := s.YorumYaz(ctx, c.kullanici, c.yayin, c.girdi); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(d.eklenenYorumlar) != 0 {
			t.Errorf("%s: yorum yazilmamali", c.ad)
		}
	}
}

// Yorum kapaliyken sahibi de 403 alir; gorunurluk yorum_kapali'dan once gelir.
func TestServis_YorumlarKapaliVeGorunurluk(t *testing.T) {
	d := yorumDeposu()
	d.yayinlar[yayinX].YorumAcik = false
	s, _ := testServisi(d)
	ctx := context.Background()
	for _, k := range []string{kullaniciA, kullaniciB} {
		if _, err := s.Yorumlar(ctx, k, yayinX, "", 20); !errors.Is(err, ErrYorumKapali) {
			t.Errorf("%s: %v, beklenen ErrYorumKapali", k, err)
		}
	}
	d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler
	if _, err := s.Yorumlar(ctx, kullaniciA, yayinX, "", 20); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorunmeyen yayin: %v", err)
	}
}

func TestServis_YorumlarSayfaVeImlec(t *testing.T) {
	d := yorumDeposu()
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	d.yorumOgeleri = []Yorum{
		{ID: yorum1, ZamanMs: intp(500), Olusturma: t0},
		{ID: yorumC, Olusturma: t0.Add(time.Minute)},
		{ID: "c31c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", Olusturma: t0},
	}
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Yorumlar(ctx, kullaniciA, yayinX, "", 1)
	if err != nil || len(sayfa.Ogeler) != 1 || sayfa.SonrakiImlec == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if _, err := s.Yorumlar(ctx, kullaniciA, yayinX, *sayfa.SonrakiImlec, 1); err != nil {
		t.Fatal(err)
	}
	im := d.sonYorumImleci
	if im == nil || im.G != 0 || im.Z == nil || *im.Z != 500 || im.ID != yorum1 || !im.T.Equal(t0) {
		t.Fatalf("zamanli imlec = %+v", im)
	}

	sayfa, err = s.Yorumlar(ctx, kullaniciA, yayinX, "", 2)
	if err != nil || sayfa.SonrakiImlec == nil {
		t.Fatal(err)
	}
	if _, err := s.Yorumlar(ctx, kullaniciA, yayinX, *sayfa.SonrakiImlec, 2); err != nil {
		t.Fatal(err)
	}
	im = d.sonYorumImleci
	if im == nil || im.G != 1 || im.Z != nil || im.ID != yorumC {
		t.Fatalf("damgasiz imlec = %+v", im)
	}

	d.yorumOgeleri = nil
	bos, err := s.Yorumlar(ctx, kullaniciA, yayinX, "", 20)
	if err != nil || bos.Ogeler == nil || bos.SonrakiImlec != nil {
		t.Fatalf("bos sayfa = %+v, %v", bos, err)
	}
}

func TestServis_YorumImleciDogrulama(t *testing.T) {
	gecerliID := "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	for _, ham := range []string{
		"bozuk!",
		*imlecKodla(YorumImleci{G: 0, T: time.Now(), ID: gecerliID}),             // G=0 ama Z yok
		*imlecKodla(YorumImleci{G: 1, Z: intp(3), T: time.Now(), ID: gecerliID}), // G=1 ama Z var
		*imlecKodla(YorumImleci{G: 2, T: time.Now(), ID: gecerliID}),
		*imlecKodla(YorumImleci{G: 0, Z: intp(-1), T: time.Now(), ID: gecerliID}),
		*imlecKodla(YorumImleci{G: 1, T: time.Now(), ID: "uuid-degil"}),
		*imlecKodla(YorumImleci{G: 1, ID: gecerliID}),
	} {
		if _, err := yorumImleciCoz(ham); !errors.Is(err, ErrDogrulama) {
			t.Errorf("%q: %v, beklenen ErrDogrulama", ham, err)
		}
	}
}

func TestServis_YorumSil(t *testing.T) {
	ctx := context.Background()
	kayit := func() *YorumKaydi {
		return &YorumKaydi{ID: yorum1, YayinID: yayinX, YazarID: kullaniciA, YayinSahibiID: kullaniciB}
	}
	// Yazar ve yayin sahibi silebilir.
	for _, k := range []string{kullaniciA, kullaniciB} {
		d := yorumDeposu()
		d.yorumKayitlari[yorum1] = kayit()
		s, _ := testServisi(d)
		if err := s.YorumSil(ctx, k, yorum1); err != nil {
			t.Fatalf("%s silemedi: %v", k, err)
		}
		if err := s.YorumSil(ctx, k, yorum1); !errors.Is(err, ErrBulunamadi) {
			t.Fatalf("tekrar silme 404 olmali: %v", err)
		}
	}
	// Ucuncu kisi: yayini goruyorsa 403, gormuyorsa 404.
	d := yorumDeposu()
	d.yorumKayitlari[yorum1] = kayit()
	s, _ := testServisi(d)
	if err := s.YorumSil(ctx, kullaniciC, yorum1); !errors.Is(err, ErrYetki) {
		t.Fatalf("baskasi: %v, beklenen ErrYetki", err)
	}
	d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler
	if err := s.YorumSil(ctx, kullaniciC, yorum1); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorunmeyen yayin: %v, beklenen ErrBulunamadi", err)
	}
	if len(d.silinenYorumlar) != 0 {
		t.Fatal("yetkisiz silme yazilmamali")
	}
	for _, id := range []string{"x", yorumC} {
		if err := s.YorumSil(ctx, kullaniciA, id); !errors.Is(err, ErrBulunamadi) {
			t.Errorf("%s: %v, beklenen ErrBulunamadi", id, err)
		}
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -62,6 +62,13 @@ type sahteDepo struct {
 	takipler      map[[2]string]bool // {takipci, takip edilen}
 	oneriKayitlar []ProfilKaydi
 	sonOneri      [2]any // {izleyici, limit}
+
+	yorumKayitlari  map[string]*YorumKaydi
+	yorumOgeleri    []Yorum
+	eklenenYorumlar []YeniYorum
+	silinenYorumlar []string
+	sonYorumImleci  *YorumImleci
+	sonYorumLimiti  int
 }
 
 func yeniSahteDepo() *sahteDepo {
@@ -71,6 +78,8 @@ func yeniSahteDepo() *sahteDepo {
 		hazirliklar: map[string]*HazirlikKaydi{},
 		yayinlanan:  map[string]HazirlikSonucu{},
 		takipler:    map[[2]string]bool{},
+
+		yorumKayitlari: map[string]*YorumKaydi{},
 	}
 }
 
@@ -378,3 +387,49 @@ func (d *sahteDepo) Oneriler(_ context.Context, izleyiciID string, limit int) ([
 	}
 	return d.oneriKayitlar, nil
 }
+
+func (d *sahteDepo) YorumEkle(_ context.Context, y YeniYorum) (*Yorum, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	d.eklenenYorumlar = append(d.eklenenYorumlar, y)
+	p := d.profiller[y.YazarID]
+	return &Yorum{ID: y.ID, Yazar: p.ozet(), Metin: y.Metin, ZamanMs: y.ZamanMs, Olusturma: time.Now()}, nil
+}
+
+func (d *sahteDepo) Yorumlar(_ context.Context, _, _ string, imlec *YorumImleci, limit int) ([]Yorum, error) {
+	d.sonYorumImleci, d.sonYorumLimiti = imlec, limit
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	n := len(d.yorumOgeleri)
+	if n > limit+1 {
+		n = limit + 1
+	}
+	return d.yorumOgeleri[:n], nil
+}
+
+func (d *sahteDepo) YorumGetir(_ context.Context, id string) (*YorumKaydi, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	k, ok := d.yorumKayitlari[id]
+	if !ok {
+		return nil, ErrBulunamadi
+	}
+	c := *k
+	return &c, nil
+}
+
+func (d *sahteDepo) YorumSil(_ context.Context, id string) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	k, ok := d.yorumKayitlari[id]
+	if !ok || k.Silindi {
+		return ErrBulunamadi
+	}
+	k.Silindi = true
+	d.silinenYorumlar = append(d.silinenYorumlar, id)
+	return nil
+}
````

**Create** `core-service/internal/handler/sosyal_yorum_test.go`:

````go
package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

func TestSosyal_YorumYaz201VeSozlesme(t *testing.T) {
	s := &sahteSosyal{yorum: &sosyal.Yorum{
		ID: "y1", Yazar: sosyal.ProfilOzeti{KullaniciAdi: "ali", GorunenAd: "Ali", AvatarRengi: 1},
		Metin: "guzel", Olusturma: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), SilebilirMiyim: true,
	}}
	durum, ham := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"guzel","zamanMs":null}`)
	beklenen := `{"id":"y1","yazar":{"kullaniciAdi":"ali","gorunenAd":"Ali","avatarRengi":1},"metin":"guzel","zamanMs":null,"olusturma":"2026-10-12T09:00:00Z","silebilirMiyim":true}`
	if durum != http.StatusCreated || ham != beklenen {
		t.Fatalf("durum=%d govde=%s", durum, ham)
	}
	if s.sonYayin != "abc" || s.sonYorum.Metin != "guzel" || s.sonYorum.ZamanMs != nil {
		t.Fatalf("iletilen = %q %+v", s.sonYayin, s.sonYorum)
	}

	sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"an","zamanMs":1500}`)
	if s.sonYorum.ZamanMs == nil || *s.sonYorum.ZamanMs != 1500 {
		t.Fatalf("zamanMs iletilmedi: %+v", s.sonYorum)
	}
}

func TestSosyal_YorumHataKodlari(t *testing.T) {
	durumlar := []struct {
		hata  error
		durum int
		kod   string
	}{
		{sosyal.ErrYorumKapali, http.StatusForbidden, "yorum_kapali"},
		{sosyal.ErrProfilYok, http.StatusConflict, "profil_yok"},
		{sosyal.ErrBulunamadi, http.StatusNotFound, "bulunamadi"},
		{fmtDogrulama("metin"), http.StatusUnprocessableEntity, "dogrulama"},
	}
	for _, d := range durumlar {
		s := &sahteSosyal{hata: d.hata}
		durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost,
			"/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":null}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("POST %v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
	s := &sahteSosyal{hata: sosyal.ErrYorumKapali}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet, "/api/social/yayinlar/abc/yorumlar", "")
	if durum != http.StatusForbidden || hataKodu(govde) != "yorum_kapali" {
		t.Fatalf("GET: durum=%d govde=%v", durum, govde)
	}
	durum, govde = sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{}, sosyalKullanici), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":1.5}`)
	if durum != http.StatusUnprocessableEntity || hataKodu(govde) != "dogrulama" {
		t.Fatalf("kesirli zamanMs: durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_YorumlarSayfasi(t *testing.T) {
	s := &sahteSosyal{yorumSayfa: &sosyal.Sayfa[sosyal.Yorum]{Ogeler: []sosyal.Yorum{}}}
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodGet,
		"/api/social/yayinlar/abc/yorumlar?imlec=xyz", "")
	if durum != http.StatusOK || s.sonImlec != "xyz" || s.sonLimit != 20 || s.sonYayin != "abc" {
		t.Fatalf("durum=%d imlec=%q limit=%d", durum, s.sonImlec, s.sonLimit)
	}
	if v, var_ := govde["sonrakiImlec"]; !var_ || v != nil {
		t.Fatalf("sonrakiImlec null olmali: %v", govde)
	}
}

func TestSosyal_YorumSil(t *testing.T) {
	s := &sahteSosyal{}
	durum, _ := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yorumlar/y9", "")
	if durum != http.StatusNoContent || s.sonYayin != "y9" {
		t.Fatalf("durum=%d id=%q", durum, s.sonYayin)
	}
	s.hata = sosyal.ErrYetki
	durum, govde := sosyalIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodDelete, "/api/social/yorumlar/y9", "")
	if durum != http.StatusForbidden || hataKodu(govde) != "yetki" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

// Yorum limiti yalnizca POST yorumlar'a baglidir.
func TestSosyal_YorumLimitiYalnizcaYazmada(t *testing.T) {
	yavasla := func(c *fiber.Ctx) error {
		return response.Error(c, fiber.StatusTooManyRequests, "yavasla", "Biraz yavasla", nil)
	}
	s := &sahteSosyal{
		yorumSayfa:  &sosyal.Sayfa[sosyal.Yorum]{Ogeler: []sosyal.Yorum{}},
		yayinYaniti: &sosyal.YayinYaniti{},
	}
	app := sosyalUygulamasiLimitli(s, sosyalKullanici, SosyalLimitleri{Yayin: gecir, Yorum: yavasla})
	durum, govde := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":null}`)
	if durum != http.StatusTooManyRequests || hataKodu(govde) != "yavasla" {
		t.Fatalf("POST yorum: durum=%d govde=%v", durum, govde)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodGet, "/api/social/yayinlar/abc/yorumlar", ""); durum != http.StatusOK {
		t.Fatalf("GET yorumlar limitlenmemeli: %d", durum)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodDelete, "/api/social/yorumlar/y1", ""); durum != http.StatusNoContent {
		t.Fatalf("DELETE yorum limitlenmemeli: %d", durum)
	}
	if durum, _ := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); durum != http.StatusAccepted {
		t.Fatalf("yayin yorum limitine takilmamali: %d", durum)
	}
}
````

**Modify** `core-service/internal/handler/sosyal_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_test.go b/core-service/internal/handler/sosyal_test.go
--- a/core-service/internal/handler/sosyal_test.go
+++ b/core-service/internal/handler/sosyal_test.go
@@ -30,6 +30,8 @@ type sahteSosyal struct {
 	begeni      *sosyal.BegeniDurumu
 	takip       *sosyal.TakipDurumu
 	oneriler    []sosyal.ProfilOzeti
+	yorum       *sosyal.Yorum
+	yorumSayfa  *sosyal.Sayfa[sosyal.Yorum]
 
 	sonKullanici string
 	sonAd        string
@@ -43,6 +45,7 @@ type sahteSosyal struct {
 	sonLimit     int
 	sonBegen     bool
 	sonTakip     bool
+	sonYorum     sosyal.YorumGirdisi
 }
 
 func (s *sahteSosyal) ProfilBen(_ context.Context, k string) (*sosyal.Profil, error) {
@@ -125,11 +128,34 @@ func (s *sahteSosyal) Oneriler(_ context.Context, k string) ([]sosyal.ProfilOzet
 	return s.oneriler, s.hata
 }
 
-// sosyalUygulamasi, /api/social grubunu sahte servisle kurar.
+func (s *sahteSosyal) Yorumlar(_ context.Context, k, id, imlec string, limit int) (*sosyal.Sayfa[sosyal.Yorum], error) {
+	s.sonKullanici, s.sonYayin, s.sonImlec, s.sonLimit = k, id, imlec, limit
+	return s.yorumSayfa, s.hata
+}
+
+func (s *sahteSosyal) YorumYaz(_ context.Context, k, id string, g sosyal.YorumGirdisi) (*sosyal.Yorum, error) {
+	s.sonKullanici, s.sonYayin, s.sonYorum = k, id, g
+	return s.yorum, s.hata
+}
+
+func (s *sahteSosyal) YorumSil(_ context.Context, k, id string) error {
+	s.sonKullanici, s.sonYayin = k, id
+	return s.hata
+}
+
+func gecir(c *fiber.Ctx) error { return c.Next() }
+
+// sosyalUygulamasi, /api/social grubunu sahte servisle ve gecirgen
+// limitlerle kurar.
 func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
+	return sosyalUygulamasiLimitli(s, userID, SosyalLimitleri{Yayin: gecir, Yorum: gecir})
+}
+
+// sosyalUygulamasiLimitli, verilen limitlerle kurar.
+func sosyalUygulamasiLimitli(s *sahteSosyal, userID string, l SosyalLimitleri) *fiber.App {
 	app := fiber.New()
 	grup := app.Group("/api/social", kullaniciOlarak(userID))
-	NewSosyalHandler(s).Rotalar(grup, func(c *fiber.Ctx) error { return c.Next() })
+	NewSosyalHandler(s).Rotalar(grup, l)
 	return app
 }
 
````

**Modify** `core-service/internal/handler/sosyal_yayin_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_yayin_test.go b/core-service/internal/handler/sosyal_yayin_test.go
--- a/core-service/internal/handler/sosyal_yayin_test.go
+++ b/core-service/internal/handler/sosyal_yayin_test.go
@@ -54,9 +54,9 @@ func TestSosyal_YayinOlusturHataKodlari(t *testing.T) {
 func TestSosyal_YayinOlusturHizSiniri(t *testing.T) {
 	app := fiber.New()
 	grup := app.Group("/api/social", kullaniciOlarak(sosyalKullanici))
-	NewSosyalHandler(&sahteSosyal{}).Rotalar(grup, func(c *fiber.Ctx) error {
+	NewSosyalHandler(&sahteSosyal{}).Rotalar(grup, SosyalLimitleri{Yayin: func(c *fiber.Ctx) error {
 		return c.Status(http.StatusTooManyRequests).JSON(fiber.Map{"error": fiber.Map{"code": "RATE_LIMITED", "message": "x"}})
-	})
+	}, Yorum: gecir})
 	if durum, _ := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); durum != http.StatusTooManyRequests {
 		t.Fatalf("yayin limiti POST /yayinlar'a uygulanmali: %d", durum)
 	}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors such as `undefined: YorumKaydi`, `undefined: YorumImleci`, `undefined: sosyal.YorumGirdisi`, `undefined: SosyalLimitleri`.

- [ ] **Step 3: Implement**

**Create** `core-service/internal/sosyal/model_yorum.go`:

````go
package sosyal

import "time"

// Yorum, sozlesme §4.1. ZamanMs damgasiz yorumda null olarak GORUNUR.
type Yorum struct {
	ID             string      `json:"id"`
	Yazar          ProfilOzeti `json:"yazar"`
	Metin          string      `json:"metin"`
	ZamanMs        *int        `json:"zamanMs"`
	Olusturma      time.Time   `json:"olusturma"`
	SilebilirMiyim bool        `json:"silebilirMiyim"`
}

// YorumGirdisi, POST /yayinlar/{id}/yorumlar govdesi.
type YorumGirdisi struct {
	Metin   string `json:"metin"`
	ZamanMs *int   `json:"zamanMs"`
}

// YeniYorum, depoya yazilacak dogrulanmis yorum.
type YeniYorum struct {
	ID      string
	YayinID string
	YazarID string
	Metin   string
	ZamanMs *int
}

// YorumKaydi, silme kararinin ihtiyac duydugu satir.
type YorumKaydi struct {
	ID            string
	YayinID       string
	YazarID       string
	YayinSahibiID string
	Silindi       bool
}
````

**Modify** `core-service/internal/sosyal/hatalar.go`:

````diff
diff --git a/core-service/internal/sosyal/hatalar.go b/core-service/internal/sosyal/hatalar.go
--- a/core-service/internal/sosyal/hatalar.go
+++ b/core-service/internal/sosyal/hatalar.go
@@ -22,6 +22,8 @@ var (
 	ErrDurum                = errors.New("yayin bu islem icin uygun durumda degil")
 	ErrKimlik               = errors.New("kimlik gecersiz")
 	ErrKendiniTakip         = errors.New("kendini takip edemezsin")
+	ErrYorumKapali          = errors.New("yayinin yorumlari kapali")
+	ErrYetki                = errors.New("bu islem icin yetkin yok")
 )
 
 // dogrulamaHatasi, ErrDogrulama'yi alan bazli bir aciklamayla sarar.
````

**Modify** `core-service/internal/sosyal/imlec.go`:

````diff
diff --git a/core-service/internal/sosyal/imlec.go b/core-service/internal/sosyal/imlec.go
--- a/core-service/internal/sosyal/imlec.go
+++ b/core-service/internal/sosyal/imlec.go
@@ -94,3 +94,41 @@ func puanImleciCoz(ham string) (*PuanImleci, error) {
 	im.ID = id
 	return &im, nil
 }
+
+// YorumImleci, yorum listesinin konumudur. Liste iki gruptur: once
+// zamanli yorumlar (G=0; zaman_ms, olusturma, id artan), sonra damgasizlar
+// (G=1; olusturma, id azalan). Z yalnizca G=0'da doludur.
+type YorumImleci struct {
+	G  int       `json:"g"`
+	Z  *int      `json:"z,omitempty"`
+	T  time.Time `json:"t"`
+	ID string    `json:"id"`
+}
+
+// yorumImleciCoz, bos dizgede nil doner.
+func yorumImleciCoz(ham string) (*YorumImleci, error) {
+	if ham == "" {
+		return nil, nil
+	}
+	var im YorumImleci
+	if err := imlecCoz(ham, &im); err != nil {
+		return nil, err
+	}
+	id, ok := kimlikCoz(im.ID)
+	gecerli := ok && !im.T.IsZero() &&
+		((im.G == 0 && im.Z != nil && *im.Z >= 0) || (im.G == 1 && im.Z == nil))
+	if !gecerli {
+		return nil, dogrulamaHatasi("imlec gecersiz")
+	}
+	im.ID = id
+	return &im, nil
+}
+
+// yorumImleci, sayfanin son yorumundan sonraki sayfanin imlecini kurar.
+func yorumImleci(son Yorum) *string {
+	im := YorumImleci{G: 1, T: son.Olusturma, ID: son.ID}
+	if son.ZamanMs != nil {
+		im.G, im.Z = 0, son.ZamanMs
+	}
+	return imlecKodla(im)
+}
````

**Modify** `core-service/internal/sosyal/depo.go`:

````diff
diff --git a/core-service/internal/sosyal/depo.go b/core-service/internal/sosyal/depo.go
--- a/core-service/internal/sosyal/depo.go
+++ b/core-service/internal/sosyal/depo.go
@@ -34,6 +34,7 @@ type Depo interface {
 	AkisDeposu
 	BegeniDeposu
 	TakipDeposu
+	YorumDeposu
 }
 
 // PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
````

**Create** `core-service/internal/sosyal/depo_yorum.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// YorumDeposu, sosyal.yorum islemleri (spec §4.5).
type YorumDeposu interface {
	// YorumEkle, yorumu yazar ve yazarin ozetiyle doner.
	YorumEkle(ctx context.Context, y YeniYorum) (*Yorum, error)
	// Yorumlar, yayinin silinmemis yorumlarini sozlesme sirasiyla en cok
	// limit+1 tane doner. SilebilirMiyim izleyiciye goredir (yazar ya da
	// yayin sahibi).
	Yorumlar(ctx context.Context, yayinID, izleyiciID string, imlec *YorumImleci, limit int) ([]Yorum, error)
	// YorumGetir, silme karari icin yorumu doner; yoksa ErrBulunamadi.
	YorumGetir(ctx context.Context, id string) (*YorumKaydi, error)
	// YorumSil, yumusak siler. Yoksa ya da zaten silinmisse ErrBulunamadi.
	YorumSil(ctx context.Context, id string) error
}

// yorumKolonlari, Yorum'un kolonlaridir: c yorum, p yazar profili.
const yorumKolonlari = `c.id::text, c.metin, c.zaman_ms, c.olusturma, c.yazar_id::text, p.kullanici_adi, p.gorunen_ad`

func yorumTara(satir pgx.Row, ekler ...any) (*Yorum, error) {
	var (
		y       Yorum
		yazarID string
	)
	hedefler := append([]any{&y.ID, &y.Metin, &y.ZamanMs, &y.Olusturma, &yazarID,
		&y.Yazar.KullaniciAdi, &y.Yazar.GorunenAd}, ekler...)
	if err := satir.Scan(hedefler...); err != nil {
		return nil, err
	}
	y.Yazar.AvatarRengi = AvatarRengi(yazarID)
	y.Olusturma = y.Olusturma.UTC()
	return &y, nil
}

func (d *PostgresDepo) YorumEkle(ctx context.Context, y YeniYorum) (*Yorum, error) {
	yorum, err := yorumTara(d.havuz.QueryRow(ctx, `
		WITH c AS (
		  INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, zaman_ms)
		  VALUES ($1, $2, $3, $4, $5)
		  RETURNING *
		)
		SELECT `+yorumKolonlari+` FROM c JOIN sosyal.profil p ON p.kullanici_id = c.yazar_id`,
		y.ID, y.YayinID, y.YazarID, y.Metin, y.ZamanMs))
	if err != nil {
		return nil, fmt.Errorf("yorum eklenemedi: %w", err)
	}
	return yorum, nil
}

// Yorumlar, iki grubu tek sorguda siralar: zaman_ms dolu olanlar once
// (zaman_ms, olusturma, id artan), damgasizlar sonra (olusturma, id
// azalan). Imlec grubu G=0 ise kalan zamanlilar ve tum damgasizlar,
// G=1 ise yalnizca kalan damgasizlar gelir.
func (d *PostgresDepo) Yorumlar(ctx context.Context, yayinID, izleyiciID string, imlec *YorumImleci, limit int) ([]Yorum, error) {
	var grup, z, t, id any
	if imlec != nil {
		grup, t, id = imlec.G, imlec.T, imlec.ID
		if imlec.Z != nil {
			z = *imlec.Z
		}
	}
	satirlar, err := d.havuz.Query(ctx, `
		SELECT `+yorumKolonlari+`, (c.yazar_id = $2 OR y.sahip_id = $2)
		  FROM sosyal.yorum c
		  JOIN sosyal.yayin y ON y.id = c.yayin_id
		  JOIN sosyal.profil p ON p.kullanici_id = c.yazar_id
		 WHERE c.yayin_id = $1 AND c.silinme IS NULL
		   AND ($3::int IS NULL
		        OR ($3::int = 0 AND (c.zaman_ms IS NULL
		            OR (c.zaman_ms, c.olusturma, c.id) > ($4::int, $5::timestamptz, $6::uuid)))
		        OR ($3::int = 1 AND c.zaman_ms IS NULL
		            AND (c.olusturma, c.id) < ($5::timestamptz, $6::uuid)))
		 ORDER BY (c.zaman_ms IS NULL), c.zaman_ms,
		          CASE WHEN c.zaman_ms IS NULL THEN c.olusturma END DESC,
		          CASE WHEN c.zaman_ms IS NULL THEN c.id END DESC,
		          c.olusturma, c.id
		 LIMIT $7`, yayinID, izleyiciID, grup, z, t, id, limit+1)
	if err != nil {
		return nil, fmt.Errorf("yorumlar okunamadi: %w", err)
	}
	defer satirlar.Close()
	yorumlar := []Yorum{}
	for satirlar.Next() {
		var silebilir bool
		y, err := yorumTara(satirlar, &silebilir)
		if err != nil {
			return nil, fmt.Errorf("yorum okunamadi: %w", err)
		}
		y.SilebilirMiyim = silebilir
		yorumlar = append(yorumlar, *y)
	}
	return yorumlar, satirlar.Err()
}

func (d *PostgresDepo) YorumGetir(ctx context.Context, id string) (*YorumKaydi, error) {
	var k YorumKaydi
	err := d.havuz.QueryRow(ctx, `
		SELECT c.id::text, c.yayin_id::text, c.yazar_id::text, y.sahip_id::text, c.silinme IS NOT NULL
		  FROM sosyal.yorum c JOIN sosyal.yayin y ON y.id = c.yayin_id
		 WHERE c.id = $1`, id).Scan(&k.ID, &k.YayinID, &k.YazarID, &k.YayinSahibiID, &k.Silindi)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("yorum okunamadi: %w", err)
	}
	return &k, nil
}

func (d *PostgresDepo) YorumSil(ctx context.Context, id string) error {
	sonuc, err := d.havuz.Exec(ctx,
		`UPDATE sosyal.yorum SET silinme = now() WHERE id = $1 AND silinme IS NULL`, id)
	if err != nil {
		return fmt.Errorf("yorum silinemedi: %w", err)
	}
	if sonuc.RowsAffected() == 0 {
		return ErrBulunamadi
	}
	return nil
}
````

**Create** `core-service/internal/sosyal/servis_yorum.go`:

````go
package sosyal

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const yorumEnUzun = 500

// yorumSayfasiKur, limit+1 yorumdan sayfayi ve sonraki imleci kurar.
func yorumSayfasiKur(ogeler []Yorum, limit int) *Sayfa[Yorum] {
	sayfa := &Sayfa[Yorum]{Ogeler: ogeler}
	if len(ogeler) > limit {
		sayfa.Ogeler = ogeler[:limit]
		sayfa.SonrakiImlec = yorumImleci(sayfa.Ogeler[limit-1])
	}
	if sayfa.Ogeler == nil {
		sayfa.Ogeler = []Yorum{}
	}
	return sayfa
}

// Yorumlar, GET /yayinlar/{id}/yorumlar. Yayin gorunmuyorsa
// ErrBulunamadi, yorumlari kapaliysa (sahibi dahil) ErrYorumKapali.
func (s *Servis) Yorumlar(ctx context.Context, kullaniciID, yayinID, hamImlec string, limit int) (*Sayfa[Yorum], error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	imlec, err := yorumImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if !k.YorumAcik {
		return nil, ErrYorumKapali
	}
	ogeler, err := s.depo.Yorumlar(ctx, k.ID, id, imlec, limit)
	if err != nil {
		return nil, err
	}
	return yorumSayfasiKur(ogeler, limit), nil
}

// YorumYaz, POST /yayinlar/{id}/yorumlar (201). Sira: govde (422),
// gorunurluk (404), yorumAcik (403 yorum_kapali), profil (409
// profil_yok), zaman araligi (422). metin kirpilir, 1-500 karakter;
// zamanMs null ya da 0 <= zamanMs <= sureSn*1000.
func (s *Servis) YorumYaz(ctx context.Context, kullaniciID, yayinID string, g YorumGirdisi) (*Yorum, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	metin := strings.TrimSpace(g.Metin)
	if n := utf8.RuneCountInString(metin); n < 1 || n > yorumEnUzun {
		return nil, dogrulamaHatasi("metin 1-500 karakter olmali")
	}
	if g.ZamanMs != nil && *g.ZamanMs < 0 {
		return nil, dogrulamaHatasi("zamanMs negatif olamaz")
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if !k.YorumAcik {
		return nil, ErrYorumKapali
	}
	if _, err := s.depo.ProfilGetir(ctx, id); err != nil {
		return nil, err
	}
	if g.ZamanMs != nil && *g.ZamanMs > k.SureSn*1000 {
		return nil, dogrulamaHatasi("zamanMs sarkinin suresini asiyor")
	}
	y, err := s.depo.YorumEkle(ctx, YeniYorum{
		ID: uuid.NewString(), YayinID: k.ID, YazarID: id, Metin: metin, ZamanMs: g.ZamanMs,
	})
	if err != nil {
		return nil, err
	}
	y.SilebilirMiyim = true
	return y, nil
}

// YorumSil, DELETE /yorumlar/{id} (204). Yalnizca yazar ya da yayin
// sahibi siler; baska biri yayini gorebiliyorsa ErrYetki (403),
// goremiyorsa ErrBulunamadi (varligi sizdirilmaz). Silinmis yorum
// ErrBulunamadi'dir (tekrar 404).
func (s *Servis) YorumSil(ctx context.Context, kullaniciID, yorumID string) error {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return err
	}
	yid, ok := kimlikCoz(yorumID)
	if !ok {
		return ErrBulunamadi
	}
	k, err := s.depo.YorumGetir(ctx, yid)
	if err != nil {
		return err
	}
	if k.Silindi {
		return ErrBulunamadi
	}
	if k.YazarID != id && k.YayinSahibiID != id {
		if _, err := s.gorunurYayin(ctx, id, k.YayinID); err != nil {
			return err
		}
		return ErrYetki
	}
	return s.depo.YorumSil(ctx, k.ID)
}
````

**Modify** `core-service/internal/handler/sosyal.go`:

````diff
diff --git a/core-service/internal/handler/sosyal.go b/core-service/internal/handler/sosyal.go
--- a/core-service/internal/handler/sosyal.go
+++ b/core-service/internal/handler/sosyal.go
@@ -33,6 +33,10 @@ type SosyalServisi interface {
 
 	Takip(ctx context.Context, kullaniciID, ad string, takip bool) (*sosyal.TakipDurumu, error)
 	Oneriler(ctx context.Context, kullaniciID string) ([]sosyal.ProfilOzeti, error)
+
+	Yorumlar(ctx context.Context, kullaniciID, yayinID, imlec string, limit int) (*sosyal.Sayfa[sosyal.Yorum], error)
+	YorumYaz(ctx context.Context, kullaniciID, yayinID string, g sosyal.YorumGirdisi) (*sosyal.Yorum, error)
+	YorumSil(ctx context.Context, kullaniciID, yorumID string) error
 }
 
 var _ SosyalServisi = (*sosyal.Servis)(nil)
@@ -46,9 +50,16 @@ func NewSosyalHandler(servis SosyalServisi) *SosyalHandler {
 	return &SosyalHandler{servis: servis}
 }
 
-// Rotalar, uclari verilen gruba baglar. yayinLimiti yalnizca
-// POST /yayinlar'a uygulanir (kullanici basina saatlik).
-func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
+// SosyalLimitleri, tek tek uclara uygulanan kullanici basina limitlerdir.
+// Hepsi doldurulmalidir.
+type SosyalLimitleri struct {
+	Yayin fiber.Handler // POST /yayinlar (saatlik)
+	Yorum fiber.Handler // POST /yayinlar/:id/yorumlar (dakikalik, 429 yavasla)
+}
+
+// Rotalar, uclari verilen gruba baglar; limitler yalnizca ilgili uclara
+// uygulanir.
+func (h *SosyalHandler) Rotalar(r fiber.Router, l SosyalLimitleri) {
 	r.Get("/profil/ben", h.ProfilBen)
 	r.Put("/profil/ben", h.ProfilKaydet)
 	r.Get("/profil/kullanici-adi/uygun", h.AdUygun)
@@ -56,7 +67,7 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
 	r.Get("/profiller/:ad/yayinlar", h.ProfilYayinlari)
 	r.Get("/akis", h.Akis)
 
-	r.Post("/yayinlar", yayinLimiti, h.YayinOlustur)
+	r.Post("/yayinlar", l.Yayin, h.YayinOlustur)
 	r.Get("/yayinlar/:id", h.Yayin)
 	r.Patch("/yayinlar/:id", h.YayinGuncelle)
 	r.Delete("/yayinlar/:id", h.YayinSil)
@@ -69,6 +80,10 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, yayinLimiti fiber.Handler) {
 	r.Put("/profiller/:ad/takip", h.TakipEt)
 	r.Delete("/profiller/:ad/takip", h.TakibiBirak)
 	r.Get("/oneriler", h.Oneriler)
+
+	r.Get("/yayinlar/:id/yorumlar", h.Yorumlar)
+	r.Post("/yayinlar/:id/yorumlar", l.Yorum, h.YorumYaz)
+	r.Delete("/yorumlar/:id", h.YorumSil)
 }
 
 // sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
@@ -89,6 +104,8 @@ var sosyalHatalari = []struct {
 	{sosyal.ErrStemYok, fiber.StatusUnprocessableEntity, "stem_yok"},
 	{sosyal.ErrDurum, fiber.StatusConflict, "durum"},
 	{sosyal.ErrKendiniTakip, fiber.StatusUnprocessableEntity, "kendini_takip"},
+	{sosyal.ErrYorumKapali, fiber.StatusForbidden, "yorum_kapali"},
+	{sosyal.ErrYetki, fiber.StatusForbidden, "yetki"},
 }
 
 // sosyalHata, servis hatasini sozlesme govdesine cevirir:
````

**Create** `core-service/internal/handler/sosyal_yorum.go`:

````go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// Yorumlar handles GET /api/social/yayinlar/:id/yorumlar (sayfali).
func (h *SosyalHandler) Yorumlar(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.Yorumlar(c.Context(), userID, c.Params("id"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// YorumYaz handles POST /api/social/yayinlar/:id/yorumlar (201 Yorum).
// Kullanici basina dakikalik limit (429 yavasla) rotada uygulanir.
func (h *SosyalHandler) YorumYaz(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var g sosyal.YorumGirdisi
	if err := c.BodyParser(&g); err != nil {
		return govdeHatasi(c)
	}
	y, err := h.servis.YorumYaz(c.Context(), userID, c.Params("id"), g)
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.Created(c, y)
}

// YorumSil handles DELETE /api/social/yorumlar/:id (204).
func (h *SosyalHandler) YorumSil(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	if err := h.servis.YorumSil(c.Context(), userID, c.Params("id")); err != nil {
		return sosyalHata(c, err)
	}
	return response.NoContent(c)
}
````

**Modify** `core-service/cmd/server/main.go`:

````diff
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -299,9 +299,13 @@ func main() {
 
 	api.Delete("/account", hesapHandler.Sil)
 
-	// Kesfet (sosyal) uclari. Yayin limiti yalnizca POST /yayinlar'dadir.
-	sosyalHandler.Rotalar(api.Group("/social"),
-		rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SosyalYayinPerHour, time.Hour))
+	// Kesfet (sosyal) uclari. Limitler yalnizca ilgili uclardadir; yorum
+	// limiti sozlesme geregi 429 yavasla doner.
+	sosyalHandler.Rotalar(api.Group("/social"), handler.SosyalLimitleri{
+		Yayin: rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SosyalYayinPerHour, time.Hour),
+		Yorum: rateLimiter.LimitKod("sosyal-yorum", cfg.RateLimit.SosyalYorumPerMin, time.Minute,
+			"yavasla", "Biraz yavasla"),
+	})
 
 	// WebSocket routes
 	app.Use("/ws", func(c *fiber.Ctx) error {
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... && go test ./internal/... -count=1
```

Expected: no gofmt output, `0 issues.`, every package `ok`; `TestPostgresDepo_YorumSirasiVeSayfalama` PASS (not SKIP). Whole Go suite after this task: 706 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/depo_yorum_test.go \
  core-service/internal/sosyal/servis_yorum_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/handler/sosyal_yorum_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/handler/sosyal_yayin_test.go \
  core-service/internal/sosyal/model_yorum.go \
  core-service/internal/sosyal/hatalar.go \
  core-service/internal/sosyal/imlec.go \
  core-service/internal/sosyal/depo.go \
  core-service/internal/sosyal/depo_yorum.go \
  core-service/internal/sosyal/servis_yorum.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_yorum.go \
  core-service/cmd/server/main.go
git commit -F - <<'EOF'
feat(sosyal): yorumlar, iki gruplu siralama ve yorum limiti

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 5: Reports (§4.7)

C3, P10. `ON CONFLICT (bildiren_id, hedef_turu, hedef_id) DO NOTHING` makes a repeat a no-op, so the handler always answers 201. `yayin_id` (N1) is the target's publication, which is also what the visibility check uses.

**Files:**

- Create: `core-service/internal/sosyal/depo_sikayet_test.go`
- Create: `core-service/internal/sosyal/servis_sikayet_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go` (fake `SikayetDeposu`)
- Create: `core-service/internal/handler/sosyal_sikayet_test.go`
- Modify: `core-service/internal/handler/sosyal_test.go` (fake service method)
- Modify: `core-service/internal/sosyal/depo.go`
- Create: `core-service/internal/sosyal/depo_sikayet.go`
- Create: `core-service/internal/sosyal/servis_sikayet.go`
- Modify: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_sikayet.go`

**Interfaces:**
- Consumes: `YorumGetir` (Task 4), `gorunurYayin`, `kimlikCoz`, `dogrulamaHatasi` (5a); fixtures `testYorumEkle`, `yorumDeposu`, `kullaniciC`, `yorum1`, `yorumC` (Task 4).
- Produces: `HedefYayin`, `HedefYorum`; `type SikayetGirdisi struct { HedefTuru, HedefID, Neden, Aciklama string }`; `type YeniSikayet`; `type SikayetDeposu interface { SikayetEkle(ctx, YeniSikayet) error }`; `(*Servis).Sikayet(ctx, kullaniciID string, g SikayetGirdisi) error`; handler `Sikayet`; route `POST /sikayetler` (201, empty body).

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/depo_sikayet_test.go`:

````go
package sosyal

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresDepo_SikayetTekrariYeniKayitAcmaz(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, bildiren := uuid.NewString(), uuid.NewString()
	yayin := testYayinEkle(t, havuz, testYayin{SahipID: sahip})
	yorum := testYorumEkle(t, havuz, "", yayin, sahip, nil, time.Now())

	yeni := func(tur, hedef, neden string) YeniSikayet {
		return YeniSikayet{ID: uuid.NewString(), BildirenID: bildiren, HedefTuru: tur, HedefID: hedef, YayinID: yayin, Neden: neden}
	}
	for _, s := range []YeniSikayet{
		yeni(HedefYayin, yayin, "spam"),
		yeni(HedefYayin, yayin, "telif"), // tekrar: ilk kayit kalir
		yeni(HedefYorum, yorum, "taciz"),
	} {
		if err := d.SikayetEkle(ctx, s); err != nil {
			t.Fatalf("SikayetEkle: %v", err)
		}
	}
	var n int
	var neden string
	if err := havuz.QueryRow(ctx, `SELECT count(*), min(neden) FILTER (WHERE hedef_turu = 'yayin')
		FROM sosyal.sikayet WHERE bildiren_id = $1 AND yayin_id = $2`, bildiren, yayin).Scan(&n, &neden); err != nil {
		t.Fatal(err)
	}
	if n != 2 || neden != "spam" {
		t.Fatalf("kayit sayisi %d, yayin nedeni %q; beklenen 2 ve ilk neden spam", n, neden)
	}

	// Yayin kalici silinince sikayetleri cascade ile gider.
	if err := d.YayinKaldir(ctx, yayin); err != nil {
		t.Fatal(err)
	}
	if err := havuz.QueryRow(ctx, `SELECT count(*) FROM sosyal.sikayet WHERE bildiren_id = $1`, bildiren).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade sonrasi %d sikayet, %v", n, err)
	}
}
````

**Create** `core-service/internal/sosyal/servis_sikayet_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestServis_SikayetYayinVeYorum(t *testing.T) {
	d := yorumDeposu()
	d.yorumKayitlari[yorum1] = &YorumKaydi{ID: yorum1, YayinID: yayinX, YazarID: kullaniciB, YayinSahibiID: kullaniciB}
	s, _ := testServisi(d)
	ctx := context.Background()

	if err := s.Sikayet(ctx, kullaniciC, SikayetGirdisi{HedefTuru: "yayin", HedefID: strings.ToUpper(yayinX), Neden: "spam"}); err != nil {
		t.Fatalf("yayin sikayeti: %v", err)
	}
	if err := s.Sikayet(ctx, kullaniciC, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorum1, Neden: "taciz", Aciklama: "  kaba  "}); err != nil {
		t.Fatalf("yorum sikayeti: %v", err)
	}
	if len(d.sikayetler) != 2 {
		t.Fatalf("sikayetler = %+v", d.sikayetler)
	}
	y, c := d.sikayetler[0], d.sikayetler[1]
	if y.BildirenID != kullaniciC || y.HedefID != yayinX || y.YayinID != yayinX || y.Neden != "spam" || y.ID == "" {
		t.Fatalf("yayin sikayeti = %+v", y)
	}
	if c.HedefTuru != "yorum" || c.HedefID != yorum1 || c.YayinID != yayinX || c.Aciklama != "kaba" {
		t.Fatalf("yorum sikayeti = %+v", c)
	}
}

func TestServis_SikayetHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad       string
		hazirla  func(d *sahteDepo)
		girdi    SikayetGirdisi
		beklenen error
	}{
		{"bilinmeyen hedef turu", nil, SikayetGirdisi{HedefTuru: "profil", HedefID: yayinX, Neden: "spam"}, ErrDogrulama},
		{"bilinmeyen neden", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinX, Neden: "kotu"}, ErrDogrulama},
		{"uzun aciklama", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinX, Neden: "diger", Aciklama: strings.Repeat("a", 301)}, ErrDogrulama},
		{"bozuk kimlik", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: "x", Neden: "spam"}, ErrBulunamadi},
		{"olmayan yayin", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinY, Neden: "spam"}, ErrBulunamadi},
		{"gorunmeyen yayin", func(d *sahteDepo) { d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler },
			SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinX, Neden: "spam"}, ErrBulunamadi},
		{"olmayan yorum", nil, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorumC, Neden: "spam"}, ErrBulunamadi},
		{"silinmis yorum", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &YorumKaydi{ID: yorum1, YayinID: yayinX, Silindi: true}
		}, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorum1, Neden: "spam"}, ErrBulunamadi},
		{"gorunmeyen yayinin yorumu", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &YorumKaydi{ID: yorum1, YayinID: yayinX}
			d.yayinlar[yayinX].Silindi = true
		}, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorum1, Neden: "spam"}, ErrBulunamadi},
	}
	for _, c := range durumlar {
		d := yorumDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		if err := s.Sikayet(ctx, kullaniciC, c.girdi); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(d.sikayetler) != 0 {
			t.Errorf("%s: sikayet yazilmamali", c.ad)
		}
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -69,6 +69,8 @@ type sahteDepo struct {
 	silinenYorumlar []string
 	sonYorumImleci  *YorumImleci
 	sonYorumLimiti  int
+
+	sikayetler []YeniSikayet
 }
 
 func yeniSahteDepo() *sahteDepo {
@@ -433,3 +435,11 @@ func (d *sahteDepo) YorumSil(_ context.Context, id string) error {
 	d.silinenYorumlar = append(d.silinenYorumlar, id)
 	return nil
 }
+
+func (d *sahteDepo) SikayetEkle(_ context.Context, s YeniSikayet) error {
+	if d.hata != nil {
+		return d.hata
+	}
+	d.sikayetler = append(d.sikayetler, s)
+	return nil
+}
````

**Create** `core-service/internal/handler/sosyal_sikayet_test.go`:

````go
package handler

import (
	"net/http"
	"testing"

	"github.com/makeasinger/api/internal/sosyal"
)

func TestSosyal_Sikayet201Govdesiz(t *testing.T) {
	s := &sahteSosyal{}
	durum, ham := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/sikayetler",
		`{"hedefTuru":"yorum","hedefId":"c1","neden":"spam","aciklama":"x"}`)
	if durum != http.StatusCreated || ham != "" {
		t.Fatalf("durum=%d govde=%q", durum, ham)
	}
	if s.sonSikayet != (sosyal.SikayetGirdisi{HedefTuru: "yorum", HedefID: "c1", Neden: "spam", Aciklama: "x"}) {
		t.Fatalf("iletilen = %+v", s.sonSikayet)
	}
	// aciklama istege bagli.
	if durum, _ := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/sikayetler",
		`{"hedefTuru":"yayin","hedefId":"y1","neden":"diger"}`); durum != http.StatusCreated || s.sonSikayet.Aciklama != "" {
		t.Fatalf("aciklamasiz: durum=%d %+v", durum, s.sonSikayet)
	}
}

func TestSosyal_SikayetHatalari(t *testing.T) {
	for _, d := range []struct {
		hata  error
		durum int
		kod   string
	}{
		{sosyal.ErrBulunamadi, http.StatusNotFound, "bulunamadi"},
		{fmtDogrulama("neden"), http.StatusUnprocessableEntity, "dogrulama"},
	} {
		durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: d.hata}, sosyalKullanici), http.MethodPost,
			"/api/social/sikayetler", `{"hedefTuru":"yayin","hedefId":"y1","neden":"spam"}`)
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}
````

**Modify** `core-service/internal/handler/sosyal_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_test.go b/core-service/internal/handler/sosyal_test.go
--- a/core-service/internal/handler/sosyal_test.go
+++ b/core-service/internal/handler/sosyal_test.go
@@ -46,6 +46,7 @@ type sahteSosyal struct {
 	sonBegen     bool
 	sonTakip     bool
 	sonYorum     sosyal.YorumGirdisi
+	sonSikayet   sosyal.SikayetGirdisi
 }
 
 func (s *sahteSosyal) ProfilBen(_ context.Context, k string) (*sosyal.Profil, error) {
@@ -143,6 +144,11 @@ func (s *sahteSosyal) YorumSil(_ context.Context, k, id string) error {
 	return s.hata
 }
 
+func (s *sahteSosyal) Sikayet(_ context.Context, k string, g sosyal.SikayetGirdisi) error {
+	s.sonKullanici, s.sonSikayet = k, g
+	return s.hata
+}
+
 func gecir(c *fiber.Ctx) error { return c.Next() }
 
 // sosyalUygulamasi, /api/social grubunu sahte servisle ve gecirgen
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors `undefined: YeniSikayet`, `undefined: SikayetGirdisi`, `undefined: sosyal.SikayetGirdisi`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/sosyal/depo.go`:

````diff
diff --git a/core-service/internal/sosyal/depo.go b/core-service/internal/sosyal/depo.go
--- a/core-service/internal/sosyal/depo.go
+++ b/core-service/internal/sosyal/depo.go
@@ -35,6 +35,7 @@ type Depo interface {
 	BegeniDeposu
 	TakipDeposu
 	YorumDeposu
+	SikayetDeposu
 }
 
 // PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
````

**Create** `core-service/internal/sosyal/depo_sikayet.go`:

````go
package sosyal

import (
	"context"
	"fmt"
)

// SikayetDeposu, sosyal.sikayet islemleri (spec §4.7).
type SikayetDeposu interface {
	// SikayetEkle, sikayeti yazar. Ayni bildirenin ayni hedefe tekrari
	// yeni kayit acmaz ve hata da degildir.
	SikayetEkle(ctx context.Context, s YeniSikayet) error
}

func (d *PostgresDepo) SikayetEkle(ctx context.Context, s YeniSikayet) error {
	if _, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden, aciklama)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bildiren_id, hedef_turu, hedef_id) DO NOTHING`,
		s.ID, s.BildirenID, s.HedefTuru, s.HedefID, s.YayinID, s.Neden, s.Aciklama); err != nil {
		return fmt.Errorf("sikayet eklenemedi: %w", err)
	}
	return nil
}
````

**Create** `core-service/internal/sosyal/servis_sikayet.go`:

````go
package sosyal

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Sikayet hedefleri ve nedenleri (spec §3 sikayet).
const (
	HedefYayin = "yayin"
	HedefYorum = "yorum"

	sikayetAciklamaEnUzun = 300
)

var sikayetNedenleri = map[string]bool{
	"spam": true, "taciz": true, "telif": true, "uygunsuz": true, "diger": true,
}

// SikayetGirdisi, POST /sikayetler govdesi. aciklama istege baglidir.
type SikayetGirdisi struct {
	HedefTuru string `json:"hedefTuru"`
	HedefID   string `json:"hedefId"`
	Neden     string `json:"neden"`
	Aciklama  string `json:"aciklama"`
}

// YeniSikayet, depoya yazilacak dogrulanmis sikayet. YayinID hedefin ait
// oldugu yayindir (hedef yorumsa yorumun yayini).
type YeniSikayet struct {
	ID         string
	BildirenID string
	HedefTuru  string
	HedefID    string
	YayinID    string
	Neden      string
	Aciklama   string
}

// Sikayet, POST /sikayetler (201). Hedef bildirene gorunur olmalidir:
// yayin detay kuralina gore, yorum silinmemis ve yayini gorunur. Aksi ya
// da bozuk kimlik ErrBulunamadi'dir. Tekrar sikayet hata degildir.
// Profil gerekmez; kendi icerigini sikayet etmek reddedilmez.
func (s *Servis) Sikayet(ctx context.Context, kullaniciID string, g SikayetGirdisi) error {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return err
	}
	if g.HedefTuru != HedefYayin && g.HedefTuru != HedefYorum {
		return dogrulamaHatasi("hedefTuru yayin ya da yorum olmali")
	}
	if !sikayetNedenleri[g.Neden] {
		return dogrulamaHatasi("neden spam, taciz, telif, uygunsuz ya da diger olmali")
	}
	aciklama := strings.TrimSpace(g.Aciklama)
	if utf8.RuneCountInString(aciklama) > sikayetAciklamaEnUzun {
		return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
	}
	hedefID, ok := kimlikCoz(g.HedefID)
	if !ok {
		return ErrBulunamadi
	}

	yayinID := hedefID
	if g.HedefTuru == HedefYorum {
		y, err := s.depo.YorumGetir(ctx, hedefID)
		if err != nil {
			return err
		}
		if y.Silindi {
			return ErrBulunamadi
		}
		yayinID = y.YayinID
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return err
	}
	return s.depo.SikayetEkle(ctx, YeniSikayet{
		ID: uuid.NewString(), BildirenID: id, HedefTuru: g.HedefTuru, HedefID: hedefID,
		YayinID: k.ID, Neden: g.Neden, Aciklama: aciklama,
	})
}
````

**Modify** `core-service/internal/handler/sosyal.go`:

````diff
diff --git a/core-service/internal/handler/sosyal.go b/core-service/internal/handler/sosyal.go
--- a/core-service/internal/handler/sosyal.go
+++ b/core-service/internal/handler/sosyal.go
@@ -37,6 +37,8 @@ type SosyalServisi interface {
 	Yorumlar(ctx context.Context, kullaniciID, yayinID, imlec string, limit int) (*sosyal.Sayfa[sosyal.Yorum], error)
 	YorumYaz(ctx context.Context, kullaniciID, yayinID string, g sosyal.YorumGirdisi) (*sosyal.Yorum, error)
 	YorumSil(ctx context.Context, kullaniciID, yorumID string) error
+
+	Sikayet(ctx context.Context, kullaniciID string, g sosyal.SikayetGirdisi) error
 }
 
 var _ SosyalServisi = (*sosyal.Servis)(nil)
@@ -84,6 +86,8 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, l SosyalLimitleri) {
 	r.Get("/yayinlar/:id/yorumlar", h.Yorumlar)
 	r.Post("/yayinlar/:id/yorumlar", l.Yorum, h.YorumYaz)
 	r.Delete("/yorumlar/:id", h.YorumSil)
+
+	r.Post("/sikayetler", h.Sikayet)
 }
 
 // sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
````

**Create** `core-service/internal/handler/sosyal_sikayet.go`:

````go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// Sikayet handles POST /api/social/sikayetler. 201, govdesiz; tekrar da
// 201'dir (yeni kayit acilmaz).
func (h *SosyalHandler) Sikayet(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var g sosyal.SikayetGirdisi
	if err := c.BodyParser(&g); err != nil {
		return govdeHatasi(c)
	}
	if err := h.servis.Sikayet(c.Context(), userID, g); err != nil {
		return sosyalHata(c, err)
	}
	// SendStatus bos govdeye "Created" yazardi; govde bilerek bos kalir.
	c.Status(fiber.StatusCreated)
	return nil
}
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... && go test ./internal/... -count=1
```

Expected: no gofmt output, `0 issues.`, every package `ok`. Whole Go suite after this task: 711 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/depo_sikayet_test.go \
  core-service/internal/sosyal/servis_sikayet_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/handler/sosyal_sikayet_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/sosyal/depo.go \
  core-service/internal/sosyal/depo_sikayet.go \
  core-service/internal/sosyal/servis_sikayet.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_sikayet.go
git commit -F - <<'EOF'
feat(sosyal): sikayetler, tekrar 201

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 6: Cover list and cover start (§4.6)

C4, P11, P12. `CoverBaslat` loads `stem_listesi` (5a N2: `{id, enstruman, sure, anahtar}`), copies each `anahtar` to `stems/{caller}/{uuid}/{stemId}{ext}` through `client.Kopyalayici` and resolves each target with `client.ResolveURL`. `main.go` now builds the copier before the handler and passes it with `KopyalayiciAyarla`, and adds the cover limiter.

**Files:**

- Create: `core-service/internal/sosyal/depo_cover_test.go`
- Create: `core-service/internal/sosyal/servis_cover_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go` (fake `CoverDeposu`)
- Create: `core-service/internal/handler/sosyal_cover_test.go`
- Modify: `core-service/internal/handler/sosyal_test.go` (fake service methods, `Cover` limiter)
- Modify: `core-service/internal/handler/sosyal_yayin_test.go` (`Cover: gecir`)
- Modify: `core-service/internal/handler/sosyal_yorum_test.go` (`Cover: gecir`)
- Modify: `core-service/internal/sosyal/depo.go`
- Create: `core-service/internal/sosyal/depo_cover.go`
- Modify: `core-service/internal/sosyal/servis.go`
- Create: `core-service/internal/sosyal/servis_cover.go`
- Modify: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_cover.go`
- Modify: `core-service/cmd/server/main.go`

**Interfaces:**
- Consumes: `client.Kopyalayici`, `client.KullaniciAnahtari`, `client.ResolveURL` (existing/5a); `zamanSayfasi`, `zamanSayfasiKur`, `zamanImleciCoz`, `gorunurlukKosulu`, `uzanti`, `StemKaydi`, `gorunurYayin` (5a); `RateLimit.SosyalCoverPerHour` (Task 2).
- Produces: `type CoverDeposu interface { Coverlar(ctx, kaynakID, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error); StemListesi(ctx, yayinID string) ([]StemKaydi, error) }`; `type CoverStemi`, `CoverBaslangici`; `CoverStemHedefi(kullaniciID, klasor, stemID, kaynak string) (string, error)`; `(*Servis).KopyalayiciAyarla(client.Kopyalayici) *Servis`, `Coverlar(ctx, kullaniciID, yayinID, imlec string, limit int) (*Sayfa[YayinOzeti], error)`, `CoverBaslat(ctx, kullaniciID, yayinID string) (*CoverBaslangici, error)`; `SosyalLimitleri.Cover`; routes `GET /yayinlar/:id/coverlar`, `POST /yayinlar/:id/cover-baslat`.

- [ ] **Step 1: Write the failing tests**

`imzaliDepolama` (5a, `servis_yayin_test.go`) checks the signed-URL path; `ozetler` (5a, `servis_akis_test.go`) and `akisKurulumu`/`tumSayfalar` (5a, `depo_akis_test.go`) are reused.

**Create** `core-service/internal/sosyal/depo_cover_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresDepo_CoverlarGorunurlukVeSira(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	kaynak := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, AltyapiAcik: true})
	baska := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, AltyapiAcik: true})

	c1 := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Olusturma: t0.Add(4 * time.Minute)})
	c2 := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, KaynakID: kaynak, Gorunurluk: GorunurlukTakipciler, Olusturma: t0.Add(3 * time.Minute)})
	c3 := testYayinEkle(t, havuz, testYayin{SahipID: ben, KaynakID: kaynak, Gorunurluk: GorunurlukLink, Olusturma: t0.Add(2 * time.Minute)})
	c4 := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Olusturma: t0.Add(time.Minute)})
	// Girmeyenler: yabancinin takipciler ve link cover'i, hazirlanan
	// (sahibine bile), silinmis, baska kaynagin cover'i.
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Gorunurluk: GorunurlukTakipciler})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: ben, KaynakID: kaynak, Durum: DurumHazirlaniyor})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Silindi: true})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: baska})

	cek := func(izleyici string) func(*ZamanImleci) ([]YayinOzeti, error) {
		return func(im *ZamanImleci) ([]YayinOzeti, error) { return d.Coverlar(ctx, kaynak, izleyici, im, 2) }
	}
	esitSira(t, "ben", tumSayfalar(t, cek(ben), 2), []string{c1, c2, c3, c4})
	esitSira(t, "yabanci izleyici", tumSayfalar(t, cek(uuid.NewString()), 2), []string{c1, c4})

	// Kaynak ozeti cover'larda doludur.
	ogeler, err := d.Coverlar(ctx, kaynak, ben, nil, 1)
	if err != nil || ogeler[0].Kaynak == nil || ogeler[0].Kaynak.YayinID != kaynak || ogeler[0].Kaynak.Silindi {
		t.Fatalf("kaynak ozeti = %+v, %v", ogeler, err)
	}
}

func TestPostgresDepo_StemListesi(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	id := testYayinEkle(t, havuz, testYayin{SahipID: uuid.NewString(), AltyapiAcik: true})

	bos, err := d.StemListesi(ctx, id)
	if err != nil || bos == nil || len(bos) != 0 {
		t.Fatalf("bos liste = %#v, %v", bos, err)
	}
	beklenen := []StemKaydi{{ID: "drums", Enstruman: "Davul", Sure: 12.5, Anahtar: "sosyal/" + id + "/stems/drums.mp3"}}
	if _, err := havuz.Exec(ctx, `UPDATE sosyal.yayin SET stem_listesi = $2 WHERE id = $1`, id, beklenen); err != nil {
		t.Fatal(err)
	}
	stemler, err := d.StemListesi(ctx, id)
	if err != nil || len(stemler) != 1 || stemler[0] != beklenen[0] {
		t.Fatalf("stemler = %+v, %v", stemler, err)
	}
	if _, err := d.StemListesi(ctx, uuid.NewString()); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan yayin: %v", err)
	}
}
````

**Create** `core-service/internal/sosyal/servis_cover_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// sahteKopyalayici, kopyalari kaydeder; hata doluysa her kopyada onu doner.
type sahteKopyalayici struct {
	kopyalar [][2]string
	hata     error
}

func (k *sahteKopyalayici) Kopyala(_ context.Context, kaynak, hedef string) error {
	if k.hata != nil {
		return k.hata
	}
	k.kopyalar = append(k.kopyalar, [2]string{kaynak, hedef})
	return nil
}

// coverDeposu: yayinX kullaniciB'nin altyapisi acik, iki stem kopyasi
// olan herkes yayinidir.
func coverDeposu() *sahteDepo {
	d := yeniSahteDepo()
	k := sahteYayin(yayinX, kullaniciB, GorunurlukHerkes, DurumYayinda)
	k.Tur, k.Bpm, k.Ton, k.SureSn = "pop", 120, Ton{Tonik: "A", Mod: "minor"}, 180
	k.Yapi = []YapiOgesi{{ID: "v1", Tur: "verse", Olcu: 8}}
	k.Sozler = Sozler{Bolumler: []SozBolumu{{BolumID: "v1", Tur: "verse", Satirlar: []string{"la"}}}}
	d.yayinlar[yayinX] = k
	d.stemListeleri = map[string][]StemKaydi{yayinX: {
		{ID: "drums", Enstruman: "Davul", Sure: 180.5, Anahtar: "sosyal/" + yayinX + "/stems/drums.mp3"},
		{ID: "bass", Enstruman: "Bas", Sure: 179, Anahtar: "sosyal/" + yayinX + "/stems/bass"},
	}}
	return d
}

func TestServis_CoverBaslat(t *testing.T) {
	d := coverDeposu()
	s, _ := testServisi(d)
	kop := &sahteKopyalayici{}
	s.KopyalayiciAyarla(kop)

	b, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX)
	if err != nil {
		t.Fatalf("CoverBaslat: %v", err)
	}
	if b.Kaynak != (KaynakOzeti{YayinID: yayinX, Baslik: "Orijinal", SahipKullaniciAdi: "sahip"}) {
		t.Fatalf("kaynak = %+v", b.Kaynak)
	}
	if b.Tur != "pop" || b.Bpm != 120 || b.Ton.Mod != "minor" || b.SureSn != 180 || len(b.Yapi) != 1 || len(b.Sozler.Bolumler) != 1 {
		t.Fatalf("baslangic = %+v", b)
	}
	if len(kop.kopyalar) != 2 || len(b.Stemler) != 2 {
		t.Fatalf("kopyalar = %v, stemler = %+v", kop.kopyalar, b.Stemler)
	}
	desen := regexp.MustCompile(`^stems/` + kullaniciA + `/([0-9a-f-]{36})/drums\.mp3$`)
	m := desen.FindStringSubmatch(kop.kopyalar[0][1])
	if kop.kopyalar[0][0] != "sosyal/"+yayinX+"/stems/drums.mp3" || m == nil {
		t.Fatalf("ilk kopya = %v", kop.kopyalar[0])
	}
	if kop.kopyalar[1][1] != "stems/"+kullaniciA+"/"+m[1]+"/bass" {
		t.Fatalf("ikinci kopya ayni klasore, uzantisiz = %v", kop.kopyalar[1])
	}
	st := b.Stemler[0]
	if st.ID != "drums" || st.Enstruman != "Davul" || st.Sure != 180.5 ||
		!strings.HasSuffix(st.FileURL, "/"+kop.kopyalar[0][1]) || !strings.Contains(st.FileURL, "makeasinger-private") {
		t.Fatalf("stem = %+v", st)
	}

	// Ikinci baslatma yeni bir klasore kopyalar.
	if _, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kop.kopyalar[2][1], m[1]) {
		t.Fatalf("her baslatma yeni klasor almali: %v", kop.kopyalar[2])
	}
}

func TestServis_CoverBaslatImzaliAdres(t *testing.T) {
	d := coverDeposu()
	s := NewServis(d, &sahteKuyruk{}, imzaliDepolama{}, testR2()).KopyalayiciAyarla(&sahteKopyalayici{})
	b, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX)
	if err != nil || !strings.HasPrefix(b.Stemler[0].FileURL, "https://imzali.example/stems/"+kullaniciA+"/") {
		t.Fatalf("imzali adres = %+v, %v", b, err)
	}
}

func TestServis_CoverBaslatKopyalayicisiz(t *testing.T) {
	s, _ := testServisi(coverDeposu())
	b, err := s.CoverBaslat(context.Background(), kullaniciA, yayinX)
	if err != nil || len(b.Stemler) != 2 {
		t.Fatalf("R2'siz (dev) baslangic = %+v, %v", b, err)
	}
}

func TestServis_CoverBaslatHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad        string
		hazirla   func(d *sahteDepo)
		kullanici string
		yayin     string
		beklenen  error
	}{
		{"altyapi kapali", func(d *sahteDepo) { d.yayinlar[yayinX].AltyapiAcik = false }, kullaniciA, yayinX, ErrAltyapiKapali},
		{"cover yayini", func(d *sahteDepo) { d.yayinlar[yayinX].KaynakYayinID = yayinY }, kullaniciA, yayinX, ErrAltyapiKapali},
		{"stem kopyasi yok", func(d *sahteDepo) { d.stemListeleri[yayinX] = []StemKaydi{} }, kullaniciA, yayinX, ErrAltyapiKapali},
		{"gorunmeyen", func(d *sahteDepo) { d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler }, kullaniciA, yayinX, ErrBulunamadi},
		{"olmayan", nil, kullaniciA, yayinY, ErrBulunamadi},
		{"bozuk id", nil, kullaniciA, "x", ErrBulunamadi},
		{"sahibinin hazirlanan yayini", func(d *sahteDepo) { d.yayinlar[yayinX].Durum = DurumHazirlaniyor }, kullaniciB, yayinX, ErrDurum},
	}
	for _, c := range durumlar {
		d := coverDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		kop := &sahteKopyalayici{}
		s.KopyalayiciAyarla(kop)
		if _, err := s.CoverBaslat(ctx, c.kullanici, c.yayin); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(kop.kopyalar) != 0 {
			t.Errorf("%s: kopya yapilmamali", c.ad)
		}
	}

	s, _ := testServisi(coverDeposu())
	s.KopyalayiciAyarla(&sahteKopyalayici{hata: errors.New("r2 kapali")})
	if _, err := s.CoverBaslat(ctx, kullaniciA, yayinX); err == nil || errors.Is(err, ErrAltyapiKapali) {
		t.Fatalf("kopya hatasi sunucu hatasi olmali: %v", err)
	}
}

func TestServis_Coverlar(t *testing.T) {
	d := coverDeposu()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Coverlar(ctx, kullaniciA, strings.ToUpper(yayinX), "", 2)
	if err != nil || len(sayfa.Ogeler) != 2 || sayfa.SonrakiImlec == nil {
		t.Fatalf("sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.tur != "cover" || d.sonAkis.sahip != yayinX || d.sonAkis.izleyici != kullaniciA || d.sonAkis.limit != 2 {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}

	d.sonAkis = akisCagrisi{}
	d.yayinlar[yayinX].AltyapiAcik = false
	bos, err := s.Coverlar(ctx, kullaniciA, yayinX, "", 20)
	if err != nil || bos.Ogeler == nil || len(bos.Ogeler) != 0 || bos.SonrakiImlec != nil {
		t.Fatalf("altyapi kapali bos sayfa = %+v, %v", bos, err)
	}
	if d.sonAkis.tur != "" {
		t.Fatal("altyapi kapaliyken depo sorgulanmamali")
	}

	d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler
	if _, err := s.Coverlar(ctx, kullaniciA, yayinX, "", 20); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gorunmeyen kaynak: %v", err)
	}
	if _, err := s.Coverlar(ctx, kullaniciA, yayinX, "bozuk!", 20); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bozuk imlec: %v", err)
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -71,6 +71,8 @@ type sahteDepo struct {
 	sonYorumLimiti  int
 
 	sikayetler []YeniSikayet
+
+	stemListeleri map[string][]StemKaydi
 }
 
 func yeniSahteDepo() *sahteDepo {
@@ -443,3 +445,14 @@ func (d *sahteDepo) SikayetEkle(_ context.Context, s YeniSikayet) error {
 	d.sikayetler = append(d.sikayetler, s)
 	return nil
 }
+
+func (d *sahteDepo) Coverlar(_ context.Context, kaynakID, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
+	return d.zamanListesi(akisCagrisi{tur: "cover", izleyici: izleyiciID, sahip: kaynakID, zaman: imlec, limit: limit})
+}
+
+func (d *sahteDepo) StemListesi(_ context.Context, yayinID string) ([]StemKaydi, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	return d.stemListeleri[yayinID], nil
+}
````

**Create** `core-service/internal/handler/sosyal_cover_test.go`:

````go
package handler

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
)

func TestSosyal_CoverBaslatSozlesme(t *testing.T) {
	s := &sahteSosyal{cover: &sosyal.CoverBaslangici{
		Kaynak: sosyal.KaynakOzeti{YayinID: "y1", Baslik: "B", SahipKullaniciAdi: "ali"},
		Tur:    "pop", Bpm: 120, Ton: sosyal.Ton{Tonik: "A", Mod: "minor"}, SureSn: 180,
		Yapi: []sosyal.YapiOgesi{}, Sozler: sosyal.Sozler{Bolumler: []sosyal.SozBolumu{}},
		Stemler: []sosyal.CoverStemi{{ID: "drums", Enstruman: "Davul", FileURL: "https://x/stems/u/k/drums.mp3", Sure: 3}},
	}}
	durum, ham := sosyalHamIstek(t, sosyalUygulamasi(s, sosyalKullanici), http.MethodPost, "/api/social/yayinlar/y1/cover-baslat", "")
	beklenen := `{"kaynak":{"yayinId":"y1","baslik":"B","sahipKullaniciAdi":"ali","silindi":false},"tur":"pop","bpm":120,` +
		`"ton":{"tonik":"A","mod":"minor"},"sureSn":180,"yapi":[],"sozler":{"bolumler":[]},` +
		`"stemler":[{"id":"drums","enstruman":"Davul","fileUrl":"https://x/stems/u/k/drums.mp3","sure":3}]}`
	if durum != http.StatusOK || ham != beklenen || s.sonYayin != "y1" {
		t.Fatalf("durum=%d govde=%s", durum, ham)
	}
}

func TestSosyal_CoverHataKodlari(t *testing.T) {
	for _, d := range []struct {
		hata  error
		durum int
		kod   string
	}{
		{sosyal.ErrAltyapiKapali, http.StatusForbidden, "altyapi_kapali"},
		{sosyal.ErrBulunamadi, http.StatusNotFound, "bulunamadi"},
		{sosyal.ErrDurum, http.StatusConflict, "durum"},
	} {
		durum, govde := sosyalIstek(t, sosyalUygulamasi(&sahteSosyal{hata: d.hata}, sosyalKullanici), http.MethodPost,
			"/api/social/yayinlar/y1/cover-baslat", "")
		if durum != d.durum || hataKodu(govde) != d.kod {
			t.Errorf("%v: durum=%d govde=%v", d.hata, durum, govde)
		}
	}
}

func TestSosyal_CoverlarSayfasiVeLimit(t *testing.T) {
	s := &sahteSosyal{
		sayfa: &sosyal.Sayfa[sosyal.YayinOzeti]{Ogeler: []sosyal.YayinOzeti{}},
		cover: &sosyal.CoverBaslangici{},
	}
	sinir := func(c *fiber.Ctx) error { return c.SendStatus(http.StatusTooManyRequests) }
	app := sosyalUygulamasiLimitli(s, sosyalKullanici, SosyalLimitleri{Yayin: gecir, Yorum: gecir, Cover: sinir})
	durum, govde := sosyalIstek(t, app, http.MethodGet, "/api/social/yayinlar/y1/coverlar?imlec=i", "")
	if durum != http.StatusOK || s.sonImlec != "i" || s.sonYayin != "y1" {
		t.Fatalf("liste: durum=%d govde=%v", durum, govde)
	}
	if v, var_ := govde["sonrakiImlec"]; !var_ || v != nil {
		t.Fatalf("sonrakiImlec null olmali: %v", govde)
	}
	if durum, _ := sosyalHamIstek(t, app, http.MethodPost, "/api/social/yayinlar/y1/cover-baslat", ""); durum != http.StatusTooManyRequests {
		t.Fatalf("cover limiti cover-baslat'a uygulanmali: %d", durum)
	}
}
````

**Modify** `core-service/internal/handler/sosyal_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_test.go b/core-service/internal/handler/sosyal_test.go
--- a/core-service/internal/handler/sosyal_test.go
+++ b/core-service/internal/handler/sosyal_test.go
@@ -32,6 +32,7 @@ type sahteSosyal struct {
 	oneriler    []sosyal.ProfilOzeti
 	yorum       *sosyal.Yorum
 	yorumSayfa  *sosyal.Sayfa[sosyal.Yorum]
+	cover       *sosyal.CoverBaslangici
 
 	sonKullanici string
 	sonAd        string
@@ -149,12 +150,22 @@ func (s *sahteSosyal) Sikayet(_ context.Context, k string, g sosyal.SikayetGirdi
 	return s.hata
 }
 
+func (s *sahteSosyal) Coverlar(_ context.Context, k, id, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error) {
+	s.sonKullanici, s.sonYayin, s.sonImlec, s.sonLimit = k, id, imlec, limit
+	return s.sayfa, s.hata
+}
+
+func (s *sahteSosyal) CoverBaslat(_ context.Context, k, id string) (*sosyal.CoverBaslangici, error) {
+	s.sonKullanici, s.sonYayin = k, id
+	return s.cover, s.hata
+}
+
 func gecir(c *fiber.Ctx) error { return c.Next() }
 
 // sosyalUygulamasi, /api/social grubunu sahte servisle ve gecirgen
 // limitlerle kurar.
 func sosyalUygulamasi(s *sahteSosyal, userID string) *fiber.App {
-	return sosyalUygulamasiLimitli(s, userID, SosyalLimitleri{Yayin: gecir, Yorum: gecir})
+	return sosyalUygulamasiLimitli(s, userID, SosyalLimitleri{Yayin: gecir, Yorum: gecir, Cover: gecir})
 }
 
 // sosyalUygulamasiLimitli, verilen limitlerle kurar.
````

**Modify** `core-service/internal/handler/sosyal_yayin_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_yayin_test.go b/core-service/internal/handler/sosyal_yayin_test.go
--- a/core-service/internal/handler/sosyal_yayin_test.go
+++ b/core-service/internal/handler/sosyal_yayin_test.go
@@ -56,7 +56,7 @@ func TestSosyal_YayinOlusturHizSiniri(t *testing.T) {
 	grup := app.Group("/api/social", kullaniciOlarak(sosyalKullanici))
 	NewSosyalHandler(&sahteSosyal{}).Rotalar(grup, SosyalLimitleri{Yayin: func(c *fiber.Ctx) error {
 		return c.Status(http.StatusTooManyRequests).JSON(fiber.Map{"error": fiber.Map{"code": "RATE_LIMITED", "message": "x"}})
-	}, Yorum: gecir})
+	}, Yorum: gecir, Cover: gecir})
 	if durum, _ := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); durum != http.StatusTooManyRequests {
 		t.Fatalf("yayin limiti POST /yayinlar'a uygulanmali: %d", durum)
 	}
````

**Modify** `core-service/internal/handler/sosyal_yorum_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_yorum_test.go b/core-service/internal/handler/sosyal_yorum_test.go
--- a/core-service/internal/handler/sosyal_yorum_test.go
+++ b/core-service/internal/handler/sosyal_yorum_test.go
@@ -97,7 +97,7 @@ func TestSosyal_YorumLimitiYalnizcaYazmada(t *testing.T) {
 		yorumSayfa:  &sosyal.Sayfa[sosyal.Yorum]{Ogeler: []sosyal.Yorum{}},
 		yayinYaniti: &sosyal.YayinYaniti{},
 	}
-	app := sosyalUygulamasiLimitli(s, sosyalKullanici, SosyalLimitleri{Yayin: gecir, Yorum: yavasla})
+	app := sosyalUygulamasiLimitli(s, sosyalKullanici, SosyalLimitleri{Yayin: gecir, Yorum: yavasla, Cover: gecir})
 	durum, govde := sosyalIstek(t, app, http.MethodPost, "/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":null}`)
 	if durum != http.StatusTooManyRequests || hataKodu(govde) != "yavasla" {
 		t.Fatalf("POST yorum: durum=%d govde=%v", durum, govde)
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors such as `d.Coverlar undefined`, `s.KopyalayiciAyarla undefined`, `undefined: sosyal.CoverBaslangici` and `unknown field Cover in struct literal of type SosyalLimitleri`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/sosyal/depo.go`:

````diff
diff --git a/core-service/internal/sosyal/depo.go b/core-service/internal/sosyal/depo.go
--- a/core-service/internal/sosyal/depo.go
+++ b/core-service/internal/sosyal/depo.go
@@ -36,6 +36,7 @@ type Depo interface {
 	TakipDeposu
 	YorumDeposu
 	SikayetDeposu
+	CoverDeposu
 }
 
 // PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
````

**Create** `core-service/internal/sosyal/depo_cover.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CoverDeposu, cover listesi ve cover baslatma sorgulari (spec §4.6).
type CoverDeposu interface {
	// Coverlar, kaynagin yayinda ve silinmemis cover'larini izleyicinin
	// listede gorebildigi kadariyla yeniden eskiye en cok limit+1 tane
	// doner.
	Coverlar(ctx context.Context, kaynakID, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// StemListesi, yayinin kopyalanmis stem'lerini (ic anahtarlariyla)
	// doner; yayin yoksa ErrBulunamadi.
	StemListesi(ctx context.Context, yayinID string) ([]StemKaydi, error)
}

func (d *PostgresDepo) Coverlar(ctx context.Context, kaynakID, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.kaynak_yayin_id = $2 AND y.durum = 'yayinda' AND ` + gorunurlukKosulu("y", "$1", false)
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID, kaynakID)
}

func (d *PostgresDepo) StemListesi(ctx context.Context, yayinID string) ([]StemKaydi, error) {
	var stemler []StemKaydi
	err := d.havuz.QueryRow(ctx, `SELECT stem_listesi FROM sosyal.yayin WHERE id = $1`, yayinID).Scan(&stemler)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("stem listesi okunamadi: %w", err)
	}
	if stemler == nil {
		stemler = []StemKaydi{}
	}
	return stemler, nil
}
````

**Modify** `core-service/internal/sosyal/servis.go`:

````diff
diff --git a/core-service/internal/sosyal/servis.go b/core-service/internal/sosyal/servis.go
--- a/core-service/internal/sosyal/servis.go
+++ b/core-service/internal/sosyal/servis.go
@@ -23,6 +23,10 @@ type Servis struct {
 	depolama client.StorageClient // nil: R2 yok, imzasiz adres uretilir
 	r2       *config.R2Config
 	simdi    func() time.Time
+
+	// kopyalayici, cover-baslat'in stem kopyalayicisi (KopyalayiciAyarla);
+	// nil: R2 yok, kopya atlanir.
+	kopyalayici client.Kopyalayici
 }
 
 // NewServis kurar. depolama nil olabilir (R2 yapilandirilmamis); main
````

**Create** `core-service/internal/sosyal/servis_cover.go`:

````go
package sosyal

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
)

// CoverStemi, cover projesine verilen stem: fileUrl, kullanicinin kendi
// stems/ onekindeki kopyanin adresidir (/api/files/sign yeniden imzalar).
type CoverStemi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	FileURL   string  `json:"fileUrl"`
	Sure      float64 `json:"sure"`
}

// CoverBaslangici, POST /yayinlar/{id}/cover-baslat yaniti (spec §4.6).
type CoverBaslangici struct {
	Kaynak  KaynakOzeti  `json:"kaynak"`
	Tur     string       `json:"tur"`
	Bpm     int          `json:"bpm"`
	Ton     Ton          `json:"ton"`
	SureSn  int          `json:"sureSn"`
	Yapi    []YapiOgesi  `json:"yapi"`
	Sozler  Sozler       `json:"sozler"`
	Stemler []CoverStemi `json:"stemler"`
}

// KopyalayiciAyarla, cover-baslat'in stem kopyalayicisini baglar ve
// servisi doner. nil (R2 yapilandirilmamis, dev) ise kopya atlanir ve
// adresler yine uretilir. main tipli nil tuzagina dusmemek icin yalnizca
// gercek istemciyi gecirir.
func (s *Servis) KopyalayiciAyarla(k client.Kopyalayici) *Servis {
	s.kopyalayici = k
	return s
}

// CoverStemHedefi, cover'a kopyalanan stem'in anahtaridir:
// stems/{kullaniciId}/{klasor}/{stemId}.{ext}. Render stem duzeniyle
// aynidir; uzanti kaynak anahtardan gelir.
func CoverStemHedefi(kullaniciID, klasor, stemID, kaynak string) (string, error) {
	return client.KullaniciAnahtari("stems", kullaniciID, klasor, stemID+uzanti(kaynak))
}

// Coverlar, GET /yayinlar/{id}/coverlar. Kaynak gorunmuyorsa
// ErrBulunamadi; altyapisi kapaliysa (cover yayini dahil) bos sayfa.
func (s *Servis) Coverlar(ctx context.Context, kullaniciID, yayinID, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	imlec, err := zamanImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if !k.AltyapiAcik {
		return zamanSayfasiKur(nil, limit), nil
	}
	ogeler, err := s.depo.Coverlar(ctx, k.ID, id, imlec, limit)
	if err != nil {
		return nil, err
	}
	return zamanSayfasiKur(ogeler, limit), nil
}

// CoverBaslat, POST /yayinlar/{id}/cover-baslat: yayinin stem'lerini
// istegi yapanin stems/ onekine es zamanli kopyalar ve cover projesinin
// baslangicini doner. Gorunmeyen yayin ErrBulunamadi; cover yayini,
// altyapisi kapali ya da stem kopyasi olmayan yayin ErrAltyapiKapali;
// sahibinin henuz yayinda olmayan yayini ErrDurum.
func (s *Servis) CoverBaslat(ctx context.Context, kullaniciID, yayinID string) (*CoverBaslangici, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if k.KaynakYayinID != "" || !k.AltyapiAcik {
		return nil, ErrAltyapiKapali
	}
	if k.Durum != DurumYayinda {
		return nil, ErrDurum
	}
	kaynakStemler, err := s.depo.StemListesi(ctx, k.ID)
	if err != nil {
		return nil, err
	}
	if len(kaynakStemler) == 0 {
		return nil, ErrAltyapiKapali
	}

	klasor := uuid.NewString()
	stemler := make([]CoverStemi, 0, len(kaynakStemler))
	for _, st := range kaynakStemler {
		hedef, err := CoverStemHedefi(id, klasor, st.ID, st.Anahtar)
		if err != nil {
			return nil, fmt.Errorf("cover stem anahtari kurulamadi: %w", err)
		}
		if s.kopyalayici != nil {
			if err := s.kopyalayici.Kopyala(ctx, st.Anahtar, hedef); err != nil {
				return nil, fmt.Errorf("cover stem kopyalanamadi: %w", err)
			}
		}
		url, _, err := client.ResolveURL(ctx, s.depolama, s.r2, hedef)
		if err != nil {
			return nil, fmt.Errorf("cover stem adresi uretilemedi: %w", err)
		}
		stemler = append(stemler, CoverStemi{ID: st.ID, Enstruman: st.Enstruman, FileURL: url, Sure: st.Sure})
	}
	return &CoverBaslangici{
		Kaynak: KaynakOzeti{YayinID: k.ID, Baslik: k.Baslik, SahipKullaniciAdi: k.Sahip.KullaniciAdi},
		Tur:    k.Tur, Bpm: k.Bpm, Ton: k.Ton, SureSn: k.SureSn,
		Yapi: k.Yapi, Sozler: k.Sozler, Stemler: stemler,
	}, nil
}
````

**Modify** `core-service/internal/handler/sosyal.go`:

````diff
diff --git a/core-service/internal/handler/sosyal.go b/core-service/internal/handler/sosyal.go
--- a/core-service/internal/handler/sosyal.go
+++ b/core-service/internal/handler/sosyal.go
@@ -39,6 +39,9 @@ type SosyalServisi interface {
 	YorumSil(ctx context.Context, kullaniciID, yorumID string) error
 
 	Sikayet(ctx context.Context, kullaniciID string, g sosyal.SikayetGirdisi) error
+
+	Coverlar(ctx context.Context, kullaniciID, yayinID, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
+	CoverBaslat(ctx context.Context, kullaniciID, yayinID string) (*sosyal.CoverBaslangici, error)
 }
 
 var _ SosyalServisi = (*sosyal.Servis)(nil)
@@ -57,6 +60,7 @@ func NewSosyalHandler(servis SosyalServisi) *SosyalHandler {
 type SosyalLimitleri struct {
 	Yayin fiber.Handler // POST /yayinlar (saatlik)
 	Yorum fiber.Handler // POST /yayinlar/:id/yorumlar (dakikalik, 429 yavasla)
+	Cover fiber.Handler // POST /yayinlar/:id/cover-baslat (saatlik)
 }
 
 // Rotalar, uclari verilen gruba baglar; limitler yalnizca ilgili uclara
@@ -88,6 +92,9 @@ func (h *SosyalHandler) Rotalar(r fiber.Router, l SosyalLimitleri) {
 	r.Delete("/yorumlar/:id", h.YorumSil)
 
 	r.Post("/sikayetler", h.Sikayet)
+
+	r.Get("/yayinlar/:id/coverlar", h.Coverlar)
+	r.Post("/yayinlar/:id/cover-baslat", l.Cover, h.CoverBaslat)
 }
 
 // sosyalHatalari, servis hatalarinin sozlesmedeki durum ve kodlaridir.
````

**Create** `core-service/internal/handler/sosyal_cover.go`:

````go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// Coverlar handles GET /api/social/yayinlar/:id/coverlar (sayfali;
// altyapi kapaliysa bos sayfa).
func (h *SosyalHandler) Coverlar(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	sayfa, err := h.servis.Coverlar(c.Context(), userID, c.Params("id"), c.Query("imlec"), sosyal.SayfaBoyutu(c.Query("limit")))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, sayfa)
}

// CoverBaslat handles POST /api/social/yayinlar/:id/cover-baslat. Govde
// yok sayilir; kullanici basina saatlik limit rotada uygulanir.
func (h *SosyalHandler) CoverBaslat(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	b, err := h.servis.CoverBaslat(c.Context(), userID, c.Params("id"))
	if err != nil {
		return sosyalHata(c, err)
	}
	return response.OK(c, b)
}
````

**Modify** `core-service/cmd/server/main.go`:

````diff
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -167,14 +167,14 @@ func main() {
 	hesapService := service.NewHesapService(kimlikSunucu, silici, redisClient, asynqClient, sosyalDepo)
 	hesapHandler := handler.NewHesapHandler(hesapService)
 
-	sosyalHandler := handler.NewSosyalHandler(sosyal.NewServis(sosyalDepo, asynqClient, storage, &cfg.R2))
-
 	// kopyalayici: arayuz degiskeni; r2Client nil iken dogrudan atamak
 	// tipli nil tuzagini kurar (bkz. storage).
 	var kopyalayici client.Kopyalayici
 	if r2Client != nil {
 		kopyalayici = r2Client
 	}
+	sosyalHandler := handler.NewSosyalHandler(
+		sosyal.NewServis(sosyalDepo, asynqClient, storage, &cfg.R2).KopyalayiciAyarla(kopyalayici))
 	sosyalWorker := worker.NewSosyalWorker(sosyalDepo, kopyalayici, audioClient, silici, hesapisaret.NewRedisDenetci(redisClient))
 
 	// Initialize handlers
@@ -305,6 +305,7 @@ func main() {
 		Yayin: rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SosyalYayinPerHour, time.Hour),
 		Yorum: rateLimiter.LimitKod("sosyal-yorum", cfg.RateLimit.SosyalYorumPerMin, time.Minute,
 			"yavasla", "Biraz yavasla"),
+		Cover: rateLimiter.Limit("sosyal-cover", cfg.RateLimit.SosyalCoverPerHour, time.Hour),
 	})
 
 	// WebSocket routes
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... && go test ./internal/... -count=1
```

Expected: no gofmt output, `0 issues.`, every package `ok`. Whole Go suite after this task: 721 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/depo_cover_test.go \
  core-service/internal/sosyal/servis_cover_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/handler/sosyal_cover_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/handler/sosyal_yayin_test.go \
  core-service/internal/handler/sosyal_yorum_test.go \
  core-service/internal/sosyal/depo.go \
  core-service/internal/sosyal/depo_cover.go \
  core-service/internal/sosyal/servis.go \
  core-service/internal/sosyal/servis_cover.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_cover.go \
  core-service/cmd/server/main.go
git commit -F - <<'EOF'
feat(sosyal): cover listesi ve cover baslatma (stem kopyasi)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 7: Public preview `GET /api/public/s/{slug}` (§4.8)

C5, P13. The route is bound by `PublicRotalar` on `app.Group("/api/public")` and registered in `main.go` before `api := app.Group("/api", apiAuthMiddleware, …)`. Fiber matches in registration order and the preview handler never calls `Next`, so neither the auth middleware nor the deleted-user gate runs for it; `publicUygulama` in the handler test reproduces that order and checks that `/api/social/*` still answers 401.

**Files:**

- Create: `core-service/internal/sosyal/depo_onizleme_test.go`
- Create: `core-service/internal/sosyal/servis_onizleme_test.go`
- Modify: `core-service/internal/sosyal/sahte_depo_test.go` (fake `OnizlemeDeposu`)
- Create: `core-service/internal/handler/sosyal_onizleme_test.go`
- Modify: `core-service/internal/handler/sosyal_test.go` (fake service method)
- Modify: `core-service/internal/sosyal/depo.go`
- Create: `core-service/internal/sosyal/depo_onizleme.go`
- Create: `core-service/internal/sosyal/servis_onizleme.go`
- Modify: `core-service/internal/handler/sosyal.go`
- Create: `core-service/internal/handler/sosyal_onizleme.go`
- Modify: `core-service/cmd/server/main.go`

**Interfaces:**
- Consumes: `SlugGecerli`, `AvatarRengi` (5a); `client.UnsignedURL`, `StorageClient.GetSignedURL` (existing); `RateLimiter.LimitIP` (Task 1); `RateLimit.PublicPerMin`, `PublicWebPerMin`, `Sosyal.WebAnahtari` (Task 2).
- Produces: `type OnizlemeDeposu interface { OnizlemeGetir(ctx, slug string) (*OnizlemeKaydi, error) }`; `type Onizleme`, `OnizlemeKaydi`; `(*Servis).Onizleme(ctx, slug string) (*Onizleme, error)`; `(*SosyalHandler).PublicRotalar(r fiber.Router, ipLimiti fiber.Handler)`, handler `Onizleme`; route `GET /api/public/s/:slug`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/sosyal/depo_onizleme_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresDepo_OnizlemeYalnizcaHerkesVeLink(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")

	durumlar := []struct {
		y        testYayin
		gorunur  bool
		onizleme bool
	}{
		{testYayin{Slug: "Herkes01", Gorunurluk: GorunurlukHerkes}, true, true},
		{testYayin{Slug: "Linkli01", Gorunurluk: GorunurlukLink}, true, false},
		{testYayin{Slug: "Takipci1", Gorunurluk: GorunurlukTakipciler}, false, false},
		{testYayin{Slug: "Hazirla1", Durum: DurumHazirlaniyor}, false, false},
		{testYayin{Slug: "Hatali01", Durum: DurumHata}, false, false},
		{testYayin{Slug: "Silinmis", Silindi: true}, false, false},
	}
	for _, c := range durumlar {
		c.y.SahipID = sahip
		id := testYayinEkle(t, havuz, c.y)
		if c.onizleme {
			if _, err := havuz.Exec(ctx, `UPDATE sosyal.yayin SET onizleme_anahtari = $2, dalga_formu = '[0.25, 1]' WHERE id = $1`,
				id, "sosyal/"+id+"/onizleme.mp3"); err != nil {
				t.Fatal(err)
			}
		}
		k, err := d.OnizlemeGetir(ctx, c.y.Slug)
		if !c.gorunur {
			if !errors.Is(err, ErrBulunamadi) {
				t.Errorf("%s: %v, beklenen ErrBulunamadi", c.y.Slug, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.y.Slug, err)
		}
		if k.Baslik != "Baslik" || k.SureSn != 180 || k.Tur != "pop" || k.SahipID != sahip ||
			k.Sahip != (ProfilOzeti{KullaniciAdi: "sahip", GorunenAd: "sahip", AvatarRengi: AvatarRengi(sahip)}) {
			t.Errorf("%s: kayit = %+v", c.y.Slug, k)
		}
		if c.onizleme && (k.OnizlemeAnahtari != "sosyal/"+id+"/onizleme.mp3" || len(k.DalgaFormu) != 2) {
			t.Errorf("%s: onizleme = %q dalga %v", c.y.Slug, k.OnizlemeAnahtari, k.DalgaFormu)
		}
		if !c.onizleme && (k.OnizlemeAnahtari != "" || k.DalgaFormu == nil || len(k.DalgaFormu) != 0) {
			t.Errorf("%s: onizlemesiz = %q dalga %#v", c.y.Slug, k.OnizlemeAnahtari, k.DalgaFormu)
		}
	}
	if _, err := d.OnizlemeGetir(ctx, "Yok12345"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan slug: %v", err)
	}
}
````

**Create** `core-service/internal/sosyal/servis_onizleme_test.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// sureliDepolama, GetSignedURL'e gelen sureyi kaydeder.
type sureliDepolama struct {
	sure time.Duration
	hata error
}

func (*sureliDepolama) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (*sureliDepolama) Delete(context.Context, string) error { return nil }
func (d *sureliDepolama) GetSignedURL(_ context.Context, key string, sure time.Duration) (string, error) {
	d.sure = sure
	return "https://imzali.example/" + key + "?sure", d.hata
}
func (*sureliDepolama) URLFor(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, errors.New("onizleme URLFor kullanmamali")
}

func onizlemeDeposu() *sahteDepo {
	d := yeniSahteDepo()
	d.onizlemeler = map[string]*OnizlemeKaydi{
		"Ab3dEf9h": {
			Onizleme: Onizleme{Baslik: "Sarki", Sahip: ProfilOzeti{KullaniciAdi: "ali"}, SureSn: 180,
				DalgaFormu: []float64{0.5}, Tur: "pop"},
			SahipID: kullaniciA, OnizlemeAnahtari: "sosyal/" + yayinX + "/onizleme.mp3",
		},
		"Zz99Zz99": {Onizleme: Onizleme{Baslik: "Onizlemesiz", DalgaFormu: []float64{}}},
	}
	return d
}

func TestServis_OnizlemeImzali1Saat(t *testing.T) {
	dep := &sureliDepolama{}
	s := NewServis(onizlemeDeposu(), &sahteKuyruk{}, dep, testR2())
	o, err := s.Onizleme(context.Background(), "Ab3dEf9h")
	if err != nil {
		t.Fatalf("Onizleme: %v", err)
	}
	if o.OnizlemeURL == nil || *o.OnizlemeURL != "https://imzali.example/sosyal/"+yayinX+"/onizleme.mp3?sure" {
		t.Fatalf("onizlemeUrl = %v", o.OnizlemeURL)
	}
	if dep.sure != time.Hour {
		t.Fatalf("imza suresi = %v, beklenen 1 saat", dep.sure)
	}
	if o.Baslik != "Sarki" || o.SureSn != 180 || o.Tur != "pop" || o.Sahip.KullaniciAdi != "ali" {
		t.Fatalf("onizleme = %+v", o)
	}

	yok, err := s.Onizleme(context.Background(), "Zz99Zz99")
	if err != nil || yok.OnizlemeURL != nil {
		t.Fatalf("onizlemesiz yayin = %+v, %v", yok, err)
	}

	dep.hata = errors.New("imza hatasi")
	if _, err := s.Onizleme(context.Background(), "Ab3dEf9h"); err == nil || errors.Is(err, ErrBulunamadi) {
		t.Fatalf("imza hatasi sunucu hatasi olmali: %v", err)
	}
}

func TestServis_OnizlemeR2siz(t *testing.T) {
	s, _ := testServisi(onizlemeDeposu())
	o, err := s.Onizleme(context.Background(), "Ab3dEf9h")
	if err != nil || o.OnizlemeURL == nil || !strings.HasSuffix(*o.OnizlemeURL, "/sosyal/"+yayinX+"/onizleme.mp3") {
		t.Fatalf("R2'siz adres = %+v, %v", o, err)
	}
}

func TestServis_OnizlemeBulunamadi(t *testing.T) {
	s, _ := testServisi(onizlemeDeposu())
	for _, slug := range []string{"Yok12345", "kisa", "Ab3dEf9h/", "../../xx"} {
		if _, err := s.Onizleme(context.Background(), slug); !errors.Is(err, ErrBulunamadi) {
			t.Errorf("%q: %v, beklenen ErrBulunamadi", slug, err)
		}
	}
}
````

**Modify** `core-service/internal/sosyal/sahte_depo_test.go`:

````diff
diff --git a/core-service/internal/sosyal/sahte_depo_test.go b/core-service/internal/sosyal/sahte_depo_test.go
--- a/core-service/internal/sosyal/sahte_depo_test.go
+++ b/core-service/internal/sosyal/sahte_depo_test.go
@@ -73,6 +73,8 @@ type sahteDepo struct {
 	sikayetler []YeniSikayet
 
 	stemListeleri map[string][]StemKaydi
+
+	onizlemeler map[string]*OnizlemeKaydi // slug -> kayit
 }
 
 func yeniSahteDepo() *sahteDepo {
@@ -456,3 +458,15 @@ func (d *sahteDepo) StemListesi(_ context.Context, yayinID string) ([]StemKaydi,
 	}
 	return d.stemListeleri[yayinID], nil
 }
+
+func (d *sahteDepo) OnizlemeGetir(_ context.Context, slug string) (*OnizlemeKaydi, error) {
+	if d.hata != nil {
+		return nil, d.hata
+	}
+	k, ok := d.onizlemeler[slug]
+	if !ok {
+		return nil, ErrBulunamadi
+	}
+	c := *k
+	return &c, nil
+}
````

**Create** `core-service/internal/handler/sosyal_onizleme_test.go`:

````go
package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/sosyal"
	"github.com/makeasinger/api/pkg/response"
)

// publicUygulama, main'deki sirayi taklit eder: once kimliksiz uc, sonra
// her istegi 401 ile kesen bir /api grubu (auth yerine).
func publicUygulama(s *sahteSosyal, ipLimiti fiber.Handler) *fiber.App {
	app := fiber.New()
	h := NewSosyalHandler(s)
	h.PublicRotalar(app.Group("/api/public"), ipLimiti)
	api := app.Group("/api", func(c *fiber.Ctx) error { return response.Unauthorized(c, "auth") })
	h.Rotalar(api.Group("/social"), SosyalLimitleri{Yayin: gecir, Yorum: gecir, Cover: gecir})
	return app
}

func TestSosyal_OnizlemeKimliksizVeSozlesme(t *testing.T) {
	s := &sahteSosyal{onizleme: &sosyal.Onizleme{
		Baslik: "Sarki", Sahip: sosyal.ProfilOzeti{KullaniciAdi: "ali", GorunenAd: "Ali", AvatarRengi: 5},
		SureSn: 180, DalgaFormu: []float64{0.5, 1}, Tur: "pop",
	}}
	app := publicUygulama(s, gecir)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/public/s/Ab3dEf9h", nil))
	if err != nil {
		t.Fatal(err)
	}
	ham, _ := io.ReadAll(resp.Body)
	beklenen := `{"baslik":"Sarki","sahip":{"kullaniciAdi":"ali","gorunenAd":"Ali","avatarRengi":5},"sureSn":180,"dalgaFormu":[0.5,1],"onizlemeUrl":null,"tur":"pop"}`
	if resp.StatusCode != http.StatusOK || string(ham) != beklenen {
		t.Fatalf("durum=%d govde=%s", resp.StatusCode, ham)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || s.sonYayin != "Ab3dEf9h" {
		t.Fatalf("Cache-Control=%q slug=%q", resp.Header.Get("Cache-Control"), s.sonYayin)
	}
	// Ayni uygulamada kimlik isteyen uclar hala 401'dir.
	if durum, _ := sosyalIstek(t, app, http.MethodGet, "/api/social/s/Ab3dEf9h", ""); durum != http.StatusUnauthorized {
		t.Fatalf("auth grubu atlanmamali: %d", durum)
	}
}

func TestSosyal_Onizleme404JSON(t *testing.T) {
	durum, govde := sosyalIstek(t, publicUygulama(&sahteSosyal{hata: sosyal.ErrBulunamadi}, gecir),
		http.MethodGet, "/api/public/s/Yok12345", "")
	if durum != http.StatusNotFound || hataKodu(govde) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}

func TestSosyal_OnizlemeIPLimitiUygulanir(t *testing.T) {
	sinir := func(c *fiber.Ctx) error { return response.RateLimited(c) }
	durum, govde := sosyalIstek(t, publicUygulama(&sahteSosyal{onizleme: &sosyal.Onizleme{}}, sinir),
		http.MethodGet, "/api/public/s/Ab3dEf9h", "")
	if durum != http.StatusTooManyRequests || hataKodu(govde) != "RATE_LIMITED" {
		t.Fatalf("durum=%d govde=%v", durum, govde)
	}
}
````

**Modify** `core-service/internal/handler/sosyal_test.go`:

````diff
diff --git a/core-service/internal/handler/sosyal_test.go b/core-service/internal/handler/sosyal_test.go
--- a/core-service/internal/handler/sosyal_test.go
+++ b/core-service/internal/handler/sosyal_test.go
@@ -33,6 +33,7 @@ type sahteSosyal struct {
 	yorum       *sosyal.Yorum
 	yorumSayfa  *sosyal.Sayfa[sosyal.Yorum]
 	cover       *sosyal.CoverBaslangici
+	onizleme    *sosyal.Onizleme
 
 	sonKullanici string
 	sonAd        string
@@ -160,6 +161,11 @@ func (s *sahteSosyal) CoverBaslat(_ context.Context, k, id string) (*sosyal.Cove
 	return s.cover, s.hata
 }
 
+func (s *sahteSosyal) Onizleme(_ context.Context, slug string) (*sosyal.Onizleme, error) {
+	s.sonYayin = slug
+	return s.onizleme, s.hata
+}
+
 func gecir(c *fiber.Ctx) error { return c.Next() }
 
 // sosyalUygulamasi, /api/social grubunu sahte servisle ve gecirgen
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ ./internal/handler/ -count=1
```

Expected: FAIL, build errors `undefined: OnizlemeKaydi`, `undefined: Onizleme`, `h.PublicRotalar undefined`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/sosyal/depo.go`:

````diff
diff --git a/core-service/internal/sosyal/depo.go b/core-service/internal/sosyal/depo.go
--- a/core-service/internal/sosyal/depo.go
+++ b/core-service/internal/sosyal/depo.go
@@ -37,6 +37,7 @@ type Depo interface {
 	YorumDeposu
 	SikayetDeposu
 	CoverDeposu
+	OnizlemeDeposu
 }
 
 // PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
````

**Create** `core-service/internal/sosyal/depo_onizleme.go`:

````go
package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OnizlemeDeposu, kimliksiz onizleme ucunun sorgusu (spec §4.8).
type OnizlemeDeposu interface {
	// OnizlemeGetir, slug'i tasiyan yayini yalnizca herkese acik
	// onizlemeye uygunsa doner: herkes ya da link, yayinda, silinmemis.
	// Aksi ErrBulunamadi'dir.
	OnizlemeGetir(ctx context.Context, slug string) (*OnizlemeKaydi, error)
}

// onizlemeKosulu, spec §4.9 tablosunun "Herkese acik onizleme"
// sutunudur. Izleyici yoktur: sahibin kendi gorunurlugu ya da takip
// burada rol oynamaz.
const onizlemeKosulu = `y.silinme IS NULL AND y.durum = 'yayinda' AND y.gorunurluk IN ('herkes', 'link')`

func (d *PostgresDepo) OnizlemeGetir(ctx context.Context, slug string) (*OnizlemeKaydi, error) {
	var (
		k        OnizlemeKaydi
		dalga    []float64
		onizleme *string
	)
	err := d.havuz.QueryRow(ctx, `
		SELECT y.baslik, y.sahip_id::text, p.kullanici_adi, p.gorunen_ad, y.sure_sn, y.dalga_formu,
		       y.onizleme_anahtari, y.tur
		  FROM sosyal.yayin y JOIN sosyal.profil p ON p.kullanici_id = y.sahip_id
		 WHERE y.slug = $1 AND `+onizlemeKosulu, slug,
	).Scan(&k.Baslik, &k.SahipID, &k.Sahip.KullaniciAdi, &k.Sahip.GorunenAd, &k.SureSn, &dalga, &onizleme, &k.Tur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("onizleme okunamadi: %w", err)
	}
	k.Sahip.AvatarRengi = AvatarRengi(k.SahipID)
	k.DalgaFormu = dalga
	if k.DalgaFormu == nil {
		k.DalgaFormu = []float64{}
	}
	if onizleme != nil {
		k.OnizlemeAnahtari = *onizleme
	}
	return &k, nil
}
````

**Create** `core-service/internal/sosyal/servis_onizleme.go`:

````go
package sosyal

import (
	"context"
	"fmt"
	"time"

	"github.com/makeasinger/api/internal/client"
)

// onizlemeOmru, onizlemeUrl imzasinin gecerlilik suresi (spec §4.8: 1
// saat). R2_PRESIGN_TTL'den bagimsizdir: sozlesme sabit 1 saat der.
const onizlemeOmru = time.Hour

// Onizleme, GET /api/public/s/{slug} yaniti. OnizlemeURL onizleme
// uretilemediyse null olarak GORUNUR; DalgaFormu hicbir zaman null
// degildir.
type Onizleme struct {
	Baslik      string      `json:"baslik"`
	Sahip       ProfilOzeti `json:"sahip"`
	SureSn      int         `json:"sureSn"`
	DalgaFormu  []float64   `json:"dalgaFormu"`
	OnizlemeURL *string     `json:"onizlemeUrl"`
	Tur         string      `json:"tur"`
}

// OnizlemeKaydi, depodan okunan onizleme ve ic alanlari.
type OnizlemeKaydi struct {
	Onizleme
	SahipID          string
	OnizlemeAnahtari string // "" ise onizleme yok
}

// Onizleme, kimliksiz onizlemeyi doner. Gecersiz bicimli slug, takipciler
// yayini, hazirlanan, hatali ya da silinmis yayin ErrBulunamadi'dir.
func (s *Servis) Onizleme(ctx context.Context, slug string) (*Onizleme, error) {
	if !SlugGecerli(slug) {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.OnizlemeGetir(ctx, slug)
	if err != nil {
		return nil, err
	}
	o := k.Onizleme
	if k.OnizlemeAnahtari != "" {
		url, err := s.onizlemeAdresi(ctx, k.OnizlemeAnahtari)
		if err != nil {
			return nil, err
		}
		o.OnizlemeURL = &url
	}
	return &o, nil
}

// onizlemeAdresi, 1 saat gecerli imzali adres uretir; R2 yoksa (dev)
// imzasiz adres doner.
func (s *Servis) onizlemeAdresi(ctx context.Context, key string) (string, error) {
	if s.depolama == nil {
		return client.UnsignedURL(key, s.r2), nil
	}
	url, err := s.depolama.GetSignedURL(ctx, key, onizlemeOmru)
	if err != nil {
		return "", fmt.Errorf("onizleme adresi imzalanamadi: %w", err)
	}
	return url, nil
}
````

**Modify** `core-service/internal/handler/sosyal.go`:

````diff
diff --git a/core-service/internal/handler/sosyal.go b/core-service/internal/handler/sosyal.go
--- a/core-service/internal/handler/sosyal.go
+++ b/core-service/internal/handler/sosyal.go
@@ -42,6 +42,8 @@ type SosyalServisi interface {
 
 	Coverlar(ctx context.Context, kullaniciID, yayinID, imlec string, limit int) (*sosyal.Sayfa[sosyal.YayinOzeti], error)
 	CoverBaslat(ctx context.Context, kullaniciID, yayinID string) (*sosyal.CoverBaslangici, error)
+
+	Onizleme(ctx context.Context, slug string) (*sosyal.Onizleme, error)
 }
 
 var _ SosyalServisi = (*sosyal.Servis)(nil)
````

**Create** `core-service/internal/handler/sosyal_onizleme.go`:

````go
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/pkg/response"
)

// PublicRotalar, kimliksiz Kesfet uclarini baglar (spec §4.8). main
// bunlari /api auth grubundan ONCE kaydeder; ipLimiti istemci IP'si
// basina (ya da web anahtariyla ortak) siniri uygular.
func (h *SosyalHandler) PublicRotalar(r fiber.Router, ipLimiti fiber.Handler) {
	r.Get("/s/:slug", ipLimiti, h.Onizleme)
}

// Onizleme handles GET /api/public/s/:slug (oturumsuz). Yalnizca herkes ve
// link yayinlari; digerleri 404 bulunamadi. Yanit imzali bir adres
// tasidigi icin ara bellege alinmaz.
func (h *SosyalHandler) Onizleme(c *fiber.Ctx) error {
	o, err := h.servis.Onizleme(c.Context(), c.Params("slug"))
	if err != nil {
		return sosyalHata(c, err)
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return response.OK(c, o)
}
````

**Modify** `core-service/cmd/server/main.go`:

````diff
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -258,6 +258,15 @@ func main() {
 	// Swagger UI
 	app.Get("/swagger/*", fiberSwagger.HandlerDefault)
 
+	// Kesfet herkese acik onizleme (oturumsuz, spec §4.8). /api auth
+	// grubundan ONCE kaydedilir: Fiber rotalari kayit sirasiyla eslestirir
+	// ve bu uc zinciri surdurmez, yani auth ve silinmis-kullanici kapisi
+	// hic calismaz. Sinir istemci IP'si basinadir (X-Real-Ip); web
+	// sunucusu X-Web-Anahtari ile ortak ve daha yuksek sinira gecer.
+	sosyalHandler.PublicRotalar(app.Group("/api/public"),
+		rateLimiter.LimitIP("public", cfg.RateLimit.PublicPerMin, time.Minute,
+			cfg.Sosyal.WebAnahtari, cfg.RateLimit.PublicWebPerMin))
+
 	// API routes
 	api := app.Group("/api", apiAuthMiddleware, middleware.SilinmisKullaniciEngeli(hesapisaret.NewRedisDenetci(redisClient)))
 
````

- [ ] **Step 4: Run every check, then smoke-run the server**

```bash
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... && go test ./internal/... -count=1
psql 'postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable' -c \
  "INSERT INTO sosyal.profil (kullanici_id, kullanici_adi, gorunen_ad) VALUES ('6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a','smoke_ali','Ali');
   INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, tur, bpm, ton_tonik, ton_mod, sure_sn, gorunurluk, altyapi_acik, yorum_acik,
     sozler, yapi, hazirlik, durum, dalga_formu, onizleme_anahtari) VALUES ('9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a','Smoke123',
     '6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a','Duman','pop',120,'A','minor',180,'link',false,true,'{\"bolumler\":[]}','[]',
     '{\"masterKey\":\"m\",\"stems\":[]}','yayinda','[0.5,1]','sosyal/9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a/onizleme.mp3');"
openssl genrsa 2048 > /tmp/k.pem && go build -o /tmp/server ./cmd/server && redis-cli -n 12 flushdb && \
  SERVER_PORT=18100 AUTH_PORT=18101 AUTH_ISSUER=http://localhost:18101 AUTH_SIGNING_KEY="$(cat /tmp/k.pem)" \
  AUTH_REDIRECT_URIS=com.makesinger.app:/oauth2redirect AUTH_CLIENT_ID=makesinger-mobil AUTH_CRYPTO_KEY=placeholder-32-bytes-change-me!! \
  AUTH_DB_URL='postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable' REDIS_DB=12 \
  SOSYAL_WEB_ANAHTARI=smoke-web-anahtari-0123456789abcdef /tmp/server > /tmp/server.log 2>&1 &
until curl -s -o /dev/null localhost:18100/health; do sleep 0.2; done
B=http://localhost:18100/api/public/s
curl -s -i $B/Yok12345 | grep -E 'HTTP|^\{'
curl -s -i -H 'X-Real-Ip: 198.51.100.77' $B/Smoke123 | grep -E 'HTTP|Cache-Control|^\{'
for i in $(seq 1 31); do curl -s -o /dev/null -w '%{http_code} ' -H 'X-Real-Ip: 203.0.113.50' $B/Smoke123; done; echo
for i in $(seq 1 40); do curl -s -o /dev/null -w '%{http_code} ' -H 'X-Real-Ip: 203.0.113.50' \
  -H 'X-Web-Anahtari: smoke-web-anahtari-0123456789abcdef' $B/Smoke123; done; echo
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Real-Ip: 203.0.113.50' -H 'X-Web-Anahtari: yanlis' $B/Smoke123
curl -s -o /dev/null -w '%{http_code}\n' localhost:18100/api/social/s/Smoke123
kill %1; redis-cli -n 12 flushdb
```

Expected: checks clean and every package `ok`. Smoke: the unknown slug answers `HTTP/1.1 404 Not Found` with `{"error":{"code":"bulunamadi",…}}`; `Smoke123` answers 200 with `Cache-Control: no-store` and `{"baslik":"Duman","sahip":{"kullaniciAdi":"smoke_ali",…},"sureSn":180,"dalgaFormu":[0.5,1],"onizlemeUrl":"https://…/sosyal/9a1c…/onizleme.mp3","tur":"pop"}`; the 31-request loop prints thirty `200` and then `429`; the web-key loop from the same, already limited IP prints forty `200`; the wrong key prints `429`; `/api/social/s/Smoke123` without a token prints `401`. The seeded rows are removed by the next `testHavuz` truncate. Whole Go suite after this task: 728 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/depo_onizleme_test.go \
  core-service/internal/sosyal/servis_onizleme_test.go \
  core-service/internal/sosyal/sahte_depo_test.go \
  core-service/internal/handler/sosyal_onizleme_test.go \
  core-service/internal/handler/sosyal_test.go \
  core-service/internal/sosyal/depo.go \
  core-service/internal/sosyal/depo_onizleme.go \
  core-service/internal/sosyal/servis_onizleme.go \
  core-service/internal/handler/sosyal.go \
  core-service/internal/handler/sosyal_onizleme.go \
  core-service/cmd/server/main.go
git commit -F - <<'EOF'
feat(sosyal): herkese acik onizleme ucu, IP limiti ve web anahtari

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 8: Account deletion also removes reports on the user's comments (§5.3)

P14. 5a's transaction already deletes the user's likes, comments, follows in both directions, filed reports, publications (likes, comments and reports on them cascade) and profile. The one gap Plan B opens is a report by someone else against the deleted user's comment on a third publication: it is neither the user's row nor reached by the `yayin_id` cascade.

**Files:**

- Modify: `core-service/internal/sosyal/depo_temizlik_test.go`
- Modify: `core-service/internal/sosyal/depo_temizlik.go`

**Interfaces:**
- Consumes: `KullaniciVerisiniSil` (5a); `testYorumEkle`, `intp`, `SikayetEkle`, `YorumSil` (Tasks 4, 5).
- Produces: No new API; `KullaniciVerisiniSil` deletes one more set of rows.

- [ ] **Step 1: Write the failing test**

**Modify** `core-service/internal/sosyal/depo_temizlik_test.go`:

````diff
diff --git a/core-service/internal/sosyal/depo_temizlik_test.go b/core-service/internal/sosyal/depo_temizlik_test.go
--- a/core-service/internal/sosyal/depo_temizlik_test.go
+++ b/core-service/internal/sosyal/depo_temizlik_test.go
@@ -4,6 +4,7 @@ import (
 	"context"
 	"sort"
 	"testing"
+	"time"
 
 	"github.com/google/uuid"
 )
@@ -83,3 +84,56 @@ func TestPostgresDepo_KullaniciVerisiniSil(t *testing.T) {
 		t.Fatalf("silinen kullanicinin yayini kalmamali: %v %v", bos, err)
 	}
 }
+
+// Silinen kullanicinin yorumlarina (baskasinin yayininda) yapilan
+// sikayetler de gider: hedefleri artik yoktur ve yayin_id cascade'i onlari
+// yakalamaz. Baskalarinin icerigine yapilan sikayetler kalir.
+func TestPostgresDepo_KullaniciVerisiniSilYorumSikayetleri(t *testing.T) {
+	havuz := testHavuz(t)
+	d := NewPostgresDepo(havuz)
+	ctx := context.Background()
+	giden, kalan, ucuncu := uuid.NewString(), uuid.NewString(), uuid.NewString()
+	testProfilEkle(t, d, giden, "giden")
+	testProfilEkle(t, d, kalan, "kalan")
+	kalaninYayini := testYayinEkle(t, havuz, testYayin{SahipID: kalan})
+	gidenYayini := testYayinEkle(t, havuz, testYayin{SahipID: giden})
+
+	gidenYorumu := testYorumEkle(t, havuz, "", kalaninYayini, giden, nil, time.Now())
+	silinmisYorum := testYorumEkle(t, havuz, "", kalaninYayini, giden, intp(5), time.Now())
+	if err := d.YorumSil(ctx, silinmisYorum); err != nil {
+		t.Fatal(err)
+	}
+	kalanYorumu := testYorumEkle(t, havuz, "", kalaninYayini, kalan, nil, time.Now())
+	yorumAltinda := testYorumEkle(t, havuz, "", gidenYayini, kalan, nil, time.Now())
+
+	sikayet := func(bildiren, tur, hedef, yayin string) {
+		if err := d.SikayetEkle(ctx, YeniSikayet{ID: uuid.NewString(), BildirenID: bildiren, HedefTuru: tur,
+			HedefID: hedef, YayinID: yayin, Neden: "spam"}); err != nil {
+			t.Fatal(err)
+		}
+	}
+	sikayet(kalan, HedefYorum, gidenYorumu, kalaninYayini)    // gider
+	sikayet(ucuncu, HedefYorum, silinmisYorum, kalaninYayini) // gider
+	sikayet(kalan, HedefYayin, gidenYayini, gidenYayini)      // cascade ile gider
+	sikayet(ucuncu, HedefYorum, yorumAltinda, gidenYayini)    // cascade ile gider
+	sikayet(ucuncu, HedefYorum, kalanYorumu, kalaninYayini)   // kalir
+	sikayet(ucuncu, HedefYayin, kalaninYayini, kalaninYayini) // kalir
+
+	if err := d.KullaniciVerisiniSil(ctx, giden); err != nil {
+		t.Fatalf("KullaniciVerisiniSil: %v", err)
+	}
+	var hedefler []string
+	satirlar, err := havuz.Query(ctx, `SELECT hedef_id::text FROM sosyal.sikayet ORDER BY hedef_turu`)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer satirlar.Close()
+	for satirlar.Next() {
+		var h string
+		if err := satirlar.Scan(&h); err != nil {
+			t.Fatal(err)
+		}
+		hedefler = append(hedefler, h)
+	}
+	esitSira(t, "kalan sikayetler", hedefler, []string{kalaninYayini, kalanYorumu})
+}
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd core-service && go test ./internal/sosyal/ -count=1 -run KullaniciVerisiniSil
```

Expected: FAIL: `TestPostgresDepo_KullaniciVerisiniSilYorumSikayetleri: kalan sikayetler: 4 oge, beklenen 2`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/sosyal/depo_temizlik.go`:

````diff
diff --git a/core-service/internal/sosyal/depo_temizlik.go b/core-service/internal/sosyal/depo_temizlik.go
--- a/core-service/internal/sosyal/depo_temizlik.go
+++ b/core-service/internal/sosyal/depo_temizlik.go
@@ -28,7 +28,9 @@ func (d *PostgresDepo) YayinlariListele(ctx context.Context, kullaniciID string)
 }
 
 // KullaniciVerisiniSil, kullanicinin sosyal kayitlarini tek islemde siler:
-// begenileri, yorumlari, takipleri (iki yon), sikayetleri, yayinlari
+// begenileri, yorumlarina yapilan sikayetleri (yorum silinince hedefsiz
+// kalirlardi; yayin_id cascade'i baskasinin yayinindaki yorumu
+// yakalamaz), yorumlari, takipleri (iki yon), sikayetleri, yayinlari
 // (onlara bagli begeni, yorum ve sikayetler cascade ile) ve profili.
 // Baskalarinin cover'lari kalir; kaynaklari silindi olarak gorunur.
 func (d *PostgresDepo) KullaniciVerisiniSil(ctx context.Context, kullaniciID string) error {
@@ -39,6 +41,8 @@ func (d *PostgresDepo) KullaniciVerisiniSil(ctx context.Context, kullaniciID str
 	defer func() { _ = islem.Rollback(ctx) }()
 	for _, sorgu := range []string{
 		`DELETE FROM sosyal.begeni WHERE kullanici_id = $1`,
+		`DELETE FROM sosyal.sikayet WHERE hedef_turu = 'yorum'
+		   AND hedef_id IN (SELECT id FROM sosyal.yorum WHERE yazar_id = $1)`,
 		`DELETE FROM sosyal.yorum WHERE yazar_id = $1`,
 		`DELETE FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1`,
 		`DELETE FROM sosyal.sikayet WHERE bildiren_id = $1`,
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... && go test ./internal/... -count=1
```

Expected: no gofmt output, `0 issues.`, every package `ok`. Whole Go suite after this task: 729 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/sosyal/depo_temizlik_test.go \
  core-service/internal/sosyal/depo_temizlik.go
git commit -F - <<'EOF'
fix(hesap): silinen kullanicinin yorumlarina yapilan sikayetleri de sil

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 9: Documentation, stack env and the contract copy

Brings `CLAUDE.md` up to date (limiters, the public route, cover copies, deletion), adds `SOSYAL_WEB_ANAHTARI` to the backend stack (P4), and updates the contract copy: §4.5 gets 409 `profil_yok` and the comment rules, and the 5a follow-ups land (`sunucu_hatasi`, 409 `durum`, `stem_yok` details, the 202 body of `tekrar-dene`), plus the Plan B codes and the web key.

**Files:**

- Modify: `CLAUDE.md`
- Modify: `docker-stack.yml`
- Modify: `docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md`

**Interfaces:**
- Consumes: Nothing.
- Produces: Documentation and one optional stack variable.

- [ ] **Step 1: Update the documents**

**Modify** `CLAUDE.md`:

````diff
diff --git a/CLAUDE.md b/CLAUDE.md
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -56,8 +56,8 @@ HTTP Request → Fiber middleware (auth, rate-limit) → Handler → Service →
 - **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate`, then by stemMode: none / `split_stem_advanced` / `split_stem`; stems downloaded and uploaded to R2 as `stems/{userId}/{projectId}/{stemId}.mp3` via `client.KullaniciAnahtari`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline), `hesap_worker.go` (delayed R2 cleanup `hesap:temizle` after account deletion), `sosyal_worker.go` (`sosyal:hazirla`: copies a publication's master and stems to `sosyal/{yayinId}/`, asks audio-service `/analiz` for 64 peaks and a 30 s preview, sets `yayinda`; `sosyal:dosya-sil`: deletes `sosyal/{yayinId}/` 24 h after a publication is deleted). Render and master workers refuse paid calls and R2 writes for deleted owners (`hesapisaret.Denetci`).
 - **client/** — HTTP clients for external services: Groq (AI lyrics), Suno (music generation), R2 (Cloudflare S3-compatible storage), audio-service (local mastering).
 - **model/** — Request/response structs and enums. All enum values defined in `enums.go`.
-- **middleware/** — `auth.go` (RS256 access tokens from `internal/kimlik` — the only auth path), `ratelimit.go` (Redis fixed window per user; limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute), `silinmis.go` (`SilinmisKullaniciEngeli`: deleted-user gate on `/api`, 401 for deleted accounts, 503 + `Retry-After: 5` when Redis is unreachable).
-- **sosyal/** — Keşfet (spec: `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md`). Own Postgres pool on `AUTH_DB_URL`, own schema `sosyal` and migration table `sosyal.schema_migrations` (embedded `migrations/*.sql`, advisory lock `0x736F7379616C31`, run at startup, additive only). No FK to the kimlik `users` table. `Servis` holds the rules (username, publish validation, detail visibility via `DetayGorebilir`), `PostgresDepo` the SQL (list visibility via `gorunurlukKosulu`; a live test proves the two agree). Errors map to lowercase contract codes in `handler/sosyal.go` (`bulunamadi`, `profil_yok`, `dogrulama`, `sahiplik`, `stem_yok`, `durum`, ...); social validation failures are 422, not 400. Lists are cursor-paged (`imlec`, `limit` 1-50, `{ogeler, sonrakiImlec}`).
+- **middleware/** — `auth.go` (RS256 access tokens from `internal/kimlik` — the only auth path), `ratelimit.go` (Redis fixed window per user; limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute, Keşfet publish/cover per hour and comments per minute; `LimitKod` answers 429 with a custom code, e.g. `yavasla` for comments; `LimitIP` limits unauthenticated routes per client IP from Traefik's `X-Real-Ip` (falls back to `c.IP()`, Fiber's `ProxyHeader` is not set) under `ratelimit:<prefix>:ip:<ip>`, and moves requests carrying the configured `X-Web-Anahtari` (constant-time compare) to the shared `ratelimit:<prefix>:web` counter), `silinmis.go` (`SilinmisKullaniciEngeli`: deleted-user gate on `/api`, 401 for deleted accounts, 503 + `Retry-After: 5` when Redis is unreachable).
+- **sosyal/** — Keşfet (spec: `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md`). Own Postgres pool on `AUTH_DB_URL`, own schema `sosyal` and migration table `sosyal.schema_migrations` (embedded `migrations/*.sql`, advisory lock `0x736F7379616C31`, run at startup, additive only). No FK to the kimlik `users` table. `Servis` holds the rules (username, publish validation, detail visibility via `DetayGorebilir`), `PostgresDepo` the SQL (list visibility via `gorunurlukKosulu`; a live test proves the two agree). Errors map to lowercase contract codes in `handler/sosyal.go` (`bulunamadi`, `profil_yok`, `dogrulama`, `sahiplik`, `stem_yok`, `durum`, `kendini_takip`, `yorum_kapali`, `yetki`, `altyapi_kapali`, ...); social validation failures are 422, not 400. Lists are cursor-paged (`imlec`, `limit` 1-50, `{ogeler, sonrakiImlec}`); comments use a two-group cursor (timed by `zaman_ms` ascending, then untimed by `olusturma` descending). Comments, reports and comment deletion are soft or idempotent as the contract requires (a repeat report is 201 without a new row). `cover-baslat` copies the publication's `stem_listesi` server-side to `stems/{caller}/{uuid}/{stemId}.{ext}` (`client.Kopyalayici`) so `/api/files/sign` can re-sign them. `GET /api/public/s/{slug}` is the only unauthenticated Keşfet route (`SosyalHandler.PublicRotalar`, registered before the `/api` auth group): `herkes`/`link` + `yayinda` only, `onizlemeUrl` signed for 1 h.
 - **hesapisaret/** — Deleted-account marker shared by middleware, `HesapService` and workers: `Anahtar(userID)` (`kimlik:silindi:<userId>`), `Omur` (24 h), `Denetci` interface and `RedisDenetci` (EXISTS).
 - **auth/** — `jwks_verifier.go`: token verifiers. `YukariAkisDogrulayici` validates **upstream** (Apple/Google) `id_token`s for the federation flow — RS256 pinned, `iss`/`aud`/`exp` enforced, `nonce` required and compared in constant time.
 - **websocket/** — Hub for broadcasting real-time job progress to clients via `GET /ws/jobs/:jobId?ticket=` (ticket from `POST /api/ws/ticket`).
@@ -75,7 +75,7 @@ HTTP Request → Fiber middleware (auth, rate-limit) → Handler → Service →
 
 ### Configuration
 
-Config priority: environment variables > `config.yaml` > Viper defaults. See `core-service/.env.example` for all available env vars. Env var names match config keys with underscores (e.g., `GROQ_API_KEY` for `groq.api_key`).
+Config priority: environment variables > `config.yaml` > Viper defaults. See `core-service/.env.example` for all available env vars. Env var names match config keys with underscores (e.g., `GROQ_API_KEY` for `groq.api_key`). Secrets can also come from `<NAME>_FILE` (Docker secrets, `readSecret`), including `SOSYAL_WEB_ANAHTARI`.
 
 ### Job lifecycle
 
@@ -94,7 +94,7 @@ Standard error codes: `VALIDATION_ERROR` (400), `UNAUTHORIZED` (401), `FORBIDDEN
 
 ### Auth
 
-Bearer token required on all `/api/*` routes. 
+Bearer token required on all `/api/*` routes, except `GET /api/public/s/{slug}` (Keşfet web preview; per-IP limit `RATELIMIT_PUBLIC_PER_MIN`, web server bypass via `X-Web-Anahtari` = `SOSYAL_WEB_ANAHTARI` with the shared `RATELIMIT_PUBLIC_WEB_PER_MIN`; empty key disables the bypass). 
 
 **`AUTH_ISSUER` is required.** The middleware validates RS256 access tokens issued by the custom OpenID Provider (`internal/kimlik/`). The public key is read from the provider process (no network call to a separate JWKS endpoint), avoiding circular startup dependencies. Token validation enforces:
 - Algorithm: RS256 only (no HMAC or "none")
@@ -106,11 +106,11 @@ Bearer token required on all `/api/*` routes.
 
 If `AUTH_ISSUER` is empty or the OpenID Provider fails to start, the service exits with a fatal error (fail-closed design).
 
-**Account deletion (`DELETE /api/account`):** `service.HesapService.Sil` validates the id, deletes the user via `kimlik.Sunucu.HesapSil` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`hesapisaret.Omur` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.KullaniciOnekleri`), then the Keşfet data through the optional `SosyalTemizleyici` (every publication id, soft-deleted included, gets `sosyal/{id}/` deleted; the rows are deleted in one transaction only if all those prefixes succeeded, so the retry can list the ids again; other users' covers stay and show the source as deleted), and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.SilinmisKullaniciEngeli` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `hesapisaret.Denetci`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.
+**Account deletion (`DELETE /api/account`):** `service.HesapService.Sil` validates the id, deletes the user via `kimlik.Sunucu.HesapSil` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`hesapisaret.Omur` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.KullaniciOnekleri`), then the Keşfet data through the optional `SosyalTemizleyici` (every publication id, soft-deleted included, gets `sosyal/{id}/` deleted; the rows are deleted in one transaction only if all those prefixes succeeded, so the retry can list the ids again: profile, publications with their likes, comments and reports, the user's likes, comments, follows in both directions, reports filed by the user and reports against the user's comments; other users' covers stay and show the source as deleted; cover stem copies live under `stems/<userId>/` and go with `KullaniciOnekleri`), and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.SilinmisKullaniciEngeli` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `hesapisaret.Denetci`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.
 
 Extracted claims (`userId`, `email`, `name`) are stored in Fiber context locals.
 
-**Ownership (Phase 3):** R2 keys are prefixed with the owner: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3`, `exports/<userId>/<exportId>.<ext>`, `previews/<userId>/<id>.mp3`. Keys are only built with `client.KullaniciAnahtari`, which rejects any client-supplied segment containing `/` or `..`. Every URL a client sends back (master stems/vocals, export inputs) is resolved with `client.SahipliAnahtarCoz`; a key outside the caller's prefix answers 404. `DELETE /api/upload/vocal/:takeId` requires `?projectId=&sectionId=`. `POST /api/files/sign` re-signs stored URLs (1-50, same order) after resolving each with `client.SahipliAnahtarCoz`; a single foreign key fails the whole request with 404 and nothing is signed. Publication copies live under `sosyal/{yayinId}/` (`client.SosyalAnahtari`); they belong to a publication, not a user prefix, so `files/sign` never signs them and only `POST /api/social/yayinlar/{id}/dosyalar` hands out a signed listen URL after the visibility check. Publishing accepts `masterUrl` and stem `url` as a full URL or a bare key (`client.SahipliAnahtarVeyaURLCoz`); a foreign key is 403 `sahiplik`.
+**Ownership (Phase 3):** R2 keys are prefixed with the owner: `vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav`, `masters/<userId>/<projectId>/<id>.wav`, `stems/<userId>/<projectId>/<id>.mp3`, `exports/<userId>/<exportId>.<ext>`, `previews/<userId>/<id>.mp3`. Keys are only built with `client.KullaniciAnahtari`, which rejects any client-supplied segment containing `/` or `..`. Every URL a client sends back (master stems/vocals, export inputs) is resolved with `client.SahipliAnahtarCoz`; a key outside the caller's prefix answers 404. `DELETE /api/upload/vocal/:takeId` requires `?projectId=&sectionId=`. `POST /api/files/sign` re-signs stored URLs (1-50, same order) after resolving each with `client.SahipliAnahtarCoz`; a single foreign key fails the whole request with 404 and nothing is signed. Publication copies live under `sosyal/{yayinId}/` (`client.SosyalAnahtari`); they belong to a publication, not a user prefix, so `files/sign` never signs them and only `POST /api/social/yayinlar/{id}/dosyalar` hands out a signed listen URL after the visibility check. Publishing accepts `masterUrl` and stem `url` as a full URL or a bare key (`client.SahipliAnahtarVeyaURLCoz`); a foreign key is 403 `sahiplik`. `POST /api/social/yayinlar/{id}/cover-baslat` is the only path from `sosyal/` back into a user prefix: it copies the stems to `stems/<callerId>/<uuid>/` and returns ordinary user URLs.
 
 **WebSocket:** `GET /ws/jobs/:jobId` requires `?ticket=` from `POST /api/ws/ticket` (body `{"jobId"}`, owner only). Tickets live 30 s in Redis (`wsbilet:<ticket>`) and are consumed with `GETDEL` before the upgrade.
 
````

**Modify** `docker-stack.yml`:

````diff
diff --git a/docker-stack.yml b/docker-stack.yml
--- a/docker-stack.yml
+++ b/docker-stack.yml
@@ -56,6 +56,9 @@ services:
       SUNO_BASE_URL: ${SUNO_BASE_URL:-https://api.sunoapi.org}
       AUDIO_SERVICE_URL: http://audio-service:8084
       AUDIO_SERVICE_TIMEOUT: "120"
+      # Kesfet web onizlemesi: make-singer-frontend'in X-Web-Anahtari degeri
+      # (iki stack'te ayni). Bos ise web sunucusu da IP basina 30/dk sinirda.
+      SOSYAL_WEB_ANAHTARI: ${SOSYAL_WEB_ANAHTARI:-}
     secrets:
       - groq_api_key
       - r2_account_id
````

**Modify** `docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md`:

````diff
diff --git a/docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md b/docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md
--- a/docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md
+++ b/docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md
@@ -106,6 +106,8 @@ Bu dosyalar herkese açık değildir. İmzalı adresler yalnızca §4'teki uçla
 - Temel yol `/api/social`. Tüm uçlar `Authorization: Bearer` ister; tek istisna §4.8.
 - JSON alan adları camelCase'tir. Zamanlar ISO 8601'dir.
 - Hata gövdesi, mevcut `pkg/response` biçimidir: `{ "error": { "code": "…", "message": "…" } }`.
+  - Beklenmeyen sunucu hatası 500 `sunucu_hatasi` döner; ayrıntı yalnızca sunucu logundadır.
+  - Gövde çözülemezse ya da alan kuralı bozulursa 422 `dogrulama` döner (400 kullanılmaz).
 - Liste uçları sayfalıdır.
   - İstek: `?imlec=<opak dize>&limit=<1–50, varsayılan 20>`.
   - Yanıt: `{ "ogeler": [...], "sonrakiImlec": "…" | null }`.
@@ -228,10 +230,10 @@ Dalga formu veya önizleme üretilemezse yayın yine `yayinda` olur: dalga formu
 |---|---|---|---|
 | `GET /yayinlar/{id}` | — | `YayinDetayi` | Görme yetkisi yoksa 404 |
 | `GET /s/{slug}` | — | `YayinDetayi` | Slug ile aynı detay (deep link için) |
-| `POST /yayinlar/{id}/tekrar-dene` | — | `202` | Yalnızca sahibi, yalnızca `hata` durumunda |
-| `PATCH /yayinlar/{id}` | `{ aciklama?, gorunurluk?, altyapiAcik?, yorumAcik? }` | `YayinDetayi` | Yalnızca sahibi. `altyapiAcik` false iken true yapılamaz, çünkü stem kopyası yoktur: 422 `stem_yok` |
+| `POST /yayinlar/{id}/tekrar-dene` | — | `202 { id, slug, durum: "hazirlaniyor" }` | Yalnızca sahibi, yalnızca `hata` durumunda; başka durumda 409 `durum`, sahibi değilse 404 |
+| `PATCH /yayinlar/{id}` | `{ aciklama?, gorunurluk?, altyapiAcik?, yorumAcik? }` | `YayinDetayi` | Yalnızca sahibi. 422 `stem_yok`: `altyapiAcik: true` istenen yayının stem kopyası yok (altyapı kapalı yayınlanmış) ya da yayın bir cover |
 | `DELETE /yayinlar/{id}` | — | `204` | Yalnızca sahibi. Yumuşak silme; dosyalar 24 saat sonra `bakim` kuyruğunda silinir |
-| `POST /yayinlar/{id}/dosyalar` | — | `{ master: { url, expiresAt } }` | Dinleme için. 1 saat geçerli |
+| `POST /yayinlar/{id}/dosyalar` | — | `{ master: { url, expiresAt? } }` | Dinleme için. 1 saat geçerli; adres kalıcıysa `expiresAt` yoktur. Yayın henüz `yayinda` değilse 409 `durum` ("hâlâ hazırlanıyor") |
 | `GET /akis?sekme=senin-icin\|takip\|yeni` | sayfalı | `YayinOzeti[]` | §5.1 |
 
 ### 4.4 Beğeni ve takip
@@ -244,20 +246,27 @@ Dalga formu veya önizleme üretilemezse yayın yine `yayinda` olur: dalga formu
 | `DELETE /profiller/{kullaniciAdi}/takip` | `{ takipci: number, takipEdiyorMuyum: false }` | |
 | `GET /oneriler` | `ProfilOzeti[]` (en çok 10, sayfasız) | Takip etmediğin, son 30 günde en çok beğeni alan yayıncılar |
 
+- Takip: kullanıcı adı yoksa 404 `bulunamadi`. `DELETE` kendine de 422 `kendini_takip` döner. Takip eden için profil gerekmez.
+- Öneriler: `herkes` ya da `takipciler` görünürlüğünde en az bir `yayinda` yayını olan yayıncılar. Puan, bu yayınların son 30 günde başkalarından aldığı beğenilerdir; sahibin kendi beğenisi sayılmaz. Eşitlikte son yayını yeni olan öne geçer. Liste boşsa `[]` döner.
+
 ### 4.5 Yorumlar
 
 | Uç | İstek | Yanıt | Hatalar |
 |---|---|---|---|
 | `GET /yayinlar/{id}/yorumlar` | sayfalı | `Yorum[]`: önce `zamanMs` artan, sonra damgasızlar (`olusturma` azalan) | 403 `yorum_kapali` |
-| `POST /yayinlar/{id}/yorumlar` | `{ metin, zamanMs: number \| null }` | `201 Yorum` | 403 `yorum_kapali`, 422 `dogrulama`, 429 `yavasla` (kullanıcı başına dakikada 10) |
-| `DELETE /yorumlar/{id}` | — | `204` | 403: yazar ya da yayın sahibi değilse |
+| `POST /yayinlar/{id}/yorumlar` | `{ metin, zamanMs: number \| null }` | `201 Yorum` | 403 `yorum_kapali`, 409 `profil_yok`: profil henüz oluşturulmamış, 422 `dogrulama`, 429 `yavasla` (kullanıcı başına dakikada 10) |
+| `DELETE /yorumlar/{id}` | — | `204` | 403 `yetki`: yazar ya da yayın sahibi değilse (yayını göremeyene 404). Silinmiş yorum 404 |
+
+- `metin` kırpılır, 1–500 karakterdir. `zamanMs` null ya da 0 ≤ `zamanMs` ≤ `sureSn` × 1000 olmalıdır.
+- `yorumAcik` false ise iki uç da yayın sahibine dahil 403 `yorum_kapali` döner. Yayın görünmüyorsa önce 404 döner.
+- 429 `yavasla` sınırı istek başınadır; geçersiz istekler de sayılır.
 
 ### 4.6 Cover
 
 | Uç | Yanıt | Hatalar |
 |---|---|---|
 | `GET /yayinlar/{id}/coverlar` | sayfalı `YayinOzeti[]` | Altyapı kapalıysa boş liste |
-| `POST /yayinlar/{id}/cover-baslat` | `CoverBaslangici` | 403 `altyapi_kapali`, 404 |
+| `POST /yayinlar/{id}/cover-baslat` | `CoverBaslangici` | 403 `altyapi_kapali` (altyapı kapalı, yayın bir cover ya da stem kopyası yok), 404, 409 `durum` (sahibin henüz `yayinda` olmayan yayını), 429 (kullanıcı başına saatte 20) |
 
 ```ts
 type CoverBaslangici = {
@@ -278,7 +287,7 @@ Sunucu yayının stem'lerini kullanıcının kendi önekine kopyalar. Anahtarlar
 
 | Uç | İstek | Yanıt |
 |---|---|---|
-| `POST /sikayetler` | `{ hedefTuru: 'yayin' \| 'yorum', hedefId, neden, aciklama? }` | `201`. Tekrar gönderilirse yine `201` döner, yeni kayıt açılmaz |
+| `POST /sikayetler` | `{ hedefTuru: 'yayin' \| 'yorum', hedefId, neden, aciklama? }` | `201`, gövdesiz. Tekrar gönderilirse yine `201` döner, yeni kayıt açılmaz. Hedef görünmüyor ya da yoksa 404, alan kuralı bozuksa 422 `dogrulama` |
 
 ### 4.8 Herkese açık önizleme (oturumsuz)
 
@@ -289,8 +298,9 @@ Sunucu yayının stem'lerini kullanıcının kendi önekine kopyalar. Anahtarlar
 ```
 
 - Yalnızca `herkes` ve `link` görünürlüğündeki, durumu `yayinda` olan yayınlar için çalışır. Diğerleri 404 döner.
-- IP başına dakikada 30 istek sınırı vardır; aşılırsa 429 döner.
-- `onizlemeUrl` 1 saat geçerli imzalı bir adrestir.
+- IP başına dakikada 30 istek sınırı vardır; aşılırsa 429 döner. IP, Traefik'in yazdığı `X-Real-Ip` başlığından okunur.
+- Web sunucusu (`make-singer-frontend`) bütün ziyaretçiler adına tek IP'den çağırır. Bu yüzden `X-Web-Anahtari: <SOSYAL_WEB_ANAHTARI>` başlığını gönderir; anahtar doğruysa istek IP yerine ortak bir sayaçta dakikada 600 ile sınırlanır. Anahtar yanlışsa olağan IP sınırı geçerlidir. Sunucuda anahtar boşsa bu yol kapalıdır.
+- `onizlemeUrl` 1 saat geçerli imzalı bir adrestir. Yanıt `Cache-Control: no-store` taşır.
 
 ### 4.9 Görünürlük kuralı
 
````

- [ ] **Step 2: Check the text and run the full suite three times**

```bash
grep -n 'LimitIP\|/api/public\|cover-baslat' CLAUDE.md && grep -n 'SOSYAL_WEB_ANAHTARI' docker-stack.yml \
  && grep -n 'profil_yok\|yetki\|X-Web-Anahtari' docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md
cd core-service && gofmt -l . && go vet ./... && go build ./... && golangci-lint run ./... \
  && for i in 1 2 3; do go test ./internal/... -count=1 || break; done
```

Expected: each grep prints its lines; checks clean and all packages `ok` three times in a row. Whole Go suite after this task: 729 passing tests (`go test ./internal/... -json`, counting subtests), 0 failed, 0 skipped.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md \
  docker-stack.yml \
  docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md
git commit -F - <<'EOF'
docs: Kesfet 5b-5d uclari, web anahtari ve sozlesme kopyasi

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```


### Task 10: make-singer-frontend: send the web key (DIFFERENT REPOSITORY)

**This task runs in the sibling repository `make-singer-frontend` (Next.js 15), not in the backend.** All paths are relative to that repository's root and every command runs there. P15: `fetchSongPreview` sends `X-Web-Anahtari` from the server-only env `SOSYAL_WEB_ANAHTARI` when it is set, so the song page (all visitors share the web server's IP) gets the backend's shared 600/min limit instead of 30/min. The tests use the existing `fetchImpl` injection plus `vi.stubEnv`. The frontend uses pnpm locally; run the tools with `npx` and do not touch `package.json`, `pnpm-lock.yaml` or `package-lock.json`.

**Files:**

- Modify: `src/lib/songPreview.test.ts`
- Modify: `src/lib/songPreview.ts`
- Modify: `docker-stack.yml`
- Modify: `README.md`

**Interfaces:**
- Consumes: `fetchSongPreview`, `clearPreviewCache`, test helpers `respond`, `options` (existing); the backend contract of Task 7.
- Produces: `FetchOptions.webKey?: string` (default `process.env.SOSYAL_WEB_ANAHTARI`); the header `X-Web-Anahtari` on `GET {API_URL}/api/public/s/{slug}`; stack env `SOSYAL_WEB_ANAHTARI`.

- [ ] **Step 1: Write the failing test**

**Modify** `src/lib/songPreview.test.ts`:

````diff
diff --git a/src/lib/songPreview.test.ts b/src/lib/songPreview.test.ts
--- a/src/lib/songPreview.test.ts
+++ b/src/lib/songPreview.test.ts
@@ -49,6 +49,7 @@ describe("songPreview", () => {
 
   afterEach(() => {
     warn.mockRestore();
+    vi.unstubAllEnvs();
   });
 
   it("accepts only 8 letter-or-digit slugs", () => {
@@ -142,6 +143,51 @@ describe("songPreview", () => {
     expect(warn).toHaveBeenCalledWith("songPreview", "Ab12Cd34", "status", 500);
   });
 
+  it("sends the web key header when a key is given", async () => {
+    const fetchImpl = respond(200);
+    await fetchSongPreview("Ab12Cd34", {
+      ...options(fetchImpl),
+      webKey: "gizli-anahtar",
+    });
+    expect(fetchImpl).toHaveBeenCalledWith(
+      "https://api.test/api/public/s/Ab12Cd34",
+      expect.objectContaining({
+        headers: {
+          Accept: "application/json",
+          "X-Web-Anahtari": "gizli-anahtar",
+        },
+      }),
+    );
+  });
+
+  it("reads the web key from SOSYAL_WEB_ANAHTARI by default", async () => {
+    vi.stubEnv("SOSYAL_WEB_ANAHTARI", "ortam-anahtari");
+    const fetchImpl = respond(200);
+    await fetchSongPreview("Ab12Cd34", options(fetchImpl));
+    expect(fetchImpl).toHaveBeenCalledWith(
+      expect.any(String),
+      expect.objectContaining({
+        headers: {
+          Accept: "application/json",
+          "X-Web-Anahtari": "ortam-anahtari",
+        },
+      }),
+    );
+  });
+
+  it("sends no web key header when the env is unset or blank", async () => {
+    for (const value of [undefined, "", "   "]) {
+      clearPreviewCache();
+      vi.stubEnv("SOSYAL_WEB_ANAHTARI", value);
+      const fetchImpl = respond(200);
+      await fetchSongPreview("Ab12Cd34", options(fetchImpl));
+      expect(fetchImpl).toHaveBeenCalledWith(
+        expect.any(String),
+        expect.objectContaining({ headers: { Accept: "application/json" } }),
+      );
+    }
+  });
+
   it("drops unknown fields from the response", () => {
     expect(parsePreview({ ...BODY, gizli: true })).toEqual(BODY);
     expect(parsePreview(null)).toBeUndefined();
````

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd ../make-singer-frontend && npx vitest run src/lib/songPreview.test.ts
```

Expected: 2 FAIL (`sends the web key header when a key is given`, `reads the web key from SOSYAL_WEB_ANAHTARI by default`): the request headers are only `{"Accept":"application/json"}`.

- [ ] **Step 3: Implement**

**Modify** `src/lib/songPreview.ts`:

````diff
diff --git a/src/lib/songPreview.ts b/src/lib/songPreview.ts
--- a/src/lib/songPreview.ts
+++ b/src/lib/songPreview.ts
@@ -18,14 +18,19 @@ type FetchOptions = {
   baseUrl?: string;
   fetchImpl?: typeof fetch;
   now?: () => number;
+  webKey?: string;
 };
 
 export const DEFAULT_API_URL = "https://makesinger-api.celalettindemir.dev";
 const SLUG = /^[\dA-Za-z]{8}$/;
 const TIMEOUT_MS = 5000;
 // Backend IP başına dakikada 30 istek sınırı koyar; tüm ziyaretçiler web
-// sunucusunun IP'sinden gelir. Önbellek bu sınırı korur.
+// sunucusunun IP'sinden gelir. SOSYAL_WEB_ANAHTARI tanımlıysa istek
+// X-Web-Anahtari başlığıyla gider ve backend onu ortak, daha yüksek bir
+// sınırda (varsayılan dakikada 600) sayar. Önbellek iki durumda da yükü
+// azaltır.
 const CACHE_TTL_MS = 5 * 60 * 1000;
+const WEB_KEY_HEADER = "X-Web-Anahtari";
 const CACHE_MAX = 500;
 
 const cache = new Map<string, { expires: number; result: PreviewResult }>();
@@ -79,17 +84,29 @@ export function parsePreview(body: unknown): SongPreview | undefined {
   };
 }
 
+// Anahtar yalnızca sunucuda okunur (NEXT_PUBLIC_ öneki yok, istemci
+// paketine girmez). Boş ya da yalnızca boşluksa başlık gönderilmez.
+function requestHeaders(webKey: string | undefined): Record<string, string> {
+  const headers: Record<string, string> = { Accept: "application/json" };
+  const key = webKey?.trim();
+  if (key) {
+    headers[WEB_KEY_HEADER] = key;
+  }
+  return headers;
+}
+
 async function request(
   slug: string,
   baseUrl: string,
   fetchImpl: typeof fetch,
+  webKey: string | undefined,
 ): Promise<PreviewResult> {
   try {
     const response = await fetchImpl(
       `${baseUrl}/api/public/s/${encodeURIComponent(slug)}`,
       {
         cache: "no-store",
-        headers: { Accept: "application/json" },
+        headers: requestHeaders(webKey),
         signal: AbortSignal.timeout(TIMEOUT_MS),
       },
     );
@@ -123,6 +140,7 @@ export async function fetchSongPreview(
     baseUrl = process.env.API_URL || DEFAULT_API_URL,
     fetchImpl = fetch,
     now = Date.now,
+    webKey = process.env.SOSYAL_WEB_ANAHTARI,
   }: FetchOptions = {},
 ): Promise<PreviewResult> {
   if (!isValidSlug(slug)) {
@@ -138,7 +156,12 @@ export async function fetchSongPreview(
   }
   const load = async (): Promise<PreviewResult> => {
     try {
-      const result = await request(slug, baseUrl.replace(/\/+$/, ""), fetchImpl);
+      const result = await request(
+        slug,
+        baseUrl.replace(/\/+$/, ""),
+        fetchImpl,
+        webKey,
+      );
       // Geçici hatalar (429, 5xx, ağ) önbelleğe alınmaz.
       if (result.kind !== "unavailable") {
         if (!cache.has(slug) && cache.size >= CACHE_MAX) {
````

**Modify** `docker-stack.yml`:

````diff
diff --git a/docker-stack.yml b/docker-stack.yml
--- a/docker-stack.yml
+++ b/docker-stack.yml
@@ -11,6 +11,9 @@ services:
       - PORT=3000
       - HOSTNAME=0.0.0.0
       - API_URL=https://makesinger-api.celalettindemir.dev
+      # Backend'in SOSYAL_WEB_ANAHTARI degeriyle ayni olmali; bos ise
+      # /s/{slug} sayfasi IP basina 30/dk sinira takilir (tek IP).
+      - SOSYAL_WEB_ANAHTARI=${SOSYAL_WEB_ANAHTARI:-}
     deploy:
       replicas: 2
       update_config:
````

**Modify** `README.md`:

````diff
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -26,6 +26,17 @@ pnpm test
 pnpm type-check
 ```
 
+## Ortam degiskenleri
+
+Yalnizca sunucuda okunur; istemci paketine girmez.
+
+| Degisken | Varsayilan | Aciklama |
+|----------|------------|----------|
+| `API_URL` | `https://makesinger-api.celalettindemir.dev` | Backend adresi. `/s/[slug]` sayfasi `GET {API_URL}/api/public/s/{slug}` cagirir |
+| `SOSYAL_WEB_ANAHTARI` | bos | Backend'in `SOSYAL_WEB_ANAHTARI` degeriyle ayni paylasilan sir. Doluysa istekler `X-Web-Anahtari` basligiyla gider ve backend onlari IP basina 30/dk yerine ortak 600/dk sinirinda sayar. Bos ise baslik gonderilmez |
+
+Anahtari iki stack'te ayni degerle verin (Portainer stack ortami), ornegin `openssl rand -hex 32`. Degistirirken once backend'i, sonra frontend'i guncelleyin; arada gecen surede sayfa IP sinirina duser ama calismaya devam eder.
+
 ## Routes
 
 | Route | Tur | Aciklama |
````

- [ ] **Step 4: Run the tests and watch them pass**

```bash
npx vitest run && npx tsc --noEmit && git status --short
```

Expected: `Tests  33 passed (33)` (30 existing + 3 new), no tsc output, and only the four files of this task modified (no lockfile).

- [ ] **Step 5: Commit**

Commit in `make-singer-frontend`, from its root (its own history and release flow; nothing is committed to the backend in this task):

```bash
git add src/lib/songPreview.test.ts \
  src/lib/songPreview.ts \
  docker-stack.yml \
  README.md
git commit -F - <<'EOF'
feat: önizleme isteğine X-Web-Anahtari başlığı (SOSYAL_WEB_ANAHTARI)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WvFWY9DZTfjxSjx1PuHcSN
EOF
```

---

## Self-Review

**1. Spec coverage**

| Spec item | Task |
|---|---|
| §4.4 `PUT/DELETE /profiller/{ad}/takip`, 422 `kendini_takip`, `GET /oneriler` (10, unfollowed, 30-day likes) | 3 (C3, P5, P6) |
| §4.5 `GET/POST /yayinlar/{id}/yorumlar` (order, 403 `yorum_kapali`, 409 `profil_yok`, 422, 429 `yavasla`), `DELETE /yorumlar/{id}` | 1 (`LimitKod`), 2 (limit), 4 (C2, P7–P9) |
| §4.6 `GET /yayinlar/{id}/coverlar` (empty when closed), `POST /yayinlar/{id}/cover-baslat` (`CoverBaslangici`, 403, 404) | 6 (C4, P11, P12) |
| §4.7 `POST /sikayetler` (repeat 201) | 5 (P10) |
| §4.8 `GET /api/public/s/{slug}` (herkes/link + yayinda, 404, 30/min/IP, 1 h signed `onizlemeUrl`) and the web key | 1 (`LimitIP`), 2 (config), 7 (C5, P13), 10 (frontend) |
| §4.9 visibility on every new endpoint | 3–7 (`gorunurYayin`, `gorunurlukKosulu`, `onizlemeKosulu`) |
| §5.2 deleted comment leaves lists and `yorumSayisi`; `yorumAcik` false → 403; closed backing hides the strip; a cover has no cover | 4, 6 |
| §5.3 account deletion (follows, comments, reports, likes, both directions) | 5a Task 10 + 8 (P14) |
| Docs, stack env, contract copy (§4.5 `profil_yok`, 5a follow-ups) | 9 |

**2. Placeholder scan.** Every step carries the exact file content or diff, the exact command and the expected outcome. No "TBD", "similar to", or "add validation" steps. The plan file was re-applied from scratch onto a fresh 5a-baseline tree with an extractor and produced a tree byte-identical to the tested scratch tree (see "Deployment and manual verification").

**3. Type consistency.** Names were taken from the compiled code: `handler.SosyalLimitleri{Yayin, Yorum, Cover}` (Task 4 introduces `Yayin`/`Yorum`, Task 6 adds `Cover` and updates all three test call sites), `handler.SosyalServisi` grown in Tasks 3–7 and still asserted with `var _ SosyalServisi = (*sosyal.Servis)(nil)`, `sosyal.Depo` embedding `TakipDeposu`, `YorumDeposu`, `SikayetDeposu`, `CoverDeposu`, `OnizlemeDeposu` (each satisfied by `*PostgresDepo` and the fake `*sahteDepo`, checked by `var _ Depo = (*PostgresDepo)(nil)` and by `testServisi`), `(*Servis).KopyalayiciAyarla` (Task 6) used in `main.go`, `RateLimiter.LimitKod`/`LimitIP` (Task 1) used in `main.go` (Tasks 4 and 7), config fields (Task 2) used in `main.go` (Tasks 4, 6, 7).

---

## Deployment and manual verification

**Order.** Backend first, frontend second; both with the same `SOSYAL_WEB_ANAHTARI`. Until the frontend sends the key, the song page simply stays on the 30/min IP limit (its 5-minute cache keeps working), so the order cannot break anything.

1. Generate the key once: `openssl rand -hex 32`. Store it in Portainer as the stack variable `SOSYAL_WEB_ANAHTARI` of both the backend stack and the frontend stack.
2. Backend: tag and push (`git tag vX.Y.Z && git push origin vX.Y.Z`), wait for the image, bump `API_VERSION` in Portainer. No migration. Optional variables: `RATELIMIT_SOSYAL_YORUM_PER_MIN` (10), `RATELIMIT_SOSYAL_COVER_PER_HOUR` (20), `RATELIMIT_PUBLIC_PER_MIN` (30), `RATELIMIT_PUBLIC_WEB_PER_MIN` (600).
3. Check the client IP the API sees: `curl -s -o /dev/null -w '%{http_code}\n' https://makesinger-api.celalettindemir.dev/api/public/s/Yok12345` from two different networks, then `redis-cli --scan --pattern 'ratelimit:public:ip:*'` on the API's Redis. Two different IPs must appear. If only one IP appears (a TLS terminator or tunnel in front of Traefik), every visitor shares 30/min: configure Traefik's `forwardedHeaders.trustedIPs` for that hop (or make it set `X-Real-Ip`) before announcing links.
4. API smoke test with a real access token (`T`) and a publication id (`ID`) and slug (`SLUG`) from 5a's verification:
   ```bash
   A=https://makesinger-api.celalettindemir.dev/api/social
   curl -s -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"metin":"Harika","zamanMs":12000}' $A/yayinlar/$ID/yorumlar   # 201 Yorum
   curl -s -H "Authorization: Bearer $T" $A/yayinlar/$ID/yorumlar                                # {ogeler:[…], sonrakiImlec:null}
   for i in $(seq 1 11); do curl -s -o /dev/null -w '%{http_code} ' -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"metin":"x","zamanMs":null}' $A/yayinlar/$ID/yorumlar; done   # ... 429
   curl -s -H "Authorization: Bearer $T" $A/oneriler                                             # [] or ProfilOzeti[]
   curl -s -i -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"hedefTuru\":\"yayin\",\"hedefId\":\"$ID\",\"neden\":\"spam\"}" $A/sikayetler | head -1   # 201, again 201
   curl -s -H "Authorization: Bearer $T" $A/yayinlar/$ID/coverlar                                # page (empty if backing closed)
   curl -s -X POST -H "Authorization: Bearer $T" $A/yayinlar/$ID/cover-baslat                    # CoverBaslangici or 403 altyapi_kapali
   curl -s https://makesinger-api.celalettindemir.dev/api/public/s/$SLUG                        # no token: preview JSON (herkes/link)
   ```
5. R2 check after `cover-baslat`: `stems/<callerId>/<uuid>/<stemId>.<ext>` objects exist in the private bucket, and `POST /api/files/sign` with one returned `fileUrl` answers a fresh signed URL.
6. Frontend: deploy the new image with `SOSYAL_WEB_ANAHTARI` set; open `https://makesinger.celalettindemir.dev/s/<SLUG>` and confirm `ratelimit:public:web` grows in Redis while `ratelimit:public:ip:<web server IP>` does not.
7. Mobile: switch the 5b–5d social calls from the fake to the real API and run comment (timed and untimed), report, follow/unfollow, suggestions and cover start on a device.

**Verification done while writing this plan.** A scratch worktree was created from backend `main` (6c4d723) and all 12 tasks of the 5a plan were applied with an extractor and committed as one "5a baseline" commit; its suite passed (673 tests) and golangci-lint reported 0 issues. The 5b–5d code was written and tested on top, one commit per task; at each of the nine backend commits `gofmt -l` was empty, `go vet`/`go build` were clean, golangci-lint v2.14.0 reported `0 issues.` and the whole suite passed (counts above). The full Go suite then passed three times in a row against the live Postgres (a separate database on the same server, because another checkout was running the 5a suite at the same time) and Redis: 729 passing tests, up from 673, 0 failed, 0 skipped. The server binary was started against that database and Redis: an unknown slug answered 404 `bulunamadi` as JSON, a seeded `link` publication answered the preview with `Cache-Control: no-store`, 31 rapid requests from one `X-Real-Ip` gave thirty 200s and a 429, 40 requests with the web key from the same exhausted IP all answered 200 (`X-RateLimit-Limit: 600`), a wrong key answered 429, and `/api/social/*` without a token stayed 401. In a scratch worktree of `make-singer-frontend` `npx vitest run` reported 33 passed and `npx tsc --noEmit` was clean. Finally, every code block of this file was extracted and re-applied, task by task, onto a fresh 5a-baseline tree (and Task 10 onto a fresh frontend tree), running each "watch them fail" command between the test and the implementation blocks (each failed as stated). Both results were byte-identical to the tested scratch trees, commit by commit, and the suites passed again (729 Go tests three times in a row, 33 vitest tests, `tsc` clean). One run in between showed a failure of the 5a test `TestButunlesik_YayinYasamDongusu` ("senin icin" empty): the local Docker Postgres clock was measured 6.6 ms ahead of the host, and 5a's `senin-icin` compares the database's `olusturma` with a host-side reference time truncated to milliseconds, so a publication created a few milliseconds earlier can fall after the reference. No Plan B code is involved; see Follow-ups.

---

## Follow-ups

- **Mobile spec copy:** the mobile repository's spec (the authoritative copy) still lacks what Task 9 added to the backend copy: 409 `profil_yok` on comments, 403 `yetki`, 409 `durum` on `cover-baslat` and `dosyalar`, `sunucu_hatasi`, the 202 body of `tekrar-dene`, the empty 201 of reports and the web key. Copy those lines over so the two stay identical.
- **Mobile handling of new codes:** `yetki` (403) and `durum` (409) on `cover-baslat` reach the app as generic errors; decide whether to show a specific message.
- **Swagger:** the sosyal handlers (5a and Plan B) carry no `@Router` annotations; add them and regenerate with `swag init -g cmd/server/main.go`. The public route should be documented without `BearerAuth`.
- **Moderation:** reports are only stored. An admin view or a threshold rule (auto-hide after N reports) is out of scope.
- **Cover-start orphans:** abandoned cover projects leave `stems/<user>/<uuid>/` copies until account deletion; a sweep of folders older than N days with no referencing publication would reclaim them.
- **Suggestion performance:** `Oneriler` scans every profile with a lateral join; add an index on `begeni (olusturma)` or a materialised score when the tables grow.
- **Comment notifications** and **blocking users** are not part of §4.5; they would need new tables.
- **5a clock-skew flake (`senin-icin`):** `seninIcin` takes the first page's reference time from the API host (`s.simdi()`), while `olusturma` comes from Postgres `now()`. When the database clock is ahead (6.6 ms measured locally), a just-created publication is excluded from the first page, and `TestButunlesik_YayinYasamDongusu` fails intermittently. Fix in 5a's code: take the reference from the database on the first page (for example `SELECT now()` once, or `COALESCE($2, now())` returned with the rows), or subtract a small tolerance; the test then becomes deterministic.
- **IP source in production:** see step 3 above; if a proxy sits in front of Traefik, record the chosen trusted-IP configuration in `traefik/` and `CLAUDE.md`.
