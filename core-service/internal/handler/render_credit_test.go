package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/service"
)

const validRenderBody = `{
 "projectId":"11111111-1111-4111-8111-111111111111",
 "brief":{"genre":"pop","vibes":["happy"],"bpm":{"mode":"fixed","value":120},"key":{"mode":"auto"},"structure":[{"id":"s1","type":"verse","bars":8}]},
 "arrangement":{"instruments":["drums"],"density":"medium","groove":"straight"}
}`

func TestRenderHandler_Insufficient402(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{Insufficient: true, Required: 62, Bal: 10, Debt: 70, Pro: true}
	svc := service.NewRenderService(rdb, nil, nil, &config.R2Config{}).WithCredits(fake)
	h := NewRenderHandler(svc, validator.New())
	app := fiber.New()
	app.Post("/start", asUser("kullanici-a"), h.Start)

	req := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(validRenderBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]int `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Details["required"] != 62 || body.Error.Details["balance"] != 10 || body.Error.Details["debt"] != 70 {
		t.Errorf("details = %v, want required 62 balance 10", body.Error.Details)
	}
}

func TestRenderHandler_ShadowReadFailure202(t *testing.T) {
	rdb := liveRedis(t)
	aq := asynq.NewClient(asynq.RedisClientOpt{Addr: "localhost:6379", DB: 12})
	t.Cleanup(func() { _ = aq.Close() })
	fake := &credit.FakeSpender{IsEnforced: false, Err: errors.New("db down")}
	svc := service.NewRenderService(rdb, aq, nil, &config.R2Config{}).WithCredits(fake)
	h := NewRenderHandler(svc, validator.New())
	app := fiber.New()
	app.Post("/start", asUser("kullanici-a"), h.Start)

	req := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(validRenderBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var body struct {
		Balance int `json:"balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Balance != 0 {
		t.Errorf("balance = %d err = %v, want 0", body.Balance, err)
	}
}

func TestRenderHandler_PlanRequiredIs403(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{}
	svc := service.NewRenderService(rdb, nil, nil, &config.R2Config{}).WithCredits(fake)
	h := NewRenderHandler(svc, validator.New())
	app := fiber.New()
	app.Post("/start", asUser("kullanici-a"), h.Start)

	body := strings.Replace(validRenderBody, `"arrangement":{`, `"stemMode":"stems","arrangement":{`, 1)
	req := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error.Code != "PLAN_REQUIRED" || out.Error.Details["requiredTier"] != "pro" {
		t.Errorf("error = %+v, want PLAN_REQUIRED requiredTier pro", out.Error)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %+v, want none", fake.Spends)
	}
}

func TestStartRender_InvalidLyrics_400_NoDebit(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{Plus: true}
	svc := service.NewRenderService(rdb, nil, nil, &config.R2Config{}).WithCredits(fake)
	h := NewRenderHandler(svc, validator.New())
	app := fiber.New()
	app.Post("/start", asUser("kullanici-a"), h.Start)

	body := strings.Replace(validRenderBody, `"arrangement":{`,
		`"vocal":{"voice":"female","sections":[{"id":"s1","type":"verse","lines":["  "]}]},"arrangement":{`, 1)
	req := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("code = %q, want VALIDATION_ERROR", out.Error.Code)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %+v, want none", fake.Spends)
	}
}
