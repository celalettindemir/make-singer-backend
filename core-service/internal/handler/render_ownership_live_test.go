package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// asUser stands in for the auth middleware and writes the userId.
func asUser(userID string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("userId", userID)
		}
		return c.Next()
	}
}

func renderApp(t *testing.T, userID string) *fiber.App {
	t.Helper()
	rdb := liveRedis(t)
	raw, _ := json.Marshal(model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued, CreatedAt: time.Now()})
	if err := rdb.Set(context.Background(), "job:is1", raw, time.Hour).Err(); err != nil {
		t.Fatalf("is yazilamadi: %v", err)
	}
	h := NewRenderHandler(service.NewRenderService(rdb, nil, nil, &config.R2Config{}), validator.New())
	app := fiber.New()
	app.Get("/status/:jobId", asUser(userID), h.Status)
	app.Post("/cancel/:jobId", asUser(userID), h.Cancel)
	return app
}

func TestRenderHandler_OtherUsersJob404(t *testing.T) {
	app := renderApp(t, "kullanici-b")
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/status/is1", nil),
		httptest.NewRequest(http.MethodPost, "/cancel/is1", nil),
	} {
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("istek: %v", err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, 404 bekleniyordu", req.Method, req.URL.Path, resp.StatusCode)
		}
	}
}

func TestRenderHandler_OwnerReads(t *testing.T) {
	app := renderApp(t, "kullanici-a")
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/status/is1", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, 200 bekleniyordu", resp.StatusCode)
	}
}

func TestRenderHandler_Unauthenticated401(t *testing.T) {
	app := renderApp(t, "")
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/status/is1", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, 401 bekleniyordu", resp.StatusCode)
	}
}
