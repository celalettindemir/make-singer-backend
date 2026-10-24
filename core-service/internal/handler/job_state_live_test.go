package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// jobStateApp serves render and master result/cancel for user-a, with one
// queued and one finished render job and one queued master job in the store.
func jobStateApp(t *testing.T) *fiber.App {
	t.Helper()
	liveRedis(t)
	repo := jobs.NewMemoryStore(nil)
	seed := []model.Job{
		{ID: "6f1c2a10-0000-4000-8000-0000000000c1", Type: model.JobTypeRender, OwnerID: "user-a", Status: model.JobStatusQueued, CreatedAt: time.Now()},
		{ID: "6f1c2a10-0000-4000-8000-0000000000c2", Type: model.JobTypeRender, OwnerID: "user-a", Status: model.JobStatusSucceeded, CreatedAt: time.Now()},
		{ID: "6f1c2a10-0000-4000-8000-0000000000c3", Type: model.JobTypeMaster, OwnerID: "user-a", Status: model.JobStatusQueued, CreatedAt: time.Now()},
	}
	for i := range seed {
		if err := repo.Create(context.Background(), &seed[i]); err != nil {
			t.Fatalf("write job: %v", err)
		}
	}
	render := NewRenderHandler(service.NewRenderService(repo, nil, nil, &config.R2Config{}), validator.New())
	master := NewMasterHandler(service.NewMasterService(repo, nil, nil, &config.R2Config{}), validator.New())
	app := fiber.New()
	app.Get("/render/result/:jobId", asUser("user-a"), render.Result)
	app.Post("/render/cancel/:jobId", asUser("user-a"), render.Cancel)
	app.Get("/master/result/:jobId", asUser("user-a"), master.Result)
	return app
}

func TestJobStateErrors_Are400(t *testing.T) {
	app := jobStateApp(t)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/render/result/6f1c2a10-0000-4000-8000-0000000000c1", nil),
		httptest.NewRequest(http.MethodPost, "/render/cancel/6f1c2a10-0000-4000-8000-0000000000c2", nil),
		httptest.NewRequest(http.MethodGet, "/master/result/6f1c2a10-0000-4000-8000-0000000000c3", nil),
	} {
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"code":"VALIDATION_ERROR"`) {
			t.Errorf("%s %s = %d %s, want 400 VALIDATION_ERROR", req.Method, req.URL.Path, resp.StatusCode, body)
		}
	}
}
