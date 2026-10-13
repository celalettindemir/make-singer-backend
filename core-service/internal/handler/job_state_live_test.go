package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// jobStateApp serves render and master result/cancel for user-a, with one
// queued and one finished render job and one queued master job in Redis.
func jobStateApp(t *testing.T) *fiber.App {
	t.Helper()
	rdb := liveRedis(t)
	jobs := []model.Job{
		{ID: "r-queued", Type: model.JobTypeRender, OwnerID: "user-a", Status: model.JobStatusQueued, CreatedAt: time.Now()},
		{ID: "r-done", Type: model.JobTypeRender, OwnerID: "user-a", Status: model.JobStatusSucceeded, CreatedAt: time.Now()},
		{ID: "m-queued", Type: model.JobTypeMaster, OwnerID: "user-a", Status: model.JobStatusQueued, CreatedAt: time.Now()},
	}
	for _, j := range jobs {
		raw, _ := json.Marshal(j)
		if err := rdb.Set(context.Background(), "job:"+j.ID, raw, time.Hour).Err(); err != nil {
			t.Fatalf("write job: %v", err)
		}
	}
	render := NewRenderHandler(service.NewRenderService(rdb, nil, nil, &config.R2Config{}), validator.New())
	master := NewMasterHandler(service.NewMasterService(rdb, nil, nil, &config.R2Config{}), validator.New())
	app := fiber.New()
	app.Get("/render/result/:jobId", asUser("user-a"), render.Result)
	app.Post("/render/cancel/:jobId", asUser("user-a"), render.Cancel)
	app.Get("/master/result/:jobId", asUser("user-a"), master.Result)
	return app
}

func TestJobStateErrors_Are400(t *testing.T) {
	app := jobStateApp(t)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/render/result/r-queued", nil),
		httptest.NewRequest(http.MethodPost, "/render/cancel/r-done", nil),
		httptest.NewRequest(http.MethodGet, "/master/result/m-queued", nil),
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
