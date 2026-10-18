package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/service"
)

func TestMasterHandler_InsufficientIs402(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Insufficient: true, Required: 3, Bal: 1, Debt: 70}
	cfg := &config.R2Config{AccountID: "hesap123", PublicBucket: "pub", PrivateBucket: "priv", PublicURL: "https://cdn.example.com"}
	svc := service.NewMasterService(rdb, nil, nil, cfg).WithCredits(fake)
	h := NewMasterHandler(svc, validator.New())
	app := fiber.New()
	app.Post("/final", asUser("kullanici-a"), h.Final)

	key, err := client.UserKey("stems", "kullanici-a", "p1", "a.mp3")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	body := `{"projectId":"11111111-1111-4111-8111-111111111111","profile":"clean","stemUrls":["` + client.UnsignedURL(key, cfg) +
		`"],"mixSnapshot":{"channels":[{"stemId":"s1","volumeDb":0}],"preset":"default"}}`
	req := httptest.NewRequest(http.MethodPost, "/final", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]int `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error.Code != "INSUFFICIENT_CREDITS" || out.Error.Details["required"] != 3 || out.Error.Details["balance"] != 1 || out.Error.Details["debt"] != 70 {
		t.Errorf("error = %+v, want INSUFFICIENT_CREDITS required 3 balance 1", out.Error)
	}
}
