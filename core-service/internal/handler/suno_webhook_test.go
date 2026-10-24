package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func sunoWebhookApp(h *SunoWebhookHandler) *fiber.App {
	app := fiber.New()
	app.Post("/api/webhooks/suno/:token", h.Handle)
	return app
}

func TestSunoWebhook_WrongToken404(t *testing.T) {
	rdb := liveRedis(t)
	app := sunoWebhookApp(NewSunoWebhookHandler(rdb, "dogru"))
	req := httptest.NewRequest("POST", "/api/webhooks/suno/yanlis", strings.NewReader(`{"data":{"task_id":"t1"}}`))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSunoWebhook_PublishesWakeForTaskID(t *testing.T) {
	for name, body := range map[string]string{
		"snake": `{"code":200,"data":{"task_id":"t-snake","callbackType":"complete"}}`,
		"camel": `{"code":200,"data":{"taskId":"t-camel"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			rdb := liveRedis(t)
			id := "t-" + name
			sub := rdb.Subscribe(context.Background(), "suno:uyan:"+id)
			defer sub.Close()
			if _, err := sub.Receive(context.Background()); err != nil {
				t.Fatal(err)
			}
			app := sunoWebhookApp(NewSunoWebhookHandler(rdb, "dogru"))
			resp, err := app.Test(httptest.NewRequest("POST", "/api/webhooks/suno/dogru", strings.NewReader(body)))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			select {
			case <-sub.Channel():
			case <-time.After(time.Second):
				t.Fatal("no wake published")
			}
		})
	}
}

func TestSunoWebhook_BadBodyStill200(t *testing.T) {
	rdb := liveRedis(t)
	app := sunoWebhookApp(NewSunoWebhookHandler(rdb, "dogru"))
	resp, err := app.Test(httptest.NewRequest("POST", "/api/webhooks/suno/dogru", strings.NewReader(`not json`)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
