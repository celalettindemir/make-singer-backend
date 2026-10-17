package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
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
	fake := &credit.FakeSpender{Insufficient: true, Required: 62, Bal: 10}
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
	if body.Error.Details["required"] != 62 || body.Error.Details["balance"] != 10 {
		t.Errorf("details = %v, want required 62 balance 10", body.Error.Details)
	}
}
