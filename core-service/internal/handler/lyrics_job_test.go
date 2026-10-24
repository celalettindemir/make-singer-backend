package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/service"
)

const lyricsJobBody = `{"projectId":"p1","mode":"generate","genre":"pop","vibes":["happy"],
	"song":[{"type":"verse"},{"type":"chorus"}],"sections":[{"index":0},{"index":1}]}`

func lyricsJobApp(t *testing.T, sp credit.Spender) (*fiber.App, *jobs.MemoryStore) {
	t.Helper()
	liveRedis(t)
	opt := asynq.RedisClientOpt{Addr: "localhost:6379", DB: 12}
	client := asynq.NewClient(opt)
	inspector := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = client.Close(); _ = inspector.Close() })
	repo := jobs.NewMemoryStore(nil)
	gen := newFakeLyrics()
	v := validator.New()
	svc := service.NewLyricsJobService(repo, client, inspector, gen, v)
	if sp != nil {
		svc.WithCredits(sp)
	}
	h := NewLyricsHandler(gen, v).WithJobs(svc)
	if sp != nil {
		h.WithCredits(sp)
	}
	app := fiber.New()
	app.Post("/jobs", asUser("kullanici-a"), h.StartJob)
	return app, repo
}

func TestLyricsHandler_StartJob202(t *testing.T) {
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true, Bal: 77}
	app, _ := lyricsJobApp(t, sp)
	status, out := lyricsCall(t, app, "/jobs", lyricsJobBody, actionA)
	if status != http.StatusAccepted {
		t.Fatalf("status = %d body %v", status, out)
	}
	if out["status"] != "queued" || out["balance"] != float64(77) || out["jobId"] == "" {
		t.Errorf("body = %v", out)
	}

	// Second start for the same project: 409 JOB_ACTIVE with the running job.
	status, out = lyricsCall(t, app, "/jobs", lyricsJobBody, "22222222-2222-4222-8222-222222222222")
	if status != http.StatusConflict {
		t.Fatalf("second start status = %d body %v", status, out)
	}
	if !strings.Contains(strings.ToUpper(toJSON(out)), "JOB_ACTIVE") {
		t.Errorf("body = %v, want JOB_ACTIVE", out)
	}

	// 402 when credits are short.
	sp.Insufficient, sp.Required = true, 5
	status, _ = lyricsCall(t, app, "/jobs", strings.Replace(lyricsJobBody, `"p1"`, `"p2"`, 1), actionA)
	if status != http.StatusPaymentRequired {
		t.Errorf("insufficient status = %d, want 402", status)
	}
}

func TestLyricsHandler_StartJobMissingActionID400(t *testing.T) {
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	app, _ := lyricsJobApp(t, sp)
	status, _ := lyricsCall(t, app, "/jobs", lyricsJobBody, "")
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if len(sp.Actions) != 0 {
		t.Errorf("charged without an action id: %+v", sp.Actions)
	}
	// An invalid body is 400 too.
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"mode":"generate"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Action-Id", actionA)
	resp, err := app.Test(req)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid body: %v %v", resp, err)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
