package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

type noHumStorage struct{ humTestStorage }

func (noHumStorage) Exists(context.Context, string) (bool, error) { return false, nil }

func humRenderBody(hum string) string {
	return strings.Replace(validRenderBody, `"arrangement"`,
		`"vocal":{"voice":"female","sections":[{"id":"s1","type":"verse","lines":["gece yolunda"]}]},"hum":`+hum+`,"arrangement"`, 1)
}

func TestRenderHandler_HumNotFound404(t *testing.T) {
	fake := &credit.FakeSpender{Bal: 100, Pro: true}
	svc := service.NewRenderService(jobs.NewMemoryStore(nil), nil, &noHumStorage{}, &config.R2Config{}).WithCredits(fake)
	h := NewRenderHandler(svc, validator.New())
	app := fiber.New()
	app.Post("/start", asUser("kullanici-a"), h.Start)
	body := humRenderBody(`{"humId":"0b8f2c1e-1111-4222-8333-444455556666","fidelity":"faithful"}`)
	req := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %d", len(fake.Spends))
	}
}

func TestRenderStartRequest_HumValidation(t *testing.T) {
	v := validator.New()
	for _, h := range []*model.HumRequest{
		{HumID: "x", Fidelity: model.HumFree},
		{HumID: "0b8f2c1e-1111-4222-8333-444455556666", Fidelity: "loose"},
		{HumID: "", Fidelity: ""},
	} {
		req := validHumStart()
		req.Hum = h
		if err := v.Struct(req); err == nil {
			t.Errorf("%+v must be invalid", h)
		}
	}
	ok := validHumStart()
	ok.Hum = &model.HumRequest{HumID: "0b8f2c1e-1111-4222-8333-444455556666", Fidelity: model.HumFaithful}
	if err := v.Struct(ok); err != nil {
		t.Errorf("valid hum rejected: %v", err)
	}
}

func validHumStart() *model.RenderStartRequest {
	var req model.RenderStartRequest
	if err := json.Unmarshal([]byte(validRenderBody), &req); err != nil {
		panic(err)
	}
	return &req
}
