package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/identity"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/worker"
	"github.com/redis/go-redis/v9"
)

// mockSource has no AI client, so the lyrics service answers with canned text.
type mockSource struct{}

func (mockSource) Active(context.Context) (lyricsprovider.Name, client.ChatClient) {
	return "", nil
}

// TestLyricsJobs_EndToEndWithMockProvider drives the real handlers, a real
// asynq server and the Postgres store: start (202), lines in the job detail,
// the full event replay, and a second start answering 409 JOB_ACTIVE.
func TestLyricsJobs_EndToEndWithMockProvider(t *testing.T) {
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx := context.Background()
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pool, err := identity.Connect(pctx, dsn)
	if err != nil {
		t.Skip("no local Postgres, skipping")
	}
	t.Cleanup(pool.Close)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 9})
	if rdb.Ping(ctx).Err() != nil {
		t.Skip("no local Redis, skipping")
	}
	t.Cleanup(func() { _ = rdb.FlushDB(ctx).Err(); _ = rdb.Close() })
	_ = rdb.FlushDB(ctx).Err()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE uretim.isler, uretim.is_olaylari, uretim.cihazlar`); err != nil {
		t.Fatal(err)
	}

	store := jobs.NewPostgresStore(pool, jobs.NewRedisBus(rdb))
	opt := asynq.RedisClientOpt{Addr: "localhost:6379", DB: 9}
	aclient := asynq.NewClient(opt)
	inspector := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = aclient.Close(); _ = inspector.Close() })

	v := validator.New()
	gen := service.NewLyricsService(mockSource{})
	jobSvc := service.NewLyricsJobService(store, aclient, inspector, gen, v)
	center := service.NewJobCenter(store, nil, nil).WithLyrics(jobSvc, jobSvc)

	srv := asynq.NewServer(opt, asynq.Config{Concurrency: 2, Queues: asynqQueues(), LogLevel: asynq.ErrorLevel})
	mux := asynq.NewServeMux()
	mux.HandleFunc(service.TaskTypeLyrics, worker.NewLyricsWorker(store, gen, nil).ProcessTask)
	if err := srv.Start(mux); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Shutdown)

	lyricsH := handler.NewLyricsHandler(gen, v).WithJobs(jobSvc)
	jobsH := handler.NewJobsHandler(center, v)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("userId", "smoke-user"); return c.Next() })
	app.Post("/api/lyrics/jobs", lyricsH.StartJob)
	app.Get("/api/jobs/:id", jobsH.Detail)
	app.Get("/api/jobs/:id/events", jobsH.Events)

	call := func(method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		if d, ok := out["data"].(map[string]any); ok {
			return resp.StatusCode, d
		}
		return resp.StatusCode, out
	}

	const body = `{"projectId":"smoke-p","mode":"generate","genre":"pop","vibes":["happy"],
		"song":[{"type":"verse"},{"type":"chorus"}],"sections":[{"index":0},{"index":1}]}`
	status, start := call(http.MethodPost, "/api/lyrics/jobs", body)
	if status != http.StatusAccepted {
		t.Fatalf("start = %d %v", status, start)
	}
	id, _ := start["jobId"].(string)
	if id == "" {
		t.Fatalf("no jobId in %v", start)
	}

	status, second := call(http.MethodPost, "/api/lyrics/jobs", body)
	if status != http.StatusConflict || !strings.Contains(strings.ToUpper(toString(second)), "JOB_ACTIVE") {
		t.Fatalf("second start = %d %v, want 409 JOB_ACTIVE", status, second)
	}

	deadline := time.Now().Add(30 * time.Second)
	var detail map[string]any
	for time.Now().Before(deadline) {
		_, detail = call(http.MethodGet, "/api/jobs/"+id, "")
		if detail["status"] == "succeeded" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if detail["status"] != "succeeded" {
		t.Fatalf("job did not finish: %v", detail)
	}
	result, _ := detail["result"].(map[string]any)
	sections, _ := result["sections"].([]any)
	if len(sections) != 2 {
		t.Fatalf("result = %v, want 2 sections", detail["result"])
	}
	for _, s := range sections {
		lines, _ := s.(map[string]any)["lines"].([]any)
		if len(lines) == 0 {
			t.Errorf("section without lines: %v", s)
		}
	}

	// A client that was away replays everything from seq 0.
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/events?afterSeq=0", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	if len(env.Data) == 0 {
		env.Data = raw
	}
	var events []struct {
		Seq  int64  `json:"seq"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(env.Data, &events); err != nil {
		var wrapped struct {
			Events []struct {
				Seq  int64  `json:"seq"`
				Kind string `json:"kind"`
			} `json:"events"`
		}
		if err2 := json.Unmarshal(env.Data, &wrapped); err2 != nil {
			t.Fatalf("events body %s: %v", raw, err)
		}
		events = wrapped.Events
	}
	kinds := map[string]int{}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %+v", i, events)
		}
		kinds[e.Kind]++
	}
	if kinds["job.queued"] != 1 || kinds["lyrics.line"] < 2 || kinds["lyrics.section_done"] != 2 || kinds["job.succeeded"] != 1 {
		t.Fatalf("event kinds = %v", kinds)
	}
}

func toString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
