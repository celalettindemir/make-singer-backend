# Kredi Faz A (Backend Ledger, Campaigns, Admin API) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship phase A of the credit system: a Postgres credit ledger (lots, entries, reservations), gift campaigns with a lazy welcome-gift claim, the admin campaign API, credit reservation around lyrics / render / final master, and a periodic sweep, all in shadow mode (`CREDITS_ENFORCE=false`: spending is recorded, never blocked).

**Architecture:** A new package `core-service/internal/credit` follows the `internal/social` pattern exactly: its own pgx pool on `AUTH_DB_URL`, schema `kredi`, embedded additive migrations with their own `kredi.schema_migrations` table and advisory lock. `credit.PostgresStore` holds the SQL (ledger, campaigns, sweep, cleanup); `credit.Service` holds the rules and implements `credit.Spender` (`Reserve` / `Commit` / `Release`). Existing code only sees `credit.Spender`, attached through `WithCredits` builders so constructors and existing tests stay unchanged; workers settle reservations from the asynq outcome. `GET /api/credits` lazily claims the active campaign through `credit.UserCreatedAt`, implemented by `*identity.Server`. A new `kredi:supur` task runs on the existing `asynq.Scheduler`.

**Tech Stack:** Go 1.25, Fiber v2, pgx v5, Asynq, Viper. No new dependencies in `go.mod`.

**Spec:** `docs/superpowers/specs/2026-10-13-kredi-sistemi-design.md` (sections Kararlar K4–K11, Maliyet tablosu, Mimari, Veri modeli without `subscriptions`, Akislar: Harcama / Kampanya talebi / Suresi dolan partiler / Hesap silme, API without subscriptions and webhooks, Hata durumlari, Test, Sevkiyat item 1). Subscriptions, store verification, webhooks, `PLAN_CREDITS_*`, mobile and web are phases B and C.

## Global Constraints

**Process**
- Every code block in this plan was compiled and tested before the plan was written (a scratch copy of `main` at `faf1e86`, all 14 tasks applied in order). Apply each block exactly. **Create** blocks are whole files; **Modify** blocks are unified diffs that `git apply` accepts from the repository root (`make-singer-backend/`), applied in task order.
- Module `github.com/makeasinger/api`; Go commands run in `core-service/`. Plain `testing`, hand-written fakes, English test names (`TestType_Behavior`), no testify.
- After every Go task: `gofmt -l .` prints nothing, `go vet ./...` and `go build ./...` are clean, and golangci-lint v2.14.0 (CI version, config `core-service/.golangci.yml`) reports `0 issues`: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` (it switches to its own Go toolchain; that is expected).
- Wire contract (`CLAUDE.md`): every task that adds or changes a route, JSON key, error code, SQL string, Redis key, task/queue name or config/env key ends with `go test ./tools/wirecheck -update` and a review of the diff (`git diff tools/wirecheck/testdata/wire.txt`). The expected additions are listed in each task. The snapshot file itself is not reproduced here; it is regenerated.
- Go identifiers, comments, logs and test names are English. Wire values stay as the spec names them (`kredi` schema, `kredi:supur`, `kredi_yetersiz`, `yetkisiz`, JSON keys in English like render/lyrics).
- API error messages only through `internal/i18n` (TR default, EN by `Accept-Language`), envelope from `pkg/response`. Never pass free text.
- Commit messages are Turkish and end exactly with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  ```
- Never run `./e2e/...` or anything that calls real Suno or Groq (paid). No pushes, no tags.

**Test database and Redis**
- Live Postgres tests use `KIMLIK_TEST_DB_URL`, default `postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable` (local docker `faz2-pg`; `docker start faz2-pg faz2-redis` if stopped). They `t.Skip` when Postgres is unreachable. Prefer a throwaway database so other sessions are not disturbed:
  ```bash
  docker exec faz2-pg psql -U postgres -c "CREATE DATABASE kredi_faz_a"
  export KIMLIK_TEST_DB_URL="postgres://postgres:kimlik-test-yerel@localhost:55432/kredi_faz_a?sslmode=disable"
  # when done: docker exec faz2-pg psql -U postgres -c "DROP DATABASE kredi_faz_a"
  ```
- `internal/credit` live tests call `testPool(t)`: connect (3 s), `credit.Migrate`, `TRUNCATE kredi.campaign_claims, kredi.campaigns, kredi.reservations, kredi.entries, kredi.lots`. Only `kredi.*` is touched. All credit DB tests live in the one `credit` package, so they never run concurrently with each other.
- Live Redis tests use `localhost:6379` (service DB 13, handler DB 12, middleware 14, identity 15) and skip without Redis.
- Known environment flake: `internal/identity` flow tests share Redis DB 15; if another checkout runs `go test ./internal/identity` at the same time, flow tests fail with `auth request not found`. They fail the same way on the untouched base commit; rerun when the other run has finished.

**HTTP contract (phase A)**

| Route | Auth | Limit | Success |
|---|---|---|---|
| `GET /api/credits` | Bearer | `kredi` 60/min/user (`RATELIMIT_CREDITS_PER_MIN`) | 200 `{balance, pools:{subscription:null, gift:{amount,nextExpiry}\|null}, plan:"free", costs:{render_mix,...}, enforced, welcomeGift:{campaignId,amount,expiresAt}\|null}` |
| `GET /api/credits/history?cursor=&limit=` | Bearer | same counter | 200 `{items:[{id,kind,amount,operation,label,createdAt}], nextCursor: string\|null}` |
| `GET /api/admin/campaigns` | Bearer + `AdminGate` | `admin` 30/min/user (`RATELIMIT_ADMIN_PER_MIN`) | 200 `{items:[Campaign], summary:{activeId,totalClaims,totalCredits}}` |
| `POST /api/admin/campaigns` `{name, quota, perUser, validityDays, endsAt?}` | same | same | 201 `Campaign` (the active one is stopped first, K6) |
| `POST /api/admin/campaigns/:id/stop` | same | same | 200 `Campaign` (idempotent) |

`Campaign` = `{id, name, perUser, quota, used, validityDays, startsAt, endsAt, stoppedAt, createdBy, createdAt, status}`; `status` ∈ `active|stopped|exhausted|expired` (derived).

| Code | Status | When |
|---|---|---|
| `kredi_yetersiz` | 402 | enforcement on and balance < price; `details: {required, balance}`. Lyrics generate/rewrite, render start, final master |
| `yetkisiz` | 403 | `/api/admin/*` and the caller is not in `ADMIN_USER_IDS` (empty list = closed) |
| `dogrulama` | 422 | campaign input invalid, unparsable admin body, bad history cursor |
| `bulunamadi` | 404 | stop of an unknown or malformed campaign id |
| `sunucu_hatasi` | 500 | other credit endpoint errors (detail only in the log) |
| `UNAUTHORIZED` | 401 | no user id / non-UUID user id |

Render, master and lyrics keep all their existing error codes; only 402 is new for them.

**Costs (config, `CREDIT_COST_*`)**: `render_mix` 12, `render_focus` 32, `render_stems` 62, `lyrics_generate` 2, `lyrics_rewrite` 1, `master_preview` 0, `master_final` 3. A cost of 0 writes no reservation.

**Queues and tasks**
- `kredi:supur`, no payload, queue `bakim`, scheduled `@every CREDITS_SWEEP_INTERVAL` (default `1h`, `<= 0` disables) with `MaxRetry(0)` and `Unique(interval − 10%)` on the scheduler that already runs `sosyal:bayat-tara`.

---

## Rulings

Binding for every task. Each one was made while writing and testing the code; challenge them first in review.

**R1. `userID` is a string.** The spec sketches `Reserve(ctx, userID uuid.UUID, ...)`; the plan uses `string` like every existing service (`social` too). The credit service validates it with `uuid.Parse` and stores the canonical form. `Reserve` returns `credit.Reservation` by value as in the spec.

**R2. Shadow mode never blocks.** With `CREDITS_ENFORCE=false`, `Service.Reserve` never returns an error: a short balance reserves amount 0 (spec), and any other failure (DB down, non-UUID id) is logged and swallowed, returning a zero reservation. A credit outage must not stop renders in phase A. A sufficient balance is spent normally in shadow mode, so campaign gifts are really consumed.

**R3. Free operations write nothing.** Cost `<= 0` returns a reservation without touching the store; `Commit` / `Release` of an unknown or empty ref is a no-op. The master preview does not call `Reserve` at all.

**R4. Worker settlement follows the asynq outcome, not `FailJob`.** The spec says "worker `FailJob` → Release", but both workers call `FailJob` before returning *retryable* errors too (e.g. Suno rejected the generate call); releasing there would make a later successful retry free. `jobCredits.settle` (worker package): success → `Commit`; error wrapping `asynq.SkipRetry` or on the last attempt (`asynqIsLastAttempt`, already used by the social worker) → `Release(job_failed)`; otherwise the reservation stays held for the next attempt, and the sweep releases it if no attempt ever comes. A failed `Commit` is logged, never returned (a task error would rerun paid work); the reservation stays held and the sweep releases it (user-favoring, spec "Hata durumlari"). Settlement uses a detached context with a 10 s timeout.

**R5. Cancel.** `CancelRender` releases (`canceled`) only when the job was still `queued`. The render worker now reads the job status at the start of every attempt and, if it is `canceled`, returns nil without calling Suno and releases. A status read error does not stop the job (previous behavior). Known race: a cancel that lands after the worker's check lets the job run while the credit is already released; the later commit is a no-op (free render, user-favoring).

**R6. Schema additions beyond the spec sketch.** `reservations.release_reason` (why credit came back; analytics), `CHECK (remaining <= amount)` on lots, `amount >= 0` on reservations, and indexes `lots_expiring`, `reservations_held`, `reservations_user`, `campaign_claims_user`. No CHECK on `entries.kind` or `lots.source` so later phases stay additive. `subscriptions` is not created (phase C).

**R7. History.** Commit rows and zero-amount rows (shadow reserves of an empty balance) are hidden. `label` stays NULL in phase A (no request carries a project name). Cursor = base64url JSON `{t, id}` over `(created_at, id)` descending; `limit` default 20, max 50, invalid → 20; a bad cursor is 422 `dogrulama`.

**R8. Phase A summary.** `plan` is always `"free"` and `pools.subscription` always `null`; `balance` is every live lot (subscription lots cannot exist yet). `costs` lists all seven operations, zero ones included. `pools.gift` is null when no live gift credit is left.

**R9. Lazy claim.** `GET /api/credits` claims only when a campaign is active, the user has no claim on it (checked before the identity lookup), and `UserCreatedAt >= starts_at` (K5). A `UserCreatedAt` error (unknown user included) only skips the claim (logged); store errors fail the request (500). `welcomeGift` is the newest claim of the last 7 days (lot amount and expiry).

**R10. Admin input bounds.** `name` 1–80 runes after trim, no NUL; `quota` 1–1 000 000; `perUser` 1–10 000; `validityDays` 1–3650; `endsAt` optional and strictly in the future. List = newest 200 campaigns, summary over the listed ones. Status: `exhausted` if `used >= quota`; else `expired` if `ends_at` passed and it was not stopped by hand before `ends_at`; else `stopped` if `stopped_at` is set; else `active`.

**R11. Campaign creation is serialized** with `pg_advisory_xact_lock(0x6B726564693032)` ("kredi02") around "stop active + insert", so two admins cannot both pass the one-active index check. Claims rely on the conditional `UPDATE ... used < quota` row lock; the claim that fills the quota sets `stopped_at`.

**R12. Config keys.** Section `credits.*` in `config.yaml` (`enforce`, `admin_user_ids`, `sweep_interval`, `cost_*`) bound explicitly to the spec's env names `CREDITS_ENFORCE`, `ADMIN_USER_IDS`, `CREDITS_SWEEP_INTERVAL`, `CREDIT_COST_*`; limits `ratelimit.credits_per_min` / `ratelimit.admin_per_min` (`RATELIMIT_CREDITS_PER_MIN`, `RATELIMIT_ADMIN_PER_MIN`). An invalid admin UUID or a negative cost stops the server at startup. Admin ids are canonicalized to lower case (the form the auth middleware writes). `docker-stack.yml` passes `CREDITS_ENFORCE` and `ADMIN_USER_IDS`.

**R13. Rate-limit keys** `ratelimit:kredi:<user>` (shared by `/api/credits` and `/history`) and `ratelimit:admin:<user>`. `AdminGate` runs before the admin limiter so non-admins never touch the counter.

**R14. Sweep.** `kredi:supur` runs on `bakim`. Order: release reservations held > 3 h (batch 500, reason `stale`) → expire lots (`expires_at <= now()`, one `expire` entry with `-remaining` and `ref = lot id`) → stop full or ended campaigns. Releasing first means credit returned to an expired lot is expired again in the same run. All steps run even if one fails; errors are joined and logged by the worker (no retry; the next tick redoes it). `startStaleSweepScheduler` becomes `startScheduler` over `periodicTasks(cfg)`.

**R15. Lyrics.** Reservation happens in `LyricsHandler`, after body validation (an invalid request costs nothing), with a fresh UUID ref per request; success → commit, any service error → release (`request_failed`). The unconfigured-Groq mock path also reserves (it is the same handler path). `NewLyricsHandler` now takes the existing `service.LyricsGenerator` interface; `*service.LyricsService` still satisfies it, so `main` and `e2e` compile unchanged.

**R16. Account deletion.** `AccountService.WithCreditCleaner(credit store)` (builder instead of a sixth constructor argument, so the 20+ existing call sites stay). The credit rows are deleted inside `Cleanup`, so the delayed `hesap:temizle` retries them too; the error is joined and only logged. Campaign `used` is not given back.

**R17. Builders and the shared fake.** `WithCredits(credit.Spender)` on `RenderService`, `MasterService`, `LyricsHandler`, `RenderWorker`, `MasterWorker`. `credit.FakeSpender` is an exported, mutex-guarded recorder in a non-test file (same approach as `identity.FakeUserStore`) so handler, service and worker tests share one fake.

**R18. Render worker job interface.** The worker's `renderService` field becomes the small `renderJobs` interface (plus the new `RenderService.JobStatus`), so `ProcessTask` can be tested with a fake; `NewRenderWorker` keeps its `*service.RenderService` parameter and avoids the typed-nil trap.

**R19. Own pool.** Credit opens its own `identity.Connect` pool (up to 10 connections per replica), as the spec requires, and its migration failure is fatal at startup like social's.

## Conflict risk with `fix/master-headroom-eq`

`git diff main...fix/master-headroom-eq --stat` (three dots, the branch's own changes) touches only `audio-service/services/master.py` and `audio-service/tests/test_master.py`. This plan changes no audio-service file, so no merge conflict is expected. (A two-dot `main..fix/master-headroom-eq` diff also lists `docs/superpowers/specs/2026-10-13-kredi-sistemi-design.md` as deleted only because the branch predates the spec commit; it is not a change on that branch.)

---

## File Structure

| File | Responsibility |
|---|---|
| `core-service/internal/credit/migrations/001_kredi.sql` | Schema `kredi`: lots, entries, reservations, campaigns, campaign_claims |
| `core-service/internal/credit/database.go` | Embedded migrations, `Migrate` under advisory lock `0x6B726564693031` |
| `core-service/internal/credit/model.go` | Operations, pools, kinds, statuses, reasons, ledger and API types |
| `core-service/internal/credit/model_campaign.go` | Campaign types and derived statuses |
| `core-service/internal/credit/errors.go` | `ErrNotFound`, `ErrValidation`, `ErrInvalidIdentity`, `InsufficientError` |
| `core-service/internal/credit/store.go` | `LedgerStore`, `PostgresStore`: grant, reserve, commit, release, balances, history |
| `core-service/internal/credit/store_campaign.go` | `CampaignStore`: active, claim, recent claim, create, stop, list |
| `core-service/internal/credit/store_sweep.go` | `SweepStore` (expire, stale reservations, finished campaigns), `DeleteUserData` |
| `core-service/internal/credit/validation.go` | `ValidationError`, id parsing, campaign rules, status, history paging |
| `core-service/internal/credit/service.go` | `Spender`, `UserCreatedAt`, `Store`, `Service` |
| `core-service/internal/credit/spender_fake.go` | `FakeSpender` shared by other packages' tests |
| `core-service/internal/credit/tasks.go` | `TaskTypeSweep`, `QueueSweep` |
| `core-service/internal/identity/server.go` | `Server.UserCreatedAt` |
| `core-service/internal/config/config.go`, `config.yaml`, `.env.example` | `CreditConfig`, credit rate limits |
| `core-service/internal/i18n/catalog.go` | Credit and admin messages |
| `core-service/internal/middleware/admin.go` | `AdminGate` |
| `core-service/internal/handler/credit.go` | `CreditService`, routes, error mapping, 402 helper |
| `core-service/internal/handler/lyrics.go`, `render.go`, `master.go` | Reservation (lyrics) and 402 mapping |
| `core-service/internal/service/render_service.go`, `master_service.go` | Reserve before enqueue, release on enqueue error / queued cancel, `JobStatus` |
| `core-service/internal/service/account_service.go` | `CreditCleaner` |
| `core-service/internal/worker/job_credits.go` | Settlement from the asynq outcome |
| `core-service/internal/worker/render_worker.go`, `master_worker.go` | `WithCredits`, canceled-job skip (render) |
| `core-service/internal/worker/credit_worker.go` | `kredi:supur` handler |
| `core-service/cmd/server/main.go` | Pool, migration, service, routes, workers, scheduler |
| `core-service/tools/wirecheck/main.go` | Migration asset entry |
| `docker-stack.yml`, `CLAUDE.md` | Deploy env, architecture docs |

Tests sit next to the code (`*_test.go`). `credit/store_helpers_test.go` holds live-DB helpers, `credit/fake_store_test.go` the in-memory store for service tests.

---

## Tasks

### Task 1: Schema `kredi`, migrations and the test pool

Creates the package with its embedded migration (R6) and the `testPool` helper every live credit test uses, and registers the migration with wirecheck.

**Files:**
- Create: `core-service/internal/credit/database_test.go`
- Create: `core-service/internal/credit/migrations/001_kredi.sql`
- Create: `core-service/internal/credit/database.go`
- Modify: `core-service/tools/wirecheck/main.go` (assets table)
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: `identity.Connect(ctx, dsn string) (*pgxpool.Pool, error)` (existing).
- Produces: `credit.Migrate(ctx context.Context, pool *pgxpool.Pool) error`; tables `kredi.lots`, `kredi.entries`, `kredi.reservations`, `kredi.campaigns`, `kredi.campaign_claims`, `kredi.schema_migrations`; test helper `testPool(t *testing.T) *pgxpool.Pool` (package `credit`, test files only).

- [ ] **Step 1: Write the failing test**

**Create** `core-service/internal/credit/database_test.go`:

````go
package credit

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/identity"
)

// testPool connects to the local Postgres, applies the credit migrations and
// empties the credit tables. The test is skipped when Postgres is absent
// (same pattern and default DSN as the identity and social tests). TRUNCATE
// touches only kredi.* tables. All live credit tests are in this one
// package, so they never run concurrently with each other.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := identity.Connect(ctx, dsn)
	if err != nil {
		t.Skip("no local Postgres, skipping")
	}
	t.Cleanup(pool.Close)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE kredi.campaign_claims, kredi.campaigns, kredi.reservations, kredi.entries, kredi.lots`); err != nil {
		t.Fatalf("could not truncate the credit tables: %v", err)
	}
	return pool
}

func TestMigrations_EmbeddedAndSorted(t *testing.T) {
	names, err := migrationNames()
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations: %v %v", names, err)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("migrations are not sorted: %q, %q", names[i-1], names[i])
		}
	}
	content, err := migrationFS.ReadFile("migrations/001_kredi.sql")
	if err != nil {
		t.Fatalf("could not read 001_kredi.sql: %v", err)
	}
	for _, table := range []string{"kredi.lots", "kredi.entries", "kredi.reservations", "kredi.campaigns", "kredi.campaign_claims"} {
		if !strings.Contains(string(content), "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("migration does not create %s", table)
		}
	}
	if strings.Contains(string(content), "REFERENCES users") || strings.Contains(string(content), "public.users") {
		t.Error("credit tables must not have an FK to public.users")
	}
	if strings.Contains(string(content), "subscriptions") {
		t.Error("the subscriptions table belongs to phase C")
	}
}

// Migrate must not fail when it runs twice (replicas start first).
func TestMigrate_RunsTwiceAndCreatesTables(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'kredi'
		  AND table_name IN ('lots','entries','reservations','campaigns','campaign_claims','schema_migrations')`,
	).Scan(&count); err != nil {
		t.Fatalf("could not count the tables: %v", err)
	}
	if count != 6 {
		t.Fatalf("expected 6 credit tables, got %d", count)
	}
}

// Only one campaign may be active (stopped_at IS NULL) at a time.
func TestMigrate_OneActiveCampaign(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	insert := `INSERT INTO kredi.campaigns (id, name, per_user, quota, validity_days, starts_at, created_by)
		VALUES (gen_random_uuid(), 'k', 10, 5, 30, now(), gen_random_uuid())`
	if _, err := pool.Exec(ctx, insert); err != nil {
		t.Fatalf("first campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, insert); err == nil {
		t.Fatal("a second active campaign must violate campaigns_one_active")
	}
}
````

- [ ] **Step 2: Run it to verify it fails**

Run: `cd core-service && go test ./internal/credit/`
Expected: build failure, `undefined: Migrate` and `undefined: migrationNames` / `migrationFS`.

- [ ] **Step 3: Write the migration and `Migrate`**

**Create** `core-service/internal/credit/migrations/001_kredi.sql`:

````sql
-- Credit system, phase A: lots, ledger entries, reservations, gift
-- campaigns. Every statement is IF NOT EXISTS because API replicas roll
-- start-first and may see the same migration twice. Additive only.
--
-- No FK to the identity users table: identity deletes the user first and
-- account deletion removes these rows itself (CreditCleaner).

-- A lot is one slice of credit from one source, with what is left of it and
-- when it expires. Held (reserved) credit is already subtracted from
-- remaining, so the balance is SUM(remaining) of the live lots.
CREATE TABLE IF NOT EXISTS kredi.lots (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL,
    pool        text NOT NULL CHECK (pool IN ('subscription', 'gift')),
    source      text NOT NULL,
    source_ref  text,
    amount      integer NOT NULL CHECK (amount > 0),
    remaining   integer NOT NULL CHECK (remaining >= 0 AND remaining <= amount),
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS lots_user_live ON kredi.lots (user_id, expires_at) WHERE remaining > 0;
CREATE INDEX IF NOT EXISTS lots_expiring ON kredi.lots (expires_at) WHERE remaining > 0;

-- Immutable ledger. amount is signed: grant/release positive,
-- reserve/expire negative, commit 0.
CREATE TABLE IF NOT EXISTS kredi.entries (
    id          bigserial PRIMARY KEY,
    user_id     uuid NOT NULL,
    kind        text NOT NULL,
    amount      integer NOT NULL,
    operation   text,
    ref         text,
    label       text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS entries_user_time ON kredi.entries (user_id, created_at DESC, id DESC);

-- Credit held for a job; allocation records which lots it came from so a
-- release can put it back.
CREATE TABLE IF NOT EXISTS kredi.reservations (
    ref             text PRIMARY KEY,
    user_id         uuid NOT NULL,
    operation       text NOT NULL,
    amount          integer NOT NULL CHECK (amount >= 0),
    allocation      jsonb NOT NULL DEFAULT '[]',
    status          text NOT NULL CHECK (status IN ('held', 'committed', 'released')),
    release_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    settled_at      timestamptz
);

CREATE INDEX IF NOT EXISTS reservations_held ON kredi.reservations (created_at) WHERE status = 'held';
CREATE INDEX IF NOT EXISTS reservations_user ON kredi.reservations (user_id);

-- Gift campaigns. Status is derived; finished campaigns also get stopped_at
-- so the one-active index stays correct.
CREATE TABLE IF NOT EXISTS kredi.campaigns (
    id             uuid PRIMARY KEY,
    name           text NOT NULL,
    per_user       integer NOT NULL CHECK (per_user > 0),
    quota          integer NOT NULL CHECK (quota > 0),
    used           integer NOT NULL DEFAULT 0 CHECK (used <= quota),
    validity_days  integer NOT NULL CHECK (validity_days > 0),
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz,
    stopped_at     timestamptz,
    created_by     uuid NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS campaigns_one_active ON kredi.campaigns ((true)) WHERE stopped_at IS NULL;

CREATE TABLE IF NOT EXISTS kredi.campaign_claims (
    campaign_id  uuid NOT NULL REFERENCES kredi.campaigns (id),
    user_id      uuid NOT NULL,
    lot_id       uuid NOT NULL,
    claimed_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign_id, user_id)
);

CREATE INDEX IF NOT EXISTS campaign_claims_user ON kredi.campaign_claims (user_id, claimed_at DESC);
````

**Create** `core-service/internal/credit/database.go`:

````go
// Package credit holds the credit ledger: lots, reservations for jobs, gift
// campaigns and the sweeps that expire lots and release stuck reservations.
// It runs in its own Postgres schema (kredi) with its own pool and migration
// table; it does not share the identity module's pool.
package credit

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

// migrationLockKey is the advisory lock key that lets only one pod run the
// credit migrations. It differs from identity's ("kimlik1") and social's
// ("sosyal1") keys, so the modules never wait for each other.
const migrationLockKey int64 = 0x6B726564693031 // "kredi01"

func migrationNames() ([]string, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Migrate creates the kredi schema and applies pending migrations on one
// connection under an advisory lock, each migration in its own transaction.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("could not get a connection for the credit migration: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("could not acquire the credit migration lock: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey); err != nil {
			log.Printf("could not release the credit migration lock: %v", err)
			// The session lock lives as long as the connection: close it so
			// the pool drops it and the lock is released.
			if closeErr := conn.Conn().Close(context.Background()); closeErr != nil {
				log.Printf("could not close the credit migration connection: %v", closeErr)
			}
		}
	}()

	if _, err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS kredi`); err != nil {
		return fmt.Errorf("could not create the credit schema: %w", err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS kredi.schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("could not create kredi.schema_migrations: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM kredi.schema_migrations WHERE version = $1)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("could not check %s: %w", name, err)
		}
		if exists {
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", name, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("could not start a transaction for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("could not apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO kredi.schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("could not record %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("could not commit %s: %w", name, err)
		}
	}
	return nil
}
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 -v ./internal/credit/ 2>&1 | grep -E '^(---|ok)'`
Expected: `TestMigrations_EmbeddedAndSorted`, `TestMigrate_RunsTwiceAndCreatesTables`, `TestMigrate_OneActiveCampaign` PASS (the last two SKIP without Postgres), `ok`.

- [ ] **Step 5: Register the migration with wirecheck**

`go test ./tools/wirecheck` now fails with `internal/credit/migrations/001_kredi.sql matches internal/*/migrations/*.sql but is not in the assets table`.

**Modify** `core-service/tools/wirecheck/main.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/tools/wirecheck/main.go b/core-service/tools/wirecheck/main.go
index 1ce04a8..67946fb 100644
--- a/core-service/tools/wirecheck/main.go
+++ b/core-service/tools/wirecheck/main.go
@@ -41,6 +41,7 @@ var assets = map[string]string{
 	"kimlik-template-cakisma": "internal/identity/sablonlar/cakisma.html",
 	"kimlik-template-giris":   "internal/identity/sablonlar/giris.html",
 	"kimlik-template-kayit":   "internal/identity/sablonlar/kayit.html",
+	"kredi-migration-001":     "internal/credit/migrations/001_kredi.sql",
 	"sosyal-migration-001":    "internal/social/migrations/001_sosyal.sql",
 	"sosyal-migration-002":    "internal/social/migrations/002_hazirlik_baslangic.sql",
 	"config-yaml":             "config.yaml",
````

Run: `go test ./tools/wirecheck -update && git diff --stat tools/wirecheck/testdata/wire.txt`
Expected additions (9 lines): `FILE kredi-migration-001 8685b51bb0da636b75aa6c536d57bae4e7500cd901f915cc3d852eb58bae794e` (the hash proves the SQL file matches this plan byte for byte), the `CREATE SCHEMA IF NOT EXISTS kredi`, `kredi.schema_migrations` and advisory-lock SQL strings, `"migrations"`, `"migrations/"`. Then `go test ./tools/wirecheck` passes.

- [ ] **Step 6: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...`
Expected: no gofmt output, `0 issues.`

```bash
git add core-service/internal/credit core-service/tools/wirecheck
git commit -m "feat(kredi): kredi semasi ve migration

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Credit configuration

Adds `CreditConfig` (enforce flag, admin ids, sweep interval, the seven prices) and the two rate limits (R12), with defaults, explicit env bindings, startup validation, `config.yaml` and `.env.example`.

**Files:**
- Create: `core-service/internal/config/credit_config_test.go`
- Modify: `core-service/internal/config/config.go`
- Modify: `core-service/config.yaml`
- Modify: `core-service/.env.example`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Produces: `config.Config.Credits config.CreditConfig` with fields `Enforce bool`, `AdminUserIDs []string`, `SweepInterval time.Duration`, `CostRenderMix, CostRenderFocus, CostRenderStems, CostLyricsGenerate, CostLyricsRewrite, CostMasterPreview, CostMasterFinal int`; `config.RateLimitConfig.CreditsPerMin int`, `AdminPerMin int`.

- [ ] **Step 1: Write the failing test**

**Create** `core-service/internal/config/credit_config_test.go`:

````go
package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad_CreditDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	c := cfg.Credits
	if c.Enforce {
		t.Error("Enforce must default to false (shadow mode)")
	}
	if len(c.AdminUserIDs) != 0 {
		t.Errorf("AdminUserIDs default = %v, want empty", c.AdminUserIDs)
	}
	if c.SweepInterval != time.Hour {
		t.Errorf("SweepInterval = %v, want 1h", c.SweepInterval)
	}
	got := []int{c.CostRenderMix, c.CostRenderFocus, c.CostRenderStems, c.CostLyricsGenerate, c.CostLyricsRewrite, c.CostMasterPreview, c.CostMasterFinal}
	want := []int{12, 32, 62, 2, 1, 0, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("costs = %v, want %v", got, want)
			break
		}
	}
	if cfg.RateLimit.CreditsPerMin != 60 || cfg.RateLimit.AdminPerMin != 30 {
		t.Errorf("limits = credits %d, admin %d; want 60, 30", cfg.RateLimit.CreditsPerMin, cfg.RateLimit.AdminPerMin)
	}
}

func TestLoad_CreditFromEnv(t *testing.T) {
	t.Setenv("CREDITS_ENFORCE", "true")
	t.Setenv("ADMIN_USER_IDS", " 6F1C2A9E-1B2C-4D3E-8F9A-0B1C2D3E4F5A , ,7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")
	t.Setenv("CREDITS_SWEEP_INTERVAL", "30m")
	t.Setenv("CREDIT_COST_RENDER_STEMS", "70")
	t.Setenv("CREDIT_COST_MASTER_FINAL", "0")
	t.Setenv("RATELIMIT_CREDITS_PER_MIN", "5")
	t.Setenv("RATELIMIT_ADMIN_PER_MIN", "6")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	c := cfg.Credits
	if !c.Enforce || c.SweepInterval != 30*time.Minute || c.CostRenderStems != 70 || c.CostMasterFinal != 0 {
		t.Errorf("credits = %+v", c)
	}
	if strings.Join(c.AdminUserIDs, ",") != "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a,7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a" {
		t.Errorf("AdminUserIDs = %v, want two canonical lower-case ids", c.AdminUserIDs)
	}
	if cfg.RateLimit.CreditsPerMin != 5 || cfg.RateLimit.AdminPerMin != 6 {
		t.Errorf("limits = %d, %d", cfg.RateLimit.CreditsPerMin, cfg.RateLimit.AdminPerMin)
	}
}

func TestLoad_InvalidAdminUserIDFails(t *testing.T) {
	t.Setenv("ADMIN_USER_IDS", "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a,admin")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "credits.admin_user_ids") {
		t.Fatalf("want an admin_user_ids error, got %v", err)
	}
}

func TestLoad_NegativeCreditCostFails(t *testing.T) {
	t.Setenv("CREDIT_COST_LYRICS_REWRITE", "-1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "credits.cost_lyrics_rewrite") {
		t.Fatalf("want a cost error, got %v", err)
	}
}
````

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/config/`
Expected: build failure, `cfg.Credits undefined` and `cfg.RateLimit.CreditsPerMin undefined`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/config/config.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/config/config.go b/core-service/internal/config/config.go
index 9776797..6211229 100644
--- a/core-service/internal/config/config.go
+++ b/core-service/internal/config/config.go
@@ -6,6 +6,7 @@ import (
 	"strings"
 	"time"
 
+	"github.com/google/uuid"
 	"github.com/makeasinger/api/internal/clientip"
 	"github.com/spf13/viper"
 )
@@ -40,6 +41,7 @@ type Config struct {
 	Audio     AudioConfig
 	Auth      AuthConfig
 	Social    SocialConfig
+	Credits   CreditConfig
 }
 
 type ServerConfig struct {
@@ -72,6 +74,11 @@ type RateLimitConfig struct {
 	// SocialCoverPerHour is the hourly per-user limit for
 	// POST /api/social/yayinlar/{id}/cover-baslat (each call copies stems).
 	SocialCoverPerHour int
+	// CreditsPerMin is the per-minute per-user limit for GET /api/credits
+	// and GET /api/credits/history (shared counter).
+	CreditsPerMin int
+	// AdminPerMin is the per-minute per-user limit for /api/admin/*.
+	AdminPerMin int
 	// PublicPerMin is the per-minute per-client-IP limit for the
 	// unauthenticated GET /api/public/s/{slug}.
 	PublicPerMin int
@@ -103,6 +110,27 @@ type SocialConfig struct {
 	StaleSweepInterval time.Duration
 }
 
+// CreditConfig is the configuration of the credit system (internal/credit).
+type CreditConfig struct {
+	// Enforce turns the balance check on. False is shadow mode: spending is
+	// recorded in the ledger but an insufficient balance never blocks a job.
+	Enforce bool
+	// AdminUserIDs are the canonical user UUIDs allowed on /api/admin/*.
+	// Empty closes the admin endpoints for everyone.
+	AdminUserIDs []string
+	// SweepInterval is how often kredi:supur runs; <= 0 disables it.
+	SweepInterval time.Duration
+
+	// Credit price of each operation; 0 means free (no reservation).
+	CostRenderMix      int
+	CostRenderFocus    int
+	CostRenderStems    int
+	CostLyricsGenerate int
+	CostLyricsRewrite  int
+	CostMasterPreview  int
+	CostMasterFinal    int
+}
+
 type GroqConfig struct {
 	APIKey  string
 	BaseURL string
@@ -287,6 +315,18 @@ func Load() (*Config, error) {
 	_ = viper.BindEnv("sosyal.web_anahtari", "SOSYAL_WEB_ANAHTARI")
 	_ = viper.BindEnv("sosyal.stale_preparation_after", "SOCIAL_STALE_PREPARATION_AFTER")
 	_ = viper.BindEnv("sosyal.stale_sweep_interval", "SOCIAL_STALE_SWEEP_INTERVAL")
+	_ = viper.BindEnv("ratelimit.credits_per_min", "RATELIMIT_CREDITS_PER_MIN")
+	_ = viper.BindEnv("ratelimit.admin_per_min", "RATELIMIT_ADMIN_PER_MIN")
+	_ = viper.BindEnv("credits.enforce", "CREDITS_ENFORCE")
+	_ = viper.BindEnv("credits.admin_user_ids", "ADMIN_USER_IDS")
+	_ = viper.BindEnv("credits.sweep_interval", "CREDITS_SWEEP_INTERVAL")
+	_ = viper.BindEnv("credits.cost_render_mix", "CREDIT_COST_RENDER_MIX")
+	_ = viper.BindEnv("credits.cost_render_focus", "CREDIT_COST_RENDER_FOCUS")
+	_ = viper.BindEnv("credits.cost_render_stems", "CREDIT_COST_RENDER_STEMS")
+	_ = viper.BindEnv("credits.cost_lyrics_generate", "CREDIT_COST_LYRICS_GENERATE")
+	_ = viper.BindEnv("credits.cost_lyrics_rewrite", "CREDIT_COST_LYRICS_REWRITE")
+	_ = viper.BindEnv("credits.cost_master_preview", "CREDIT_COST_MASTER_PREVIEW")
+	_ = viper.BindEnv("credits.cost_master_final", "CREDIT_COST_MASTER_FINAL")
 
 	// Defaults
 	viper.SetDefault("server.port", "8000")
@@ -311,6 +351,20 @@ func Load() (*Config, error) {
 	viper.SetDefault("sosyal.stale_preparation_after", "1h")
 	viper.SetDefault("sosyal.stale_sweep_interval", "15m")
 	viper.SetDefault("ratelimit.cloudflare_ranges", strings.Join(clientip.DefaultCloudflareRanges, ","))
+	viper.SetDefault("ratelimit.credits_per_min", 60)
+	viper.SetDefault("ratelimit.admin_per_min", 30)
+
+	// Credit defaults: phase A ships in shadow mode (enforce=false).
+	viper.SetDefault("credits.enforce", false)
+	viper.SetDefault("credits.admin_user_ids", "")
+	viper.SetDefault("credits.sweep_interval", "1h")
+	viper.SetDefault("credits.cost_render_mix", 12)
+	viper.SetDefault("credits.cost_render_focus", 32)
+	viper.SetDefault("credits.cost_render_stems", 62)
+	viper.SetDefault("credits.cost_lyrics_generate", 2)
+	viper.SetDefault("credits.cost_lyrics_rewrite", 1)
+	viper.SetDefault("credits.cost_master_preview", 0)
+	viper.SetDefault("credits.cost_master_final", 3)
 
 	// Groq defaults
 	viper.SetDefault("groq.base_url", "https://api.groq.com/openai/v1")
@@ -408,6 +462,8 @@ func Load() (*Config, error) {
 			PublicPerMin:             viper.GetInt("ratelimit.public_per_min"),
 			PublicWebPerMin:          viper.GetInt("ratelimit.public_web_per_min"),
 			PublicWebVisitorPerMin:   viper.GetInt("ratelimit.public_web_visitor_per_min"),
+			CreditsPerMin:            viper.GetInt("ratelimit.credits_per_min"),
+			AdminPerMin:              viper.GetInt("ratelimit.admin_per_min"),
 		},
 		Groq: GroqConfig{
 			APIKey:  viper.GetString("groq.api_key"),
@@ -460,6 +516,26 @@ func Load() (*Config, error) {
 			StalePreparationAfter: viper.GetDuration("sosyal.stale_preparation_after"),
 			StaleSweepInterval:    viper.GetDuration("sosyal.stale_sweep_interval"),
 		},
+		Credits: CreditConfig{
+			Enforce:            viper.GetBool("credits.enforce"),
+			SweepInterval:      viper.GetDuration("credits.sweep_interval"),
+			CostRenderMix:      viper.GetInt("credits.cost_render_mix"),
+			CostRenderFocus:    viper.GetInt("credits.cost_render_focus"),
+			CostRenderStems:    viper.GetInt("credits.cost_render_stems"),
+			CostLyricsGenerate: viper.GetInt("credits.cost_lyrics_generate"),
+			CostLyricsRewrite:  viper.GetInt("credits.cost_lyrics_rewrite"),
+			CostMasterPreview:  viper.GetInt("credits.cost_master_preview"),
+			CostMasterFinal:    viper.GetInt("credits.cost_master_final"),
+		},
+	}
+
+	adminIDs, err := parseAdminUserIDs(listValue("credits.admin_user_ids"))
+	if err != nil {
+		return nil, fmt.Errorf("credits.admin_user_ids: %w", err)
+	}
+	cfg.Credits.AdminUserIDs = adminIDs
+	if err := cfg.Credits.validateCosts(); err != nil {
+		return nil, err
 	}
 
 	// An env var set to the empty string must DISABLE the Cloudflare
@@ -494,6 +570,59 @@ func Load() (*Config, error) {
 	return cfg, nil
 }
 
+// listValue reads a comma-separated list key. config.yaml may also write it
+// as a YAML list; GetString would turn that into "", so it is joined.
+func listValue(key string) string {
+	if list, ok := viper.Get(key).([]any); ok {
+		parts := make([]string, 0, len(list))
+		for _, item := range list {
+			parts = append(parts, fmt.Sprint(item))
+		}
+		return strings.Join(parts, ",")
+	}
+	return viper.GetString(key)
+}
+
+// parseAdminUserIDs parses the comma-separated admin list into canonical
+// (lower-case) UUID strings, the form the auth middleware stores. An
+// invalid entry is a startup error: a typo must not silently lock the
+// intended admin out or let a malformed value through.
+func parseAdminUserIDs(raw string) ([]string, error) {
+	var ids []string
+	for _, part := range strings.Split(raw, ",") {
+		part = strings.TrimSpace(part)
+		if part == "" {
+			continue
+		}
+		id, err := uuid.Parse(part)
+		if err != nil {
+			return nil, fmt.Errorf("invalid user id %q: %w", part, err)
+		}
+		ids = append(ids, id.String())
+	}
+	return ids, nil
+}
+
+// validateCosts rejects negative prices: a negative reservation would add
+// credit instead of spending it.
+func (c CreditConfig) validateCosts() error {
+	costs := map[string]int{
+		"credits.cost_render_mix":      c.CostRenderMix,
+		"credits.cost_render_focus":    c.CostRenderFocus,
+		"credits.cost_render_stems":    c.CostRenderStems,
+		"credits.cost_lyrics_generate": c.CostLyricsGenerate,
+		"credits.cost_lyrics_rewrite":  c.CostLyricsRewrite,
+		"credits.cost_master_preview":  c.CostMasterPreview,
+		"credits.cost_master_final":    c.CostMasterFinal,
+	}
+	for key, value := range costs {
+		if value < 0 {
+			return fmt.Errorf("%s must not be negative (got %d)", key, value)
+		}
+	}
+	return nil
+}
+
 // buildFederationClients builds the federation provider list from the
 // AUTH_FED_* values read from viper.
 //
````

**Modify** `core-service/config.yaml` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/config.yaml b/core-service/config.yaml
index 96aff9a..5a46cff 100644
--- a/core-service/config.yaml
+++ b/core-service/config.yaml
@@ -22,6 +22,8 @@ ratelimit:
   public_per_min: 30 # GET /api/public/s/{slug}, per client IP
   public_web_per_min: 600 # same endpoint, shared by all web requests carrying X-Web-Anahtari
   public_web_visitor_per_min: 30 # per visitor (X-Ziyaretci-Ip) within the web requests, also checked; <=0 disables
+  credits_per_min: 60 # GET /api/credits and /api/credits/history, per user (shared counter)
+  admin_per_min: 30 # /api/admin/*, per user
   # Cloudflare edge ranges whose CF-Connecting-IP is trusted for per-IP limits (comma-separated CIDRs).
   # Unset = the built-in Cloudflare list (clientip.DefaultCloudflareRanges); empty string disables the header.
   # cloudflare_ranges: ""
@@ -32,6 +34,19 @@ sosyal:
   stale_preparation_after: 1h # SOCIAL_STALE_PREPARATION_AFTER: preparing longer than this -> failed
   stale_sweep_interval: 15m # SOCIAL_STALE_SWEEP_INTERVAL: sweep period; <= 0 disables the scheduler
 
+# Credit system. enforce=false is shadow mode: spending is recorded, never blocked.
+credits:
+  enforce: false # CREDITS_ENFORCE
+  admin_user_ids: "" # ADMIN_USER_IDS: comma-separated user UUIDs allowed on /api/admin; empty = closed
+  sweep_interval: 1h # CREDITS_SWEEP_INTERVAL: kredi:supur period; <= 0 disables the scheduler
+  cost_render_mix: 12 # CREDIT_COST_RENDER_MIX
+  cost_render_focus: 32 # CREDIT_COST_RENDER_FOCUS
+  cost_render_stems: 62 # CREDIT_COST_RENDER_STEMS
+  cost_lyrics_generate: 2 # CREDIT_COST_LYRICS_GENERATE
+  cost_lyrics_rewrite: 1 # CREDIT_COST_LYRICS_REWRITE
+  cost_master_preview: 0 # CREDIT_COST_MASTER_PREVIEW
+  cost_master_final: 3 # CREDIT_COST_MASTER_FINAL
+
 # Groq AI Configuration
 groq:
   api_key: "" # Set via GROQ_API_KEY env var
````

**Modify** `core-service/.env.example` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/.env.example b/core-service/.env.example
index 3cc9645..69e73cc 100644
--- a/core-service/.env.example
+++ b/core-service/.env.example
@@ -25,6 +25,10 @@ RATELIMIT_PUBLIC_PER_MIN=30
 RATELIMIT_PUBLIC_WEB_PER_MIN=600
 # Per-visitor limit inside web requests (X-Web-Anahtari + X-Ziyaretci-Ip), checked before the shared one; <=0 disables
 RATELIMIT_PUBLIC_WEB_VISITOR_PER_MIN=30
+# GET /api/credits and /api/credits/history, per user per minute (shared counter)
+RATELIMIT_CREDITS_PER_MIN=60
+# /api/admin/*, per user per minute
+RATELIMIT_ADMIN_PER_MIN=30
 # Cloudflare edge ranges (comma-separated CIDRs): CF-Connecting-IP is trusted for per-IP limits only when the
 # peer (Traefik X-Real-Ip) is inside them. Unset = built-in Cloudflare list; empty value = header never trusted.
 # An invalid entry stops the server at startup.
@@ -40,6 +44,23 @@ SOSYAL_WEB_ANAHTARI=
 SOCIAL_STALE_PREPARATION_AFTER=1h
 SOCIAL_STALE_SWEEP_INTERVAL=15m
 
+# Credit system. CREDITS_ENFORCE=false is shadow mode: spending is recorded in the ledger
+# but an insufficient balance never blocks a job. Turn it on only after store billing ships.
+CREDITS_ENFORCE=false
+# Comma-separated user UUIDs allowed on /api/admin/* (gift campaigns). Empty = admin closed.
+# An invalid entry stops the server at startup.
+ADMIN_USER_IDS=
+# kredi:supur period (expire lots, release reservations held > 3h, stop finished campaigns); <= 0 disables.
+CREDITS_SWEEP_INTERVAL=1h
+# Credit price per operation (GET /api/credits returns them as "costs"); 0 = free.
+CREDIT_COST_RENDER_MIX=12
+CREDIT_COST_RENDER_FOCUS=32
+CREDIT_COST_RENDER_STEMS=62
+CREDIT_COST_LYRICS_GENERATE=2
+CREDIT_COST_LYRICS_REWRITE=1
+CREDIT_COST_MASTER_PREVIEW=0
+CREDIT_COST_MASTER_FINAL=3
+
 # Groq AI Configuration
 GROQ_API_KEY=gsk_...
 GROQ_BASE_URL=https://api.groq.com/openai/v1
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/config/`
Expected: `ok` (all existing config tests still pass).

- [ ] **Step 5: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review the diff.
Expected: the `config-yaml` and `env-example` hashes change; new STR lines for `CREDITS_ENFORCE`, `ADMIN_USER_IDS`, `CREDITS_SWEEP_INTERVAL`, the seven `CREDIT_COST_*`, `RATELIMIT_CREDITS_PER_MIN`, `RATELIMIT_ADMIN_PER_MIN` and every `credits.*` / `ratelimit.credits_per_min` / `ratelimit.admin_per_min` key (62 added, 2 removed lines).

- [ ] **Step 6: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/config core-service/config.yaml core-service/.env.example core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): kredi ayarlari ve hiz sinirlari

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Ledger store (grant, reserve, commit, release, balance, history)

The core SQL: reservation order earliest-expiring lot first, regardless of pool, with `FOR UPDATE` (K4), insufficient balance when enforced, amount 0 in shadow mode, idempotency per ref, release back to the allocated lots even if expired, history without commit and zero rows (R3, R6, R7).

**Files:**
- Create: `core-service/internal/credit/store_helpers_test.go`
- Create: `core-service/internal/credit/store_test.go`
- Create: `core-service/internal/credit/model.go`
- Create: `core-service/internal/credit/errors.go`
- Create: `core-service/internal/credit/store.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: `testPool` (Task 1).
- Produces:
  - `type Operation string` with `OpRenderMix`, `OpRenderFocus`, `OpRenderStems`, `OpLyricsGenerate`, `OpLyricsRewrite`, `OpMasterPreview`, `OpMasterFinal`; `type Costs map[Operation]int`.
  - Constants `PoolSubscription`, `PoolGift`, `SourceCampaign`, `SourceAdmin`, `KindGrant|KindReserve|KindCommit|KindRelease|KindExpire`, `StatusHeld|StatusCommitted|StatusReleased`, `ReasonJobFailed|ReasonCanceled|ReasonEnqueueFailed|ReasonRequestFailed|ReasonStale`, `PlanFree`.
  - Types `Reservation{Ref, UserID string; Operation Operation; Amount int; Status string}`, `ReserveInput{Ref, UserID string; Operation Operation; Amount int; Enforce bool}`, `GrantInput{UserID, Pool, Source, SourceRef string; Amount int; ExpiresAt time.Time}`, `Lot`, `Balances{Subscription, Gift int; GiftNextExpiry *time.Time}`, `Summary`, `Pools`, `SubscriptionPool`, `GiftPool`, `WelcomeGift{CampaignID string; Amount int; ExpiresAt time.Time}`, `HistoryItem`, `HistoryPage`, `HistoryCursor{CreatedAt time.Time; ID int64}`.
  - Errors `ErrNotFound`, `ErrValidation`, `ErrInvalidIdentity`, `*InsufficientError{Required, Balance int}`, `AsInsufficient(err error) (*InsufficientError, bool)`.
  - `type LedgerStore interface` (methods below), `NewPostgresStore(pool *pgxpool.Pool) *PostgresStore` with `Grant(ctx, GrantInput) (*Lot, error)`, `Reserve(ctx, ReserveInput) (*Reservation, error)`, `Commit(ctx, ref string) error`, `Release(ctx, ref, reason string) error`, `Balances(ctx, userID string) (*Balances, error)`, `History(ctx, userID string, after *HistoryCursor, limit int) ([]HistoryItem, error)`.
  - Unexported `grantInTx(ctx, pgx.Tx, GrantInput) (*Lot, error)`, `uniqueViolation(error) bool` (Task 4 uses `grantInTx`).
  - Test helpers `testGrant`, `lotRemaining`, `entryAmounts`, `reservationStatus`, `newUser`, `reserveInput(user, ref string, amount int, enforce bool) ReserveInput`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/credit/store_helpers_test.go`:

````go
package credit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testGrant adds a lot through the store.
func testGrant(t *testing.T, store *PostgresStore, userID, pool string, amount int, expiresIn time.Duration) *Lot {
	t.Helper()
	lot, err := store.Grant(context.Background(), GrantInput{
		UserID: userID, Pool: pool, Source: SourceAdmin, Amount: amount, ExpiresAt: time.Now().Add(expiresIn),
	})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	return lot
}

func lotRemaining(t *testing.T, pool *pgxpool.Pool, lotID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT remaining FROM kredi.lots WHERE id = $1`, lotID).Scan(&n); err != nil {
		t.Fatalf("lot remaining: %v", err)
	}
	return n
}

// entryAmounts returns kind:amount pairs of the user's entries in insert order.
func entryAmounts(t *testing.T, pool *pgxpool.Pool, userID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT kind || ':' || amount FROM kredi.entries WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("entry: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func reservationStatus(t *testing.T, pool *pgxpool.Pool, ref string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM kredi.reservations WHERE ref = $1`, ref).Scan(&status); err != nil {
		t.Fatalf("reservation status: %v", err)
	}
	return status
}

func newUser() string { return uuid.NewString() }
````

**Create** `core-service/internal/credit/store_test.go`:

````go
package credit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func reserveInput(user, ref string, amount int, enforce bool) ReserveInput {
	return ReserveInput{Ref: ref, UserID: user, Operation: OpRenderFocus, Amount: amount, Enforce: enforce}
}

// K4: the lot that expires first is spent first, whatever its pool.
func TestPostgresStore_ReserveSpendsEarliestExpiringFirst(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	user := newUser()
	giftLate := testGrant(t, store, user, PoolGift, 10, 30*24*time.Hour)
	giftSoon := testGrant(t, store, user, PoolGift, 10, 2*24*time.Hour)
	sub := testGrant(t, store, user, PoolSubscription, 5, 10*24*time.Hour)

	r, err := store.Reserve(ctx, reserveInput(user, "job-1", 12, true))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if r.Amount != 12 || r.Status != StatusHeld {
		t.Fatalf("reservation = %+v", r)
	}
	if got := []int{lotRemaining(t, pool, sub.ID), lotRemaining(t, pool, giftSoon.ID), lotRemaining(t, pool, giftLate.ID)}; got[0] != 3 || got[1] != 0 || got[2] != 10 {
		t.Fatalf("remaining (sub, soon, late) = %v, want [3 0 10]", got)
	}
	b, err := store.Balances(ctx, user)
	if err != nil || b.Subscription != 3 || b.Gift != 10 {
		t.Fatalf("balances = %+v, %v", b, err)
	}
}

func TestPostgresStore_ReserveInsufficientWhenEnforced(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	user := newUser()
	lot := testGrant(t, store, user, PoolGift, 20, time.Hour)

	_, err := store.Reserve(context.Background(), reserveInput(user, "job-1", 32, true))
	var insufficient *InsufficientError
	if !errors.As(err, &insufficient) || insufficient.Required != 32 || insufficient.Balance != 20 {
		t.Fatalf("want InsufficientError{32, 20}, got %v", err)
	}
	if lotRemaining(t, pool, lot.ID) != 20 {
		t.Fatal("a refused reservation must not touch the lots")
	}
	if got := strings.Join(entryAmounts(t, pool, user), ","); got != "grant:20" {
		t.Fatalf("entries = %s, want only the grant", got)
	}
}

// Shadow mode records the attempt with amount 0 and takes nothing.
func TestPostgresStore_ReserveShadowRecordsZero(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	user := newUser()
	lot := testGrant(t, store, user, PoolGift, 20, time.Hour)

	r, err := store.Reserve(context.Background(), reserveInput(user, "job-1", 32, false))
	if err != nil || r.Amount != 0 {
		t.Fatalf("shadow reserve = %+v, %v", r, err)
	}
	if lotRemaining(t, pool, lot.ID) != 20 {
		t.Fatal("shadow reserve must not take credit")
	}
	if got := strings.Join(entryAmounts(t, pool, user), ","); got != "grant:20,reserve:0" {
		t.Fatalf("entries = %s", got)
	}
	if reservationStatus(t, pool, "job-1") != StatusHeld {
		t.Fatal("shadow reservation must be held")
	}
}

// With enough balance, shadow mode spends like enforced mode.
func TestPostgresStore_ReserveShadowSpendsWhenBalanceSuffices(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	user := newUser()
	lot := testGrant(t, store, user, PoolGift, 20, time.Hour)
	if r, err := store.Reserve(context.Background(), reserveInput(user, "job-1", 12, false)); err != nil || r.Amount != 12 {
		t.Fatalf("Reserve = %+v, %v", r, err)
	}
	if lotRemaining(t, pool, lot.ID) != 8 {
		t.Fatal("credit was not taken")
	}
}

func TestPostgresStore_ReserveIgnoresExpiredLots(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	user := newUser()
	testGrant(t, store, user, PoolGift, 50, -time.Minute)
	_, err := store.Reserve(context.Background(), reserveInput(user, "job-1", 1, true))
	if e, ok := AsInsufficient(err); !ok || e.Balance != 0 {
		t.Fatalf("expired lots must not count: %v", err)
	}
}

func TestPostgresStore_ReserveIsIdempotentPerRef(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	user := newUser()
	lot := testGrant(t, store, user, PoolGift, 20, time.Hour)
	for i := 0; i < 2; i++ {
		if r, err := store.Reserve(ctx, reserveInput(user, "job-1", 5, true)); err != nil || r.Amount != 5 {
			t.Fatalf("Reserve #%d = %+v, %v", i, r, err)
		}
	}
	if lotRemaining(t, pool, lot.ID) != 15 {
		t.Fatal("the same ref must be charged once")
	}
}

func TestPostgresStore_ReleaseRestoresLotsEvenWhenExpired(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	user := newUser()
	a := testGrant(t, store, user, PoolGift, 4, time.Hour)
	b := testGrant(t, store, user, PoolGift, 10, 2*time.Hour)
	if _, err := store.Reserve(ctx, reserveInput(user, "job-1", 6, true)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kredi.lots SET expires_at = now() - interval '1 minute' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.Release(ctx, "job-1", ReasonJobFailed); err != nil {
			t.Fatalf("Release #%d: %v", i, err)
		}
	}
	if lotRemaining(t, pool, a.ID) != 4 || lotRemaining(t, pool, b.ID) != 10 {
		t.Fatalf("lots = %d, %d; want 4, 10", lotRemaining(t, pool, a.ID), lotRemaining(t, pool, b.ID))
	}
	if err := store.Commit(ctx, "job-1"); err != nil {
		t.Fatalf("Commit after release: %v", err)
	}
	if got := strings.Join(entryAmounts(t, pool, user), ","); got != "grant:4,grant:10,reserve:-6,release:6" {
		t.Fatalf("entries = %s", got)
	}
	if reservationStatus(t, pool, "job-1") != StatusReleased {
		t.Fatal("status must be released")
	}
	var reason string
	if err := pool.QueryRow(ctx, `SELECT release_reason FROM kredi.reservations WHERE ref = 'job-1'`).Scan(&reason); err != nil || reason != ReasonJobFailed {
		t.Fatalf("release_reason = %q, %v", reason, err)
	}
}

func TestPostgresStore_CommitIsIdempotentAndFinal(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	user := newUser()
	lot := testGrant(t, store, user, PoolGift, 10, time.Hour)
	if _, err := store.Reserve(ctx, reserveInput(user, "job-1", 3, true)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := store.Commit(ctx, "job-1"); err != nil {
			t.Fatalf("Commit #%d: %v", i, err)
		}
	}
	if err := store.Release(ctx, "job-1", ReasonJobFailed); err != nil {
		t.Fatalf("Release after commit: %v", err)
	}
	if lotRemaining(t, pool, lot.ID) != 7 {
		t.Fatal("a committed reservation must not be returned")
	}
	if got := strings.Join(entryAmounts(t, pool, user), ","); got != "grant:10,reserve:-3,commit:0" {
		t.Fatalf("entries = %s", got)
	}
}

func TestPostgresStore_UnknownRefIsNoop(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	if err := store.Commit(context.Background(), "nope"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := store.Release(context.Background(), "nope", ReasonJobFailed); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// Concurrent reservations of one user never overspend (FOR UPDATE).
func TestPostgresStore_ConcurrentReservesNeverOverspend(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	user := newUser()
	lot := testGrant(t, store, user, PoolGift, 10, time.Hour)

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, refused := 0, 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.Reserve(context.Background(), reserveInput(user, "job-"+string(rune('a'+i)), 3, true))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded++
			} else if _, ok := AsInsufficient(err); ok {
				refused++
			} else {
				t.Errorf("Reserve: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if succeeded != 3 || refused != 3 || lotRemaining(t, pool, lot.ID) != 1 {
		t.Fatalf("succeeded=%d refused=%d remaining=%d; want 3, 3, 1", succeeded, refused, lotRemaining(t, pool, lot.ID))
	}
}

func TestPostgresStore_HistoryHidesCommitAndZeroRowsAndPages(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	user := newUser()
	testGrant(t, store, user, PoolGift, 10, time.Hour)
	if _, err := store.Reserve(ctx, reserveInput(user, "job-1", 3, true)); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(ctx, "job-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reserve(ctx, reserveInput(user, "job-2", 30, false)); err != nil { // shadow: amount 0
		t.Fatal(err)
	}
	if _, err := store.Reserve(ctx, reserveInput(user, "job-3", 2, true)); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(ctx, "job-3", ReasonJobFailed); err != nil {
		t.Fatal(err)
	}

	first, err := store.History(ctx, user, nil, 2)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(first) != 2 || first[0].Kind != KindRelease || first[0].Amount != 2 || first[1].Kind != KindReserve || first[1].Amount != -2 {
		t.Fatalf("first page = %+v", first)
	}
	if first[1].Operation == nil || *first[1].Operation != string(OpRenderFocus) || first[1].Label != nil {
		t.Fatalf("operation/label = %v/%v", first[1].Operation, first[1].Label)
	}
	last := first[len(first)-1]
	second, err := store.History(ctx, user, &HistoryCursor{CreatedAt: last.CreatedAt, ID: last.ID}, 2)
	if err != nil {
		t.Fatalf("History page 2: %v", err)
	}
	if len(second) != 2 || second[0].Kind != KindReserve || second[0].Amount != -3 || second[1].Kind != KindGrant {
		t.Fatalf("second page = %+v", second)
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/credit/`
Expected: build failure, `undefined: PostgresStore`, `undefined: GrantInput`, `undefined: ReserveInput`, ...

- [ ] **Step 3: Implement the types, errors and store**

**Create** `core-service/internal/credit/model.go`:

````go
package credit

import "time"

// Operation is a credit-spending operation. The values are wire contract:
// keys of "costs" in GET /api/credits and the operation column of entries
// and reservations.
type Operation string

const (
	OpRenderMix      Operation = "render_mix"
	OpRenderFocus    Operation = "render_focus"
	OpRenderStems    Operation = "render_stems"
	OpLyricsGenerate Operation = "lyrics_generate"
	OpLyricsRewrite  Operation = "lyrics_rewrite"
	OpMasterPreview  Operation = "master_preview"
	OpMasterFinal    Operation = "master_final"
)

// Costs is the credit price of each operation. A missing operation is free.
type Costs map[Operation]int

// Lot pools and sources (kredi.lots.pool / source).
const (
	PoolSubscription = "subscription"
	PoolGift         = "gift"

	SourceCampaign = "campaign"
	SourceAdmin    = "admin"
)

// Entry kinds (kredi.entries.kind).
const (
	KindGrant   = "grant"
	KindReserve = "reserve"
	KindCommit  = "commit"
	KindRelease = "release"
	KindExpire  = "expire"
)

// Reservation statuses (kredi.reservations.status).
const (
	StatusHeld      = "held"
	StatusCommitted = "committed"
	StatusReleased  = "released"
)

// Release reasons (kredi.reservations.release_reason).
const (
	ReasonJobFailed     = "job_failed"
	ReasonCanceled      = "canceled"
	ReasonEnqueueFailed = "enqueue_failed"
	ReasonRequestFailed = "request_failed"
	ReasonStale         = "stale"
)

// Reservation is credit held for one job or request, identified by ref.
type Reservation struct {
	Ref       string
	UserID    string
	Operation Operation
	Amount    int
	Status    string
}

// allocation is one lot's share of a reservation (reservations.allocation).
type allocation struct {
	LotID  string `json:"lot_id"`
	Amount int    `json:"amount"`
}

// ReserveInput is a store-level reservation request. Amount is the price
// already resolved by the service.
type ReserveInput struct {
	Ref       string
	UserID    string
	Operation Operation
	Amount    int
	// Enforce makes an insufficient balance an *InsufficientError. When
	// false (shadow mode) the reservation is recorded with amount 0.
	Enforce bool
}

// GrantInput adds a lot.
type GrantInput struct {
	UserID    string
	Pool      string
	Source    string
	SourceRef string
	Amount    int
	ExpiresAt time.Time
}

// Lot is one slice of credit.
type Lot struct {
	ID        string
	UserID    string
	Pool      string
	Source    string
	Amount    int
	Remaining int
	ExpiresAt time.Time
}

// Balances are the live (unexpired, non-empty) lot totals per pool.
type Balances struct {
	Subscription   int
	Gift           int
	GiftNextExpiry *time.Time
}

// Summary is the body of GET /api/credits.
type Summary struct {
	Balance     int          `json:"balance"`
	Pools       Pools        `json:"pools"`
	Plan        string       `json:"plan"`
	Costs       Costs        `json:"costs"`
	Enforced    bool         `json:"enforced"`
	WelcomeGift *WelcomeGift `json:"welcomeGift"`
}

// Pools splits the balance; a pool with nothing live is null.
type Pools struct {
	Subscription *SubscriptionPool `json:"subscription"`
	Gift         *GiftPool         `json:"gift"`
}

// SubscriptionPool is filled from phase C on; phase A always reports null.
type SubscriptionPool struct {
	Amount   int       `json:"amount"`
	Plan     string    `json:"plan"`
	RenewsAt time.Time `json:"renewsAt"`
}

// GiftPool is the live gift credit and the earliest expiry among its lots.
type GiftPool struct {
	Amount     int       `json:"amount"`
	NextExpiry time.Time `json:"nextExpiry"`
}

// WelcomeGift is the user's most recent campaign claim, shown for 7 days.
type WelcomeGift struct {
	CampaignID string    `json:"campaignId"`
	Amount     int       `json:"amount"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// PlanFree is the only plan before subscriptions exist.
const PlanFree = "free"

// HistoryItem is one visible ledger row.
type HistoryItem struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Amount    int       `json:"amount"`
	Operation *string   `json:"operation"`
	Label     *string   `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
}

// HistoryPage is the body of GET /api/credits/history. Items is never null;
// NextCursor is null on the last page.
type HistoryPage struct {
	Items      []HistoryItem `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

// HistoryCursor is the position after the last item of a page.
type HistoryCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        int64     `json:"id"`
}
````

**Create** `core-service/internal/credit/errors.go`:

````go
package credit

import (
	"errors"
	"fmt"
)

// Service errors. The HTTP layer maps them to contract codes; these texts
// only reach logs.
var (
	ErrNotFound        = errors.New("not found")
	ErrValidation      = errors.New("validation error")
	ErrInvalidIdentity = errors.New("invalid identity")
)

// InsufficientError is returned by Reserve when enforcement is on and the
// live balance is below the price (402 kredi_yetersiz with
// details {required, balance}).
type InsufficientError struct {
	Required int
	Balance  int
}

func (e *InsufficientError) Error() string {
	return fmt.Sprintf("insufficient credits: required %d, balance %d", e.Required, e.Balance)
}

// AsInsufficient reports whether err carries an *InsufficientError.
func AsInsufficient(err error) (*InsufficientError, bool) {
	var e *InsufficientError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
````

**Create** `core-service/internal/credit/store.go`:

````go
package credit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LedgerStore covers lots, ledger entries and reservations.
type LedgerStore interface {
	// Grant adds a lot and its grant entry.
	Grant(ctx context.Context, in GrantInput) (*Lot, error)
	// Reserve holds credit for in.Ref from the user's live lots, the lot
	// that expires first spent first, whatever its pool. A ref that already has a
	// reservation returns it unchanged (idempotent).
	Reserve(ctx context.Context, in ReserveInput) (*Reservation, error)
	// Commit marks a held reservation committed; otherwise a no-op.
	Commit(ctx context.Context, ref string) error
	// Release returns a held reservation's credit to its lots (expired lots
	// included); otherwise a no-op.
	Release(ctx context.Context, ref, reason string) error
	// Balances returns the live totals per pool.
	Balances(ctx context.Context, userID string) (*Balances, error)
	// History returns visible entries (no commit rows, no zero amounts),
	// newest first, after the cursor.
	History(ctx context.Context, userID string, after *HistoryCursor, limit int) ([]HistoryItem, error)
}

// PostgresStore is the Postgres implementation of the credit stores. User
// ids arrive validated as UUIDs (the service uses parseUserID).
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

var _ LedgerStore = (*PostgresStore)(nil)

// queryRower is the QueryRow part shared by *pgxpool.Pool and pgx.Tx.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *PostgresStore) Grant(ctx context.Context, in GrantInput) (*Lot, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not start the grant transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lot, err := grantInTx(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("could not commit the grant: %w", err)
	}
	return lot, nil
}

// grantInTx inserts the lot and its grant entry (ref = source ref).
func grantInTx(ctx context.Context, tx pgx.Tx, in GrantInput) (*Lot, error) {
	lot := &Lot{
		ID: uuid.NewString(), UserID: in.UserID, Pool: in.Pool, Source: in.Source,
		Amount: in.Amount, Remaining: in.Amount, ExpiresAt: in.ExpiresAt,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kredi.lots (id, user_id, pool, source, source_ref, amount, remaining, expires_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $6, $7)`,
		lot.ID, in.UserID, in.Pool, in.Source, in.SourceRef, in.Amount, in.ExpiresAt); err != nil {
		return nil, fmt.Errorf("could not insert the lot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kredi.entries (user_id, kind, amount, ref)
		VALUES ($1, 'grant', $2, NULLIF($3, ''))`, in.UserID, in.Amount, in.SourceRef); err != nil {
		return nil, fmt.Errorf("could not insert the grant entry: %w", err)
	}
	return lot, nil
}

func getReservation(ctx context.Context, q queryRower, ref string) (*Reservation, error) {
	var r Reservation
	var op string
	err := q.QueryRow(ctx, `
		SELECT ref, user_id::text, operation, amount, status
		  FROM kredi.reservations WHERE ref = $1`, ref,
	).Scan(&r.Ref, &r.UserID, &op, &r.Amount, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the reservation: %w", err)
	}
	r.Operation = Operation(op)
	return &r, nil
}

// liveLot is a lot's id and remaining credit at reservation time.
type liveLot struct {
	id        string
	remaining int
}

// allocate takes amount from the lots in order.
func allocate(lots []liveLot, amount int) []allocation {
	out := []allocation{}
	for _, lot := range lots {
		if amount == 0 {
			break
		}
		take := min(lot.remaining, amount)
		out = append(out, allocation{LotID: lot.id, Amount: take})
		amount -= take
	}
	return out
}

func (s *PostgresStore) Reserve(ctx context.Context, in ReserveInput) (*Reservation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not start the reserve transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, err := getReservation(ctx, tx, in.Ref)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// The lot that expires first is spent first, whatever its pool (K4).
	// FOR UPDATE serializes concurrent reservations of the same user.
	rows, err := tx.Query(ctx, `
		SELECT id::text, remaining FROM kredi.lots
		 WHERE user_id = $1 AND remaining > 0 AND expires_at > now()
		 ORDER BY expires_at, created_at, id
		 FOR UPDATE`, in.UserID)
	if err != nil {
		return nil, fmt.Errorf("could not lock the lots: %w", err)
	}
	var lots []liveLot
	balance := 0
	for rows.Next() {
		var lot liveLot
		if err := rows.Scan(&lot.id, &lot.remaining); err != nil {
			rows.Close()
			return nil, fmt.Errorf("could not read a lot: %w", err)
		}
		lots = append(lots, lot)
		balance += lot.remaining
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read the lots: %w", err)
	}

	amount := in.Amount
	if balance < amount {
		if in.Enforce {
			return nil, &InsufficientError{Required: amount, Balance: balance}
		}
		// Shadow mode: record the attempt, take nothing.
		amount = 0
	}
	allocations := allocate(lots, amount)
	for _, a := range allocations {
		if _, err := tx.Exec(ctx,
			`UPDATE kredi.lots SET remaining = remaining - $2 WHERE id = $1`, a.LotID, a.Amount); err != nil {
			return nil, fmt.Errorf("could not take credit from a lot: %w", err)
		}
	}
	allocationJSON, err := json.Marshal(allocations)
	if err != nil {
		return nil, fmt.Errorf("could not encode the allocation: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO kredi.reservations (ref, user_id, operation, amount, allocation, status)
		VALUES ($1, $2, $3, $4, $5, 'held')`,
		in.Ref, in.UserID, string(in.Operation), amount, allocationJSON)
	if uniqueViolation(err) {
		// A concurrent call with the same ref won; return its reservation.
		_ = tx.Rollback(ctx)
		return getReservation(ctx, s.pool, in.Ref)
	}
	if err != nil {
		return nil, fmt.Errorf("could not insert the reservation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO kredi.entries (user_id, kind, amount, operation, ref)
		VALUES ($1, 'reserve', $2, $3, $4)`, in.UserID, -amount, string(in.Operation), in.Ref); err != nil {
		return nil, fmt.Errorf("could not insert the reserve entry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("could not commit the reservation: %w", err)
	}
	return &Reservation{Ref: in.Ref, UserID: in.UserID, Operation: in.Operation, Amount: amount, Status: StatusHeld}, nil
}

func (s *PostgresStore) Commit(ctx context.Context, ref string) error {
	if _, err := s.pool.Exec(ctx, `
		WITH c AS (
			UPDATE kredi.reservations SET status = 'committed', settled_at = now()
			 WHERE ref = $1 AND status = 'held'
			RETURNING user_id, operation, ref
		)
		INSERT INTO kredi.entries (user_id, kind, amount, operation, ref)
		SELECT user_id, 'commit', 0, operation, ref FROM c`, ref); err != nil {
		return fmt.Errorf("could not commit the reservation: %w", err)
	}
	return nil
}

func (s *PostgresStore) Release(ctx context.Context, ref, reason string) error {
	// The lots update is a data-modifying CTE: Postgres runs it even though
	// the INSERT does not read it. Expired lots get their credit back too;
	// the balance ignores them and the next sweep expires it again.
	if _, err := s.pool.Exec(ctx, `
		WITH r AS (
			UPDATE kredi.reservations
			   SET status = 'released', settled_at = now(), release_reason = $2
			 WHERE ref = $1 AND status = 'held'
			RETURNING user_id, operation, ref, amount, allocation
		), restored AS (
			UPDATE kredi.lots l SET remaining = l.remaining + a.amount
			  FROM r, jsonb_to_recordset(r.allocation) AS a(lot_id uuid, amount integer)
			 WHERE l.id = a.lot_id
			RETURNING l.id
		)
		INSERT INTO kredi.entries (user_id, kind, amount, operation, ref)
		SELECT user_id, 'release', amount, operation, ref FROM r`, ref, reason); err != nil {
		return fmt.Errorf("could not release the reservation: %w", err)
	}
	return nil
}

func (s *PostgresStore) Balances(ctx context.Context, userID string) (*Balances, error) {
	var b Balances
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(remaining) FILTER (WHERE pool = 'subscription'), 0),
		       COALESCE(SUM(remaining) FILTER (WHERE pool = 'gift'), 0),
		       MIN(expires_at) FILTER (WHERE pool = 'gift')
		  FROM kredi.lots
		 WHERE user_id = $1 AND remaining > 0 AND expires_at > now()`, userID,
	).Scan(&b.Subscription, &b.Gift, &b.GiftNextExpiry); err != nil {
		return nil, fmt.Errorf("could not read the balance: %w", err)
	}
	return &b, nil
}

func (s *PostgresStore) History(ctx context.Context, userID string, after *HistoryCursor, limit int) ([]HistoryItem, error) {
	var afterTime *time.Time
	var afterID *int64
	if after != nil {
		afterTime, afterID = &after.CreatedAt, &after.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, amount, operation, label, created_at FROM kredi.entries
		 WHERE user_id = $1 AND kind <> 'commit' AND amount <> 0
		   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::bigint))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`, userID, afterTime, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("could not list the history: %w", err)
	}
	defer rows.Close()
	items := []HistoryItem{}
	for rows.Next() {
		var item HistoryItem
		if err := rows.Scan(&item.ID, &item.Kind, &item.Amount, &item.Operation, &item.Label, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("could not read a history entry: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 -v ./internal/credit/ 2>&1 | grep -E '^(---|ok)'`
Expected (with Postgres): PASS for `TestPostgresStore_ReserveSpendsEarliestExpiringFirst`, `..._ReserveInsufficientWhenEnforced`, `..._ReserveShadowRecordsZero`, `..._ReserveShadowSpendsWhenBalanceSuffices`, `..._ReserveIgnoresExpiredLots`, `..._ReserveIsIdempotentPerRef`, `..._ReleaseRestoresLotsEvenWhenExpired`, `..._CommitIsIdempotentAndFinal`, `..._UnknownRefIsNoop`, `..._ConcurrentReservesNeverOverspend`, `..._HistoryHidesCommitAndZeroRowsAndPages`, plus Task 1's tests; `ok`. Run `go test -count=3 ./internal/credit/` once to make sure the concurrency test is stable.

- [ ] **Step 5: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 69 added lines: the ledger SQL strings, the operation values (`render_mix` ... `master_final`), pool/kind/status/reason values (`gift`, `grant`, `held`, `committed`, `job_failed`, `canceled`, `enqueue_failed`, `request_failed`, `stale`, `free`, ...), `"23505"`, and the JSON tags of `Summary`, `Pools`, `GiftPool`, `SubscriptionPool`, `WelcomeGift`, `HistoryItem`, `HistoryPage`, `HistoryCursor` (`t`, `id`) and `allocation` (`lot_id`, `amount`).

- [ ] **Step 6: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/credit core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): defter deposu (rezerve, onay, iade, bakiye, gecmis)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Campaign store

Claims with the conditional quota UPDATE (the filling claim stops the campaign), one active campaign (K6, R11), idempotent stop, recent claim for `welcomeGift`.

**Files:**
- Create: `core-service/internal/credit/store_campaign_test.go`
- Create: `core-service/internal/credit/model_campaign.go`
- Create: `core-service/internal/credit/store_campaign.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: `grantInTx`, `PostgresStore`, `ErrNotFound`, `WelcomeGift` (Task 3).
- Produces: constants `CampaignActive|CampaignStopped|CampaignExhausted|CampaignExpired`; `type Campaign struct{ID, Name string; PerUser, Quota, Used, ValidityDays int; StartsAt time.Time; EndsAt, StoppedAt *time.Time; CreatedBy string; CreatedAt time.Time; Status string}` (JSON keys in the HTTP table); `type NewCampaign struct{Name string; PerUser, Quota, ValidityDays int; EndsAt *time.Time; CreatedBy string}`; `type CampaignStore interface` with `ActiveCampaign(ctx) (*Campaign, error)` (nil, nil when none), `HasClaim(ctx, campaignID, userID string) (bool, error)`, `Claim(ctx, campaignID, userID string) (bool, error)`, `RecentClaim(ctx, userID string, window time.Duration) (*WelcomeGift, error)`, `CreateCampaign(ctx, NewCampaign) (*Campaign, error)`, `StopCampaign(ctx, id string) (*Campaign, error)`, `ListCampaigns(ctx, limit int) ([]Campaign, error)`; test helper `testCampaign(t, store, quota, perUser int) *Campaign`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/credit/store_campaign_test.go`:

````go
package credit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testCampaign(t *testing.T, store *PostgresStore, quota, perUser int) *Campaign {
	t.Helper()
	c, err := store.CreateCampaign(context.Background(), NewCampaign{
		Name: "Hos geldin", PerUser: perUser, Quota: quota, ValidityDays: 30, CreatedBy: newUser(),
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return c
}

func TestPostgresStore_ClaimGrantsGiftOnce(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	c := testCampaign(t, store, 10, 50)
	user := newUser()

	ok, err := store.Claim(ctx, c.ID, user)
	if err != nil || !ok {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if ok, err := store.Claim(ctx, c.ID, user); err != nil || ok {
		t.Fatalf("second claim = %v, %v; want false", ok, err)
	}
	if has, err := store.HasClaim(ctx, c.ID, user); err != nil || !has {
		t.Fatalf("HasClaim = %v, %v", has, err)
	}
	b, err := store.Balances(ctx, user)
	if err != nil || b.Gift != 50 || b.GiftNextExpiry == nil {
		t.Fatalf("balances = %+v, %v", b, err)
	}
	if d := time.Until(*b.GiftNextExpiry); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("gift expires in %v, want ~30 days from the claim (K7)", d)
	}
	if got := strings.Join(entryAmounts(t, pool, user), ","); got != "grant:50" {
		t.Fatalf("entries = %s", got)
	}
	active, err := store.ActiveCampaign(ctx)
	if err != nil || active == nil || active.Used != 1 {
		t.Fatalf("active = %+v, %v; want used 1", active, err)
	}
}

// N parallel claims never pass the quota; the last slot stops the campaign.
func TestPostgresStore_ClaimQuotaRace(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	c := testCampaign(t, store, 3, 10)

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.Claim(context.Background(), c.ID, newUser())
			if err != nil {
				t.Errorf("Claim: %v", err)
			}
			if ok {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 3 {
		t.Fatalf("granted = %d, want 3", granted)
	}
	list, err := store.ListCampaigns(context.Background(), 10)
	if err != nil || len(list) != 1 || list[0].Used != 3 || list[0].StoppedAt == nil {
		t.Fatalf("campaign = %+v, %v; want used 3 and stopped", list, err)
	}
	if active, _ := store.ActiveCampaign(context.Background()); active != nil {
		t.Fatal("a full campaign must not stay active")
	}
}

func TestPostgresStore_ClaimRefusesEndedOrStopped(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	c := testCampaign(t, store, 10, 5)
	if _, err := pool.Exec(ctx, `UPDATE kredi.campaigns SET ends_at = now() - interval '1 minute' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Claim(ctx, c.ID, newUser()); err != nil || ok {
		t.Fatalf("ended campaign claim = %v, %v", ok, err)
	}
	other := testCampaign(t, store, 10, 5)
	if _, err := store.StopCampaign(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Claim(ctx, other.ID, newUser()); err != nil || ok {
		t.Fatalf("stopped campaign claim = %v, %v", ok, err)
	}
}

// K6: a new campaign stops the active one.
func TestPostgresStore_CreateCampaignStopsActive(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()
	first := testCampaign(t, store, 10, 5)
	second := testCampaign(t, store, 20, 7)
	active, err := store.ActiveCampaign(ctx)
	if err != nil || active == nil || active.ID != second.ID {
		t.Fatalf("active = %+v, %v; want the second", active, err)
	}
	list, err := store.ListCampaigns(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID || list[1].StoppedAt == nil {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestPostgresStore_StopCampaignIdempotentAndNotFound(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()
	c := testCampaign(t, store, 10, 5)
	first, err := store.StopCampaign(ctx, c.ID)
	if err != nil || first.StoppedAt == nil {
		t.Fatalf("stop = %+v, %v", first, err)
	}
	again, err := store.StopCampaign(ctx, c.ID)
	if err != nil || !again.StoppedAt.Equal(*first.StoppedAt) {
		t.Fatalf("second stop changed stopped_at: %+v, %v", again, err)
	}
	if _, err := store.StopCampaign(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown campaign: %v", err)
	}
}

func TestPostgresStore_RecentClaimWindow(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	c := testCampaign(t, store, 10, 25)
	user := newUser()
	if g, err := store.RecentClaim(ctx, user, 7*24*time.Hour); err != nil || g != nil {
		t.Fatalf("before the claim = %+v, %v", g, err)
	}
	if ok, err := store.Claim(ctx, c.ID, user); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	g, err := store.RecentClaim(ctx, user, 7*24*time.Hour)
	if err != nil || g == nil || g.CampaignID != c.ID || g.Amount != 25 {
		t.Fatalf("recent claim = %+v, %v", g, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kredi.campaign_claims SET claimed_at = now() - interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if g, err := store.RecentClaim(ctx, user, 7*24*time.Hour); err != nil || g != nil {
		t.Fatalf("after 8 days = %+v, %v; want nil", g, err)
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/credit/`
Expected: build failure, `undefined: NewCampaign`, `store.Claim undefined`, ...

- [ ] **Step 3: Implement**

**Create** `core-service/internal/credit/model_campaign.go`:

````go
package credit

import "time"

// Campaign statuses, derived from the row (never stored).
const (
	CampaignActive    = "active"
	CampaignStopped   = "stopped"
	CampaignExhausted = "exhausted"
	CampaignExpired   = "expired"
)

// Campaign is a gift campaign as the admin API shows it.
type Campaign struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	PerUser      int        `json:"perUser"`
	Quota        int        `json:"quota"`
	Used         int        `json:"used"`
	ValidityDays int        `json:"validityDays"`
	StartsAt     time.Time  `json:"startsAt"`
	EndsAt       *time.Time `json:"endsAt"`
	StoppedAt    *time.Time `json:"stoppedAt"`
	CreatedBy    string     `json:"createdBy"`
	CreatedAt    time.Time  `json:"createdAt"`
	Status       string     `json:"status"`
}

// NewCampaign is a validated campaign to insert; it starts now.
type NewCampaign struct {
	Name         string
	PerUser      int
	Quota        int
	ValidityDays int
	EndsAt       *time.Time
	CreatedBy    string
}
````

**Create** `core-service/internal/credit/store_campaign.go`:

````go
package credit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CampaignStore covers gift campaigns and their claims.
type CampaignStore interface {
	// ActiveCampaign returns the campaign without stopped_at, or nil.
	ActiveCampaign(ctx context.Context) (*Campaign, error)
	// HasClaim reports whether the user already claimed the campaign.
	HasClaim(ctx context.Context, campaignID, userID string) (bool, error)
	// Claim gives the campaign's gift to the user in one transaction. It
	// returns false when the campaign is stopped, ended or full, or the user
	// already claimed it. The claim that fills the quota stops the campaign.
	Claim(ctx context.Context, campaignID, userID string) (bool, error)
	// RecentClaim returns the user's newest claim made within window, or nil.
	RecentClaim(ctx context.Context, userID string, window time.Duration) (*WelcomeGift, error)
	// CreateCampaign stops the active campaign (K6) and starts the new one.
	CreateCampaign(ctx context.Context, in NewCampaign) (*Campaign, error)
	// StopCampaign sets stopped_at if it is not set yet; ErrNotFound if the
	// campaign does not exist.
	StopCampaign(ctx context.Context, id string) (*Campaign, error)
	// ListCampaigns returns the newest campaigns first.
	ListCampaigns(ctx context.Context, limit int) ([]Campaign, error)
}

var _ CampaignStore = (*PostgresStore)(nil)

// campaignLockKey serializes campaign creation, so two admins starting a
// campaign at once cannot both pass the "stop the active one" step.
const campaignLockKey int64 = 0x6B726564693032 // "kredi02"

const campaignColumns = `id::text, name, per_user, quota, used, validity_days,
	starts_at, ends_at, stopped_at, created_by::text, created_at`

func scanCampaign(row pgx.Row) (*Campaign, error) {
	var c Campaign
	if err := row.Scan(&c.ID, &c.Name, &c.PerUser, &c.Quota, &c.Used, &c.ValidityDays,
		&c.StartsAt, &c.EndsAt, &c.StoppedAt, &c.CreatedBy, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PostgresStore) ActiveCampaign(ctx context.Context) (*Campaign, error) {
	c, err := scanCampaign(s.pool.QueryRow(ctx,
		`SELECT `+campaignColumns+` FROM kredi.campaigns WHERE stopped_at IS NULL`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the active campaign: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) HasClaim(ctx context.Context, campaignID, userID string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM kredi.campaign_claims WHERE campaign_id = $1 AND user_id = $2)`,
		campaignID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("could not check the claim: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) Claim(ctx context.Context, campaignID, userID string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("could not start the claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The conditional UPDATE is the quota guard: the row lock serializes
	// concurrent claims and used never passes quota.
	var perUser int
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE kredi.campaigns
		   SET used = used + 1,
		       stopped_at = CASE WHEN used + 1 >= quota THEN now() ELSE stopped_at END
		 WHERE id = $1 AND stopped_at IS NULL AND used < quota
		   AND (ends_at IS NULL OR ends_at > now())
		RETURNING per_user, now() + make_interval(days => validity_days)`, campaignID,
	).Scan(&perUser, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not take a campaign slot: %w", err)
	}
	lot, err := grantInTx(ctx, tx, GrantInput{
		UserID: userID, Pool: PoolGift, Source: SourceCampaign, SourceRef: campaignID,
		Amount: perUser, ExpiresAt: expiresAt,
	})
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO kredi.campaign_claims (campaign_id, user_id, lot_id)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, campaignID, userID, lot.ID)
	if err != nil {
		return false, fmt.Errorf("could not record the claim: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already claimed: the rollback also undoes the slot and the lot.
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("could not commit the claim: %w", err)
	}
	return true, nil
}

func (s *PostgresStore) RecentClaim(ctx context.Context, userID string, window time.Duration) (*WelcomeGift, error) {
	var g WelcomeGift
	err := s.pool.QueryRow(ctx, `
		SELECT c.campaign_id::text, l.amount, l.expires_at
		  FROM kredi.campaign_claims c
		  JOIN kredi.lots l ON l.id = c.lot_id
		 WHERE c.user_id = $1 AND c.claimed_at > now() - $2::interval
		 ORDER BY c.claimed_at DESC
		 LIMIT 1`, userID, window,
	).Scan(&g.CampaignID, &g.Amount, &g.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the recent claim: %w", err)
	}
	return &g, nil
}

func (s *PostgresStore) CreateCampaign(ctx context.Context, in NewCampaign) (*Campaign, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not start the campaign transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, campaignLockKey); err != nil {
		return nil, fmt.Errorf("could not lock campaign creation: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE kredi.campaigns SET stopped_at = now() WHERE stopped_at IS NULL`); err != nil {
		return nil, fmt.Errorf("could not stop the active campaign: %w", err)
	}
	c, err := scanCampaign(tx.QueryRow(ctx, `
		INSERT INTO kredi.campaigns (id, name, per_user, quota, validity_days, starts_at, ends_at, created_by)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, now(), $5, $6)
		RETURNING `+campaignColumns,
		in.Name, in.PerUser, in.Quota, in.ValidityDays, in.EndsAt, in.CreatedBy))
	if err != nil {
		return nil, fmt.Errorf("could not insert the campaign: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("could not commit the campaign: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) StopCampaign(ctx context.Context, id string) (*Campaign, error) {
	c, err := scanCampaign(s.pool.QueryRow(ctx, `
		UPDATE kredi.campaigns SET stopped_at = COALESCE(stopped_at, now())
		 WHERE id = $1
		RETURNING `+campaignColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not stop the campaign: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) ListCampaigns(ctx context.Context, limit int) ([]Campaign, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+campaignColumns+` FROM kredi.campaigns
		ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("could not list the campaigns: %w", err)
	}
	defer rows.Close()
	campaigns := []Campaign{}
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, fmt.Errorf("could not read a campaign: %w", err)
		}
		campaigns = append(campaigns, *c)
	}
	return campaigns, rows.Err()
}
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=3 ./internal/credit/`
Expected: `ok`; with `-v`, `TestPostgresStore_ClaimGrantsGiftOnce`, `..._ClaimQuotaRace`, `..._ClaimRefusesEndedOrStopped`, `..._CreateCampaignStopsActive`, `..._StopCampaignIdempotentAndNotFound`, `..._RecentClaimWindow` PASS.

- [ ] **Step 5: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 29 added lines: campaign SQL strings (`pg_advisory_xact_lock`, the claim UPDATE, `campaignColumns`, ...), `active`, `stopped`, `exhausted`, `expired`, and the `Campaign` JSON tags.

- [ ] **Step 6: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/credit core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): kampanya deposu ve kota korumali talep

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Sweep and cleanup store

Expire lots with one `expire` entry each, list stale held reservations, stop finished campaigns, delete a user's credit rows (R14, R16).

**Files:**
- Create: `core-service/internal/credit/store_sweep_test.go`
- Create: `core-service/internal/credit/store_sweep.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: Tasks 3–4 store and helpers.
- Produces: `type SweepStore interface{ ExpireLots(ctx) (int64, error); StaleReservations(ctx, olderThan time.Duration, limit int) ([]string, error); StopFinishedCampaigns(ctx) (int64, error) }`; `type UserDataCleaner interface{ DeleteUserData(ctx, userID string) error }`; `(*PostgresStore).DeleteUserData(ctx context.Context, userID string) error`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/credit/store_sweep_test.go`:

````go
package credit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPostgresStore_ExpireLotsWritesExpireEntries(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	user := newUser()
	expired := testGrant(t, store, user, PoolGift, 7, -time.Minute)
	live := testGrant(t, store, user, PoolGift, 5, time.Hour)

	n, err := store.ExpireLots(ctx)
	if err != nil || n != 1 {
		t.Fatalf("ExpireLots = %d, %v; want 1", n, err)
	}
	if lotRemaining(t, pool, expired.ID) != 0 || lotRemaining(t, pool, live.ID) != 5 {
		t.Fatal("only the expired lot must be zeroed")
	}
	if got := strings.Join(entryAmounts(t, pool, user), ","); got != "grant:7,grant:5,expire:-7" {
		t.Fatalf("entries = %s", got)
	}
	var ref string
	if err := pool.QueryRow(ctx, `SELECT ref FROM kredi.entries WHERE kind = 'expire'`).Scan(&ref); err != nil || ref != expired.ID {
		t.Fatalf("expire ref = %q, %v; want the lot id", ref, err)
	}
	if n, err := store.ExpireLots(ctx); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v; want 0", n, err)
	}
}

func TestPostgresStore_StaleReservations(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	user := newUser()
	testGrant(t, store, user, PoolGift, 30, time.Hour)
	for _, ref := range []string{"old-held", "new-held", "old-committed"} {
		if _, err := store.Reserve(ctx, reserveInput(user, ref, 3, true)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Commit(ctx, "old-committed"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kredi.reservations SET created_at = now() - interval '4 hours' WHERE ref IN ('old-held', 'old-committed')`); err != nil {
		t.Fatal(err)
	}
	refs, err := store.StaleReservations(ctx, 3*time.Hour, 100)
	if err != nil || len(refs) != 1 || refs[0] != "old-held" {
		t.Fatalf("stale = %v, %v; want [old-held]", refs, err)
	}
}

func TestPostgresStore_StopFinishedCampaigns(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	c := testCampaign(t, store, 10, 5)
	if n, err := store.StopFinishedCampaigns(ctx); err != nil || n != 0 {
		t.Fatalf("running campaign stopped: %d, %v", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE kredi.campaigns SET ends_at = now() - interval '1 minute' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := store.StopFinishedCampaigns(ctx); err != nil || n != 1 {
		t.Fatalf("ended campaign = %d, %v; want 1", n, err)
	}
	if active, _ := store.ActiveCampaign(ctx); active != nil {
		t.Fatal("the ended campaign is still active")
	}
}

func TestPostgresStore_DeleteUserData(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	c := testCampaign(t, store, 10, 5)
	gone, kept := newUser(), newUser()
	for _, u := range []string{gone, kept} {
		if ok, err := store.Claim(ctx, c.ID, u); err != nil || !ok {
			t.Fatalf("claim: %v, %v", ok, err)
		}
		if _, err := store.Reserve(ctx, reserveInput(u, "job-"+u, 2, true)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteUserData(ctx, gone); err != nil {
		t.Fatalf("DeleteUserData: %v", err)
	}
	count := func(user string) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM kredi.entries WHERE user_id = $1) +
			(SELECT count(*) FROM kredi.reservations WHERE user_id = $1) +
			(SELECT count(*) FROM kredi.campaign_claims WHERE user_id = $1) +
			(SELECT count(*) FROM kredi.lots WHERE user_id = $1)`, user).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(gone) != 0 || count(kept) != 5 {
		t.Fatalf("rows left: deleted user %d, other user %d (want 0, 5)", count(gone), count(kept))
	}
	active, _ := store.ActiveCampaign(ctx)
	if active == nil || active.Used != 2 {
		t.Fatalf("used must not be given back: %+v", active)
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/credit/`
Expected: build failure, `store.ExpireLots undefined`, `store.StaleReservations undefined`, ...

- [ ] **Step 3: Implement**

**Create** `core-service/internal/credit/store_sweep.go`:

````go
package credit

import (
	"context"
	"fmt"
	"time"
)

// SweepStore covers the periodic kredi:supur work.
type SweepStore interface {
	// ExpireLots zeroes the remaining credit of expired lots and writes one
	// expire entry (-remaining, ref = lot id) per lot. It returns the count.
	ExpireLots(ctx context.Context) (int64, error)
	// StaleReservations returns refs of reservations held for longer than
	// olderThan, oldest first.
	StaleReservations(ctx context.Context, olderThan time.Duration, limit int) ([]string, error)
	// StopFinishedCampaigns stops active campaigns that are full or past
	// ends_at, so the one-active index stays correct.
	StopFinishedCampaigns(ctx context.Context) (int64, error)
}

// UserDataCleaner deletes a user's credit rows on account deletion.
type UserDataCleaner interface {
	DeleteUserData(ctx context.Context, userID string) error
}

var (
	_ SweepStore      = (*PostgresStore)(nil)
	_ UserDataCleaner = (*PostgresStore)(nil)
)

func (s *PostgresStore) ExpireLots(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		WITH due AS (
			SELECT id, user_id, remaining FROM kredi.lots
			 WHERE expires_at <= now() AND remaining > 0
			 FOR UPDATE SKIP LOCKED
		), zeroed AS (
			UPDATE kredi.lots l SET remaining = 0
			  FROM due WHERE l.id = due.id
			RETURNING due.id, due.user_id, due.remaining
		)
		INSERT INTO kredi.entries (user_id, kind, amount, ref)
		SELECT user_id, 'expire', -remaining, id::text FROM zeroed`)
	if err != nil {
		return 0, fmt.Errorf("could not expire lots: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *PostgresStore) StaleReservations(ctx context.Context, olderThan time.Duration, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ref FROM kredi.reservations
		 WHERE status = 'held' AND created_at < now() - $1::interval
		 ORDER BY created_at
		 LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("could not list stale reservations: %w", err)
	}
	defer rows.Close()
	refs := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("could not read a stale reservation: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (s *PostgresStore) StopFinishedCampaigns(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE kredi.campaigns SET stopped_at = now()
		 WHERE stopped_at IS NULL
		   AND (used >= quota OR (ends_at IS NOT NULL AND ends_at <= now()))`)
	if err != nil {
		return 0, fmt.Errorf("could not stop finished campaigns: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteUserData deletes the user's entries, reservations, claims and lots
// in one transaction. Campaign used counters are not given back.
func (s *PostgresStore) DeleteUserData(ctx context.Context, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("could not start the credit delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, query := range []string{
		`DELETE FROM kredi.entries WHERE user_id = $1`,
		`DELETE FROM kredi.reservations WHERE user_id = $1`,
		`DELETE FROM kredi.campaign_claims WHERE user_id = $1`,
		`DELETE FROM kredi.lots WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(ctx, query, userID); err != nil {
			return fmt.Errorf("could not delete credit data: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("could not commit the credit delete: %w", err)
	}
	return nil
}
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/credit/`
Expected: `ok`; `TestPostgresStore_ExpireLotsWritesExpireEntries`, `..._StaleReservations`, `..._StopFinishedCampaigns`, `..._DeleteUserData` PASS.

- [ ] **Step 5: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 7 added lines: the four `DELETE FROM kredi.* WHERE user_id = $1` strings and the three sweep SQL strings.

- [ ] **Step 6: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/credit core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): supurme ve kullanici verisi silme sorgulari

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Credit service, validation and the shared fake spender

The rules: priced, idempotent `Spender` with shadow mode (R1–R3), lazy campaign claim (R9), summary (R8), history paging (R7), admin create/stop/list (R10), sweep (R14), `FakeSpender` (R17). Adds the campaign validation messages to i18n.

**Files:**
- Create: `core-service/internal/credit/fake_store_test.go`
- Create: `core-service/internal/credit/service_test.go`
- Modify: `core-service/internal/i18n/catalog.go`
- Create: `core-service/internal/credit/validation.go`
- Create: `core-service/internal/credit/service.go`
- Create: `core-service/internal/credit/spender_fake.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: everything from Tasks 3–5; `config.CreditConfig` (Task 2); `i18n.KeyCursorInvalid` (existing).
- Produces:
  - `type Spender interface{ Reserve(ctx, userID string, op Operation, ref string) (Reservation, error); Commit(ctx, ref string) error; Release(ctx, ref, reason string) error }`.
  - `type UserCreatedAt interface{ UserCreatedAt(ctx context.Context, userID string) (time.Time, error) }`.
  - `type Store interface{ LedgerStore; CampaignStore; SweepStore }`.
  - `NewService(store Store, users UserCreatedAt, costs Costs, enforce bool) *Service`; `CostsFromConfig(c *config.CreditConfig) Costs`.
  - `(*Service)`: `Reserve`, `Commit`, `Release` (Spender), `Summary(ctx, userID string) (*Summary, error)`, `History(ctx, userID, cursor string, limit int) (*HistoryPage, error)`, `Campaigns(ctx) (*CampaignList, error)`, `CreateCampaign(ctx, adminID string, in CampaignInput) (*Campaign, error)`, `StopCampaign(ctx, campaignID string) (*Campaign, error)`, `Sweep(ctx) (SweepResult, error)`.
  - `type CampaignInput struct{Name string; Quota, PerUser, ValidityDays int; EndsAt *time.Time}` (JSON `name, quota, perUser, validityDays, endsAt`); `type CampaignList struct{Items []Campaign; Summary CampaignSummary}`; `type CampaignSummary struct{ActiveID *string; TotalClaims, TotalCredits int}`; `type SweepResult struct{ReleasedReservations int; ExpiredLots, StoppedCampaigns int64}`.
  - `*ValidationError{Key i18n.Key; Args []any}` (unwraps to `ErrValidation`); `PageSize(raw string) int`.
  - `credit.FakeSpender{ReserveErr error; Reserved []ReserveCall; Committed []string; Released []ReleaseCall}`, `ReserveCall{UserID string; Operation Operation; Ref string}`, `ReleaseCall{Ref, Reason string}`.
  - i18n keys `KeyCampaignNameLength`, `KeyCampaignQuotaRange`, `KeyCampaignPerUserRange`, `KeyCampaignValidityRange`, `KeyCampaignEndsAtPast`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/credit/fake_store_test.go`:

````go
package credit

import (
	"context"
	"time"
)

// fakeStore is an in-memory Store for the service tests. It only behaves
// enough to exercise the service's decisions; SQL behavior is covered by
// the live Postgres tests.
type fakeStore struct {
	err error // returned by every method when set

	reserveIn  []ReserveInput
	reserveErr error
	commits    []string
	releases   []ReleaseCall
	releaseErr error

	balances    Balances
	recentClaim *WelcomeGift

	active      *Campaign
	hasClaim    bool
	claims      []string // user ids passed to Claim
	created     []NewCampaign
	stopped     []string
	campaigns   []Campaign
	historyIn   []*HistoryCursor
	history     []HistoryItem
	staleRefs   []string
	expired     int64
	stoppedDone int64
	calls       []string // sweep call order
}

func (f *fakeStore) Grant(context.Context, GrantInput) (*Lot, error) { return &Lot{}, f.err }

func (f *fakeStore) Reserve(_ context.Context, in ReserveInput) (*Reservation, error) {
	f.reserveIn = append(f.reserveIn, in)
	if f.reserveErr != nil {
		return nil, f.reserveErr
	}
	return &Reservation{Ref: in.Ref, UserID: in.UserID, Operation: in.Operation, Amount: in.Amount, Status: StatusHeld}, nil
}

func (f *fakeStore) Commit(_ context.Context, ref string) error {
	f.commits = append(f.commits, ref)
	return f.err
}

func (f *fakeStore) Release(_ context.Context, ref, reason string) error {
	f.calls = append(f.calls, "release")
	f.releases = append(f.releases, ReleaseCall{Ref: ref, Reason: reason})
	return f.releaseErr
}

func (f *fakeStore) Balances(context.Context, string) (*Balances, error) {
	b := f.balances
	return &b, f.err
}

func (f *fakeStore) History(_ context.Context, _ string, after *HistoryCursor, limit int) ([]HistoryItem, error) {
	f.historyIn = append(f.historyIn, after)
	items := f.history
	if len(items) > limit {
		items = items[:limit]
	}
	return items, f.err
}

func (f *fakeStore) ActiveCampaign(context.Context) (*Campaign, error) { return f.active, f.err }

func (f *fakeStore) HasClaim(context.Context, string, string) (bool, error) { return f.hasClaim, f.err }

func (f *fakeStore) Claim(_ context.Context, _ string, userID string) (bool, error) {
	f.claims = append(f.claims, userID)
	return true, f.err
}

func (f *fakeStore) RecentClaim(context.Context, string, time.Duration) (*WelcomeGift, error) {
	return f.recentClaim, f.err
}

func (f *fakeStore) CreateCampaign(_ context.Context, in NewCampaign) (*Campaign, error) {
	f.created = append(f.created, in)
	return &Campaign{ID: "c1", Name: in.Name, PerUser: in.PerUser, Quota: in.Quota, ValidityDays: in.ValidityDays, EndsAt: in.EndsAt}, f.err
}

func (f *fakeStore) StopCampaign(_ context.Context, id string) (*Campaign, error) {
	f.stopped = append(f.stopped, id)
	now := time.Now()
	return &Campaign{ID: id, Quota: 10, StoppedAt: &now}, f.err
}

func (f *fakeStore) ListCampaigns(context.Context, int) ([]Campaign, error) {
	return append([]Campaign(nil), f.campaigns...), f.err
}

func (f *fakeStore) ExpireLots(context.Context) (int64, error) {
	f.calls = append(f.calls, "expire")
	return f.expired, f.err
}

func (f *fakeStore) StaleReservations(context.Context, time.Duration, int) ([]string, error) {
	f.calls = append(f.calls, "stale")
	return f.staleRefs, f.err
}

func (f *fakeStore) StopFinishedCampaigns(context.Context) (int64, error) {
	f.calls = append(f.calls, "stop")
	return f.stoppedDone, f.err
}

// fakeUsers answers UserCreatedAt.
type fakeUsers struct {
	createdAt time.Time
	err       error
}

func (u fakeUsers) UserCreatedAt(context.Context, string) (time.Time, error) {
	return u.createdAt, u.err
}

var testCosts = Costs{
	OpRenderMix: 12, OpRenderFocus: 32, OpRenderStems: 62,
	OpLyricsGenerate: 2, OpLyricsRewrite: 1, OpMasterPreview: 0, OpMasterFinal: 3,
}
````

**Create** `core-service/internal/credit/service_test.go`:

````go
package credit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/i18n"
)

const testUser = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

func TestService_ReservePricesTheOperation(t *testing.T) {
	store := &fakeStore{}
	s := NewService(store, nil, testCosts, true)
	r, err := s.Reserve(context.Background(), strings.ToUpper(testUser), OpRenderFocus, "job-1")
	if err != nil || r.Amount != 32 {
		t.Fatalf("Reserve = %+v, %v", r, err)
	}
	if len(store.reserveIn) != 1 {
		t.Fatalf("store calls = %d", len(store.reserveIn))
	}
	in := store.reserveIn[0]
	if in.UserID != testUser || in.Amount != 32 || !in.Enforce || in.Ref != "job-1" || in.Operation != OpRenderFocus {
		t.Fatalf("store input = %+v", in)
	}
}

// Spec: zero-cost operations (master preview) write nothing.
func TestService_ReserveFreeOperationWritesNothing(t *testing.T) {
	store := &fakeStore{}
	s := NewService(store, nil, testCosts, true)
	if _, err := s.Reserve(context.Background(), testUser, OpMasterPreview, "x"); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(store.reserveIn) != 0 {
		t.Fatal("a free operation must not touch the store")
	}
}

func TestService_ReserveEnforcedReturnsErrors(t *testing.T) {
	short := &InsufficientError{Required: 32, Balance: 20}
	s := NewService(&fakeStore{reserveErr: short}, nil, testCosts, true)
	if _, err := s.Reserve(context.Background(), testUser, OpRenderFocus, "job-1"); !errors.Is(err, short) {
		t.Fatalf("want the insufficient error, got %v", err)
	}
	if _, err := s.Reserve(context.Background(), "not-a-uuid", OpRenderFocus, "job-1"); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("want ErrInvalidIdentity, got %v", err)
	}
}

// Shadow mode never blocks: store errors and bad ids are swallowed.
func TestService_ReserveShadowNeverBlocks(t *testing.T) {
	store := &fakeStore{reserveErr: errors.New("pg is down")}
	s := NewService(store, nil, testCosts, false)
	r, err := s.Reserve(context.Background(), testUser, OpRenderStems, "job-1")
	if err != nil || r.Ref != "job-1" || r.Amount != 0 {
		t.Fatalf("shadow Reserve = %+v, %v", r, err)
	}
	if store.reserveIn[0].Enforce {
		t.Fatal("the store must be told enforcement is off")
	}
	if _, err := s.Reserve(context.Background(), "not-a-uuid", OpRenderStems, "job-2"); err != nil {
		t.Fatalf("shadow Reserve with a bad id: %v", err)
	}
}

func TestService_CommitReleaseSkipEmptyRef(t *testing.T) {
	store := &fakeStore{}
	s := NewService(store, nil, testCosts, true)
	ctx := context.Background()
	if err := s.Commit(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "", ReasonJobFailed); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "b", ReasonCanceled); err != nil {
		t.Fatal(err)
	}
	if len(store.commits) != 1 || store.commits[0] != "a" || len(store.releases) != 1 || store.releases[0] != (ReleaseCall{"b", ReasonCanceled}) {
		t.Fatalf("commits=%v releases=%v", store.commits, store.releases)
	}
}

func activeCampaign(startsAt time.Time) *Campaign {
	return &Campaign{ID: "c1", PerUser: 50, Quota: 100, ValidityDays: 30, StartsAt: startsAt}
}

func TestService_SummaryClaimsForNewUser(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	store := &fakeStore{active: activeCampaign(start)}
	s := NewService(store, fakeUsers{createdAt: start.Add(time.Minute)}, testCosts, false)
	if _, err := s.Summary(context.Background(), testUser); err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(store.claims) != 1 || store.claims[0] != testUser {
		t.Fatalf("claims = %v", store.claims)
	}
}

// K5: users who signed up before the campaign started get nothing.
func TestService_SummarySkipsClaimForOlderUser(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	cases := map[string]struct {
		store *fakeStore
		users UserCreatedAt
	}{
		"signed up before":   {&fakeStore{active: activeCampaign(start)}, fakeUsers{createdAt: start.Add(-time.Second)}},
		"already claimed":    {&fakeStore{active: activeCampaign(start), hasClaim: true}, fakeUsers{createdAt: start.Add(time.Minute)}},
		"no campaign":        {&fakeStore{}, fakeUsers{createdAt: start.Add(time.Minute)}},
		"sign-up time error": {&fakeStore{active: activeCampaign(start)}, fakeUsers{err: errors.New("no user")}},
		"no user directory":  {&fakeStore{active: activeCampaign(start)}, nil},
	}
	for name, tc := range cases {
		s := NewService(tc.store, tc.users, testCosts, false)
		if _, err := s.Summary(context.Background(), testUser); err != nil {
			t.Fatalf("%s: Summary: %v", name, err)
		}
		if len(tc.store.claims) != 0 {
			t.Errorf("%s: claimed %v", name, tc.store.claims)
		}
	}
}

func TestService_SummaryBody(t *testing.T) {
	expiry := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	gift := &WelcomeGift{CampaignID: "c1", Amount: 50, ExpiresAt: expiry}
	store := &fakeStore{balances: Balances{Gift: 24, GiftNextExpiry: &expiry}, recentClaim: gift}
	s := NewService(store, nil, testCosts, false)
	sum, err := s.Summary(context.Background(), testUser)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	raw, _ := json.Marshal(sum)
	want := `{"balance":24,"pools":{"subscription":null,"gift":{"amount":24,"nextExpiry":"2026-12-31T23:59:59Z"}},` +
		`"plan":"free","costs":{"lyrics_generate":2,"lyrics_rewrite":1,"master_final":3,"master_preview":0,` +
		`"render_focus":32,"render_mix":12,"render_stems":62},"enforced":false,` +
		`"welcomeGift":{"campaignId":"c1","amount":50,"expiresAt":"2026-12-31T23:59:59Z"}}`
	if string(raw) != want {
		t.Fatalf("body =\n%s\nwant\n%s", raw, want)
	}
	empty, _ := json.Marshal(mustSummary(t, NewService(&fakeStore{}, nil, testCosts, true)))
	if !strings.Contains(string(empty), `"pools":{"subscription":null,"gift":null}`) ||
		!strings.Contains(string(empty), `"welcomeGift":null`) || !strings.Contains(string(empty), `"enforced":true`) {
		t.Fatalf("empty body = %s", empty)
	}
}

func mustSummary(t *testing.T, s *Service) *Summary {
	t.Helper()
	sum, err := s.Summary(context.Background(), testUser)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestService_HistoryPaging(t *testing.T) {
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	items := []HistoryItem{{ID: 3, CreatedAt: at}, {ID: 2, CreatedAt: at}, {ID: 1, CreatedAt: at}}
	store := &fakeStore{history: items}
	s := NewService(store, nil, testCosts, false)
	page, err := s.History(context.Background(), testUser, "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if _, err := s.History(context.Background(), testUser, *page.NextCursor, 2); err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got := store.historyIn[1]; got == nil || got.ID != 2 || !got.CreatedAt.Equal(at) {
		t.Fatalf("cursor passed to the store = %+v", got)
	}
	store.history = items[:1]
	last, err := s.History(context.Background(), testUser, "", 2)
	if err != nil || last.NextCursor != nil {
		t.Fatalf("last page = %+v, %v", last, err)
	}
	var v *ValidationError
	if _, err := s.History(context.Background(), testUser, "%%%", 2); !errors.As(err, &v) || v.Key != i18n.KeyCursorInvalid {
		t.Fatalf("bad cursor: %v", err)
	}
	raw, _ := json.Marshal(&HistoryPage{Items: []HistoryItem{}})
	if string(raw) != `{"items":[],"nextCursor":null}` {
		t.Fatalf("empty page = %s", raw)
	}
}

func TestPageSize(t *testing.T) {
	for raw, want := range map[string]int{"": 20, "x": 20, "0": 20, "5": 5, "50": 50, "51": 50} {
		if got := PageSize(raw); got != want {
			t.Errorf("PageSize(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestService_CreateCampaignValidation(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	valid := CampaignInput{Name: " Lansman ", Quota: 500, PerUser: 50, ValidityDays: 30}
	cases := []struct {
		mutate func(*CampaignInput)
		key    i18n.Key
	}{
		{func(in *CampaignInput) { in.Name = "  " }, i18n.KeyCampaignNameLength},
		{func(in *CampaignInput) { in.Name = strings.Repeat("a", 81) }, i18n.KeyCampaignNameLength},
		{func(in *CampaignInput) { in.Quota = 0 }, i18n.KeyCampaignQuotaRange},
		{func(in *CampaignInput) { in.PerUser = 10001 }, i18n.KeyCampaignPerUserRange},
		{func(in *CampaignInput) { in.ValidityDays = 0 }, i18n.KeyCampaignValidityRange},
		{func(in *CampaignInput) { in.EndsAt = &past }, i18n.KeyCampaignEndsAtPast},
	}
	for _, tc := range cases {
		in := valid
		tc.mutate(&in)
		store := &fakeStore{}
		_, err := NewService(store, nil, testCosts, false).CreateCampaign(context.Background(), testUser, in)
		var v *ValidationError
		if !errors.As(err, &v) || v.Key != tc.key || !errors.Is(err, ErrValidation) {
			t.Errorf("%+v: got %v, want %v", in, err, tc.key)
		}
		if len(store.created) != 0 {
			t.Errorf("%+v: invalid input reached the store", in)
		}
	}
	store := &fakeStore{}
	c, err := NewService(store, nil, testCosts, false).CreateCampaign(context.Background(), testUser, valid)
	if err != nil || c.Status != CampaignActive || store.created[0].Name != "Lansman" || store.created[0].CreatedBy != testUser {
		t.Fatalf("create = %+v, %v, %+v", c, err, store.created)
	}
}

func TestService_StopCampaign(t *testing.T) {
	store := &fakeStore{}
	s := NewService(store, nil, testCosts, false)
	if _, err := s.StopCampaign(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}
	c, err := s.StopCampaign(context.Background(), strings.ToUpper(testUser))
	if err != nil || c.Status != CampaignStopped || store.stopped[0] != testUser {
		t.Fatalf("stop = %+v, %v, %v", c, err, store.stopped)
	}
}

func TestCampaignStatus(t *testing.T) {
	now := time.Now()
	before, after := now.Add(-time.Hour), now.Add(time.Hour)
	earlier := now.Add(-2 * time.Hour)
	cases := []struct {
		c    Campaign
		want string
	}{
		{Campaign{Quota: 10}, CampaignActive},
		{Campaign{Quota: 10, EndsAt: &after}, CampaignActive},
		{Campaign{Quota: 10, Used: 10, StoppedAt: &before}, CampaignExhausted},
		{Campaign{Quota: 10, EndsAt: &before}, CampaignExpired},
		{Campaign{Quota: 10, EndsAt: &before, StoppedAt: &now}, CampaignExpired},
		{Campaign{Quota: 10, EndsAt: &before, StoppedAt: &earlier}, CampaignStopped},
		{Campaign{Quota: 10, StoppedAt: &before}, CampaignStopped},
	}
	for i, tc := range cases {
		if got := campaignStatus(&tc.c, now); got != tc.want {
			t.Errorf("case %d: %s, want %s", i, got, tc.want)
		}
	}
}

func TestService_CampaignsSummary(t *testing.T) {
	now := time.Now()
	store := &fakeStore{campaigns: []Campaign{
		{ID: "new", Quota: 10, Used: 2, PerUser: 50},
		{ID: "old", Quota: 5, Used: 5, PerUser: 20, StoppedAt: &now},
	}}
	list, err := NewService(store, nil, testCosts, false).Campaigns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list.Summary.ActiveID == nil || *list.Summary.ActiveID != "new" || list.Summary.TotalClaims != 7 || list.Summary.TotalCredits != 200 {
		t.Fatalf("summary = %+v", list.Summary)
	}
	if list.Items[0].Status != CampaignActive || list.Items[1].Status != CampaignExhausted {
		t.Fatalf("statuses = %s, %s", list.Items[0].Status, list.Items[1].Status)
	}
}

func TestService_SweepOrderAndErrors(t *testing.T) {
	store := &fakeStore{staleRefs: []string{"a", "b"}, expired: 4, stoppedDone: 1}
	res, err := NewService(store, nil, testCosts, false).Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if strings.Join(store.calls, ",") != "stale,release,release,expire,stop" {
		t.Fatalf("order = %v", store.calls)
	}
	if res != (SweepResult{ReleasedReservations: 2, ExpiredLots: 4, StoppedCampaigns: 1}) {
		t.Fatalf("result = %+v", res)
	}
	if store.releases[0].Reason != ReasonStale {
		t.Fatalf("reason = %s", store.releases[0].Reason)
	}
	failing := &fakeStore{staleRefs: []string{"a"}, releaseErr: errors.New("boom")}
	res, err = NewService(failing, nil, testCosts, false).Sweep(context.Background())
	if err == nil || res.ReleasedReservations != 0 || strings.Join(failing.calls, ",") != "stale,release,expire,stop" {
		t.Fatalf("failing sweep = %+v, %v, %v", res, err, failing.calls)
	}
}

func TestCostsFromConfig(t *testing.T) {
	c := CostsFromConfig(&config.CreditConfig{CostRenderMix: 1, CostRenderFocus: 2, CostRenderStems: 3,
		CostLyricsGenerate: 4, CostLyricsRewrite: 5, CostMasterPreview: 6, CostMasterFinal: 7})
	want := Costs{OpRenderMix: 1, OpRenderFocus: 2, OpRenderStems: 3, OpLyricsGenerate: 4, OpLyricsRewrite: 5, OpMasterPreview: 6, OpMasterFinal: 7}
	if len(c) != len(want) {
		t.Fatalf("costs = %v", c)
	}
	for op, v := range want {
		if c[op] != v {
			t.Errorf("%s = %d, want %d", op, c[op], v)
		}
	}
}

func TestFakeSpender_Records(t *testing.T) {
	f := &FakeSpender{}
	ctx := context.Background()
	if _, err := f.Reserve(ctx, "u", OpLyricsRewrite, "r"); err != nil {
		t.Fatal(err)
	}
	_ = f.Commit(ctx, "r")
	_ = f.Release(ctx, "r", ReasonRequestFailed)
	if len(f.Reserved) != 1 || f.Committed[0] != "r" || f.Released[0].Reason != ReasonRequestFailed {
		t.Fatalf("fake = %+v", f)
	}
	f.ReserveErr = &InsufficientError{Required: 1}
	if _, err := f.Reserve(ctx, "u", OpLyricsRewrite, "r2"); err == nil {
		t.Fatal("ReserveErr was not returned")
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/credit/`
Expected: build failure, `undefined: NewService`, `undefined: ReleaseCall`, `undefined: i18n.KeyCampaignNameLength`, ...

- [ ] **Step 3: Add the i18n messages**

**Modify** `core-service/internal/i18n/catalog.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/i18n/catalog.go b/core-service/internal/i18n/catalog.go
index 4cfe7de..3441c89 100644
--- a/core-service/internal/i18n/catalog.go
+++ b/core-service/internal/i18n/catalog.go
@@ -104,6 +104,13 @@ var (
 	KeyCommentTimeBeyondDuration = Key{"comment_time_beyond_duration"}
 	KeyReportTargetInvalid       = Key{"report_target_invalid"}
 	KeyReportReasonInvalid       = Key{"report_reason_invalid"}
+
+	// Credit campaign validation (422 dogrulama).
+	KeyCampaignNameLength    = Key{"campaign_name_length"}
+	KeyCampaignQuotaRange    = Key{"campaign_quota_range"}
+	KeyCampaignPerUserRange  = Key{"campaign_per_user_range"}
+	KeyCampaignValidityRange = Key{"campaign_validity_range"}
+	KeyCampaignEndsAtPast    = Key{"campaign_ends_at_past"}
 )
 
 // texts holds one message in both languages. Verbs (%s) must match.
@@ -205,4 +212,10 @@ var catalog = map[Key]texts{
 	KeyCommentTimeBeyondDuration: {"zamanMs şarkının süresini aşıyor.", "zamanMs exceeds the song's duration."},
 	KeyReportTargetInvalid:       {"hedefTuru yayin ya da yorum olmalı.", "hedefTuru must be yayin or yorum."},
 	KeyReportReasonInvalid:       {"neden spam, taciz, telif, uygunsuz ya da diger olmalı.", "neden must be spam, taciz, telif, uygunsuz or diger."},
+
+	KeyCampaignNameLength:    {"Kampanya adı 1-80 karakter olmalı.", "Campaign name must be 1-80 characters."},
+	KeyCampaignQuotaRange:    {"quota 1-1000000 aralığında olmalı.", "quota must be between 1 and 1000000."},
+	KeyCampaignPerUserRange:  {"perUser 1-10000 aralığında olmalı.", "perUser must be between 1 and 10000."},
+	KeyCampaignValidityRange: {"validityDays 1-3650 aralığında olmalı.", "validityDays must be between 1 and 3650."},
+	KeyCampaignEndsAtPast:    {"endsAt gelecekte bir zaman olmalı.", "endsAt must be in the future."},
 }
````

- [ ] **Step 4: Implement validation, service and the fake spender**

**Create** `core-service/internal/credit/validation.go`:

````go
package credit

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/i18n"
)

// ValidationError is a field-level validation failure (422 dogrulama); the
// HTTP layer renders Key with Args in the request's language.
type ValidationError struct {
	Key  i18n.Key
	Args []any
}

func (e *ValidationError) Error() string {
	return ErrValidation.Error() + ": " + i18n.Message(i18n.EN, e.Key, e.Args...)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

func invalid(key i18n.Key, args ...any) error {
	return &ValidationError{Key: key, Args: args}
}

// parseID returns the canonical form of a UUID.
func parseID(raw string) (string, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", false
	}
	return id.String(), true
}

// parseUserID validates the caller's id; the auth middleware always writes
// a UUID, anything else is a configuration error (401).
func parseUserID(raw string) (string, error) {
	id, ok := parseID(raw)
	if !ok {
		return "", ErrInvalidIdentity
	}
	return id, nil
}

// CampaignInput is the body of POST /api/admin/campaigns.
type CampaignInput struct {
	Name         string     `json:"name"`
	Quota        int        `json:"quota"`
	PerUser      int        `json:"perUser"`
	ValidityDays int        `json:"validityDays"`
	EndsAt       *time.Time `json:"endsAt"`
}

// Campaign input bounds.
const (
	maxCampaignName     = 80
	maxCampaignQuota    = 1_000_000
	maxCampaignPerUser  = 10_000
	maxCampaignValidity = 3650
)

// validateCampaign checks the admin input and returns the row to insert.
func validateCampaign(in CampaignInput, createdBy string, now time.Time) (NewCampaign, error) {
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxCampaignName || strings.ContainsRune(name, 0) {
		return NewCampaign{}, invalid(i18n.KeyCampaignNameLength)
	}
	if in.Quota < 1 || in.Quota > maxCampaignQuota {
		return NewCampaign{}, invalid(i18n.KeyCampaignQuotaRange)
	}
	if in.PerUser < 1 || in.PerUser > maxCampaignPerUser {
		return NewCampaign{}, invalid(i18n.KeyCampaignPerUserRange)
	}
	if in.ValidityDays < 1 || in.ValidityDays > maxCampaignValidity {
		return NewCampaign{}, invalid(i18n.KeyCampaignValidityRange)
	}
	if in.EndsAt != nil && !in.EndsAt.After(now) {
		return NewCampaign{}, invalid(i18n.KeyCampaignEndsAtPast)
	}
	return NewCampaign{
		Name: name, PerUser: in.PerUser, Quota: in.Quota, ValidityDays: in.ValidityDays,
		EndsAt: in.EndsAt, CreatedBy: createdBy,
	}, nil
}

// campaignStatus derives the display status. Full wins over ended; a
// campaign stopped by hand before its end date stays "stopped".
func campaignStatus(c *Campaign, now time.Time) string {
	switch {
	case c.Used >= c.Quota:
		return CampaignExhausted
	case c.EndsAt != nil && !c.EndsAt.After(now) && (c.StoppedAt == nil || !c.StoppedAt.Before(*c.EndsAt)):
		return CampaignExpired
	case c.StoppedAt != nil:
		return CampaignStopped
	default:
		return CampaignActive
	}
}

// History page size bounds (?limit=).
const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 50
)

// PageSize interprets ?limit=: 20 when empty or invalid, at most 50.
func PageSize(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return defaultHistoryLimit
	}
	return min(n, maxHistoryLimit)
}

func encodeHistoryCursor(c HistoryCursor) *string {
	b, _ := json.Marshal(c)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

// decodeHistoryCursor returns nil for an empty string.
func decodeHistoryCursor(raw string) (*HistoryCursor, error) {
	if raw == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c HistoryCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.CreatedAt.IsZero() || c.ID <= 0 {
		return nil, invalid(i18n.KeyCursorInvalid)
	}
	return &c, nil
}
````

**Create** `core-service/internal/credit/service.go`:

````go
package credit

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// Spender is the part of the credit system that job-starting code sees:
// hold credit before the work (ref = job id or a request uuid), commit it
// on success, release it on failure. Implemented by *Service.
type Spender interface {
	Reserve(ctx context.Context, userID string, op Operation, ref string) (Reservation, error)
	Commit(ctx context.Context, ref string) error
	Release(ctx context.Context, ref, reason string) error
}

// UserCreatedAt tells when a user signed up (implemented by
// *identity.Server). Campaigns only go to users who signed up after the
// campaign started (K5).
type UserCreatedAt interface {
	UserCreatedAt(ctx context.Context, userID string) (time.Time, error)
}

// Store is every persistent operation of the credit system.
type Store interface {
	LedgerStore
	CampaignStore
	SweepStore
}

var (
	_ Store   = (*PostgresStore)(nil)
	_ Spender = (*Service)(nil)
)

const (
	// welcomeGiftWindow is how long GET /api/credits keeps returning a claim.
	welcomeGiftWindow = 7 * 24 * time.Hour
	// staleReservationAfter: a reservation held this long lost its job.
	staleReservationAfter = 3 * time.Hour
	// staleReleaseBatch caps the releases of one sweep run.
	staleReleaseBatch = 500
	// campaignListLimit caps GET /api/admin/campaigns.
	campaignListLimit = 200
)

// Service applies the credit rules.
type Service struct {
	store   Store
	users   UserCreatedAt // nil: campaigns are never claimed
	costs   Costs
	enforce bool
	now     func() time.Time
}

// NewService builds the service. With enforce false (shadow mode) Reserve
// never blocks: a short balance reserves 0 and a store error is logged.
func NewService(store Store, users UserCreatedAt, costs Costs, enforce bool) *Service {
	return &Service{store: store, users: users, costs: costs, enforce: enforce, now: time.Now}
}

// CostsFromConfig maps the configured prices to operations.
func CostsFromConfig(c *config.CreditConfig) Costs {
	return Costs{
		OpRenderMix:      c.CostRenderMix,
		OpRenderFocus:    c.CostRenderFocus,
		OpRenderStems:    c.CostRenderStems,
		OpLyricsGenerate: c.CostLyricsGenerate,
		OpLyricsRewrite:  c.CostLyricsRewrite,
		OpMasterPreview:  c.CostMasterPreview,
		OpMasterFinal:    c.CostMasterFinal,
	}
}

// Reserve holds the operation's price for ref. A free operation writes
// nothing. When enforcement is on, a short balance is an *InsufficientError;
// when it is off, nothing blocks: a short balance reserves 0 and any error
// is logged and swallowed.
func (s *Service) Reserve(ctx context.Context, userID string, op Operation, ref string) (Reservation, error) {
	r, err := s.reserve(ctx, userID, op, ref)
	if err != nil && !s.enforce {
		log.Printf("credit: shadow reserve of %s for %s failed, continuing: %v", op, ref, err)
		return Reservation{Ref: ref, UserID: userID, Operation: op, Status: StatusHeld}, nil
	}
	return r, err
}

func (s *Service) reserve(ctx context.Context, userID string, op Operation, ref string) (Reservation, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return Reservation{}, err
	}
	cost := s.costs[op]
	if cost <= 0 {
		return Reservation{Ref: ref, UserID: id, Operation: op, Status: StatusHeld}, nil
	}
	if ref == "" {
		return Reservation{}, errors.New("credit: reservation ref is empty")
	}
	r, err := s.store.Reserve(ctx, ReserveInput{Ref: ref, UserID: id, Operation: op, Amount: cost, Enforce: s.enforce})
	if err != nil {
		return Reservation{}, err
	}
	return *r, nil
}

// Commit settles a held reservation; unknown or settled refs are a no-op.
func (s *Service) Commit(ctx context.Context, ref string) error {
	if ref == "" {
		return nil
	}
	return s.store.Commit(ctx, ref)
}

// Release gives a held reservation back; unknown or settled refs are a no-op.
func (s *Service) Release(ctx context.Context, ref, reason string) error {
	if ref == "" {
		return nil
	}
	return s.store.Release(ctx, ref, reason)
}

// Summary returns GET /api/credits. It first claims the active campaign's
// welcome gift for an eligible user (lazy claim: there is no sign-up event).
func (s *Service) Summary(ctx context.Context, userID string) (*Summary, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	if err := s.claimWelcomeGift(ctx, id); err != nil {
		return nil, err
	}
	b, err := s.store.Balances(ctx, id)
	if err != nil {
		return nil, err
	}
	gift, err := s.store.RecentClaim(ctx, id, welcomeGiftWindow)
	if err != nil {
		return nil, err
	}
	summary := &Summary{
		Balance:     b.Subscription + b.Gift,
		Plan:        PlanFree,
		Costs:       s.costs,
		Enforced:    s.enforce,
		WelcomeGift: gift,
	}
	if b.Gift > 0 && b.GiftNextExpiry != nil {
		summary.Pools.Gift = &GiftPool{Amount: b.Gift, NextExpiry: *b.GiftNextExpiry}
	}
	return summary, nil
}

// claimWelcomeGift gives the active campaign's gift to a user who signed up
// after it started (K5) and has not claimed it yet. A sign-up time that
// cannot be read only skips the claim; the next call tries again.
func (s *Service) claimWelcomeGift(ctx context.Context, userID string) error {
	if s.users == nil {
		return nil
	}
	campaign, err := s.store.ActiveCampaign(ctx)
	if err != nil || campaign == nil {
		return err
	}
	claimed, err := s.store.HasClaim(ctx, campaign.ID, userID)
	if err != nil || claimed {
		return err
	}
	createdAt, err := s.users.UserCreatedAt(ctx, userID)
	if err != nil {
		log.Printf("credit: could not read the sign-up time, campaign claim skipped: %v", err)
		return nil
	}
	if createdAt.Before(campaign.StartsAt) {
		return nil
	}
	_, err = s.store.Claim(ctx, campaign.ID, userID)
	return err
}

// History returns one page of the user's visible ledger rows.
func (s *Service) History(ctx context.Context, userID, cursor string, limit int) (*HistoryPage, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	after, err := decodeHistoryCursor(cursor)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > maxHistoryLimit {
		limit = defaultHistoryLimit
	}
	items, err := s.store.History(ctx, id, after, limit+1)
	if err != nil {
		return nil, err
	}
	page := &HistoryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeHistoryCursor(HistoryCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

// CampaignList is the body of GET /api/admin/campaigns.
type CampaignList struct {
	Items   []Campaign      `json:"items"`
	Summary CampaignSummary `json:"summary"`
}

// CampaignSummary totals the listed campaigns.
type CampaignSummary struct {
	ActiveID     *string `json:"activeId"`
	TotalClaims  int     `json:"totalClaims"`
	TotalCredits int     `json:"totalCredits"`
}

// Campaigns lists the newest campaigns with their derived status.
func (s *Service) Campaigns(ctx context.Context) (*CampaignList, error) {
	campaigns, err := s.store.ListCampaigns(ctx, campaignListLimit)
	if err != nil {
		return nil, err
	}
	now := s.now()
	list := &CampaignList{Items: campaigns}
	for i := range list.Items {
		c := &list.Items[i]
		c.Status = campaignStatus(c, now)
		if c.Status == CampaignActive {
			list.Summary.ActiveID = &c.ID
		}
		list.Summary.TotalClaims += c.Used
		list.Summary.TotalCredits += c.Used * c.PerUser
	}
	return list, nil
}

// CreateCampaign validates the input, stops the active campaign and starts
// the new one (K6).
func (s *Service) CreateCampaign(ctx context.Context, adminID string, in CampaignInput) (*Campaign, error) {
	id, err := parseUserID(adminID)
	if err != nil {
		return nil, err
	}
	row, err := validateCampaign(in, id, s.now())
	if err != nil {
		return nil, err
	}
	c, err := s.store.CreateCampaign(ctx, row)
	if err != nil {
		return nil, err
	}
	c.Status = campaignStatus(c, s.now())
	return c, nil
}

// StopCampaign stops a campaign; a malformed or unknown id is ErrNotFound.
func (s *Service) StopCampaign(ctx context.Context, campaignID string) (*Campaign, error) {
	id, ok := parseID(campaignID)
	if !ok {
		return nil, ErrNotFound
	}
	c, err := s.store.StopCampaign(ctx, id)
	if err != nil {
		return nil, err
	}
	c.Status = campaignStatus(c, s.now())
	return c, nil
}

// SweepResult counts what one sweep changed.
type SweepResult struct {
	ReleasedReservations int
	ExpiredLots          int64
	StoppedCampaigns     int64
}

// Sweep releases reservations held longer than 3 hours (their job was
// lost; the user gets the credit back), expires lots and stops finished
// campaigns. Releases run first so credit returned to an expired lot is
// expired again in the same run. Every step runs even if an earlier one
// failed; the errors are joined.
func (s *Service) Sweep(ctx context.Context) (SweepResult, error) {
	var result SweepResult
	var errs []error
	refs, err := s.store.StaleReservations(ctx, staleReservationAfter, staleReleaseBatch)
	if err != nil {
		errs = append(errs, err)
	}
	for _, ref := range refs {
		if err := s.store.Release(ctx, ref, ReasonStale); err != nil {
			errs = append(errs, fmt.Errorf("release %s: %w", ref, err))
			continue
		}
		result.ReleasedReservations++
	}
	if result.ExpiredLots, err = s.store.ExpireLots(ctx); err != nil {
		errs = append(errs, err)
	}
	if result.StoppedCampaigns, err = s.store.StopFinishedCampaigns(ctx); err != nil {
		errs = append(errs, err)
	}
	return result, errors.Join(errs...)
}
````

**Create** `core-service/internal/credit/spender_fake.go`:

````go
package credit

import (
	"context"
	"sync"
)

// FakeSpender is an in-memory Spender for the tests of the packages that
// spend credits (handler, service, worker). It records every call;
// ReserveErr, when set, is returned by Reserve.
type FakeSpender struct {
	mu         sync.Mutex
	ReserveErr error
	Reserved   []ReserveCall
	Committed  []string
	Released   []ReleaseCall
}

// ReserveCall is one recorded Reserve.
type ReserveCall struct {
	UserID    string
	Operation Operation
	Ref       string
}

// ReleaseCall is one recorded Release.
type ReleaseCall struct {
	Ref    string
	Reason string
}

var _ Spender = (*FakeSpender)(nil)

func (f *FakeSpender) Reserve(_ context.Context, userID string, op Operation, ref string) (Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reserved = append(f.Reserved, ReserveCall{UserID: userID, Operation: op, Ref: ref})
	if f.ReserveErr != nil {
		return Reservation{}, f.ReserveErr
	}
	return Reservation{Ref: ref, UserID: userID, Operation: op, Status: StatusHeld}, nil
}

func (f *FakeSpender) Commit(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Committed = append(f.Committed, ref)
	return nil
}

func (f *FakeSpender) Release(_ context.Context, ref, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Released = append(f.Released, ReleaseCall{Ref: ref, Reason: reason})
	return nil
}
````

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/credit/ ./internal/i18n/`
Expected: `ok` for both (`TestCatalogComplete` checks the new keys have TR and EN texts).

- [ ] **Step 6: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 32 added lines: the five campaign message keys and their TR/EN texts, the `CampaignInput`, `CampaignList`, `CampaignSummary` JSON tags (`name`, `quota`, `perUser`, `validityDays`, `endsAt`, `items`, `summary`, `activeId`, `totalClaims`, `totalCredits`).

- [ ] **Step 7: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/credit core-service/internal/i18n core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): kredi servisi, golge mod ve kampanya kurallari

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: `UserCreatedAt` on the identity server

K5 needs the sign-up time; `*identity.Server` provides it without exposing its pool.

**Files:**
- Create: `core-service/internal/identity/user_created_at_test.go`
- Modify: `core-service/internal/identity/server.go`

**Interfaces:**
- Consumes: `UserStore.ByID`, `ErrUserNotFound`, `NewFakeUserStore`, `testPostgresUserStore` (existing).
- Produces: `func (s *Server) UserCreatedAt(ctx context.Context, userID string) (time.Time, error)` — satisfies `credit.UserCreatedAt`; malformed or unknown id → `ErrUserNotFound`.

- [ ] **Step 1: Write the failing test**

**Create** `core-service/internal/identity/user_created_at_test.go`:

````go
package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestServer_UserCreatedAt(t *testing.T) {
	users := NewFakeUserStore()
	srv := &Server{users: users}
	ctx := context.Background()
	u, err := users.Create(ctx, "kredi@ornek.com", "Ad", "uzun-sifre-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := srv.UserCreatedAt(ctx, u.ID)
	if err != nil || !got.Equal(u.CreatedAt) {
		t.Fatalf("UserCreatedAt = %v, %v; want %v", got, err, u.CreatedAt)
	}
	if got.IsZero() || time.Since(got) > time.Minute {
		t.Fatalf("created_at looks wrong: %v", got)
	}
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		if _, err := srv.UserCreatedAt(ctx, id); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("%q: want ErrUserNotFound, got %v", id, err)
		}
	}
}

// The Postgres store reads users.created_at (live; skipped without Postgres).
func TestServer_UserCreatedAtPostgres(t *testing.T) {
	users := testPostgresUserStore(t)
	srv := &Server{users: users}
	ctx := context.Background()
	u, err := users.Create(ctx, "kredi-"+uuid.NewString()+"@ornek.com", "Ad", "uzun-sifre-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = users.Delete(context.Background(), u.ID) })
	got, err := srv.UserCreatedAt(ctx, u.ID)
	if err != nil || got.Sub(u.CreatedAt).Abs() > time.Millisecond {
		t.Fatalf("UserCreatedAt = %v, %v; want %v", got, err, u.CreatedAt)
	}
}
````

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/identity -run UserCreatedAt`
Expected: build failure, `srv.UserCreatedAt undefined (type *Server has no field or method UserCreatedAt)`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/identity/server.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/identity/server.go b/core-service/internal/identity/server.go
index 608747f..5d14261 100644
--- a/core-service/internal/identity/server.go
+++ b/core-service/internal/identity/server.go
@@ -19,6 +19,7 @@ import (
 	"sync/atomic"
 	"time"
 
+	"github.com/google/uuid"
 	"github.com/jackc/pgx/v5/pgxpool"
 	"github.com/redis/go-redis/v9"
 	"github.com/zitadel/oidc/v3/pkg/oidc"
@@ -662,6 +663,21 @@ func (s *Server) APIPublicKey() *rsa.PublicKey {
 	return &s.signingKey.private.PublicKey
 }
 
+// UserCreatedAt returns when the user signed up. The credit system uses it
+// to give campaign gifts only to users who signed up after the campaign
+// started (credit.UserCreatedAt). A malformed or unknown id is
+// ErrUserNotFound.
+func (s *Server) UserCreatedAt(ctx context.Context, userID string) (time.Time, error) {
+	if _, err := uuid.Parse(userID); err != nil {
+		return time.Time{}, ErrUserNotFound
+	}
+	u, err := s.users.ByID(ctx, userID)
+	if err != nil {
+		return time.Time{}, err
+	}
+	return u.CreatedAt, nil
+}
+
 // DeleteAccount deletes the user's identity record. Federated links and
 // refresh families go away with ON DELETE CASCADE; no new access token can
 // be produced. For the access tokens that remain, the deleted-user block
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 -v ./internal/identity -run UserCreatedAt 2>&1 | grep -E '^(---|ok)'`
Expected: `TestServer_UserCreatedAt` PASS, `TestServer_UserCreatedAtPostgres` PASS (SKIP without Postgres), `ok`. Then `go test -count=1 ./internal/identity` → `ok` (see the Redis DB 15 note in Global Constraints if flow tests fail).

- [ ] **Step 5: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go test ./tools/wirecheck && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → wirecheck `ok` (no wire change), `0 issues.`

```bash
git add core-service/internal/identity
git commit -m "feat(kimlik): kayit zamani kredi kampanyalari icin okunur

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Credit handlers, admin gate and the 402 helper

`GET /api/credits`, `GET /api/credits/history`, the admin campaign endpoints, `AdminGate` (403 `yetkisiz`, empty list closes), error mapping, `insufficientCredits` (402 `kredi_yetersiz` with `details {required, balance}`).

**Files:**
- Create: `core-service/internal/middleware/admin_test.go`
- Create: `core-service/internal/handler/credit_test.go`
- Modify: `core-service/internal/i18n/catalog.go`
- Create: `core-service/internal/middleware/admin.go`
- Create: `core-service/internal/handler/credit.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: `credit.Service` methods and types (Task 6); `requestOwner`, `asUser`, `doSocialRequest`, `errorCode` (existing handler code and test helpers); `middleware.GetUserID` (existing).
- Produces:
  - `middleware.AdminGate(adminUserIDs []string) fiber.Handler`, `middleware.CodeAdminForbidden = "yetkisiz"`.
  - `handler.CreditService` interface (Summary, History, Campaigns, CreateCampaign, StopCampaign), `handler.NewCreditHandler(CreditService) *CreditHandler`, `(*CreditHandler).Routes(r fiber.Router)` (binds `GET /` and `GET /history`), `(*CreditHandler).AdminRoutes(r fiber.Router)` (binds `GET /campaigns`, `POST /campaigns`, `POST /campaigns/:id/stop`).
  - Package-level `insufficientCredits(c *fiber.Ctx, e *credit.InsufficientError) error` and `creditError(c *fiber.Ctx, err error) error` (Tasks 9–11 use `insufficientCredits`).
  - Test helper `creditTestUser` (handler package).
  - i18n keys `KeyCreditsInsufficient`, `KeyAdminForbidden`, `KeyCampaignNotFound`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/middleware/admin_test.go`:

````go
package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

const adminID = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

func adminApp(admins []string, userID string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("userId", userID)
		}
		return c.Next()
	})
	app.Use(AdminGate(admins))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func adminStatus(t *testing.T, app *fiber.App) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body.Error.Code
}

func TestAdminGate(t *testing.T) {
	cases := []struct {
		name   string
		admins []string
		user   string
		status int
		code   string
	}{
		{"admin", []string{adminID}, adminID, http.StatusOK, ""},
		{"not an admin", []string{adminID}, "7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", http.StatusForbidden, "yetkisiz"},
		{"empty list closes", nil, adminID, http.StatusForbidden, "yetkisiz"},
		{"no user", []string{adminID}, "", http.StatusUnauthorized, "UNAUTHORIZED"},
	}
	for _, tc := range cases {
		status, code := adminStatus(t, adminApp(tc.admins, tc.user))
		if status != tc.status || code != tc.code {
			t.Errorf("%s: %d %q, want %d %q", tc.name, status, code, tc.status, tc.code)
		}
	}
}
````

**Create** `core-service/internal/handler/credit_test.go`:

````go
package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/middleware"
)

const creditTestUser = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

// fakeCreditService records the last call and returns the configured values.
type fakeCreditService struct {
	err      error
	summary  *credit.Summary
	page     *credit.HistoryPage
	list     *credit.CampaignList
	campaign *credit.Campaign

	lastUser   string
	lastCursor string
	lastLimit  int
	lastInput  credit.CampaignInput
	lastID     string
}

func (f *fakeCreditService) Summary(_ context.Context, userID string) (*credit.Summary, error) {
	f.lastUser = userID
	return f.summary, f.err
}

func (f *fakeCreditService) History(_ context.Context, userID, cursor string, limit int) (*credit.HistoryPage, error) {
	f.lastUser, f.lastCursor, f.lastLimit = userID, cursor, limit
	return f.page, f.err
}

func (f *fakeCreditService) Campaigns(context.Context) (*credit.CampaignList, error) {
	return f.list, f.err
}

func (f *fakeCreditService) CreateCampaign(_ context.Context, adminID string, in credit.CampaignInput) (*credit.Campaign, error) {
	f.lastUser, f.lastInput = adminID, in
	return f.campaign, f.err
}

func (f *fakeCreditService) StopCampaign(_ context.Context, id string) (*credit.Campaign, error) {
	f.lastID = id
	return f.campaign, f.err
}

// creditApp builds /api/credits and /api/admin like main does (without
// rate limits); admins are the AdminGate list.
func creditApp(s *fakeCreditService, userID string, admins []string) *fiber.App {
	app := fiber.New()
	api := app.Group("/api", asUser(userID))
	h := NewCreditHandler(s)
	h.Routes(api.Group("/credits"))
	h.AdminRoutes(api.Group("/admin", middleware.AdminGate(admins)))
	return app
}

func TestCreditHandler_SummaryBody(t *testing.T) {
	s := &fakeCreditService{summary: &credit.Summary{Balance: 5, Plan: credit.PlanFree, Costs: credit.Costs{credit.OpRenderMix: 12}}}
	for _, path := range []string{"/api/credits", "/api/credits/"} {
		status, body := doSocialRequest(t, creditApp(s, creditTestUser, nil), http.MethodGet, path, "")
		if status != http.StatusOK || body["balance"] != float64(5) || body["plan"] != "free" {
			t.Fatalf("%s: %d %v", path, status, body)
		}
		for _, key := range []string{"pools", "costs", "enforced", "welcomeGift"} {
			if _, ok := body[key]; !ok {
				t.Errorf("%s: key %s missing", path, key)
			}
		}
	}
	if s.lastUser != creditTestUser {
		t.Fatalf("user = %q", s.lastUser)
	}
}

func TestCreditHandler_HistoryPassesCursorAndLimit(t *testing.T) {
	s := &fakeCreditService{page: &credit.HistoryPage{Items: []credit.HistoryItem{}}}
	status, body := doSocialRequest(t, creditApp(s, creditTestUser, nil), http.MethodGet, "/api/credits/history?cursor=abc&limit=99", "")
	if status != http.StatusOK || s.lastCursor != "abc" || s.lastLimit != 50 {
		t.Fatalf("%d %v cursor=%q limit=%d", status, body, s.lastCursor, s.lastLimit)
	}
	if _, ok := body["nextCursor"]; !ok {
		t.Fatalf("nextCursor must be present: %v", body)
	}
}

func TestCreditHandler_ErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&credit.ValidationError{Key: i18n.KeyCursorInvalid}, http.StatusUnprocessableEntity, "dogrulama"},
		{credit.ErrInvalidIdentity, http.StatusUnauthorized, "UNAUTHORIZED"},
		{errors.New("pg is down"), http.StatusInternalServerError, "sunucu_hatasi"},
	}
	for _, tc := range cases {
		s := &fakeCreditService{err: tc.err}
		status, body := doSocialRequest(t, creditApp(s, creditTestUser, nil), http.MethodGet, "/api/credits/history", "")
		if status != tc.status || errorCode(body) != tc.code {
			t.Errorf("%v: %d %v", tc.err, status, body)
		}
	}
}

func TestCreditHandler_InsufficientCredits402(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c *fiber.Ctx) error {
		return creditError(c, &credit.InsufficientError{Required: 32, Balance: 20})
	})
	status, body := doSocialRequest(t, app, http.MethodGet, "/x", "")
	if status != http.StatusPaymentRequired || errorCode(body) != "kredi_yetersiz" {
		t.Fatalf("%d %v", status, body)
	}
	details := body["error"].(map[string]any)["details"].(map[string]any)
	if details["required"] != float64(32) || details["balance"] != float64(20) {
		t.Fatalf("details = %v", details)
	}
}

func TestCreditHandler_AdminForbiddenForNonAdmin(t *testing.T) {
	s := &fakeCreditService{list: &credit.CampaignList{Items: []credit.Campaign{}}}
	status, body := doSocialRequest(t, creditApp(s, creditTestUser, nil), http.MethodGet, "/api/admin/campaigns", "")
	if status != http.StatusForbidden || errorCode(body) != "yetkisiz" {
		t.Fatalf("%d %v", status, body)
	}
}

func TestCreditHandler_AdminCampaignFlow(t *testing.T) {
	admins := []string{creditTestUser}
	s := &fakeCreditService{
		list:     &credit.CampaignList{Items: []credit.Campaign{}},
		campaign: &credit.Campaign{ID: "c1", Status: credit.CampaignActive, StartsAt: time.Now()},
	}
	app := creditApp(s, creditTestUser, admins)

	if status, body := doSocialRequest(t, app, http.MethodGet, "/api/admin/campaigns", ""); status != http.StatusOK || body["items"] == nil {
		t.Fatalf("list: %d %v", status, body)
	}
	status, body := doSocialRequest(t, app, http.MethodPost, "/api/admin/campaigns",
		`{"name":"Lansman","quota":500,"perUser":50,"validityDays":30,"endsAt":"2030-01-01T00:00:00Z"}`)
	if status != http.StatusCreated || body["id"] != "c1" {
		t.Fatalf("create: %d %v", status, body)
	}
	if in := s.lastInput; in.Name != "Lansman" || in.Quota != 500 || in.PerUser != 50 || in.ValidityDays != 30 || in.EndsAt == nil || s.lastUser != creditTestUser {
		t.Fatalf("input = %+v by %q", in, s.lastUser)
	}
	if status, body := doSocialRequest(t, app, http.MethodPost, "/api/admin/campaigns", `{"name":`); status != http.StatusUnprocessableEntity || errorCode(body) != "dogrulama" {
		t.Fatalf("bad body: %d %v", status, body)
	}
	if status, _ := doSocialRequest(t, app, http.MethodPost, "/api/admin/campaigns/c1/stop", ""); status != http.StatusOK || s.lastID != "c1" {
		t.Fatalf("stop: %d id=%q", status, s.lastID)
	}
	s.err = credit.ErrNotFound
	if status, body := doSocialRequest(t, app, http.MethodPost, "/api/admin/campaigns/nope/stop", ""); status != http.StatusNotFound || errorCode(body) != "bulunamadi" {
		t.Fatalf("stop unknown: %d %v", status, body)
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/middleware ./internal/handler`
Expected: build failures, `undefined: AdminGate`, `undefined: NewCreditHandler`, `undefined: creditError`.

- [ ] **Step 3: Add the i18n messages**

**Modify** `core-service/internal/i18n/catalog.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/i18n/catalog.go b/core-service/internal/i18n/catalog.go
index 3441c89..7f0ff2b 100644
--- a/core-service/internal/i18n/catalog.go
+++ b/core-service/internal/i18n/catalog.go
@@ -105,6 +105,11 @@ var (
 	KeyReportTargetInvalid       = Key{"report_target_invalid"}
 	KeyReportReasonInvalid       = Key{"report_reason_invalid"}
 
+	// Credits and the admin area.
+	KeyCreditsInsufficient = Key{"credits_insufficient"}
+	KeyAdminForbidden      = Key{"admin_forbidden"}
+	KeyCampaignNotFound    = Key{"campaign_not_found"}
+
 	// Credit campaign validation (422 dogrulama).
 	KeyCampaignNameLength    = Key{"campaign_name_length"}
 	KeyCampaignQuotaRange    = Key{"campaign_quota_range"}
@@ -213,6 +218,10 @@ var catalog = map[Key]texts{
 	KeyReportTargetInvalid:       {"hedefTuru yayin ya da yorum olmalı.", "hedefTuru must be yayin or yorum."},
 	KeyReportReasonInvalid:       {"neden spam, taciz, telif, uygunsuz ya da diger olmalı.", "neden must be spam, taciz, telif, uygunsuz or diger."},
 
+	KeyCreditsInsufficient: {"Bu işlem için yeterli kredin yok.", "You do not have enough credits for this."},
+	KeyAdminForbidden:      {"Bu işlem yalnızca yöneticilere açık.", "This action is for administrators only."},
+	KeyCampaignNotFound:    {"Kampanya bulunamadı.", "Campaign not found."},
+
 	KeyCampaignNameLength:    {"Kampanya adı 1-80 karakter olmalı.", "Campaign name must be 1-80 characters."},
 	KeyCampaignQuotaRange:    {"quota 1-1000000 aralığında olmalı.", "quota must be between 1 and 1000000."},
 	KeyCampaignPerUserRange:  {"perUser 1-10000 aralığında olmalı.", "perUser must be between 1 and 10000."},
````

- [ ] **Step 4: Implement the gate and the handler**

**Create** `core-service/internal/middleware/admin.go`:

````go
package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// CodeAdminForbidden is the 403 code of the admin gate.
const CodeAdminForbidden = "yetkisiz"

// AdminGate lets only the listed user ids through (403 yetkisiz for
// everyone else). An empty list closes the guarded routes completely. The
// ids must be canonical lower-case UUIDs, the form the auth middleware
// writes (config.parseAdminUserIDs produces it).
func AdminGate(adminUserIDs []string) fiber.Handler {
	allowed := make(map[string]bool, len(adminUserIDs))
	for _, id := range adminUserIDs {
		allowed[id] = true
	}
	return func(c *fiber.Ctx) error {
		userID := GetUserID(c)
		if userID == "" {
			return response.Unauthorized(c, i18n.KeyUnauthenticated)
		}
		if !allowed[userID] {
			return response.Error(c, fiber.StatusForbidden, CodeAdminForbidden, i18n.KeyAdminForbidden, nil)
		}
		return c.Next()
	}
}
````

**Create** `core-service/internal/handler/credit.go`:

````go
package handler

import (
	"context"
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// Credit error codes (wire contract).
const (
	codeInsufficientCredits = "kredi_yetersiz"
	codeCreditValidation    = "dogrulama"
	codeCreditNotFound      = "bulunamadi"
	codeCreditServerError   = "sunucu_hatasi"
)

// CreditService is the service surface of /api/credits and /api/admin
// (implemented by *credit.Service).
type CreditService interface {
	Summary(ctx context.Context, userID string) (*credit.Summary, error)
	History(ctx context.Context, userID, cursor string, limit int) (*credit.HistoryPage, error)
	Campaigns(ctx context.Context) (*credit.CampaignList, error)
	CreateCampaign(ctx context.Context, adminID string, in credit.CampaignInput) (*credit.Campaign, error)
	StopCampaign(ctx context.Context, campaignID string) (*credit.Campaign, error)
}

var _ CreditService = (*credit.Service)(nil)

// CreditHandler serves the balance, history and admin campaign endpoints.
type CreditHandler struct {
	service CreditService
}

func NewCreditHandler(service CreditService) *CreditHandler {
	return &CreditHandler{service: service}
}

// Routes binds GET /api/credits and GET /api/credits/history to the
// /api/credits group.
func (h *CreditHandler) Routes(r fiber.Router) {
	r.Get("/", h.Summary)
	r.Get("/history", h.History)
}

// AdminRoutes binds the campaign endpoints to the /api/admin group (which
// must already carry middleware.AdminGate).
func (h *CreditHandler) AdminRoutes(r fiber.Router) {
	r.Get("/campaigns", h.Campaigns)
	r.Post("/campaigns", h.CreateCampaign)
	r.Post("/campaigns/:id/stop", h.StopCampaign)
}

// creditShortfall is the details object of 402 kredi_yetersiz.
type creditShortfall struct {
	Required int `json:"required"`
	Balance  int `json:"balance"`
}

// insufficientCredits answers 402 kredi_yetersiz with {required, balance}.
func insufficientCredits(c *fiber.Ctx, e *credit.InsufficientError) error {
	return response.Error(c, fiber.StatusPaymentRequired, codeInsufficientCredits, i18n.KeyCreditsInsufficient,
		creditShortfall{Required: e.Required, Balance: e.Balance})
}

// creditError maps credit service errors to the contract; anything
// unrecognized is 500 sunucu_hatasi with the detail only in the log.
func creditError(c *fiber.Ctx, err error) error {
	if e, ok := credit.AsInsufficient(err); ok {
		return insufficientCredits(c, e)
	}
	if errors.Is(err, credit.ErrInvalidIdentity) {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var v *credit.ValidationError
	if errors.As(err, &v) {
		return response.Error(c, fiber.StatusUnprocessableEntity, codeCreditValidation, v.Key, nil, v.Args...)
	}
	if errors.Is(err, credit.ErrNotFound) {
		return response.Error(c, fiber.StatusNotFound, codeCreditNotFound, i18n.KeyCampaignNotFound, nil)
	}
	log.Printf("credit: %s %s: %v", c.Method(), c.Route().Path, err)
	return response.Error(c, fiber.StatusInternalServerError, codeCreditServerError, i18n.KeyInternal, nil)
}

// Summary handles GET /api/credits: balance, pools, prices, enforcement and
// the welcome gift (claimed here for eligible new users).
func (h *CreditHandler) Summary(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	summary, err := h.service.Summary(c.Context(), userID)
	if err != nil {
		return creditError(c, err)
	}
	return response.OK(c, summary)
}

// History handles GET /api/credits/history?cursor=&limit=.
func (h *CreditHandler) History(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	page, err := h.service.History(c.Context(), userID, c.Query("cursor"), credit.PageSize(c.Query("limit")))
	if err != nil {
		return creditError(c, err)
	}
	return response.OK(c, page)
}

// Campaigns handles GET /api/admin/campaigns.
func (h *CreditHandler) Campaigns(c *fiber.Ctx) error {
	list, err := h.service.Campaigns(c.Context())
	if err != nil {
		return creditError(c, err)
	}
	return response.OK(c, list)
}

// CreateCampaign handles POST /api/admin/campaigns; it stops the active
// campaign and answers 201 with the new one.
func (h *CreditHandler) CreateCampaign(c *fiber.Ctx) error {
	adminID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var in credit.CampaignInput
	if err := c.BodyParser(&in); err != nil {
		return response.Error(c, fiber.StatusUnprocessableEntity, codeCreditValidation, i18n.KeyInvalidBody, nil)
	}
	campaign, err := h.service.CreateCampaign(c.Context(), adminID, in)
	if err != nil {
		return creditError(c, err)
	}
	return response.Created(c, campaign)
}

// StopCampaign handles POST /api/admin/campaigns/:id/stop (idempotent).
func (h *CreditHandler) StopCampaign(c *fiber.Ctx) error {
	campaign, err := h.service.StopCampaign(c.Context(), c.Params("id"))
	if err != nil {
		return creditError(c, err)
	}
	return response.OK(c, campaign)
}
````

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/middleware ./internal/handler ./internal/i18n`
Expected: `ok` ×3 (`TestAdminGate`, `TestCreditHandler_*` pass; `GET /api/credits` and `/api/credits/` both answer 200).

- [ ] **Step 6: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 30 added lines: `ROUTE GET /`, `ROUTE GET /history`, `ROUTE GET /campaigns`, `ROUTE POST /campaigns`, `ROUTE POST /campaigns/:id/stop`, the codes `kredi_yetersiz`, `yetkisiz`, `dogrulama`, `bulunamadi`, `sunucu_hatasi`, query keys `cursor`, `limit`, param `id`, the three message keys with texts, tags `json:"required"`, `json:"balance"`.

- [ ] **Step 7: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/handler core-service/internal/middleware core-service/internal/i18n core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): bakiye, gecmis ve yonetici kampanya ucu

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Lyrics generate/rewrite reserve, commit, release

Synchronous flow in the handler (R15): validate → reserve (402 when enforced and short) → Groq → commit on success, release on error.

**Files:**
- Create: `core-service/internal/handler/lyrics_credit_test.go`
- Modify: `core-service/internal/handler/lyrics.go`
- Modify: `core-service/internal/handler/credit.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: `credit.Spender`, `credit.FakeSpender`, `credit.OpLyricsGenerate`, `credit.OpLyricsRewrite`, `credit.ReasonRequestFailed`, `insufficientCredits` (Task 8); `service.LyricsGenerator` (existing interface).
- Produces: `NewLyricsHandler(svc service.LyricsGenerator, v *validator.Validate) *LyricsHandler` (parameter type widened), `(*LyricsHandler).WithCredits(spender credit.Spender) *LyricsHandler`; in `handler/credit.go`: `errMissingOwner` and `creditStartError(c *fiber.Ctx, err error) error` (401 / 402 / generic 500).

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/handler/lyrics_credit_test.go`:

````go
package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
)

// fakeLyrics answers generate and rewrite with err (nil = success).
type fakeLyrics struct {
	err   error
	calls int
}

func (f *fakeLyrics) Generate(context.Context, *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &model.LyricsGenerateResponse{Drafts: [][]string{{"a"}}}, nil
}

func (f *fakeLyrics) Rewrite(context.Context, *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &model.LyricsRewriteResponse{Lines: []string{"a"}}, nil
}

func lyricsApp(gen *fakeLyrics, spender *credit.FakeSpender, userID string) *fiber.App {
	h := NewLyricsHandler(gen, validator.New()).WithCredits(spender)
	app := fiber.New()
	app.Post("/generate", asUser(userID), h.Generate)
	app.Post("/rewrite", asUser(userID), h.Rewrite)
	return app
}

const generateBody = `{"genre":"pop","sectionType":"verse","vibes":["x"]}`
const rewriteBody = `{"currentLyrics":"l","genre":"pop","sectionType":"verse","vibes":["x"]}`

func TestLyricsHandler_CommitsOnSuccess(t *testing.T) {
	spender := &credit.FakeSpender{}
	app := lyricsApp(&fakeLyrics{}, spender, creditTestUser)
	if status, _ := doSocialRequest(t, app, http.MethodPost, "/generate", generateBody); status != http.StatusOK {
		t.Fatalf("generate status = %d", status)
	}
	if status, _ := doSocialRequest(t, app, http.MethodPost, "/rewrite", rewriteBody); status != http.StatusOK {
		t.Fatalf("rewrite status = %d", status)
	}
	if len(spender.Reserved) != 2 || spender.Reserved[0].Operation != credit.OpLyricsGenerate ||
		spender.Reserved[1].Operation != credit.OpLyricsRewrite || spender.Reserved[0].UserID != creditTestUser {
		t.Fatalf("reserved = %+v", spender.Reserved)
	}
	if len(spender.Committed) != 2 || spender.Committed[0] != spender.Reserved[0].Ref || len(spender.Released) != 0 {
		t.Fatalf("committed = %v released = %v", spender.Committed, spender.Released)
	}
	if spender.Reserved[0].Ref == spender.Reserved[1].Ref || spender.Reserved[0].Ref == "" {
		t.Fatal("each request needs its own ref")
	}
}

func TestLyricsHandler_ReleasesOnAIError(t *testing.T) {
	spender := &credit.FakeSpender{}
	app := lyricsApp(&fakeLyrics{err: errors.New("groq down")}, spender, creditTestUser)
	if status, _ := doSocialRequest(t, app, http.MethodPost, "/generate", generateBody); status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	if len(spender.Released) != 1 || spender.Released[0].Reason != credit.ReasonRequestFailed || len(spender.Committed) != 0 {
		t.Fatalf("released = %v committed = %v", spender.Released, spender.Committed)
	}
}

func TestLyricsHandler_InsufficientCreditsSkipsGroq(t *testing.T) {
	gen := &fakeLyrics{}
	spender := &credit.FakeSpender{ReserveErr: &credit.InsufficientError{Required: 2, Balance: 1}}
	status, body := doSocialRequest(t, lyricsApp(gen, spender, creditTestUser), http.MethodPost, "/generate", generateBody)
	if status != http.StatusPaymentRequired || errorCode(body) != "kredi_yetersiz" || gen.calls != 0 {
		t.Fatalf("%d %v calls=%d", status, body, gen.calls)
	}
}

// A request that fails validation costs nothing.
func TestLyricsHandler_InvalidBodyReservesNothing(t *testing.T) {
	spender := &credit.FakeSpender{}
	if status, _ := doSocialRequest(t, lyricsApp(&fakeLyrics{}, spender, creditTestUser), http.MethodPost, "/generate", `{"genre":"x"}`); status != http.StatusBadRequest {
		t.Fatalf("status = %d", status)
	}
	if len(spender.Reserved) != 0 {
		t.Fatal("an invalid request must not reserve")
	}
}

func TestLyricsHandler_WithoutCreditsUnchanged(t *testing.T) {
	gen := &fakeLyrics{}
	app := fiber.New()
	app.Post("/generate", NewLyricsHandler(gen, validator.New()).Generate)
	if status, _ := doSocialRequest(t, app, http.MethodPost, "/generate", generateBody); status != http.StatusOK || gen.calls != 1 {
		t.Fatalf("status = %d calls = %d", status, gen.calls)
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/handler -run Lyrics`
Expected: build failure, `cannot use gen (variable of type *fakeLyrics) as *service.LyricsService value` and `NewLyricsHandler(...).WithCredits undefined`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/handler/credit.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/handler/credit.go b/core-service/internal/handler/credit.go
index b1fa171..4b28dd0 100644
--- a/core-service/internal/handler/credit.go
+++ b/core-service/internal/handler/credit.go
@@ -67,6 +67,22 @@ func insufficientCredits(c *fiber.Ctx, e *credit.InsufficientError) error {
 		creditShortfall{Required: e.Required, Balance: e.Balance})
 }
 
+// errMissingOwner: the request carries no user id (auth misconfiguration).
+var errMissingOwner = errors.New("request has no user id")
+
+// creditStartError answers a failed reservation before paid work: 401
+// without a user, 402 kredi_yetersiz for a short balance (enforcement on),
+// otherwise the endpoint's generic 500.
+func creditStartError(c *fiber.Ctx, err error) error {
+	if errors.Is(err, errMissingOwner) {
+		return response.Unauthorized(c, i18n.KeyUnauthenticated)
+	}
+	if e, ok := credit.AsInsufficient(err); ok {
+		return insufficientCredits(c, e)
+	}
+	return internalError(c, err)
+}
+
 // creditError maps credit service errors to the contract; anything
 // unrecognized is 500 sunucu_hatasi with the detail only in the log.
 func creditError(c *fiber.Ctx, err error) error {
````

**Modify** `core-service/internal/handler/lyrics.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/handler/lyrics.go b/core-service/internal/handler/lyrics.go
index e5af356..e8d9a88 100644
--- a/core-service/internal/handler/lyrics.go
+++ b/core-service/internal/handler/lyrics.go
@@ -1,8 +1,12 @@
 package handler
 
 import (
+	"log"
+
 	"github.com/go-playground/validator/v10"
 	"github.com/gofiber/fiber/v2"
+	"github.com/google/uuid"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -10,17 +14,60 @@ import (
 )
 
 type LyricsHandler struct {
-	service   *service.LyricsService
+	service   service.LyricsGenerator
 	validator *validator.Validate
+	credits   credit.Spender // nil: no credit accounting
 }
 
-func NewLyricsHandler(svc *service.LyricsService, v *validator.Validate) *LyricsHandler {
+func NewLyricsHandler(svc service.LyricsGenerator, v *validator.Validate) *LyricsHandler {
 	return &LyricsHandler{
 		service:   svc,
 		validator: v,
 	}
 }
 
+// WithCredits makes generate and rewrite reserve their price before the
+// Groq call, commit it on success and release it on failure.
+func (h *LyricsHandler) WithCredits(spender credit.Spender) *LyricsHandler {
+	h.credits = spender
+	return h
+}
+
+// reserveCredits holds the operation's price for this request under a new
+// ref. It returns "" when credits are off.
+func (h *LyricsHandler) reserveCredits(c *fiber.Ctx, op credit.Operation) (string, error) {
+	if h.credits == nil {
+		return "", nil
+	}
+	userID, ok := requestOwner(c)
+	if !ok {
+		return "", errMissingOwner
+	}
+	ref := uuid.NewString()
+	if _, err := h.credits.Reserve(c.Context(), userID, op, ref); err != nil {
+		return "", err
+	}
+	return ref, nil
+}
+
+// settleCredits commits the request's reservation when the AI call
+// succeeded and releases it otherwise; errors are only logged (a held
+// reservation is released by the sweep).
+func (h *LyricsHandler) settleCredits(c *fiber.Ctx, ref string, succeeded bool) {
+	if h.credits == nil || ref == "" {
+		return
+	}
+	var err error
+	if succeeded {
+		err = h.credits.Commit(c.Context(), ref)
+	} else {
+		err = h.credits.Release(c.Context(), ref, credit.ReasonRequestFailed)
+	}
+	if err != nil {
+		log.Printf("credit: could not settle lyrics reservation %s: %v", ref, err)
+	}
+}
+
 // Generate handles POST /api/lyrics/generate
 // @Summary      Generate lyrics
 // @Description  Generate lyrics for a song section using AI
@@ -31,6 +78,7 @@ func NewLyricsHandler(svc *service.LyricsService, v *validator.Validate) *Lyrics
 // @Success      200 {object} model.LyricsGenerateResponse
 // @Failure      400 {object} response.ErrorResponse
 // @Failure      401 {object} response.ErrorResponse
+// @Failure      402 {object} response.ErrorResponse
 // @Failure      429 {object} response.ErrorResponse
 // @Failure      502 {object} response.ErrorResponse
 // @Security     BearerAuth
@@ -45,7 +93,12 @@ func (h *LyricsHandler) Generate(c *fiber.Ctx) error {
 		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
+	ref, err := h.reserveCredits(c, credit.OpLyricsGenerate)
+	if err != nil {
+		return creditStartError(c, err)
+	}
 	result, err := h.service.Generate(c.Context(), &req)
+	h.settleCredits(c, ref, err == nil)
 	if err != nil {
 		return aiError(c, err)
 	}
@@ -63,6 +116,7 @@ func (h *LyricsHandler) Generate(c *fiber.Ctx) error {
 // @Success      200 {object} model.LyricsRewriteResponse
 // @Failure      400 {object} response.ErrorResponse
 // @Failure      401 {object} response.ErrorResponse
+// @Failure      402 {object} response.ErrorResponse
 // @Failure      429 {object} response.ErrorResponse
 // @Failure      502 {object} response.ErrorResponse
 // @Security     BearerAuth
@@ -77,7 +131,12 @@ func (h *LyricsHandler) Rewrite(c *fiber.Ctx) error {
 		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
 	}
 
+	ref, err := h.reserveCredits(c, credit.OpLyricsRewrite)
+	if err != nil {
+		return creditStartError(c, err)
+	}
 	result, err := h.service.Rewrite(c.Context(), &req)
+	h.settleCredits(c, ref, err == nil)
 	if err != nil {
 		return aiError(c, err)
 	}
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/handler && go vet ./...`
Expected: `ok`; `go vet ./...` also compiles `e2e` (which still passes `*service.LyricsService`).

- [ ] **Step 5: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 5 added lines: `"request has no user id"` (package-level sentinel) and four empty-string literals.

- [ ] **Step 6: Lint, commit**

Run: `gofmt -l . && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/handler core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): soz uretme ve yeniden yazma kredi ayirir

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Render — reserve before enqueue, settle in the worker, skip canceled jobs

`StartRender` reserves the stem mode's price with `ref = jobID` before the job record (402 on short balance), releases on save/enqueue failure; `CancelRender` releases a queued job (R5); the worker skips canceled jobs without calling Suno and settles from the asynq outcome (R4, R18).

**Files:**
- Create: `core-service/internal/service/render_credit_test.go`
- Create: `core-service/internal/handler/render_credit_test.go`
- Create: `core-service/internal/worker/render_credit_test.go`
- Modify: `core-service/internal/service/render_service.go`
- Modify: `core-service/internal/handler/render.go`
- Create: `core-service/internal/worker/job_credits.go`
- Modify: `core-service/internal/worker/render_worker.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: `credit.Spender`, `credit.FakeSpender`, operations and reasons (Tasks 3, 6); `insufficientCredits`, `creditTestUser`, `asUser`, `doSocialRequest`, `errorCode` (Task 8 / existing); `asynqIsLastAttempt` (existing, `worker/social_worker.go`); `liveRedis`, `liveRedisAddr`, `storeJob`, `testR2Cfg` (existing service tests); `testWorker`, `fakeSuno`, `fakeStorage`, `audioServer`, `testPayload` (existing worker tests).
- Produces:
  - `(*service.RenderService).WithCredits(spender credit.Spender) *RenderService`, `(*service.RenderService).JobStatus(ctx, jobID string) (model.JobStatus, error)`, unexported `renderOperation(model.StemMode) credit.Operation` and `releaseCredits(ctx, spender credit.Spender, jobID, reason string)` (Task 11 reuses `releaseCredits`).
  - Worker: `jobCredits{spender credit.Spender; isLastAttempt func(context.Context) bool}`, `newJobCredits() jobCredits`, `(jobCredits).settle(ctx, ref string, err error)`, `(jobCredits).release(ctx, ref, reason string)`; `renderJobs` interface; `(*RenderWorker).WithCredits(spender credit.Spender) *RenderWorker`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/service/render_credit_test.go`:

````go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
)

func TestRenderOperation(t *testing.T) {
	cases := map[model.StemMode]credit.Operation{
		model.StemModeMix: credit.OpRenderMix, model.StemModeFocus: credit.OpRenderFocus,
		model.StemModeStems: credit.OpRenderStems, "": credit.OpRenderMix,
	}
	for mode, want := range cases {
		if got := renderOperation(mode); got != want {
			t.Errorf("%q: %s, want %s", mode, got, want)
		}
	}
}

func TestRenderService_StartRenderReservesJobID(t *testing.T) {
	rdb := liveRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	defer ac.Close()
	spender := &credit.FakeSpender{}
	s := NewRenderService(rdb, ac, nil, testR2Cfg()).WithCredits(spender)

	drums := model.InstrumentDrums
	resp, err := s.StartRender(context.Background(), "kullanici-a", &model.RenderStartRequest{
		ProjectID:       uuid.New().String(),
		Arrangement:     model.Arrangement{Instruments: []model.Instrument{model.InstrumentDrums}},
		StemMode:        model.StemModeFocus,
		FocusInstrument: &drums,
	})
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	want := credit.ReserveCall{UserID: "kullanici-a", Operation: credit.OpRenderFocus, Ref: resp.JobID}
	if len(spender.Reserved) != 1 || spender.Reserved[0] != want {
		t.Fatalf("reserved = %+v, want %+v", spender.Reserved, want)
	}
	if len(spender.Released) != 0 {
		t.Fatalf("released = %+v", spender.Released)
	}
}

// Redis is nil: a refused reservation must stop BEFORE the job record.
func TestRenderService_InsufficientCreditsCreateNoJob(t *testing.T) {
	short := &credit.InsufficientError{Required: 62, Balance: 10}
	s := NewRenderService(nil, nil, nil, testR2Cfg()).WithCredits(&credit.FakeSpender{ReserveErr: short})
	_, err := s.StartRender(context.Background(), "kullanici-a", &model.RenderStartRequest{ProjectID: uuid.New().String(), StemMode: model.StemModeStems})
	if !errors.Is(err, short) {
		t.Fatalf("want the insufficient error, got %v", err)
	}
}

func TestRenderService_EnqueueFailureReleases(t *testing.T) {
	rdb := liveRedis(t)
	dead := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond})
	defer dead.Close()
	spender := &credit.FakeSpender{}
	s := NewRenderService(rdb, dead, nil, testR2Cfg()).WithCredits(spender)

	if _, err := s.StartRender(context.Background(), "kullanici-a", &model.RenderStartRequest{ProjectID: uuid.New().String()}); err == nil {
		t.Fatal("StartRender must fail when the queue is down")
	}
	if len(spender.Reserved) != 1 || len(spender.Released) != 1 ||
		spender.Released[0] != (credit.ReleaseCall{Ref: spender.Reserved[0].Ref, Reason: credit.ReasonEnqueueFailed}) {
		t.Fatalf("reserved = %+v released = %+v", spender.Reserved, spender.Released)
	}
}

func TestRenderService_CancelReleasesOnlyQueuedJobs(t *testing.T) {
	rdb := liveRedis(t)
	spender := &credit.FakeSpender{}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(spender)
	storeJob(t, rdb, &model.Job{ID: "kuyrukta", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued})
	storeJob(t, rdb, &model.Job{ID: "calisiyor", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusRunning})

	for _, id := range []string{"kuyrukta", "calisiyor"} {
		if _, err := s.CancelRender(context.Background(), "kullanici-a", id); err != nil {
			t.Fatalf("CancelRender %s: %v", id, err)
		}
	}
	if len(spender.Released) != 1 || spender.Released[0] != (credit.ReleaseCall{Ref: "kuyrukta", Reason: credit.ReasonCanceled}) {
		t.Fatalf("released = %+v", spender.Released)
	}
	status, err := s.JobStatus(context.Background(), "kuyrukta")
	if err != nil || status != model.JobStatusCanceled {
		t.Fatalf("JobStatus = %q, %v", status, err)
	}
}
````

**Create** `core-service/internal/handler/render_credit_test.go`:

````go
package handler

import (
	"net/http"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/service"
)

const renderStartBody = `{"projectId":"7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a",
	"brief":{"genre":"pop","vibes":["x"],"bpm":{"mode":"auto"},"key":{"mode":"auto"},
		"structure":[{"id":"s1","type":"verse","bars":8}]},
	"arrangement":{"instruments":["drums"],"density":"medium","groove":"straight"},
	"stemMode":"stems"}`

// Redis is nil: the refused reservation answers 402 before any job record.
func TestRenderHandler_InsufficientCredits402(t *testing.T) {
	spender := &credit.FakeSpender{ReserveErr: &credit.InsufficientError{Required: 62, Balance: 12}}
	h := NewRenderHandler(service.NewRenderService(nil, nil, nil, &config.R2Config{}).WithCredits(spender), validator.New())
	app := fiber.New()
	app.Post("/start", asUser(creditTestUser), h.Start)
	status, body := doSocialRequest(t, app, http.MethodPost, "/start", renderStartBody)
	if status != http.StatusPaymentRequired || errorCode(body) != "kredi_yetersiz" {
		t.Fatalf("%d %v", status, body)
	}
	if len(spender.Reserved) != 1 || spender.Reserved[0].Operation != credit.OpRenderStems {
		t.Fatalf("reserved = %+v", spender.Reserved)
	}
}
````

**Create** `core-service/internal/worker/render_credit_test.go`:

````go
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/websocket"
)

// fakeRenderJobs is an in-memory job record for ProcessTask tests.
type fakeRenderJobs struct {
	status    model.JobStatus
	completed int
	failed    int
}

func (f *fakeRenderJobs) JobStatus(context.Context, string) (model.JobStatus, error) {
	return f.status, nil
}
func (f *fakeRenderJobs) UpdateJobProgress(context.Context, string, int, string) error { return nil }
func (f *fakeRenderJobs) CompleteJob(context.Context, string, interface{}) error {
	f.completed++
	return nil
}
func (f *fakeRenderJobs) FailJob(context.Context, string, string) error {
	f.failed++
	return nil
}
func (f *fakeRenderJobs) BuildResult(context.Context, *model.RenderResultRecord) (*model.RenderResultResponse, error) {
	return &model.RenderResultResponse{}, nil
}

// creditWorker is a render worker with fake jobs, a fake spender and an
// unread hub (its channel is buffered, so broadcasts do not block).
func creditWorker(s *fakeSuno, jobs *fakeRenderJobs, spender *credit.FakeSpender, lastAttempt bool) *RenderWorker {
	w := testWorker(s, &fakeStorage{}).WithCredits(spender)
	w.renderService = jobs
	w.hub = websocket.NewHub()
	w.credits.isLastAttempt = func(context.Context) bool { return lastAttempt }
	return w
}

func renderTask(t *testing.T) *asynq.Task {
	t.Helper()
	p := testPayload()
	p.StemMode = model.StemModeMix
	raw, _ := json.Marshal(p)
	body, _ := json.Marshal(map[string]any{"jobId": "j1", "payload": json.RawMessage(raw)})
	return asynq.NewTask("render:process", body)
}

func TestRenderWorker_CanceledJobSkipsSunoAndReleases(t *testing.T) {
	s := &fakeSuno{}
	spender := &credit.FakeSpender{}
	w := creditWorker(s, &fakeRenderJobs{status: model.JobStatusCanceled}, spender, false)
	if err := w.ProcessTask(context.Background(), renderTask(t)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if s.generateCalls != 0 {
		t.Fatal("Suno must not be called for a canceled job")
	}
	if len(spender.Released) != 1 || spender.Released[0] != (credit.ReleaseCall{Ref: "j1", Reason: credit.ReasonCanceled}) {
		t.Fatalf("released = %+v", spender.Released)
	}
}

func TestRenderWorker_SuccessCommits(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 120}}
	jobs := &fakeRenderJobs{status: model.JobStatusQueued}
	spender := &credit.FakeSpender{}
	if err := creditWorker(s, jobs, spender, false).ProcessTask(context.Background(), renderTask(t)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if jobs.completed != 1 || len(spender.Committed) != 1 || spender.Committed[0] != "j1" || len(spender.Released) != 0 {
		t.Fatalf("completed=%d committed=%v released=%v", jobs.completed, spender.Committed, spender.Released)
	}
}

// After the paid call every failure is SkipRetry: the job is over, so the
// credit goes back.
func TestRenderWorker_PaidFailureReleases(t *testing.T) {
	s := &fakeSuno{waitErr: errors.New("timed out")}
	spender := &credit.FakeSpender{}
	err := creditWorker(s, &fakeRenderJobs{status: model.JobStatusQueued}, spender, false).ProcessTask(context.Background(), renderTask(t))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got %v", err)
	}
	if len(spender.Released) != 1 || spender.Released[0] != (credit.ReleaseCall{Ref: "j1", Reason: credit.ReasonJobFailed}) || len(spender.Committed) != 0 {
		t.Fatalf("released = %+v committed = %v", spender.Released, spender.Committed)
	}
}

// A retryable failure keeps the reservation until the last attempt.
func TestRenderWorker_RetryableFailureReleasesOnLastAttempt(t *testing.T) {
	for _, last := range []bool{false, true} {
		s := &fakeSuno{generateErr: errors.New("suno busy")}
		spender := &credit.FakeSpender{}
		err := creditWorker(s, &fakeRenderJobs{status: model.JobStatusQueued}, spender, last).ProcessTask(context.Background(), renderTask(t))
		if err == nil || errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("last=%v: want a retryable error, got %v", last, err)
		}
		want := 0
		if last {
			want = 1
		}
		if len(spender.Released) != want || len(spender.Committed) != 0 {
			t.Fatalf("last=%v: released = %+v committed = %v", last, spender.Released, spender.Committed)
		}
	}
}

func TestJobCredits_WithoutSpenderIsNoop(t *testing.T) {
	j := newJobCredits()
	j.settle(context.Background(), "j1", nil)
	j.settle(context.Background(), "j1", asynq.SkipRetry)
	j.release(context.Background(), "j1", credit.ReasonCanceled)
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/service ./internal/handler ./internal/worker`
Expected: build failures, `NewRenderService(...).WithCredits undefined`, `undefined: renderOperation`, `s.JobStatus undefined`, `undefined: newJobCredits`, `w.credits undefined`.

- [ ] **Step 3: Implement the service and the handler mapping**

**Modify** `core-service/internal/service/render_service.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/service/render_service.go b/core-service/internal/service/render_service.go
index f155869..19d7e11 100644
--- a/core-service/internal/service/render_service.go
+++ b/core-service/internal/service/render_service.go
@@ -4,12 +4,14 @@ import (
 	"context"
 	"encoding/json"
 	"fmt"
+	"log"
 	"time"
 
 	"github.com/google/uuid"
 	"github.com/hibiken/asynq"
 	"github.com/makeasinger/api/internal/client"
 	"github.com/makeasinger/api/internal/config"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/redis/go-redis/v9"
 )
@@ -25,6 +27,7 @@ type RenderService struct {
 	asynqClient *asynq.Client
 	r2Client    client.StorageClient
 	r2Cfg       *config.R2Config
+	credits     credit.Spender // nil: no credit accounting
 }
 
 func NewRenderService(redisClient *redis.Client, asynqClient *asynq.Client, r2Client client.StorageClient, r2Cfg *config.R2Config) *RenderService {
@@ -36,6 +39,37 @@ func NewRenderService(redisClient *redis.Client, asynqClient *asynq.Client, r2Cl
 	}
 }
 
+// WithCredits makes StartRender reserve the render's price (ref = job id)
+// before the job is queued, and CancelRender release it while the job is
+// still queued. The worker settles every other outcome.
+func (s *RenderService) WithCredits(spender credit.Spender) *RenderService {
+	s.credits = spender
+	return s
+}
+
+// renderOperation is the credit operation of a stem mode (mix by default).
+func renderOperation(mode model.StemMode) credit.Operation {
+	switch mode {
+	case model.StemModeFocus:
+		return credit.OpRenderFocus
+	case model.StemModeStems:
+		return credit.OpRenderStems
+	default:
+		return credit.OpRenderMix
+	}
+}
+
+// releaseCredits gives the job's reservation back; errors are only logged
+// (the sweep releases a reservation that stays held).
+func releaseCredits(ctx context.Context, spender credit.Spender, jobID, reason string) {
+	if spender == nil {
+		return
+	}
+	if err := spender.Release(context.WithoutCancel(ctx), jobID, reason); err != nil {
+		log.Printf("credit: could not release reservation %s (%s): %v", jobID, reason, err)
+	}
+}
+
 // BuildResult produces the client response from the stored record: each
 // stem's URL and expiry are regenerated ON EVERY CALL; the record holds no
 // signed URL.
@@ -81,6 +115,14 @@ func (s *RenderService) StartRender(ctx context.Context, userID string, req *mod
 	jobID := uuid.New().String()
 	now := time.Now()
 
+	// Credit is held BEFORE the job exists: a short balance (enforcement on)
+	// creates no job record and no task.
+	if s.credits != nil {
+		if _, err := s.credits.Reserve(ctx, userID, renderOperation(stemMode), jobID); err != nil {
+			return nil, err
+		}
+	}
+
 	// Create job record
 	job := &model.Job{
 		ID:        jobID,
@@ -110,12 +152,14 @@ func (s *RenderService) StartRender(ctx context.Context, userID string, req *mod
 
 	// Save job to Redis
 	if err := s.saveJob(ctx, job); err != nil {
+		releaseCredits(ctx, s.credits, jobID, credit.ReasonEnqueueFailed)
 		return nil, fmt.Errorf("failed to save job: %w", err)
 	}
 
 	// Create Asynq task
 	task, err := newRenderTask(jobID, payloadBytes)
 	if err != nil {
+		releaseCredits(ctx, s.credits, jobID, credit.ReasonEnqueueFailed)
 		return nil, fmt.Errorf("failed to create task: %w", err)
 	}
 
@@ -126,6 +170,7 @@ func (s *RenderService) StartRender(ctx context.Context, userID string, req *mod
 		asynq.Retention(24*time.Hour),
 	)
 	if err != nil {
+		releaseCredits(ctx, s.credits, jobID, credit.ReasonEnqueueFailed)
 		return nil, fmt.Errorf("failed to enqueue task: %w", err)
 	}
 
@@ -187,6 +232,7 @@ func (s *RenderService) CancelRender(ctx context.Context, userID, jobID string)
 		return nil, ErrJobAlreadyCompleted
 	}
 
+	wasQueued := job.Status == model.JobStatusQueued
 	job.Status = model.JobStatusCanceled
 	now := time.Now()
 	job.CompletedAt = &now
@@ -194,6 +240,12 @@ func (s *RenderService) CancelRender(ctx context.Context, userID, jobID string)
 	if err := s.saveJob(ctx, job); err != nil {
 		return nil, err
 	}
+	// A queued job has not called Suno: its credit goes back now (the worker
+	// skips canceled jobs). A running job may already have paid Suno; the
+	// worker's outcome settles it.
+	if wasQueued {
+		releaseCredits(ctx, s.credits, jobID, credit.ReasonCanceled)
+	}
 
 	return &model.RenderCancelResponse{
 		Success: true,
@@ -202,6 +254,16 @@ func (s *RenderService) CancelRender(ctx context.Context, userID, jobID string)
 	}, nil
 }
 
+// JobStatus returns the stored status of a job without an owner check
+// (called by worker).
+func (s *RenderService) JobStatus(ctx context.Context, jobID string) (model.JobStatus, error) {
+	job, err := s.getJob(ctx, jobID)
+	if err != nil {
+		return "", err
+	}
+	return job.Status, nil
+}
+
 // UpdateJobProgress updates job progress (called by worker)
 func (s *RenderService) UpdateJobProgress(ctx context.Context, jobID string, progress int, step string) error {
 	job, err := s.getJob(ctx, jobID)
````

**Modify** `core-service/internal/handler/render.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/handler/render.go b/core-service/internal/handler/render.go
index 28bac12..d399456 100644
--- a/core-service/internal/handler/render.go
+++ b/core-service/internal/handler/render.go
@@ -5,6 +5,7 @@ import (
 
 	"github.com/go-playground/validator/v10"
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -33,6 +34,7 @@ func NewRenderHandler(svc *service.RenderService, v *validator.Validate) *Render
 // @Success      202 {object} model.RenderStartResponse
 // @Failure      400 {object} response.ErrorResponse
 // @Failure      401 {object} response.ErrorResponse
+// @Failure      402 {object} response.ErrorResponse
 // @Failure      429 {object} response.ErrorResponse
 // @Failure      500 {object} response.ErrorResponse
 // @Security     BearerAuth
@@ -57,6 +59,9 @@ func (h *RenderHandler) Start(c *fiber.Ctx) error {
 		if errors.Is(err, model.ErrInvalidStemSettings) {
 			return response.ValidationError(c, i18n.KeyStemSettingsInvalid, nil)
 		}
+		if e, ok := credit.AsInsufficient(err); ok {
+			return insufficientCredits(c, e)
+		}
 		return internalError(c, err)
 	}
 
````

- [ ] **Step 4: Implement worker settlement and the canceled-job skip**

**Create** `core-service/internal/worker/job_credits.go`:

````go
package worker

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
)

// settleTimeout bounds a commit or release; it runs on a context detached
// from the task's, which may already be past its deadline.
const settleTimeout = 10 * time.Second

// jobCredits closes a job's credit reservation (ref = job id) from the
// task's outcome.
type jobCredits struct {
	spender       credit.Spender // nil: credits off
	isLastAttempt func(context.Context) bool
}

func newJobCredits() jobCredits {
	return jobCredits{isLastAttempt: asynqIsLastAttempt}
}

// settle commits on success and releases when the task will not run again
// (SkipRetry or the last attempt). While asynq still retries, the
// reservation stays held; if the retries never come, the sweep releases it.
func (j jobCredits) settle(ctx context.Context, ref string, err error) {
	if j.spender == nil || ref == "" {
		return
	}
	switch {
	case err == nil:
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
		defer cancel()
		// A failed commit is not returned: a task error would make asynq
		// rerun paid work. The reservation stays held and the sweep releases
		// it (the error favors the user).
		if cerr := j.spender.Commit(sctx, ref); cerr != nil {
			log.Printf("credit: could not commit reservation %s: %v", ref, cerr)
		}
	case errors.Is(err, asynq.SkipRetry) || j.isLastAttempt(ctx):
		j.release(ctx, ref, credit.ReasonJobFailed)
	}
}

// release gives the reservation back; errors are only logged.
func (j jobCredits) release(ctx context.Context, ref, reason string) {
	if j.spender == nil || ref == "" {
		return
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err := j.spender.Release(sctx, ref, reason); err != nil {
		log.Printf("credit: could not release reservation %s (%s): %v", ref, reason, err)
	}
}
````

**Modify** `core-service/internal/worker/render_worker.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/worker/render_worker.go b/core-service/internal/worker/render_worker.go
index 15c4476..19ca1c0 100644
--- a/core-service/internal/worker/render_worker.go
+++ b/core-service/internal/worker/render_worker.go
@@ -14,6 +14,7 @@ import (
 	"github.com/google/uuid"
 	"github.com/hibiken/asynq"
 	"github.com/makeasinger/api/internal/client"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/deletionmark"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -50,30 +51,69 @@ func ownerCanWrite(ctx context.Context, d deletionmark.Checker, ownerID string)
 	return nil
 }
 
+// renderJobs is the part of *service.RenderService the render worker uses.
+type renderJobs interface {
+	JobStatus(ctx context.Context, jobID string) (model.JobStatus, error)
+	UpdateJobProgress(ctx context.Context, jobID string, progress int, step string) error
+	CompleteJob(ctx context.Context, jobID string, result interface{}) error
+	FailJob(ctx context.Context, jobID string, errMsg string) error
+	BuildResult(ctx context.Context, record *model.RenderResultRecord) (*model.RenderResultResponse, error)
+}
+
 // RenderWorker processes render jobs
 type RenderWorker struct {
-	renderService   *service.RenderService
+	renderService   renderJobs // nil only in tests that never touch the job record
 	music           client.MusicGenerator
 	r2Client        client.StorageClient
 	hub             *websocket.Hub
 	downloader      *http.Client
 	pollInterval    time.Duration
 	deletionChecker deletionmark.Checker // nil: no check
+	credits         jobCredits
 }
 
 // NewRenderWorker creates a new render worker
 func NewRenderWorker(renderService *service.RenderService, music client.MusicGenerator, r2Client client.StorageClient, hub *websocket.Hub, deletionChecker deletionmark.Checker) *RenderWorker {
+	// A nil pointer is not stored in the interface (typed-nil trap).
+	var jobs renderJobs
+	if renderService != nil {
+		jobs = renderService
+	}
 	return &RenderWorker{
-		renderService:   renderService,
+		renderService:   jobs,
 		music:           music,
 		r2Client:        r2Client,
 		hub:             hub,
 		downloader:      &http.Client{Timeout: 30 * time.Second},
 		pollInterval:    5 * time.Second,
 		deletionChecker: deletionChecker,
+		credits:         newJobCredits(),
 	}
 }
 
+// WithCredits makes the worker settle the job's reservation (ref = job
+// id): commit on success, release when the job fails for good or was
+// canceled while queued.
+func (w *RenderWorker) WithCredits(spender credit.Spender) *RenderWorker {
+	w.credits.spender = spender
+	return w
+}
+
+// jobCanceled reports whether the job was canceled before this attempt
+// started. A status that cannot be read does not stop the job (behavior
+// before the check existed).
+func (w *RenderWorker) jobCanceled(ctx context.Context, jobID string) bool {
+	if w.renderService == nil {
+		return false
+	}
+	status, err := w.renderService.JobStatus(ctx, jobID)
+	if err != nil {
+		log.Printf("Job %s: could not read the job status, continuing: %v", jobID, err)
+		return false
+	}
+	return status == model.JobStatusCanceled
+}
+
 // ProcessTask handles render task processing
 func (w *RenderWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
 	var taskPayload struct {
@@ -88,8 +128,22 @@ func (w *RenderWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
 	jobID := taskPayload.JobID
 	log.Printf("Starting render job: %s", jobID)
 
+	// A job canceled while queued must not reach Suno; its credit goes back.
+	if w.jobCanceled(ctx, jobID) {
+		log.Printf("Render job %s was canceled before it started; Suno is not called", jobID)
+		w.credits.release(ctx, jobID, credit.ReasonCanceled)
+		return nil
+	}
+
+	err := w.process(ctx, jobID, taskPayload.Payload)
+	w.credits.settle(ctx, jobID, err)
+	return err
+}
+
+// process runs one attempt of the job.
+func (w *RenderWorker) process(ctx context.Context, jobID string, raw json.RawMessage) error {
 	var payload model.RenderJobPayload
-	if err := json.Unmarshal(taskPayload.Payload, &payload); err != nil {
+	if err := json.Unmarshal(raw, &payload); err != nil {
 		w.failJob(ctx, jobID, "Invalid payload")
 		return fmt.Errorf("failed to unmarshal render payload: %w", err)
 	}
````

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/service ./internal/handler ./internal/worker && go test -count=1 -race ./internal/worker`
Expected: `ok` for all; with `-v`: `TestRenderService_StartRenderReservesJobID`, `..._InsufficientCreditsCreateNoJob`, `..._EnqueueFailureReleases`, `..._CancelReleasesOnlyQueuedJobs`, `TestRenderOperation`, `TestRenderHandler_InsufficientCredits402`, `TestRenderWorker_CanceledJobSkipsSunoAndReleases`, `..._SuccessCommits`, `..._PaidFailureReleases`, `..._RetryableFailureReleasesOnLastAttempt`, `TestJobCredits_WithoutSpenderIsNoop` PASS (the Redis-backed ones SKIP without Redis). All existing render worker tests still pass.

- [ ] **Step 6: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 3 added empty-string literals only.

- [ ] **Step 7: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/internal/service core-service/internal/handler core-service/internal/worker core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): render kredisi ayrilir, worker sonuca gore kapatir

Kuyrukta iptal edilen is Suno cagrilmadan atlanir ve kredisi iade edilir.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Final master — reserve before enqueue, settle in the worker

Same pattern as render for `master_final` (3 credits); the preview (0 credits) never reserves.

**Files:**
- Create: `core-service/internal/service/master_credit_test.go`
- Create: `core-service/internal/handler/master_credit_test.go`
- Create: `core-service/internal/worker/master_credit_test.go`
- Modify: `core-service/internal/service/master_service.go`
- Modify: `core-service/internal/handler/master.go`
- Modify: `core-service/internal/worker/master_worker.go`

**Interfaces:**
- Consumes: `releaseCredits` (Task 10, service package), `jobCredits`, `newJobCredits` (Task 10, worker package), `insufficientCredits` (Task 8), `masterTestWorker`, `masterPayload`, `fakeAudio`, `testR2Cfg` (existing worker tests).
- Produces: `(*service.MasterService).WithCredits(spender credit.Spender) *MasterService`, `(*worker.MasterWorker).WithCredits(spender credit.Spender) *MasterWorker`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/service/master_credit_test.go`:

````go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
)

func finalRequest() *model.MasterFinalRequest {
	return &model.MasterFinalRequest{
		ProjectID: uuid.New().String(),
		StemURLs:  []string{client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", testR2Cfg())},
	}
}

func TestMasterService_StartFinalReservesJobID(t *testing.T) {
	rdb := liveRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	defer ac.Close()
	spender := &credit.FakeSpender{}
	s := NewMasterService(rdb, ac, nil, testR2Cfg()).WithCredits(spender)

	resp, err := s.StartFinal(context.Background(), "kullanici-a", finalRequest())
	if err != nil {
		t.Fatalf("StartFinal: %v", err)
	}
	want := credit.ReserveCall{UserID: "kullanici-a", Operation: credit.OpMasterFinal, Ref: resp.JobID}
	if len(spender.Reserved) != 1 || spender.Reserved[0] != want || len(spender.Released) != 0 {
		t.Fatalf("reserved = %+v released = %+v", spender.Reserved, spender.Released)
	}
}

// Redis is nil: a refused reservation must stop BEFORE the job record.
func TestMasterService_InsufficientCreditsCreateNoJob(t *testing.T) {
	short := &credit.InsufficientError{Required: 3, Balance: 0}
	s := NewMasterService(nil, nil, nil, testR2Cfg()).WithCredits(&credit.FakeSpender{ReserveErr: short})
	if _, err := s.StartFinal(context.Background(), "kullanici-a", finalRequest()); !errors.Is(err, short) {
		t.Fatalf("want the insufficient error, got %v", err)
	}
}

// A request rejected by the ownership check costs nothing.
func TestMasterService_ForeignStemReservesNothing(t *testing.T) {
	spender := &credit.FakeSpender{}
	s := NewMasterService(nil, nil, nil, testR2Cfg()).WithCredits(spender)
	req := finalRequest()
	req.StemURLs = []string{client.UnsignedURL("stems/kullanici-b/p1/s1.mp3", testR2Cfg())}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrKeyNotOwned) {
		t.Fatalf("want ErrKeyNotOwned, got %v", err)
	}
	if len(spender.Reserved) != 0 {
		t.Fatalf("reserved = %+v", spender.Reserved)
	}
}

func TestMasterService_EnqueueFailureReleases(t *testing.T) {
	rdb := liveRedis(t)
	dead := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond})
	defer dead.Close()
	spender := &credit.FakeSpender{}
	s := NewMasterService(rdb, dead, nil, testR2Cfg()).WithCredits(spender)
	if _, err := s.StartFinal(context.Background(), "kullanici-a", finalRequest()); err == nil {
		t.Fatal("StartFinal must fail when the queue is down")
	}
	if len(spender.Released) != 1 || spender.Released[0] != (credit.ReleaseCall{Ref: spender.Reserved[0].Ref, Reason: credit.ReasonEnqueueFailed}) {
		t.Fatalf("released = %+v", spender.Released)
	}
}

// The preview costs 0 credits and never reserves.
func TestMasterService_PreviewReservesNothing(t *testing.T) {
	spender := &credit.FakeSpender{}
	s := NewMasterService(nil, nil, nil, testR2Cfg()).WithCredits(spender)
	if _, err := s.Preview(context.Background(), "kullanici-a", &model.MasterPreviewRequest{}); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(spender.Reserved) != 0 {
		t.Fatalf("reserved = %+v", spender.Reserved)
	}
}
````

**Create** `core-service/internal/handler/master_credit_test.go`:

````go
package handler

import (
	"net/http"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/service"
)

// Redis is nil: the final master's refused reservation answers 402.
func TestMasterHandler_InsufficientCredits402(t *testing.T) {
	cfg := &config.R2Config{AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
		PublicURL: "https://makesinger-cdn.celalettindemir.dev"}
	spender := &credit.FakeSpender{ReserveErr: &credit.InsufficientError{Required: 3, Balance: 0}}
	h := NewMasterHandler(service.NewMasterService(nil, nil, nil, cfg).WithCredits(spender), validator.New())
	app := fiber.New()
	app.Post("/final", asUser(creditTestUser), h.Final)
	stem := client.UnsignedURL("stems/"+creditTestUser+"/p1/s1.mp3", cfg)
	body := `{"projectId":"7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a","profile":"clean","stemUrls":["` + stem + `"],
		"mixSnapshot":{"channels":[{"stemId":"s1","volumeDb":0}],"preset":"default"}}`
	status, resp := doSocialRequest(t, app, http.MethodPost, "/final", body)
	if status != http.StatusPaymentRequired || errorCode(resp) != "kredi_yetersiz" {
		t.Fatalf("%d %v", status, resp)
	}
}
````

**Create** `core-service/internal/worker/master_credit_test.go`:

````go
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/websocket"
)

// okAudio masters successfully (the output is written by the real service).
type okAudio struct{ fakeAudio }

func (a *okAudio) Master(context.Context, *client.MasterRequest) (*client.MasterResponse, error) {
	a.masterCalls++
	return &client.MasterResponse{Duration: 180, PeakDb: -0.3, LUFS: -14}, nil
}

func masterCreditWorker(audio client.AudioProcessor, spender *credit.FakeSpender, lastAttempt bool) *MasterWorker {
	w := masterTestWorker(&fakeAudio{}, nil).WithCredits(spender)
	w.audioClient = audio
	w.hub = websocket.NewHub()
	w.credits.isLastAttempt = func(context.Context) bool { return lastAttempt }
	return w
}

func masterTask(t *testing.T, stemURL string) *asynq.Task {
	t.Helper()
	p := masterPayload()
	if stemURL != "" {
		p.StemURLs = []string{stemURL}
	}
	raw, _ := json.Marshal(p)
	body, _ := json.Marshal(map[string]any{"jobId": "m1", "payload": json.RawMessage(raw)})
	return asynq.NewTask("master:process", body)
}

func TestMasterWorker_SuccessCommits(t *testing.T) {
	spender := &credit.FakeSpender{}
	if err := masterCreditWorker(&okAudio{}, spender, false).ProcessTask(context.Background(), masterTask(t, "")); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(spender.Committed) != 1 || spender.Committed[0] != "m1" || len(spender.Released) != 0 {
		t.Fatalf("committed = %v released = %v", spender.Committed, spender.Released)
	}
}

func TestMasterWorker_FailureReleasesOnLastAttempt(t *testing.T) {
	for _, last := range []bool{false, true} {
		spender := &credit.FakeSpender{}
		audio := &fakeAudio{err: errors.New("audio down")}
		if err := masterCreditWorker(audio, spender, last).ProcessTask(context.Background(), masterTask(t, "")); err == nil {
			t.Fatalf("last=%v: want an error", last)
		}
		want := 0
		if last {
			want = 1
		}
		if len(spender.Released) != want || len(spender.Committed) != 0 {
			t.Fatalf("last=%v: released = %+v committed = %v", last, spender.Released, spender.Committed)
		}
	}
}

// A foreign stem is SkipRetry: the job is over on the first attempt.
func TestMasterWorker_SkipRetryReleases(t *testing.T) {
	spender := &credit.FakeSpender{}
	foreign := client.UnsignedURL("stems/kullanici-b/p1/s1.mp3", testR2Cfg())
	err := masterCreditWorker(&okAudio{}, spender, false).ProcessTask(context.Background(), masterTask(t, foreign))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got %v", err)
	}
	if len(spender.Released) != 1 || spender.Released[0] != (credit.ReleaseCall{Ref: "m1", Reason: credit.ReasonJobFailed}) {
		t.Fatalf("released = %+v", spender.Released)
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/service ./internal/handler ./internal/worker`
Expected: build failures, `NewMasterService(...).WithCredits undefined`, `masterTestWorker(...).WithCredits undefined`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/service/master_service.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/service/master_service.go b/core-service/internal/service/master_service.go
index 4b60fc1..267c658 100644
--- a/core-service/internal/service/master_service.go
+++ b/core-service/internal/service/master_service.go
@@ -10,6 +10,7 @@ import (
 	"github.com/hibiken/asynq"
 	"github.com/makeasinger/api/internal/client"
 	"github.com/makeasinger/api/internal/config"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/redis/go-redis/v9"
 )
@@ -20,6 +21,7 @@ type MasterService struct {
 	asynqClient *asynq.Client
 	r2Client    client.StorageClient
 	r2Cfg       *config.R2Config
+	credits     credit.Spender // nil: no credit accounting
 }
 
 func NewMasterService(redisClient *redis.Client, asynqClient *asynq.Client, r2Client client.StorageClient, r2Cfg *config.R2Config) *MasterService {
@@ -31,6 +33,14 @@ func NewMasterService(redisClient *redis.Client, asynqClient *asynq.Client, r2Cl
 	}
 }
 
+// WithCredits makes StartFinal reserve the final master's price (ref = job
+// id) before the job is queued. The preview costs nothing and reserves
+// nothing; the worker settles the final job's outcome.
+func (s *MasterService) WithCredits(spender credit.Spender) *MasterService {
+	s.credits = spender
+	return s
+}
+
 // Preview generates a 20-second master preview
 func (s *MasterService) Preview(ctx context.Context, userID string, req *model.MasterPreviewRequest) (*model.MasterPreviewResponse, error) {
 	// TODO: Implement actual preview generation
@@ -71,6 +81,14 @@ func (s *MasterService) StartFinal(ctx context.Context, userID string, req *mode
 	jobID := uuid.New().String()
 	now := time.Now()
 
+	// Credit is held BEFORE the job exists (after the input checks, so a
+	// rejected request costs nothing).
+	if s.credits != nil {
+		if _, err := s.credits.Reserve(ctx, userID, credit.OpMasterFinal, jobID); err != nil {
+			return nil, err
+		}
+	}
+
 	// Create job record
 	job := &model.Job{
 		ID:        jobID,
@@ -100,12 +118,14 @@ func (s *MasterService) StartFinal(ctx context.Context, userID string, req *mode
 
 	// Save job to Redis
 	if err := s.saveJob(ctx, job); err != nil {
+		releaseCredits(ctx, s.credits, jobID, credit.ReasonEnqueueFailed)
 		return nil, fmt.Errorf("failed to save job: %w", err)
 	}
 
 	// Create Asynq task
 	task, err := newMasterTask(jobID, payloadBytes)
 	if err != nil {
+		releaseCredits(ctx, s.credits, jobID, credit.ReasonEnqueueFailed)
 		return nil, fmt.Errorf("failed to create task: %w", err)
 	}
 
@@ -116,6 +136,7 @@ func (s *MasterService) StartFinal(ctx context.Context, userID string, req *mode
 		asynq.Retention(24*time.Hour),
 	)
 	if err != nil {
+		releaseCredits(ctx, s.credits, jobID, credit.ReasonEnqueueFailed)
 		return nil, fmt.Errorf("failed to enqueue task: %w", err)
 	}
 
````

**Modify** `core-service/internal/handler/master.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/handler/master.go b/core-service/internal/handler/master.go
index 5ede30b..23f46bd 100644
--- a/core-service/internal/handler/master.go
+++ b/core-service/internal/handler/master.go
@@ -6,6 +6,7 @@ import (
 	"github.com/go-playground/validator/v10"
 	"github.com/gofiber/fiber/v2"
 	"github.com/makeasinger/api/internal/client"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -71,6 +72,7 @@ func (h *MasterHandler) Preview(c *fiber.Ctx) error {
 // @Success      202 {object} model.MasterFinalResponse
 // @Failure      400 {object} response.ErrorResponse
 // @Failure      401 {object} response.ErrorResponse
+// @Failure      402 {object} response.ErrorResponse
 // @Failure      404 {object} response.ErrorResponse
 // @Failure      429 {object} response.ErrorResponse
 // @Failure      500 {object} response.ErrorResponse
@@ -99,6 +101,9 @@ func (h *MasterHandler) Final(c *fiber.Ctx) error {
 		if errors.Is(err, client.ErrInvalidURL) {
 			return response.ValidationError(c, i18n.KeyInvalidFileURL, nil)
 		}
+		if e, ok := credit.AsInsufficient(err); ok {
+			return insufficientCredits(c, e)
+		}
 		return internalError(c, err)
 	}
 
````

**Modify** `core-service/internal/worker/master_worker.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/worker/master_worker.go b/core-service/internal/worker/master_worker.go
index e2bb8bd..6d6ec17 100644
--- a/core-service/internal/worker/master_worker.go
+++ b/core-service/internal/worker/master_worker.go
@@ -13,6 +13,7 @@ import (
 	"github.com/hibiken/asynq"
 	"github.com/makeasinger/api/internal/client"
 	"github.com/makeasinger/api/internal/config"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/deletionmark"
 	"github.com/makeasinger/api/internal/model"
 	"github.com/makeasinger/api/internal/service"
@@ -29,6 +30,7 @@ type MasterWorker struct {
 	hub             *websocket.Hub
 	r2Cfg           *config.R2Config
 	deletionChecker deletionmark.Checker // nil: no check
+	credits         jobCredits
 }
 
 // NewMasterWorker creates a new master worker
@@ -41,9 +43,17 @@ func NewMasterWorker(redisClient *redis.Client, audioClient client.AudioProcesso
 		hub:             hub,
 		r2Cfg:           r2Cfg,
 		deletionChecker: deletionChecker,
+		credits:         newJobCredits(),
 	}
 }
 
+// WithCredits makes the worker settle the final master's reservation (ref
+// = job id): commit on success, release when the job fails for good.
+func (w *MasterWorker) WithCredits(spender credit.Spender) *MasterWorker {
+	w.credits.spender = spender
+	return w
+}
+
 // ProcessTask handles mastering task processing
 func (w *MasterWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
 	var taskPayload struct {
@@ -58,8 +68,15 @@ func (w *MasterWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
 	jobID := taskPayload.JobID
 	log.Printf("Starting master job: %s", jobID)
 
+	err := w.process(ctx, jobID, taskPayload.Payload)
+	w.credits.settle(ctx, jobID, err)
+	return err
+}
+
+// process runs one attempt of the job.
+func (w *MasterWorker) process(ctx context.Context, jobID string, raw json.RawMessage) error {
 	var payload model.MasterJobPayload
-	if err := json.Unmarshal(taskPayload.Payload, &payload); err != nil {
+	if err := json.Unmarshal(raw, &payload); err != nil {
 		w.failJob(ctx, jobID, "Invalid payload")
 		return fmt.Errorf("failed to unmarshal master payload: %w", err)
 	}
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/service ./internal/handler ./internal/worker`
Expected: `ok` ×3; `TestMasterService_StartFinalReservesJobID`, `..._InsufficientCreditsCreateNoJob`, `..._ForeignStemReservesNothing`, `..._EnqueueFailureReleases`, `..._PreviewReservesNothing`, `TestMasterHandler_InsufficientCredits402`, `TestMasterWorker_SuccessCommits`, `..._FailureReleasesOnLastAttempt`, `..._SkipRetryReleases` PASS.

- [ ] **Step 5: Check wirecheck, lint, vet, commit**

Run: `go test ./tools/wirecheck && gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...`
Expected: wirecheck `ok` (no wire change), `0 issues.`

```bash
git add core-service/internal/service core-service/internal/handler core-service/internal/worker
git commit -m "feat(kredi): final master kredisi ayrilir ve worker'da kapanir

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Account deletion removes credit rows

Optional `CreditCleaner` next to `SocialCleaner` (R16).

**Files:**
- Create: `core-service/internal/service/account_service_credit_test.go`
- Modify: `core-service/internal/service/account_service.go`

**Interfaces:**
- Consumes: `fakeIdentity`, `fakeDeleter`, `fakeQueue`, `unreachableRedis`, `accountID` (existing account tests); `credit.UserDataCleaner` shape from Task 5 (`*credit.PostgresStore` satisfies the new interface).
- Produces: `type CreditCleaner interface{ DeleteUserData(ctx context.Context, userID string) error }`, `(*AccountService).WithCreditCleaner(credits CreditCleaner) *AccountService`.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/service/account_service_credit_test.go`:

````go
package service

import (
	"context"
	"errors"
	"testing"
)

type fakeCreditCleaner struct {
	deleted []string
	err     error
}

func (c *fakeCreditCleaner) DeleteUserData(_ context.Context, userID string) error {
	c.deleted = append(c.deleted, userID)
	return c.err
}

func TestAccountService_DeleteRemovesCreditData(t *testing.T) {
	credits := &fakeCreditCleaner{}
	deleter := &fakeDeleter{}
	queue := &fakeQueue{}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), queue, nil).WithCreditCleaner(credits)
	if err := svc.Delete(context.Background(), accountID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(credits.deleted) != 1 || credits.deleted[0] != accountID {
		t.Fatalf("credit rows deleted for %v", credits.deleted)
	}
	if len(queue.tasks) != 1 {
		t.Fatal("the delayed cleanup must still be scheduled")
	}
}

// A credit cleanup error is joined but does not stop the R2 cleanup or the
// delayed task, and Delete itself still succeeds.
func TestAccountService_CreditCleanupErrorDoesNotStopDeletion(t *testing.T) {
	credits := &fakeCreditCleaner{err: errors.New("pg is down")}
	deleter := &fakeDeleter{}
	queue := &fakeQueue{}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), queue, nil).WithCreditCleaner(credits)
	if err := svc.Cleanup(context.Background(), accountID); err == nil {
		t.Fatal("Cleanup must report the credit error")
	}
	if len(deleter.prefixes) == 0 {
		t.Fatal("R2 prefixes must still be deleted")
	}
	if err := svc.Delete(context.Background(), accountID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(queue.tasks) != 1 {
		t.Fatal("the delayed cleanup must still be scheduled")
	}
}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/service -run Credit`
Expected: build failure, `NewAccountService(...).WithCreditCleaner undefined`.

- [ ] **Step 3: Implement**

**Modify** `core-service/internal/service/account_service.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/internal/service/account_service.go b/core-service/internal/service/account_service.go
index 4e7eba9..8a86311 100644
--- a/core-service/internal/service/account_service.go
+++ b/core-service/internal/service/account_service.go
@@ -54,18 +54,32 @@ type SocialCleaner interface {
 	DeleteUserData(ctx context.Context, userID string) error
 }
 
+// CreditCleaner deletes the user's credit rows (lots, entries,
+// reservations, campaign claims) on account deletion (implemented by
+// *credit.PostgresStore). Campaign counters are not given back.
+type CreditCleaner interface {
+	DeleteUserData(ctx context.Context, userID string) error
+}
+
 type AccountService struct {
 	identity      AccountDeleter
 	prefixDeleter client.PrefixDeleter // nil: R2 not configured
 	redis         *redis.Client
 	queue         Enqueuer
 	social        SocialCleaner // nil: no social module
+	credits       CreditCleaner // nil: no credit module
 }
 
 func NewAccountService(accountDeleter AccountDeleter, prefixDeleter client.PrefixDeleter, rdb *redis.Client, queue Enqueuer, social SocialCleaner) *AccountService {
 	return &AccountService{identity: accountDeleter, prefixDeleter: prefixDeleter, redis: rdb, queue: queue, social: social}
 }
 
+// WithCreditCleaner makes the cleanup also delete the user's credit rows.
+func (s *AccountService) WithCreditCleaner(credits CreditCleaner) *AccountService {
+	s.credits = credits
+	return s
+}
+
 // Delete deletes the account and all its files. Once the identity record is
 // gone the user cannot repeat the request (their token is rejected from then
 // on), so errors of the following steps are logged but do not fail the request.
@@ -103,8 +117,10 @@ func (s *AccountService) Delete(ctx context.Context, userID string) error {
 	return nil
 }
 
-// Cleanup deletes all of the user's R2 prefixes and Discover data. Without R2
-// the prefixes are skipped. If one prefix fails the others are still tried.
+// Cleanup deletes all of the user's R2 prefixes, Discover data and credit
+// rows. Without R2 the prefixes are skipped. If one step fails the others
+// are still tried; the delayed task runs Cleanup again (every step is
+// idempotent).
 func (s *AccountService) Cleanup(ctx context.Context, userID string) error {
 	prefixes, err := client.UserPrefixes(userID)
 	if err != nil {
@@ -123,6 +139,11 @@ func (s *AccountService) Cleanup(ctx context.Context, userID string) error {
 			errs = append(errs, err)
 		}
 	}
+	if s.credits != nil {
+		if err := s.credits.DeleteUserData(ctx, userID); err != nil {
+			errs = append(errs, fmt.Errorf("could not delete credit data: %w", err))
+		}
+	}
 	return errors.Join(errs...)
 }
 
````

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/service ./internal/worker`
Expected: `ok` ×2 (the account worker uses `Cleanup` too).

- [ ] **Step 5: Check wirecheck, lint, vet, commit**

Run: `go test ./tools/wirecheck && gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → wirecheck `ok`, `0 issues.`

```bash
git add core-service/internal/service
git commit -m "feat(kredi): hesap silme kredi satirlarini da siler

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Credit pool, service and the `kredi:supur` sweep in `main`

Opens the credit pool, runs the migration (fatal on failure), builds `credit.Service` with `identityServer` as `UserCreatedAt`, adds the sweep worker and generalizes the scheduler (R14, R19).

**Files:**
- Create: `core-service/internal/worker/credit_worker_test.go`
- Create: `core-service/internal/credit/tasks.go`
- Create: `core-service/internal/worker/credit_worker.go`
- Modify: `core-service/cmd/server/main_test.go`
- Modify: `core-service/cmd/server/main.go`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: `credit.Migrate`, `credit.NewPostgresStore`, `credit.NewService`, `credit.CostsFromConfig`, `credit.SweepResult` (Tasks 1–6), `(*identity.Server).UserCreatedAt` (Task 7), `cfg.Credits` (Task 2).
- Produces: `credit.TaskTypeSweep = "kredi:supur"`, `credit.QueueSweep = "bakim"`; `worker.CreditSweeper` interface, `worker.NewCreditWorker(CreditSweeper) *CreditWorker`, `(*CreditWorker).Sweep(ctx, *asynq.Task) error`; in `main`: `creditStore`, `creditService` variables (Task 14 uses them), `periodicTask{taskType, queue string; interval time.Duration}`, `periodicTasks(cfg *config.Config) []periodicTask`, `startScheduler(cfg *config.Config) (*asynq.Scheduler, error)` replacing `startStaleSweepScheduler`; `startWorkerServer` gains a `creditWorker *worker.CreditWorker` parameter.

- [ ] **Step 1: Write the failing tests**

**Create** `core-service/internal/worker/credit_worker_test.go`:

````go
package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/makeasinger/api/internal/credit"
)

type fakeCreditSweeper struct {
	result credit.SweepResult
	err    error
	calls  int
}

func (f *fakeCreditSweeper) Sweep(context.Context) (credit.SweepResult, error) {
	f.calls++
	return f.result, f.err
}

func TestCreditWorker_Sweep(t *testing.T) {
	s := &fakeCreditSweeper{result: credit.SweepResult{ReleasedReservations: 1, ExpiredLots: 2}}
	if err := NewCreditWorker(s).Sweep(context.Background(), nil); err != nil || s.calls != 1 {
		t.Fatalf("Sweep = %v, calls %d", err, s.calls)
	}
	failing := &fakeCreditSweeper{err: errors.New("pg is down")}
	if err := NewCreditWorker(failing).Sweep(context.Background(), nil); err == nil {
		t.Fatal("a sweep error must be returned")
	}
}
````

**Modify** `core-service/cmd/server/main_test.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/cmd/server/main_test.go b/core-service/cmd/server/main_test.go
index 4127fec..dd9b3d3 100644
--- a/core-service/cmd/server/main_test.go
+++ b/core-service/cmd/server/main_test.go
@@ -8,8 +8,10 @@ import (
 	"net/http"
 	"net/http/httptest"
 	"testing"
+	"time"
 
 	"github.com/gofiber/fiber/v2"
+	"github.com/makeasinger/api/internal/config"
 	"github.com/makeasinger/api/internal/i18n"
 	"github.com/makeasinger/api/internal/social"
 )
@@ -68,3 +70,27 @@ func TestCustomErrorHandler(t *testing.T) {
 		}
 	}
 }
+
+func TestPeriodicTasks(t *testing.T) {
+	cfg := &config.Config{
+		Social:  config.SocialConfig{StaleSweepInterval: 15 * time.Minute},
+		Credits: config.CreditConfig{SweepInterval: time.Hour},
+	}
+	got := periodicTasks(cfg)
+	want := []periodicTask{
+		{"sosyal:bayat-tara", "sosyal", 15 * time.Minute},
+		{"kredi:supur", "bakim", time.Hour},
+	}
+	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
+		t.Fatalf("periodicTasks = %+v, want %+v", got, want)
+	}
+
+	cfg.Social.StaleSweepInterval = 0
+	if got := periodicTasks(cfg); len(got) != 1 || got[0].taskType != "kredi:supur" {
+		t.Fatalf("social disabled: %+v", got)
+	}
+	cfg.Credits.SweepInterval = -time.Second
+	if got := periodicTasks(cfg); len(got) != 0 {
+		t.Fatalf("both disabled: %+v", got)
+	}
+}
````

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/worker ./cmd/server`
Expected: build failures, `undefined: NewCreditWorker`, `undefined: periodicTasks`, `undefined: periodicTask`.

- [ ] **Step 3: Implement the task constant and the worker**

**Create** `core-service/internal/credit/tasks.go`:

````go
package credit

// TaskTypeSweep is the periodic sweep: release reservations held for more
// than 3 hours, expire lots, stop finished campaigns. It runs on the bakim
// queue from the asynq scheduler in cmd/server.
const TaskTypeSweep = "kredi:supur"

// QueueSweep is the asynq queue of TaskTypeSweep.
const QueueSweep = "bakim"
````

**Create** `core-service/internal/worker/credit_worker.go`:

````go
package worker

import (
	"context"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
)

// CreditSweeper runs one credit sweep (implemented by *credit.Service).
type CreditSweeper interface {
	Sweep(ctx context.Context) (credit.SweepResult, error)
}

// CreditWorker handles kredi:supur.
type CreditWorker struct {
	sweeper CreditSweeper
}

func NewCreditWorker(sweeper CreditSweeper) *CreditWorker {
	return &CreditWorker{sweeper: sweeper}
}

// Sweep runs the credit sweep and logs what changed. The task is scheduled
// with MaxRetry(0): a failed run is simply redone at the next tick.
func (w *CreditWorker) Sweep(ctx context.Context, _ *asynq.Task) error {
	result, err := w.sweeper.Sweep(ctx)
	if result != (credit.SweepResult{}) {
		log.Printf("credit sweep: released %d stale reservation(s), expired %d lot(s), stopped %d campaign(s)",
			result.ReleasedReservations, result.ExpiredLots, result.StoppedCampaigns)
	}
	if err != nil {
		return fmt.Errorf("credit sweep: %w", err)
	}
	return nil
}
````

- [ ] **Step 4: Wire the pool, service, worker and scheduler**

**Modify** `core-service/cmd/server/main.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
index 48ea5c3..f214c07 100644
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -23,6 +23,7 @@ import (
 	"github.com/makeasinger/api/docs"
 	"github.com/makeasinger/api/internal/client"
 	"github.com/makeasinger/api/internal/config"
+	"github.com/makeasinger/api/internal/credit"
 	"github.com/makeasinger/api/internal/deletionmark"
 	"github.com/makeasinger/api/internal/handler"
 	"github.com/makeasinger/api/internal/i18n"
@@ -110,6 +111,23 @@ func main() {
 	}
 	socialStore := social.NewPostgresStore(socialPool)
 
+	// Credits: same database, its own pool, schema (kredi) and migration
+	// table. FATAL like social: without the credit tables nothing can be
+	// reserved. *identity.Server supplies the sign-up time for campaigns.
+	creditPool, err := identity.Connect(ctx, cfg.Auth.DBURL)
+	if err != nil {
+		log.Fatalf("Could not connect to the credit database: %v", err)
+	}
+	defer creditPool.Close()
+	if err := credit.Migrate(ctx, creditPool); err != nil {
+		log.Fatalf("Credit migration failed: %v", err)
+	}
+	creditStore := credit.NewPostgresStore(creditPool)
+	creditService := credit.NewService(creditStore, identityServer, credit.CostsFromConfig(&cfg.Credits), cfg.Credits.Enforce)
+	if !cfg.Credits.Enforce {
+		log.Println("Credits in shadow mode (CREDITS_ENFORCE=false): spending is recorded, never blocked")
+	}
+
 	// Initialize Asynq client
 	asynqClient := asynq.NewClient(asynq.RedisClientOpt{
 		Addr:     cfg.Redis.Addr,
@@ -337,7 +355,8 @@ func main() {
 	}))
 
 	// Start Asynq worker server
-	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub, accountService, socialWorker)
+	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub, accountService, socialWorker,
+		worker.NewCreditWorker(creditService))
 
 	// Graceful shutdown
 	quit := make(chan os.Signal, 1)
@@ -370,6 +389,7 @@ func startWorkerServer(
 	hub *ws.Hub,
 	accountService *service.AccountService,
 	socialWorker *worker.SocialWorker,
+	creditWorker *worker.CreditWorker,
 ) {
 	asynqLogLevel := asynq.InfoLevel
 	if strings.EqualFold(cfg.Server.LogLevel, "debug") {
@@ -412,10 +432,11 @@ func startWorkerServer(
 	mux.HandleFunc(social.TaskTypePrepare, socialWorker.Prepare)
 	mux.HandleFunc(social.TaskTypeDeleteFiles, socialWorker.DeleteFiles)
 	mux.HandleFunc(social.TaskTypeSweepStale, socialWorker.SweepStale)
+	mux.HandleFunc(credit.TaskTypeSweep, creditWorker.Sweep)
 
-	scheduler, err := startStaleSweepScheduler(cfg)
+	scheduler, err := startScheduler(cfg)
 	if err != nil {
-		log.Printf("Stale sweep scheduler could not start: %v", err)
+		log.Printf("Periodic task scheduler could not start: %v", err)
 	}
 	if scheduler != nil {
 		defer scheduler.Shutdown()
@@ -426,16 +447,39 @@ func startWorkerServer(
 	}
 }
 
-// startStaleSweepScheduler starts the periodic sosyal:bayat-tara schedule.
-// Every API replica runs one; Unique keeps them from piling up duplicate
-// jobs (the sweep is idempotent, so an occasional duplicate is harmless).
-// The lock is a little shorter than the interval so a replica's own next tick
-// is never swallowed by its previous one. It returns nil when the interval is
-// <= 0 (disabled).
-func startStaleSweepScheduler(cfg *config.Config) (*asynq.Scheduler, error) {
-	interval := cfg.Social.StaleSweepInterval
-	if interval <= 0 {
-		log.Println("Stale sweep scheduler disabled (social stale_sweep_interval <= 0)")
+// periodicTask is one entry of the asynq scheduler.
+type periodicTask struct {
+	taskType string
+	queue    string
+	interval time.Duration
+}
+
+// periodicTasks lists the enabled periodic tasks: sosyal:bayat-tara every
+// SOCIAL_STALE_SWEEP_INTERVAL and kredi:supur every CREDITS_SWEEP_INTERVAL.
+// An interval <= 0 disables its task.
+func periodicTasks(cfg *config.Config) []periodicTask {
+	var tasks []periodicTask
+	if cfg.Social.StaleSweepInterval > 0 {
+		tasks = append(tasks, periodicTask{social.TaskTypeSweepStale, social.QueueSocial, cfg.Social.StaleSweepInterval})
+	} else {
+		log.Println("Stale sweep disabled (social stale_sweep_interval <= 0)")
+	}
+	if cfg.Credits.SweepInterval > 0 {
+		tasks = append(tasks, periodicTask{credit.TaskTypeSweep, credit.QueueSweep, cfg.Credits.SweepInterval})
+	} else {
+		log.Println("Credit sweep disabled (credits sweep_interval <= 0)")
+	}
+	return tasks
+}
+
+// startScheduler starts the periodic tasks. Every API replica runs one
+// scheduler; Unique keeps them from piling up duplicate jobs (the sweeps are
+// idempotent, so an occasional duplicate is harmless). The lock is a little
+// shorter than the interval so a replica's own next tick is never swallowed
+// by its previous one. It returns nil when every task is disabled.
+func startScheduler(cfg *config.Config) (*asynq.Scheduler, error) {
+	tasks := periodicTasks(cfg)
+	if len(tasks) == 0 {
 		return nil, nil
 	}
 	scheduler := asynq.NewScheduler(asynq.RedisClientOpt{
@@ -443,14 +487,16 @@ func startStaleSweepScheduler(cfg *config.Config) (*asynq.Scheduler, error) {
 		Password: cfg.Redis.Password,
 		DB:       cfg.Redis.DB,
 	}, nil)
-	if _, err := scheduler.Register(
-		"@every "+interval.String(),
-		asynq.NewTask(social.TaskTypeSweepStale, nil),
-		asynq.Queue(social.QueueSocial),
-		asynq.MaxRetry(0),
-		asynq.Unique(interval-interval/10),
-	); err != nil {
-		return nil, err
+	for _, p := range tasks {
+		if _, err := scheduler.Register(
+			"@every "+p.interval.String(),
+			asynq.NewTask(p.taskType, nil),
+			asynq.Queue(p.queue),
+			asynq.MaxRetry(0),
+			asynq.Unique(p.interval-p.interval/10),
+		); err != nil {
+			return nil, err
+		}
 	}
 	if err := scheduler.Start(); err != nil {
 		return nil, err
````

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test -count=1 ./cmd/server ./internal/worker`
Expected: `ok` ×2 (`TestPeriodicTasks`, `TestCreditWorker_Sweep` pass).

- [ ] **Step 6: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 2 added lines, `STR "bakim"` and `STR "kredi:supur"`.

- [ ] **Step 7: Lint, vet, commit**

Run: `gofmt -l . && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`

```bash
git add core-service/cmd core-service/internal/credit core-service/internal/worker core-service/tools/wirecheck/testdata/wire.txt
git commit -m "feat(kredi): kredi havuzu ve saatlik kredi:supur gorevi

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Routes, credit wiring, deploy env and docs

Mounts `/api/credits` and `/api/admin` with their limits (R13), attaches `credit.Spender` to render, master, lyrics and both workers, the credit cleaner to account deletion, passes the new env in `docker-stack.yml`, documents the module in `CLAUDE.md`.

**Files:**
- Modify: `core-service/cmd/server/main.go`
- Modify: `docker-stack.yml`
- Modify: `CLAUDE.md`
- Regenerate: `core-service/tools/wirecheck/testdata/wire.txt`

**Interfaces:**
- Consumes: everything above; `creditService`, `creditStore` (Task 13); `startWorkerServer` gains `credits credit.Spender`.
- Produces: the HTTP surface in Global Constraints.

- [ ] **Step 1: Wire everything in `main`**

**Modify** `core-service/cmd/server/main.go` (unified diff; `git apply` from the repository root):

````diff
diff --git a/core-service/cmd/server/main.go b/core-service/cmd/server/main.go
index f214c07..59378d7 100644
--- a/core-service/cmd/server/main.go
+++ b/core-service/cmd/server/main.go
@@ -171,8 +171,9 @@ func main() {
 
 	// Initialize services
 	lyricsService := service.NewLyricsService(groqClient)
-	renderService := service.NewRenderService(redisClient, asynqClient, storage, &cfg.R2)
-	masterService := service.NewMasterService(redisClient, asynqClient, storage, &cfg.R2)
+	// Render and final master reserve credit (ref = job id) before queueing.
+	renderService := service.NewRenderService(redisClient, asynqClient, storage, &cfg.R2).WithCredits(creditService)
+	masterService := service.NewMasterService(redisClient, asynqClient, storage, &cfg.R2).WithCredits(creditService)
 	exportService := service.NewExportService(storage, audioClient, &cfg.R2)
 	uploadService := service.NewUploadService(storage, &cfg.R2)
 	fileService := service.NewFileService(storage, &cfg.R2)
@@ -186,7 +187,8 @@ func main() {
 	}
 
 	// Account deletion: the user record is kept in our own OP.
-	accountService := service.NewAccountService(identityServer, prefixDeleter, redisClient, asynqClient, socialStore)
+	accountService := service.NewAccountService(identityServer, prefixDeleter, redisClient, asynqClient, socialStore).
+		WithCreditCleaner(creditStore)
 	accountHandler := handler.NewAccountHandler(accountService)
 
 	// copier: interface variable; assigning r2Client directly while it is
@@ -201,7 +203,8 @@ func main() {
 		WithStaleSweep(socialStore, cfg.Social.StalePreparationAfter)
 
 	// Initialize handlers
-	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
+	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate).WithCredits(creditService)
+	creditHandler := handler.NewCreditHandler(creditService)
 	renderHandler := handler.NewRenderHandler(renderService, validate)
 	masterHandler := handler.NewMasterHandler(masterService, validate)
 	exportHandler := handler.NewExportHandler(exportService, validate)
@@ -331,6 +334,15 @@ func main() {
 
 	api.Delete("/account", accountHandler.Delete)
 
+	// Credits: balance (claims the active campaign's welcome gift) and
+	// history share one per-user counter.
+	creditHandler.Routes(api.Group("/credits", rateLimiter.Limit("kredi", cfg.RateLimit.CreditsPerMin, time.Minute)))
+
+	// Admin (gift campaigns): only ADMIN_USER_IDS pass; an empty list closes
+	// it. The gate runs first, so non-admins do not touch the counter.
+	creditHandler.AdminRoutes(api.Group("/admin", middleware.AdminGate(cfg.Credits.AdminUserIDs),
+		rateLimiter.Limit("admin", cfg.RateLimit.AdminPerMin, time.Minute)))
+
 	// Discover (social) endpoints. Limits apply only to the relevant
 	// endpoints; by contract the comment limit returns 429 "yavasla".
 	socialHandler.Routes(api.Group("/social"), handler.SocialLimits{
@@ -356,7 +368,7 @@ func main() {
 
 	// Start Asynq worker server
 	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub, accountService, socialWorker,
-		worker.NewCreditWorker(creditService))
+		worker.NewCreditWorker(creditService), creditService)
 
 	// Graceful shutdown
 	quit := make(chan os.Signal, 1)
@@ -390,6 +402,7 @@ func startWorkerServer(
 	accountService *service.AccountService,
 	socialWorker *worker.SocialWorker,
 	creditWorker *worker.CreditWorker,
+	credits credit.Spender,
 ) {
 	asynqLogLevel := asynq.InfoLevel
 	if strings.EqualFold(cfg.Server.LogLevel, "debug") {
@@ -422,8 +435,10 @@ func startWorkerServer(
 	// Workers check the job owner's deleted mark before the paid Suno call
 	// and before every R2 write.
 	deletionChecker := deletionmark.NewRedisChecker(redisClient)
-	renderWorker := worker.NewRenderWorker(renderService, sunoClient, storage, hub, deletionChecker)
-	masterWorker := worker.NewMasterWorker(redisClient, audioClient, storage, masterService, hub, &cfg.R2, deletionChecker)
+	// Workers commit the job's credit on success and release it when the job
+	// fails for good (or was canceled while queued).
+	renderWorker := worker.NewRenderWorker(renderService, sunoClient, storage, hub, deletionChecker).WithCredits(credits)
+	masterWorker := worker.NewMasterWorker(redisClient, audioClient, storage, masterService, hub, &cfg.R2, deletionChecker).WithCredits(credits)
 
 	mux := asynq.NewServeMux()
 	mux.HandleFunc(service.TaskTypeRender, renderWorker.ProcessTask)
````

- [ ] **Step 2: Deploy env**

**Modify** `docker-stack.yml` (unified diff; `git apply` from the repository root):

````diff
diff --git a/docker-stack.yml b/docker-stack.yml
index 68ab0a6..386daeb 100644
--- a/docker-stack.yml
+++ b/docker-stack.yml
@@ -59,6 +59,10 @@ services:
       # Kesfet web onizlemesi: make-singer-frontend'in X-Web-Anahtari degeri
       # (iki stack'te ayni). Bos ise web sunucusu da IP basina 30/dk sinirda.
       SOSYAL_WEB_ANAHTARI: ${SOSYAL_WEB_ANAHTARI:-}
+      # Kredi sistemi: false = golge mod (harcama kaydedilir, engellenmez).
+      CREDITS_ENFORCE: ${CREDITS_ENFORCE:-false}
+      # /api/admin (hediye kampanyalari) icin virgulle ayrilmis kullanici UUID'leri; bos = kapali.
+      ADMIN_USER_IDS: ${ADMIN_USER_IDS:-}
     secrets:
       - groq_api_key
       - r2_account_id
````

- [ ] **Step 3: Document the module**

**Modify** `CLAUDE.md` (unified diff; `git apply` from the repository root):

````diff
diff --git a/CLAUDE.md b/CLAUDE.md
index 4ef72d7..9e10ec3 100644
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -6,7 +6,7 @@ This file provides guidance to Claude Code (claude.ai/code) when working with co
 
 Monorepo with two services plus reverse proxy configuration:
 
-- **core-service/** — Go API backend (Fiber v2 + Asynq workers). Includes `internal/identity/` for custom OpenID Provider and `internal/social/` for Keşfet (social).
+- **core-service/** — Go API backend (Fiber v2 + Asynq workers). Includes `internal/identity/` for custom OpenID Provider, `internal/social/` for Keşfet (social) and `internal/credit/` for the credit ledger.
 - **audio-service/** — Python audio processing (FFmpeg-based mastering, Keşfet waveform and preview via `/analiz`)
 - **traefik/** — Reverse proxy configuration
 
@@ -51,13 +51,14 @@ HTTP Request → Fiber middleware (auth, rate-limit) → Handler → Service →
 
 ### Key layers (`core-service/internal/`)
 
-- **handler/** — Fiber route handlers. One file per domain: `lyrics`, `render`, `master`, `export`, `upload`, `account` (`DELETE /api/account`), `files` (`POST /api/files/sign`), `social*` (`/api/social/*`, routes bound by `SocialHandler.Routes`).
+- **handler/** — Fiber route handlers. One file per domain: `lyrics`, `render`, `master`, `export`, `upload`, `account` (`DELETE /api/account`), `files` (`POST /api/files/sign`), `social*` (`/api/social/*`, routes bound by `SocialHandler.Routes`), `credit` (`GET /api/credits`, `GET /api/credits/history`, admin `GET|POST /api/admin/campaigns`, `POST /api/admin/campaigns/:id/stop`; `CreditHandler.Routes` / `AdminRoutes`; `insufficientCredits` writes 402 `kredi_yetersiz` with `details {required, balance}` for lyrics, render start and final master).
 - **service/** — Business logic. Mirrors handler structure. Services enqueue Asynq jobs for render/master operations.
-- **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate`, then by stemMode: none / `split_stem_advanced` / `split_stem`; stems downloaded and uploaded to R2 as `stems/{userId}/{projectId}/{stemId}.mp3` via `client.UserKey`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline), `account_worker.go` (delayed R2 cleanup `hesap:temizle` after account deletion), `social_worker.go` (`sosyal:hazirla`: copies a publication's master and stems to `sosyal/{yayinId}/`, asks audio-service `/analiz` for 64 peaks and a 30 s preview, sets `yayinda`; `sosyal:dosya-sil`: deletes `sosyal/{yayinId}/` 24 h after a publication is deleted; `sosyal:bayat-tara`: fails publications stuck in `hazirlaniyor` for longer than `SOCIAL_STALE_PREPARATION_AFTER` (default 1h, measured from the `hazirlik_baslangic` column that `ResetForRetry` refreshes) so the owner can retry; scheduled by an `asynq.Scheduler` started next to the worker server in `cmd/server/main.go` every `SOCIAL_STALE_SWEEP_INTERVAL` (default 15m, `<= 0` disables) on the `sosyal` queue with `MaxRetry(0)` and `Unique`, so API replicas do not pile up duplicates). Render and master workers refuse paid calls and R2 writes for deleted owners (`deletionmark.Checker`).
+- **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate`, then by stemMode: none / `split_stem_advanced` / `split_stem`; stems downloaded and uploaded to R2 as `stems/{userId}/{projectId}/{stemId}.mp3` via `client.UserKey`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline), `account_worker.go` (delayed R2 cleanup `hesap:temizle` after account deletion), `social_worker.go` (`sosyal:hazirla`: copies a publication's master and stems to `sosyal/{yayinId}/`, asks audio-service `/analiz` for 64 peaks and a 30 s preview, sets `yayinda`; `sosyal:dosya-sil`: deletes `sosyal/{yayinId}/` 24 h after a publication is deleted; `sosyal:bayat-tara`: fails publications stuck in `hazirlaniyor` for longer than `SOCIAL_STALE_PREPARATION_AFTER` (default 1h, measured from the `hazirlik_baslangic` column that `ResetForRetry` refreshes) so the owner can retry; scheduled by an `asynq.Scheduler` started next to the worker server in `cmd/server/main.go` every `SOCIAL_STALE_SWEEP_INTERVAL` (default 15m, `<= 0` disables) on the `sosyal` queue with `MaxRetry(0)` and `Unique`, so API replicas do not pile up duplicates), `credit_worker.go` (`kredi:supur` on `bakim`, every `CREDITS_SWEEP_INTERVAL`, default 1h, same scheduler: releases reservations held > 3 h, expires lots, stops full or ended campaigns). Render and master workers refuse paid calls and R2 writes for deleted owners (`deletionmark.Checker`). Both settle the job's credit reservation (ref = job id, `job_credits.go`): commit on success, release on `SkipRetry` or the last attempt, nothing while asynq still retries; the render worker skips a job canceled while queued without calling Suno and releases its credit.
 - **client/** — HTTP clients for external services: Groq (AI lyrics), Suno (music generation), R2 (Cloudflare S3-compatible storage), audio-service (local mastering).
 - **model/** — Request/response structs and enums. All enum values defined in `enums.go`.
-- **middleware/** — `auth.go` (RS256 access tokens from `internal/identity` — the only auth path), `ratelimit.go` (Redis fixed window per user; limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute, Keşfet publish/cover per hour and comments per minute; `LimitWithCode` answers 429 with a custom code, e.g. `yavasla` for comments; `LimitIP` limits unauthenticated routes per client IP from Traefik's `X-Real-Ip` (falls back to `c.IP()`, Fiber's `ProxyHeader` is not set), replaced by `CF-Connecting-IP` only when that peer address is inside `ratelimit.cloudflare_ranges` (`RATELIMIT_CLOUDFLARE_RANGES`; default = Cloudflare's published ranges in `internal/clientip`; empty disables; invalid entry = startup error), because behind Cloudflare the peer is the shared edge IP and the header is forgeable from any other peer; the OP listener's login/signup limits apply the same rule on the address chosen from `X-Forwarded-For` under `ratelimit:<prefix>:ip:<ip>` (IPv4-mapped addresses as IPv4, IPv6 per /64), and moves requests carrying the configured `X-Web-Anahtari` (constant-time compare) to the shared `ratelimit:<prefix>:web` counter; if such a request also has a parseable `X-Ziyaretci-Ip` (the visitor IP sent by make-singer-frontend, trusted only together with the valid key) it must first fit the per-visitor bucket `ratelimit:<prefix>:web:ip:<ipKey(visitor)>` (`RATELIMIT_PUBLIC_WEB_VISITOR_PER_MIN`, default 30, `<=0` disables) and then the shared one), `deleted_user.go` (`DeletedUserGate`: deleted-user gate on `/api`, 401 for deleted accounts, 503 + `Retry-After: 5` when Redis is unreachable).
+- **middleware/** — `auth.go` (RS256 access tokens from `internal/identity` — the only auth path), `ratelimit.go` (Redis fixed window per user; limits per user: lyrics/min, render/master/export/upload per hour, `ws/ticket` and `files/sign` per minute, Keşfet publish/cover per hour and comments per minute; `LimitWithCode` answers 429 with a custom code, e.g. `yavasla` for comments; `LimitIP` limits unauthenticated routes per client IP from Traefik's `X-Real-Ip` (falls back to `c.IP()`, Fiber's `ProxyHeader` is not set), replaced by `CF-Connecting-IP` only when that peer address is inside `ratelimit.cloudflare_ranges` (`RATELIMIT_CLOUDFLARE_RANGES`; default = Cloudflare's published ranges in `internal/clientip`; empty disables; invalid entry = startup error), because behind Cloudflare the peer is the shared edge IP and the header is forgeable from any other peer; the OP listener's login/signup limits apply the same rule on the address chosen from `X-Forwarded-For` under `ratelimit:<prefix>:ip:<ip>` (IPv4-mapped addresses as IPv4, IPv6 per /64), and moves requests carrying the configured `X-Web-Anahtari` (constant-time compare) to the shared `ratelimit:<prefix>:web` counter; if such a request also has a parseable `X-Ziyaretci-Ip` (the visitor IP sent by make-singer-frontend, trusted only together with the valid key) it must first fit the per-visitor bucket `ratelimit:<prefix>:web:ip:<ipKey(visitor)>` (`RATELIMIT_PUBLIC_WEB_VISITOR_PER_MIN`, default 30, `<=0` disables) and then the shared one), `deleted_user.go` (`DeletedUserGate`: deleted-user gate on `/api`, 401 for deleted accounts, 503 + `Retry-After: 5` when Redis is unreachable), `admin.go` (`AdminGate`: 403 `yetkisiz` unless the user id is in `ADMIN_USER_IDS`). Credits: `/api/credits` 60/min (`RATELIMIT_CREDITS_PER_MIN`, key `kredi`), `/api/admin` 30/min (`RATELIMIT_ADMIN_PER_MIN`, key `admin`).
 - **social/** — Keşfet (spec: `make-singer-mobile/docs/superpowers/specs/2026-10-11-kesfet-design.md`). Own Postgres pool on `AUTH_DB_URL`, own schema `sosyal` and migration table `sosyal.schema_migrations` (embedded `migrations/*.sql`, advisory lock `0x736F7379616C31`, run at startup, additive only). No FK to the identity `users` table. `Service` holds the rules (username, publish validation, detail visibility via `CanSeeDetail`), `PostgresStore` the SQL (list visibility via `visibilityCondition`; a live test proves the two agree). Errors map to lowercase contract codes in `handler/social.go` (`bulunamadi`, `profil_yok`, `dogrulama`, `sahiplik`, `stem_yok`, `durum`, `kendini_takip`, `yorum_kapali`, `yetki`, `altyapi_kapali`, ...; unmapped errors are 500 `sunucu_hatasi`); social validation failures are 422, not 400. Lists are cursor-paged (`imlec`, `limit` 1-50, `{ogeler, sonrakiImlec}`); comments use a two-group cursor (timed by `zaman_ms` ascending, then untimed by `olusturma` descending). Comments, reports and comment deletion are soft or idempotent as the contract requires (a repeat report is 201 without a new row). `cover-baslat` copies the publication's `stem_listesi` server-side to `stems/{caller}/cover-{sourcePublicationID}/{stemId}.{ext}` (`client.Copier`; one deterministic folder per caller and source, repeat starts overwrite the same objects because source stems are immutable) so `/api/files/sign` can re-sign them. `GET /api/public/s/{slug}` is the only unauthenticated Keşfet route (`SocialHandler.PublicRoutes`, registered before the `/api` auth group): `herkes`/`link` + `yayinda` only, `onizlemeUrl` signed for 1 h, `Cache-Control: no-store`. Migration `002_hazirlik_baslangic.sql` adds `yayin.hazirlik_baslangic` (set on insert by default and by `ResetForRetry`) plus a partial index on `hazirlaniyor` rows; `PostgresStore.MarkStalePreparations` (the `StaleSweeper` interface) moves non-deleted rows stuck in `hazirlaniyor` longer than the threshold to `hata`.
+- **credit/** — Credit ledger (spec: `docs/superpowers/specs/2026-10-13-kredi-sistemi-design.md`; phase A, no subscriptions yet). Own Postgres pool on `AUTH_DB_URL`, schema `kredi`, migration table `kredi.schema_migrations` (embedded `migrations/*.sql`, advisory lock `0x6B726564693031`, additive only), no FK to `users`. Tables: `lots` (credit slices with `remaining` and `expires_at`; balance = live `SUM(remaining)`), `entries` (immutable ledger), `reservations` (held/committed/released, `allocation` jsonb), `campaigns` (one active via a partial unique index), `campaign_claims`. `Service` implements `credit.Spender` (`Reserve` / `Commit` / `Release`, idempotent per ref; earliest-expiring lot first regardless of pool, `FOR UPDATE`). `CREDITS_ENFORCE=false` (default) is shadow mode: a short balance reserves 0 and a reservation error is logged, never returned. Prices come from `CREDIT_COST_*` (0 = free, no reservation). `GET /api/credits` lazily claims the active campaign for users created after it started (`UserCreatedAt`, implemented by `identity.Server`) and returns the claim as `welcomeGift` for 7 days. Admin routes sit behind `middleware.AdminGate` (`ADMIN_USER_IDS`, 403 `yetkisiz`, empty list = closed). Lyrics (handler, sync), render start and final master (services, before enqueue) reserve through `credit.Spender`; `credit.FakeSpender` is the shared test fake. Errors: 402 `kredi_yetersiz`, 422 `dogrulama`, 404 `bulunamadi`, 500 `sunucu_hatasi`.
 - **deletionmark/** — Deleted-account marker shared by middleware, `AccountService` and workers: `Key(userID)` (`kimlik:silindi:<userId>`), `TTL` (24 h), `Checker` interface and `RedisChecker` (EXISTS).
 - **auth/** — `jwks_verifier.go`: token verifiers. `UpstreamVerifier` validates **upstream** (Apple/Google) `id_token`s for the federation flow — RS256 pinned, `iss`/`aud`/`exp` enforced, `nonce` required and compared in constant time.
 - **websocket/** — Hub for broadcasting real-time job progress to clients via `GET /ws/jobs/:jobId?ticket=` (ticket from `POST /api/ws/ticket`).
@@ -79,7 +80,7 @@ Config priority: environment variables > `config.yaml` > Viper defaults. See `co
 
 ### Job lifecycle
 
-Jobs are stored in Redis as JSON with key `job:{jobId}` and 24-hour TTL. States: `queued` → `running` → `succeeded` / `failed` / `canceled`. Progress updates (0-100%) are pushed to WebSocket subscribers. Asynq runs four queues: `render` (weight 6), `master` (weight 4), `sosyal` (weight 2, publication preparation) and `bakim` (weight 1, delayed account and publication file cleanup).
+Jobs are stored in Redis as JSON with key `job:{jobId}` and 24-hour TTL. States: `queued` → `running` → `succeeded` / `failed` / `canceled`. Progress updates (0-100%) are pushed to WebSocket subscribers. Asynq runs four queues: `render` (weight 6), `master` (weight 4), `sosyal` (weight 2, publication preparation) and `bakim` (weight 1, delayed account and publication file cleanup, credit sweep).
 Each job record carries `ownerId` (the `sub` of the user who started it). `status`, `result` and `cancel` load the job through `readOwnedJob` (`internal/service/job_record.go`) and answer **404** — not 403 — when the caller is not the owner; records without `ownerId` are visible to nobody.
 
 ### API response conventions
@@ -112,7 +113,7 @@ Bearer token required on all `/api/*` routes, except `GET /api/public/s/{slug}`
 
 If `AUTH_ISSUER` is empty or the OpenID Provider fails to start, the service exits with a fatal error (fail-closed design).
 
-**Account deletion (`DELETE /api/account`):** `service.AccountService.Delete` validates the id, deletes the user via `identity.Server.DeleteAccount` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`deletionmark.TTL` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.UserPrefixes`), then the Keşfet data through the optional `SocialCleaner` (first `DeleteUserTraces` deletes the profile, likes, comments, follows, reports filed and reports on the user's comments in one transaction, so the user and their publications vanish from every read path at once even if R2 is down; its error is joined but does not stop the next steps; then every publication id, soft-deleted included, gets `sosyal/{id}/` deleted; the `yayin` rows are deleted by `DeleteUserData` only if all those prefixes succeeded, so the retry can list the ids again; other users' covers stay and show the source as deleted; cover stem copies live under `stems/<userId>/` and go with `UserPrefixes`), and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.DeletedUserGate` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `deletionmark.Checker`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.
+**Account deletion (`DELETE /api/account`):** `service.AccountService.Delete` validates the id, deletes the user via `identity.Server.DeleteAccount` (a failure here aborts before R2 is touched), then, with errors only logged since the user can no longer retry, writes the `kimlik:silindi:<userId>` marker (`deletionmark.TTL` = 24 h, the job record lifetime), deletes all R2 prefixes (`client.UserPrefixes`), then the Keşfet data through the optional `SocialCleaner` (first `DeleteUserTraces` deletes the profile, likes, comments, follows, reports filed and reports on the user's comments in one transaction, so the user and their publications vanish from every read path at once even if R2 is down; its error is joined but does not stop the next steps; then every publication id, soft-deleted included, gets `sosyal/{id}/` deleted; the `yayin` rows are deleted by `DeleteUserData` only if all those prefixes succeeded, so the retry can list the ids again; other users' covers stay and show the source as deleted; cover stem copies live under `stems/<userId>/` and go with `UserPrefixes`), then the credit rows through the optional `CreditCleaner` (`credit.PostgresStore.DeleteUserData`: lots, entries, reservations, campaign claims; campaign counters are not given back; errors joined, not fatal), and enqueues a delayed `hesap:temizle` task (`bakim` queue, 1 h later, `MaxRetry(10)`) that repeats the R2 cleanup. `middleware.DeletedUserGate` on the `/api` group rejects tokens of marked users with 401 (503 + `Retry-After: 5` if Redis is unreachable), which also blocks starting new jobs. Jobs queued or running at deletion time check the owner through `deletionmark.Checker`: the render worker checks before the paid Suno `GenerateMusic` call (deleted → `SkipRetry`, Suno not called; check error → retryable, nothing paid yet) and before every stem upload (deleted or check error → `SkipRetry`, the paid path), and the master worker checks before the audio-service call that writes to R2. Known limit: `job:{id}` records (`ownerId`, result keys) stay in Redis until their 24 h TTL.
 
 Extracted claims (`userId`, `email`, `name`) are stored in Fiber context locals.
 
````

- [ ] **Step 4: Update the wire snapshot**

Run: `go test ./tools/wirecheck -update`, review.
Expected: 6 added lines: `ROUTE GROUP /credits`, `ROUTE GROUP /admin`, `STR "/credits"`, `STR "/admin"`, `STR "kredi"`, `STR "admin"` (the rate-limit key prefixes).

- [ ] **Step 5: Full verification**

Run (in `core-service/`):
```bash
gofmt -l .
go build ./... && go vet ./...
go test -count=1 ./cmd/... ./internal/... ./tools/...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
```
Expected: no gofmt output; every package `ok` (`internal/websocket` has no tests); `0 issues.` Never run `./e2e/...`.

- [ ] **Step 6: Optional smoke start (no paid calls)**

With a scratch RSA key and the test database, the server starts, migrates `kredi`, logs `Credits in shadow mode (CREDITS_ENFORCE=false): spending is recorded, never blocked`, answers `GET /health` 200 and `GET /api/credits` 401 without a token, and processes `kredi:supur` on `bakim`. Use a free Redis DB so no other session's queues are touched, then flush it:
```bash
openssl genrsa -traditional -out /tmp/kredi-key.pem 2048
SERVER_PORT=18000 AUTH_PORT=18001 AUTH_ISSUER=http://localhost:18001 AUTH_CLIENT_ID=test \
AUTH_REDIRECT_URIS="com.test:/cb" AUTH_DB_URL="$KIMLIK_TEST_DB_URL" AUTH_SIGNING_KEY="$(cat /tmp/kredi-key.pem)" \
AUTH_CRYPTO_KEY='placeholder-32-bytes-change-me!!' REDIS_DB=11 CREDITS_SWEEP_INTERVAL=2s go run ./cmd/server
# other terminal: curl -s localhost:18000/health; curl -si localhost:18000/api/credits | head -1
# afterwards: docker exec faz2-redis redis-cli -n 11 flushdb; rm /tmp/kredi-key.pem
```

- [ ] **Step 7: Commit**

```bash
git add core-service/cmd core-service/tools/wirecheck/testdata/wire.txt docker-stack.yml CLAUDE.md
git commit -m "feat(kredi): kredi uclari baglandi, harcamalar golge modda kaydedilir

/api/credits ve /api/admin kampanya uclari, render, final master ve soz
islemleri kredi ayirir; CREDITS_ENFORCE=false iken hicbir is engellenmez.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Deployment notes (after merge, not part of the tasks)

- Set `ADMIN_USER_IDS` (the admin's user UUID) in the Portainer stack env; leave `CREDITS_ENFORCE` unset (false). Phase A needs no other new env.
- The migration runs at startup on every replica under the advisory lock; it is additive, so a rollback to the previous image just ignores the `kredi` schema.
- Turning enforcement on is out of scope (spec Sevkiyat item 4, after phase C ships).

## Spec coverage check

| Spec item | Task |
|---|---|
| `internal/credit` package, own pool/schema/migration table/lock, no FK | 1, 13 |
| Data model (lots, entries, reservations, campaigns, campaign_claims) | 1 |
| Reserve order K4, `FOR UPDATE`, insufficient, shadow mode, Commit/Release idempotent, release to expired lots | 3, 6 |
| Cost 0 never reserves | 6, 11 |
| Lyrics / render / master links table | 9, 10, 11 |
| Render cancel while queued, worker skips canceled job | 10 |
| Stuck reservation release (3 h, periodic) | 5, 6, 13 |
| Campaign claim (lazy, K5, conditional UPDATE, PK, filling claim stops) | 4, 6, 7 |
| `welcomeGift` for 7 days | 4, 6 |
| One active campaign, create stops active (K6), validity from claim (K7) | 4, 6 |
| Expiring lots and finished campaigns in `kredi:supur` | 5, 6, 13 |
| Account deletion `CreditCleaner` | 5, 12, 14 |
| `GET /api/credits`, history, admin endpoints, 402/403 codes, i18n, wirecheck | 8, 14 |
| Rate limits 60/min and 30/min, `AdminGate`, empty list closed | 2, 8, 14 |
| `CREDIT_COST_*`, `CREDITS_ENFORCE`, `ADMIN_USER_IDS` config | 2 |
| Tests listed in the spec "Test" section for phase A | 3–13 |
