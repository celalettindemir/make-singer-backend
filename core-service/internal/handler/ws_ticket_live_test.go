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

const ticketJobID = "0b6f3c1e-8a52-4d0e-9a51-6d3f8f2b7c11"

func ticketApp(t *testing.T, userID string) *fiber.App {
	t.Helper()
	rdb := liveRedis(t)
	raw, _ := json.Marshal(model.Job{ID: ticketJobID, OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	if err := rdb.Set(context.Background(), "job:"+ticketJobID, raw, time.Hour).Err(); err != nil {
		t.Fatalf("is yazilamadi: %v", err)
	}
	h := NewWSTicketHandler(service.NewWSTicketService(rdb), validator.New())
	app := fiber.New()
	app.Post("/api/ws/ticket", asUser(userID), h.Issue)
	// In the real route websocket.New follows Gate; here the decision of Gate
	// is measured without an upgrade.
	app.Get("/ws/jobs/:jobId", h.Gate, func(c *fiber.Ctx) error { return c.SendString("gecti") })
	return app
}

func fetchTicket(t *testing.T, app *fiber.App) (*http.Response, model.WSTicketResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/ws/ticket", strings.NewReader(`{"jobId":"`+ticketJobID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("bilet istegi: %v", err)
	}
	var b model.WSTicketResponse
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &b)
	return resp, b
}

func TestWSTicketHandler_OwnerConnectsWithTicket(t *testing.T) {
	app := ticketApp(t, "kullanici-a")
	resp, b := fetchTicket(t, app)
	if resp.StatusCode != http.StatusOK || b.Ticket == "" {
		t.Fatalf("bilet = %d %+v", resp.StatusCode, b)
	}

	path := "/ws/jobs/" + ticketJobID + "?ticket=" + b.Ticket
	first, _ := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if first.StatusCode != http.StatusOK {
		t.Errorf("ilk baglanti = %d, 200 bekleniyordu", first.StatusCode)
	}
	second, _ := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if second.StatusCode != http.StatusUnauthorized {
		t.Errorf("ikinci baglanti = %d, 401 bekleniyordu", second.StatusCode)
	}
}

func TestWSTicketHandler_OtherUserTicket404(t *testing.T) {
	app := ticketApp(t, "kullanici-b")
	resp, _ := fetchTicket(t, app)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, 404 bekleniyordu", resp.StatusCode)
	}
}

func TestWSTicketHandler_NoTicket401(t *testing.T) {
	app := ticketApp(t, "kullanici-a")
	resp, _ := app.Test(httptest.NewRequest(http.MethodGet, "/ws/jobs/"+ticketJobID, nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, 401 bekleniyordu", resp.StatusCode)
	}
}
