package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/pkg/response"
)

// creditsLedger is a minimal in-memory credit.Ledger for handler tests.
type creditsLedger struct{ credit.Ledger }

func (creditsLedger) EnsureAccount(_ context.Context, userID string) (credit.Account, error) {
	return credit.Account{UserID: userID, Permanent: 5, StoreToken: uuid.New()}, nil
}

func (creditsLedger) LatestSubscription(context.Context, string) (*credit.Subscription, error) {
	return nil, nil
}

func (creditsLedger) Credit(_ context.Context, userID, _, _, _ string, _ int) (credit.Account, bool, error) {
	return credit.Account{UserID: userID}, false, nil
}

func (creditsLedger) History(context.Context, string, int64, int) ([]credit.Movement, error) {
	return []credit.Movement{{ID: 3, Tur: "paket", Amount: 100, CreatedAt: time.Unix(0, 0).UTC()}}, nil
}

func newCreditsApp(userID string) *fiber.App {
	svc := credit.NewService(creditsLedger{}, credit.Settings{
		Welcome: 30, Costs: credit.Costs{RenderMix: 12, RenderFocus: 32, RenderStems: 62, LyricsGenerate: 2, LyricsRewrite: 1},
	})
	h := NewCreditsHandler(svc)
	app := fiber.New()
	g := app.Group("/api/credits", asUser(userID))
	g.Get("/", h.Get)
	g.Get("/history", h.History)
	g.Post("/welcome", h.ClaimWelcome)
	return app
}

func creditsRequest(t *testing.T, app *fiber.App, method, path string) (int, map[string]any) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body %q: %v", raw, err)
	}
	return resp.StatusCode, out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestCreditsHandler_GetReturnsSummaryJSON(t *testing.T) {
	status, body := creditsRequest(t, newCreditsApp("u1"), http.MethodGet, "/api/credits/")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	want := []string{"balance", "costs", "enforced", "permanentCredits", "plan", "planCredits", "storeAccountToken", "welcome"}
	got := sortedKeys(body)
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
	if body["plan"] != nil || body["balance"] != float64(5) || body["enforced"] != false {
		t.Fatalf("body = %v", body)
	}
	costs := body["costs"].(map[string]any)
	if costs["render"].(map[string]any)["stems"] != float64(62) || costs["lyrics"].(map[string]any)["rewrite"] != float64(1) {
		t.Fatalf("costs = %v", costs)
	}
}

func TestCreditsHandler_HistoryPageJSON(t *testing.T) {
	status, body := creditsRequest(t, newCreditsApp("u1"), http.MethodGet, "/api/credits/history")
	items, _ := body["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || body["nextCursor"] != nil {
		t.Fatalf("status=%d body=%v", status, body)
	}
	item := items[0].(map[string]any)
	if item["id"] != "3" || item["kind"] != "pack" || item["operation"] != nil || item["amount"] != float64(100) {
		t.Fatalf("item = %v", item)
	}
}

func TestCreditsHandler_HistoryBadCursor400(t *testing.T) {
	status, body := creditsRequest(t, newCreditsApp("u1"), http.MethodGet, "/api/credits/history?cursor=zzz")
	if status != http.StatusBadRequest || errorCode(body) != response.CodeValidationError {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestCreditsHandler_ClaimWelcomeReturnsSummary(t *testing.T) {
	status, body := creditsRequest(t, newCreditsApp("u1"), http.MethodPost, "/api/credits/welcome")
	if status != http.StatusOK || body["storeAccountToken"] == nil {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestCreditsHandler_UnauthorizedWithoutUser(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/credits/"}, {http.MethodGet, "/api/credits/history"}, {http.MethodPost, "/api/credits/welcome"},
	} {
		status, body := creditsRequest(t, newCreditsApp(""), tc.method, tc.path)
		if status != http.StatusUnauthorized || errorCode(body) != response.CodeUnauthorized {
			t.Fatalf("%s %s: status=%d body=%v", tc.method, tc.path, status, body)
		}
	}
}

func TestInsufficientCredits_Envelope(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c *fiber.Ctx) error { return response.InsufficientCredits(c, 62, 10) })
	status, body := creditsRequest(t, app, http.MethodGet, "/x")
	if status != http.StatusPaymentRequired || errorCode(body) != "INSUFFICIENT_CREDITS" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	e := body["error"].(map[string]any)
	d := e["details"].(map[string]any)
	if d["required"] != float64(62) || d["balance"] != float64(10) || e["message"] != "Yeterli krediniz yok." {
		t.Fatalf("error = %v", e)
	}
}
