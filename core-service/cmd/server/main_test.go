package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/social"
)

func TestCustomErrorHandler(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		key    i18n.Key
	}{
		{"not found", fiber.ErrNotFound, 404, i18n.KeyRouteNotFound},
		{"method not allowed", fiber.ErrMethodNotAllowed, 405, i18n.KeyMethodNotAllowed},
		{"body too large", fiber.ErrRequestEntityTooLarge, 413, i18n.KeyBodyTooLarge},
		{"upgrade required", fiber.ErrUpgradeRequired, 426, i18n.KeyUpgradeRequired},
		{"other fiber error", fiber.NewError(fiber.StatusTeapot), 418, i18n.KeyRequestFailed},
		{"wrapped fiber error", fmt.Errorf("wrap: %w", fiber.ErrNotFound), 404, i18n.KeyRouteNotFound},
		{"non-fiber error", errors.New("secret-db-dsn leaked"), 500, i18n.KeyInternal},
		// social.ValidationError is rendered by the social handler, not here;
		// at this layer it is an unknown error and must not leak.
		{"social validation error", &social.ValidationError{Key: i18n.KeyFieldFileURLInvalid}, 500, i18n.KeyInternal},
	}
	langs := []struct {
		header string
		lang   i18n.Lang
	}{{"", i18n.TR}, {"en", i18n.EN}}

	for _, tt := range tests {
		for _, l := range langs {
			t.Run(tt.name+"/"+string(l.lang), func(t *testing.T) {
				app := fiber.New(fiber.Config{ErrorHandler: customErrorHandler})
				app.Get("/x", func(c *fiber.Ctx) error { return tt.err })
				req := httptest.NewRequest(http.MethodGet, "/x", nil)
				if l.header != "" {
					req.Header.Set("Accept-Language", l.header)
				}
				resp, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := io.ReadAll(resp.Body)
				var body struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatalf("body %s: %v", raw, err)
				}
				want := i18n.Message(l.lang, tt.key)
				if resp.StatusCode != tt.status || body.Error.Code != "SERVICE_ERROR" || body.Error.Message != want {
					t.Fatalf("got %d %q %q, want %d SERVICE_ERROR %q", resp.StatusCode, body.Error.Code, body.Error.Message, tt.status, want)
				}
			})
		}
	}
}

type rejectingAppleVerifier struct{}

func (rejectingAppleVerifier) Notification(string) (*appstore.Notification, error) {
	return nil, errors.New("bad signature")
}

// creditTestApp builds an app through the same newAPIRouter main uses, with a
// Bearer-only stand-in for the OP middleware.
func creditTestApp(t *testing.T) *fiber.App {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 14})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	cfg := &config.Config{RateLimit: config.RateLimitConfig{WebhooksPerMin: 1000, PurchasesPerMin: 1000}}
	rl := middleware.NewRateLimiter(rdb, nil)
	h := creditRouteHandlers{
		Credits:   handler.NewCreditsHandler(nil),
		Purchases: handler.NewPurchasesHandler(nil, validator.New()),
		Webhooks:  handler.NewWebhooksHandler(nil, rejectingAppleVerifier{}, nil, nil),
	}
	app := fiber.New()
	api := newAPIRouter(app, h, rl, cfg, func(c *fiber.Ctx) error {
		if c.Get("Authorization") == "" {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		return c.Next()
	})
	registerCreditRoutes(api, h, rl, cfg)
	return app
}

func TestRoutes_WebhookReachableWithoutBearer(t *testing.T) {
	app := creditTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/appstore", strings.NewReader(`{"signedPayload":"garbage"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 from the handler (not 401 from the auth group)", resp.StatusCode)
	}
}

func TestRoutes_GoogleWebhookUnconfiguredIs503(t *testing.T) {
	app := creditTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/googleplay", strings.NewReader(`{}`))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for an unconfigured push verifier", resp.StatusCode)
	}
}

func TestRoutes_CreditsRequireAuth(t *testing.T) {
	app := creditTestApp(t)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/credits"},
		{http.MethodGet, "/api/credits/history"},
		{http.MethodPost, "/api/purchases/apple"},
		{http.MethodPost, "/api/purchases/google"},
	} {
		resp, err := app.Test(httptest.NewRequest(r.method, r.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", r.method, r.path, resp.StatusCode)
		}
	}
}

func TestScheduler_RegistersRefill(t *testing.T) {
	cfg := &config.Config{}
	cfg.Credits.RefillInterval = time.Hour
	entries := scheduleEntries(cfg)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (sweep disabled)", len(entries))
	}
	e := entries[0]
	if e.spec != "@every 1h0m0s" || e.task.Type() != credit.TaskTypeRefill {
		t.Fatalf("entry = %q %q", e.spec, e.task.Type())
	}
	var queue string
	var unique time.Duration
	for _, o := range e.opts {
		switch o.Type() {
		case asynq.QueueOpt:
			queue = o.Value().(string)
		case asynq.UniqueOpt:
			unique = o.Value().(time.Duration)
		}
	}
	if queue != credit.QueueCredit || unique != time.Hour-time.Hour/10 {
		t.Fatalf("queue %q unique %v", queue, unique)
	}
}

func TestScheduler_BothDisabledStartsNothing(t *testing.T) {
	s, err := startScheduler(&config.Config{})
	if s != nil || err != nil {
		t.Fatalf("got %v, %v; want nil, nil", s, err)
	}
}

func TestScheduler_RegistersExpiry(t *testing.T) {
	cfg := &config.Config{}
	cfg.Credits.ExpireInterval = 30 * time.Minute
	entries := scheduleEntries(cfg)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (refill and sweep disabled)", len(entries))
	}
	e := entries[0]
	if e.spec != "@every 30m0s" || e.task.Type() != credit.TaskTypeExpire {
		t.Fatalf("entry = %q %q", e.spec, e.task.Type())
	}
	if credit.TaskTypeExpire != "kredi:sure" {
		t.Fatalf("task type = %q", credit.TaskTypeExpire)
	}
}
