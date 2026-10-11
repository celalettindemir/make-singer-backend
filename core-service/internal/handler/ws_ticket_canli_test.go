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
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

const biletIsID = "0b6f3c1e-8a52-4d0e-9a51-6d3f8f2b7c11"

func biletUygulamasi(t *testing.T, userID string) *fiber.App {
	t.Helper()
	rdb := canliRedis(t)
	ham, _ := json.Marshal(model.Job{ID: biletIsID, OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	if err := rdb.Set(context.Background(), "job:"+biletIsID, ham, time.Hour).Err(); err != nil {
		t.Fatalf("is yazilamadi: %v", err)
	}
	h := NewWSTicketHandler(service.NewWSTicketService(rdb), validator.New())
	app := fiber.New()
	app.Post("/api/ws/ticket", kullaniciOlarak(userID), h.Issue)
	// Gercek rotada Gate'ten sonra websocket.New gelir; burada upgrade
	// olmadan Gate'in karari olculur.
	app.Get("/ws/jobs/:jobId", h.Gate, func(c *fiber.Ctx) error { return c.SendString("gecti") })
	return app
}

func biletAl(t *testing.T, app *fiber.App) (*http.Response, model.WSTicketResponse) {
	t.Helper()
	istek := httptest.NewRequest(http.MethodPost, "/api/ws/ticket", strings.NewReader(`{"jobId":"`+biletIsID+`"}`))
	istek.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(istek)
	if err != nil {
		t.Fatalf("bilet istegi: %v", err)
	}
	var b model.WSTicketResponse
	govde, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(govde, &b)
	return resp, b
}

func TestWSTicketHandler_SahibiBiletleBaglanir(t *testing.T) {
	app := biletUygulamasi(t, "kullanici-a")
	resp, b := biletAl(t, app)
	if resp.StatusCode != http.StatusOK || b.Ticket == "" {
		t.Fatalf("bilet = %d %+v", resp.StatusCode, b)
	}

	yol := "/ws/jobs/" + biletIsID + "?ticket=" + b.Ticket
	ilk, _ := app.Test(httptest.NewRequest(http.MethodGet, yol, nil))
	if ilk.StatusCode != http.StatusOK {
		t.Errorf("ilk baglanti = %d, 200 bekleniyordu", ilk.StatusCode)
	}
	ikinci, _ := app.Test(httptest.NewRequest(http.MethodGet, yol, nil))
	if ikinci.StatusCode != http.StatusUnauthorized {
		t.Errorf("ikinci baglanti = %d, 401 bekleniyordu", ikinci.StatusCode)
	}
}

func TestWSTicketHandler_BaskasinaBilet404(t *testing.T) {
	app := biletUygulamasi(t, "kullanici-b")
	resp, _ := biletAl(t, app)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, 404 bekleniyordu", resp.StatusCode)
	}
}

func TestWSTicketHandler_BiletsizBaglanti401(t *testing.T) {
	app := biletUygulamasi(t, "kullanici-a")
	resp, _ := app.Test(httptest.NewRequest(http.MethodGet, "/ws/jobs/"+biletIsID, nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, 401 bekleniyordu", resp.StatusCode)
	}
}
