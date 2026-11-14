package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

type importTestAudio struct {
	client.ImportAudio
	ms  int
	err error
}

func (a importTestAudio) PrepareImport(context.Context, *client.ImportPrepareRequest) (*client.ImportPrepareResponse, error) {
	if a.err != nil {
		return nil, a.err
	}
	return &client.ImportPrepareResponse{DurationMs: a.ms}, nil
}
func (a importTestAudio) IsConfigured() bool { return true }

type importTestQueue struct{}

func (importTestQueue) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{}, nil
}

type importTestEnv struct {
	app     *fiber.App
	repo    *jobs.MemoryStore
	spender *credit.FakeSpender
}

func importApp(ms int, enabled bool, maxBytes int64) *importTestEnv {
	e := &importTestEnv{repo: jobs.NewMemoryStore(nil), spender: &credit.FakeSpender{IsEnforced: true, Bal: 100}}
	svc := service.NewImportService(service.ImportDeps{
		Cfg:  config.ImportConfig{Enabled: enabled, MinMs: 30000, MaxMs: 480000},
		Repo: e.repo, Storage: &humTestStorage{}, Audio: importTestAudio{ms: ms}, Queue: importTestQueue{}, Credits: e.spender,
	})
	h := NewImportHandler(svc, maxBytes, 30000, 480000)
	e.app = fiber.New(fiber.Config{BodyLimit: 20 * 1024 * 1024})
	e.app.Post("/imports", func(c *fiber.Ctx) error { c.Locals("userId", "user-1"); return h.Upload(c) })
	return e
}

func importRequest(t *testing.T, app *fiber.App, projectID, contentType string, size int) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if projectID != "" {
		_ = w.WriteField("projectId", projectID)
	}
	_ = w.WriteField("projectName", "Gece")
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="song.mp3"`)
	header.Set("Content-Type", contentType)
	part, _ := w.CreatePart(header)
	_, _ = part.Write(make([]byte, size))
	_ = w.Close()
	req := httptest.NewRequest("POST", "/imports", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	// Errors are wrapped as {"error": {...}}; success bodies are returned as is.
	if inner, ok := got["error"].(map[string]any); ok {
		got = inner
	}
	return resp.StatusCode, got
}

func TestImportHandler_MapsErrors(t *testing.T) {
	t.Run("202", func(t *testing.T) {
		e := importApp(95000, true, 1024)
		code, got := importRequest(t, e.app, "p1", "audio/mpeg", 4)
		if code != 202 || got["durationMs"] != float64(95000) || got["jobId"] == "" {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("too large", func(t *testing.T) {
		code, got := importRequest(t, importApp(95000, true, 8).app, "p1", "audio/mpeg", 64)
		det, _ := got["details"].(map[string]any)
		if code != 400 || det["maxSize"] != float64(8) {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("content type", func(t *testing.T) {
		code, got := importRequest(t, importApp(95000, true, 1024).app, "p1", "text/plain", 4)
		det, _ := got["details"].(map[string]any)
		if code != 400 || det["contentType"] != "text/plain" {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("bad project id", func(t *testing.T) {
		code, _ := importRequest(t, importApp(95000, true, 1024).app, "a/b", "audio/mpeg", 4)
		if code != 400 {
			t.Fatalf("%d", code)
		}
		code, _ = importRequest(t, importApp(95000, true, 1024).app, "", "audio/mpeg", 4)
		if code != 400 {
			t.Fatalf("missing: %d", code)
		}
	})
	t.Run("duration", func(t *testing.T) {
		code, got := importRequest(t, importApp(1000, true, 1024).app, "p1", "audio/mpeg", 4)
		det, _ := got["details"].(map[string]any)
		if code != 400 || det["minMs"] != float64(30000) || det["maxMs"] != float64(480000) || det["durationMs"] != float64(1000) {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		code, got := importRequest(t, importApp(95000, false, 1024).app, "p1", "audio/mpeg", 4)
		if code != 503 || got["code"] != "SERVICE_ERROR" {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("402", func(t *testing.T) {
		e := importApp(95000, true, 1024)
		e.spender.Insufficient = true
		code, got := importRequest(t, e.app, "p1", "audio/mpeg", 4)
		if code != 402 || got["code"] != "INSUFFICIENT_CREDITS" {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("409", func(t *testing.T) {
		e := importApp(95000, true, 1024)
		_ = e.repo.Create(context.Background(), &model.Job{ID: "11111111-1111-4111-8111-aaaaaaaaaaaa", Type: model.JobTypeImport, OwnerID: "user-1", Status: model.JobStatusQueued, CreatedAt: time.Now()})
		code, got := importRequest(t, e.app, "p1", "audio/mpeg", 4)
		det, _ := got["details"].(map[string]any)
		if code != 409 || got["code"] != "JOB_ACTIVE" || det["jobId"] != "11111111-1111-4111-8111-aaaaaaaaaaaa" {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		e := importApp(0, true, 1024)
		svc := service.NewImportService(service.ImportDeps{
			Cfg:  config.ImportConfig{Enabled: true, MinMs: 30000, MaxMs: 480000},
			Repo: e.repo, Storage: &humTestStorage{}, Audio: importTestAudio{err: client.ErrAudioUnprocessable}, Queue: importTestQueue{}, Credits: e.spender,
		})
		h := NewImportHandler(svc, 1024, 30000, 480000)
		app := fiber.New()
		app.Post("/imports", func(c *fiber.Ctx) error { c.Locals("userId", "user-1"); return h.Upload(c) })
		code, _ := importRequest(t, app, "p1", "audio/mpeg", 4)
		if code != 400 {
			t.Fatalf("%d", code)
		}
	})
}
