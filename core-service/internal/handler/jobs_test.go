package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// signingStorage returns a different URL on every call.
type signingStorage struct{ n int }

func (s *signingStorage) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (s *signingStorage) Delete(context.Context, string) error { return nil }
func (s *signingStorage) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (s *signingStorage) URLFor(_ context.Context, key string) (string, time.Time, error) {
	s.n++
	return fmt.Sprintf("https://signed.example/%s?sig=%d", key, s.n), time.Now().Add(time.Hour), nil
}

type lyricsFake struct{ canceled, retried []string }

func (f *lyricsFake) Cancel(_ context.Context, _, id string) error {
	f.canceled = append(f.canceled, id)
	return nil
}
func (f *lyricsFake) Retry(_ context.Context, _, id string) error {
	f.retried = append(f.retried, id)
	return nil
}

type jobsEnv struct {
	repo   *jobs.MemoryStore
	lyrics *lyricsFake
	app    *fiber.App
}

func newJobsEnv(t *testing.T, userID string) *jobsEnv {
	t.Helper()
	repo := jobs.NewMemoryStore(nil)
	r2 := &config.R2Config{}
	st := &signingStorage{}
	render := service.NewRenderService(repo, nil, st, r2)
	master := service.NewMasterService(repo, nil, st, r2)
	lf := &lyricsFake{}
	center := service.NewJobCenter(repo, render, master).WithLyrics(lf, lf)
	h := NewJobsHandler(center, validator.New())
	app := fiber.New()
	app.Get("/jobs", asUser(userID), h.List)
	app.Get("/jobs/:id", asUser(userID), h.Detail)
	app.Get("/jobs/:id/events", asUser(userID), h.Events)
	app.Post("/jobs/:id/cancel", asUser(userID), h.Cancel)
	app.Post("/jobs/:id/retry", asUser(userID), h.Retry)
	return &jobsEnv{repo: repo, lyrics: lf, app: app}
}

func (e *jobsEnv) add(t *testing.T, owner, typ string, status model.JobStatus, created time.Time, finished *time.Time, result []byte) string {
	t.Helper()
	id := uuid.NewString()
	j := &model.Job{ID: id, Type: typ, OwnerID: owner, Status: status, CreatedAt: created,
		CompletedAt: finished, Result: result, ProjectID: "proj-" + id[:4]}
	if err := e.repo.Create(context.Background(), j); err != nil {
		t.Fatalf("create: %v", err)
	}
	return id
}

func (e *jobsEnv) do(t *testing.T, method, path string) (int, []byte) {
	t.Helper()
	resp, err := e.app.Test(httptest.NewRequest(method, path, nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func ago(d time.Duration) *time.Time { t := time.Now().Add(-d); return &t }

func TestJobsHandler_ListActiveAndRecent(t *testing.T) {
	e := newJobsEnv(t, "u1")
	running := e.add(t, "u1", model.JobTypeLyrics, model.JobStatusRunning, time.Now().Add(-3*24*time.Hour), nil, nil)
	recent := e.add(t, "u1", model.JobTypeRender, model.JobStatusSucceeded, time.Now().Add(-3*time.Hour), ago(2*time.Hour), nil)
	e.add(t, "u1", model.JobTypeMaster, model.JobStatusSucceeded, time.Now().Add(-73*time.Hour), ago(72*time.Hour), nil)
	e.add(t, "u2", model.JobTypeRender, model.JobStatusRunning, time.Now(), nil, nil)

	code, body := e.do(t, http.MethodGet, "/jobs")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var out struct {
		Jobs []model.JobView `json:"jobs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2: %s", len(out.Jobs), body)
	}
	// newest first by creation: the 3 h old render, then the 3 day old running job.
	if out.Jobs[0].ID != recent || out.Jobs[1].ID != running {
		t.Errorf("order = %s, %s", out.Jobs[0].ID, out.Jobs[1].ID)
	}

	// An explicit since widens the window.
	since := time.Now().Add(-100 * time.Hour).UTC().Format(time.RFC3339)
	_, body = e.do(t, http.MethodGet, "/jobs?since="+since)
	_ = json.Unmarshal(body, &out)
	if len(out.Jobs) != 3 {
		t.Errorf("with since: %d jobs, want 3", len(out.Jobs))
	}
	if code, _ := e.do(t, http.MethodGet, "/jobs?since=yesterday"); code != 400 {
		t.Errorf("bad since = %d, want 400", code)
	}
}

func TestJobsHandler_ListEmptyIsArray(t *testing.T) {
	e := newJobsEnv(t, "u1")
	_, body := e.do(t, http.MethodGet, "/jobs")
	if string(body) != `{"jobs":[]}` {
		t.Errorf("body = %s", body)
	}
}

func TestJobsHandler_DetailRenderBuildsSignedResult(t *testing.T) {
	e := newJobsEnv(t, "u1")
	rec := model.RenderResultRecord{ID: "r1", BPM: 100, StemMode: "mix",
		Stems: []model.StemResultRecord{{ID: "s1", Instrument: "drums", FileKey: "stems/s1.wav"}}}
	raw, _ := json.Marshal(rec)
	id := e.add(t, "u1", model.JobTypeRender, model.JobStatusSucceeded, time.Now().Add(-time.Hour), ago(30*time.Minute), raw)
	queued := e.add(t, "u1", model.JobTypeRender, model.JobStatusQueued, time.Now(), nil, nil)

	var sigs []string
	for i := 0; i < 2; i++ {
		code, body := e.do(t, http.MethodGet, "/jobs/"+id)
		if code != 200 {
			t.Fatalf("status %d: %s", code, body)
		}
		var d struct {
			Result struct {
				Stems []struct {
					FileURL string `json:"fileUrl"`
				} `json:"stems"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &d); err != nil || len(d.Result.Stems) != 1 {
			t.Fatalf("body %s err %v", body, err)
		}
		sigs = append(sigs, d.Result.Stems[0].FileURL)
	}
	if sigs[0] == sigs[1] || sigs[0] == "" {
		t.Errorf("URLs not freshly signed per read: %v", sigs)
	}

	_, body := e.do(t, http.MethodGet, "/jobs/"+queued)
	var d map[string]json.RawMessage
	_ = json.Unmarshal(body, &d)
	if string(d["result"]) != "null" {
		t.Errorf("queued result = %s, want null", d["result"])
	}
}

func TestJobsHandler_DetailLyricsShowsPartialLines(t *testing.T) {
	e := newJobsEnv(t, "u1")
	partial := `{"sections":[{"index":0,"status":"done","lines":["a","b"]},{"index":1,"status":"writing","lines":["c"]}]}`
	id := e.add(t, "u1", model.JobTypeLyrics, model.JobStatusRunning, time.Now(), nil, []byte(partial))
	bare := e.add(t, "u1", model.JobTypeLyrics, model.JobStatusQueued, time.Now(), nil, nil)

	_, body := e.do(t, http.MethodGet, "/jobs/"+id)
	var d struct {
		Status string `json:"status"`
		Result struct {
			Sections []struct {
				Status string   `json:"status"`
				Lines  []string `json:"lines"`
			} `json:"sections"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "running" || len(d.Result.Sections) != 2 || d.Result.Sections[1].Lines[0] != "c" {
		t.Errorf("body = %s", body)
	}
	_, body = e.do(t, http.MethodGet, "/jobs/"+bare)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(body, &m)
	if string(m["result"]) != `{"sections":[]}` {
		t.Errorf("bare lyrics result = %s", m["result"])
	}
}

func TestJobsHandler_EventsAfterSeq(t *testing.T) {
	e := newJobsEnv(t, "u1")
	id := e.add(t, "u1", model.JobTypeLyrics, model.JobStatusRunning, time.Now(), nil, nil)
	for _, text := range []string{"x", "y"} {
		text := text
		if err := e.repo.UpdateWith(context.Background(), id, func(_ *model.Job, em jobs.Emitter) error {
			em.Emit(jobs.KindLine, map[string]any{"section": 0, "text": text})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// seq 1 is job.queued, 2 and 3 are the lines.
	code, body := e.do(t, http.MethodGet, "/jobs/"+id+"/events?afterSeq=1")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Events) != 2 || out.Events[0]["seq"].(float64) != 2 || out.Events[0]["kind"] != "lyrics.line" {
		t.Errorf("events = %s", body)
	}
	if _, leaked := out.Events[0]["ownerId"]; leaked {
		t.Error("ownerId leaked on the wire")
	}
	for _, bad := range []string{"-1", "abc"} {
		if code, _ := e.do(t, http.MethodGet, "/jobs/"+id+"/events?afterSeq="+bad); code != 400 {
			t.Errorf("afterSeq=%s = %d, want 400", bad, code)
		}
	}
}

func TestJobsHandler_CancelDispatchesByType(t *testing.T) {
	e := newJobsEnv(t, "u1")
	render := e.add(t, "u1", model.JobTypeRender, model.JobStatusQueued, time.Now(), nil, nil)
	master := e.add(t, "u1", model.JobTypeMaster, model.JobStatusQueued, time.Now(), nil, nil)
	lyrics := e.add(t, "u1", model.JobTypeLyrics, model.JobStatusRunning, time.Now(), nil, nil)

	for _, id := range []string{render, master} {
		code, body := e.do(t, http.MethodPost, "/jobs/"+id+"/cancel")
		var v model.JobView
		_ = json.Unmarshal(body, &v)
		if code != 200 || v.Status != model.JobStatusCanceled || v.ID != id {
			t.Errorf("cancel %s = %d %s", id, code, body)
		}
	}
	if code, _ := e.do(t, http.MethodPost, "/jobs/"+lyrics+"/cancel"); code != 200 || len(e.lyrics.canceled) != 1 || e.lyrics.canceled[0] != lyrics {
		t.Errorf("lyrics cancel code %d canceled %v", code, e.lyrics.canceled)
	}
	done := e.add(t, "u1", model.JobTypeRender, model.JobStatusSucceeded, time.Now(), ago(time.Minute), nil)
	if code, _ := e.do(t, http.MethodPost, "/jobs/"+done+"/cancel"); code != 400 {
		t.Errorf("cancel finished = %d, want 400", code)
	}
}

func TestJobsHandler_RetryRules(t *testing.T) {
	e := newJobsEnv(t, "u1")
	lyrics := e.add(t, "u1", model.JobTypeLyrics, model.JobStatusSucceeded, time.Now(), ago(time.Minute), nil)
	render := e.add(t, "u1", model.JobTypeRender, model.JobStatusFailed, time.Now(), ago(time.Minute), nil)

	if code, _ := e.do(t, http.MethodPost, "/jobs/"+lyrics+"/retry"); code != 200 || len(e.lyrics.retried) != 1 {
		t.Errorf("lyrics retry code %d retried %v", code, e.lyrics.retried)
	}
	code, body := e.do(t, http.MethodPost, "/jobs/"+render+"/retry")
	var errBody struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(body, &errBody)
	if code != 400 || errBody.Error.Code != "JOB_NOT_RETRYABLE" {
		t.Errorf("render retry = %d %s", code, body)
	}
}

func TestJobsHandler_OtherUser404(t *testing.T) {
	owner := newJobsEnv(t, "u1")
	id := owner.add(t, "u1", model.JobTypeLyrics, model.JobStatusRunning, time.Now(), nil, nil)
	// Same store, another caller.
	other := fiber.New()
	h := NewJobsHandler(service.NewJobCenter(owner.repo, nil, nil).WithLyrics(owner.lyrics, owner.lyrics), validator.New())
	other.Get("/jobs/:id", asUser("u2"), h.Detail)
	other.Get("/jobs/:id/events", asUser("u2"), h.Events)
	other.Post("/jobs/:id/cancel", asUser("u2"), h.Cancel)
	other.Post("/jobs/:id/retry", asUser("u2"), h.Retry)
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/jobs/"+id, nil),
		httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/events", nil),
		httptest.NewRequest(http.MethodPost, "/jobs/"+id+"/cancel", nil),
		httptest.NewRequest(http.MethodPost, "/jobs/"+id+"/retry", nil),
		httptest.NewRequest(http.MethodGet, "/jobs/"+uuid.NewString(), nil),
	} {
		resp, err := other.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 404 {
			t.Errorf("%s %s = %d, want 404", r.Method, r.URL.Path, resp.StatusCode)
		}
	}
	if len(owner.lyrics.canceled)+len(owner.lyrics.retried) != 0 {
		t.Error("another user's call reached the lyrics service")
	}
	if code, _ := owner.do(t, http.MethodGet, "/jobs/not-a-uuid"); code != 400 {
		t.Errorf("bad id = %d, want 400", code)
	}
}
