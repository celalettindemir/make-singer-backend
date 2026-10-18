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
	return credit.Account{UserID: userID, StoreToken: uuid.New()}, nil
}

func (creditsLedger) Lots(context.Context, string, time.Time) ([]credit.Lot, error) {
	return []credit.Lot{{Source: credit.LotPack, Amount: 5}}, nil
}

func (creditsLedger) Gift(context.Context, string, time.Time) (*credit.Lot, error) { return nil, nil }

func (creditsLedger) LatestSubscription(context.Context, string) (*credit.Subscription, error) {
	return nil, nil
}

func (creditsLedger) History(_ context.Context, _, cursor string, filter credit.HistoryFilter) (credit.Page, error) {
	if cursor == "zzz" {
		return credit.Page{}, credit.ErrBadCursor
	}
	if filter == credit.FilterSpend {
		return credit.Page{}, nil
	}
	return credit.Page{Items: []credit.Movement{{ID: 3, Tur: "paket", Amount: 100, CreatedAt: time.Unix(0, 0).UTC()}}}, nil
}

func newCreditsApp(userID string) *fiber.App {
	svc := credit.NewService(creditsLedger{}, credit.Settings{
		PlanCredits: map[credit.Tier]int{credit.TierPlus: 300, credit.TierPro: 1000},
		Costs:       credit.Costs{RenderMix: 12, RenderFocus: 32, RenderStems: 62, LyricsGenerate: 2, LyricsRewrite: 1, Master: 3},
	})
	h := NewCreditsHandler(svc)
	app := fiber.New()
	g := app.Group("/api/credits", asUser(userID))
	g.Get("/", h.Get)
	g.Get("/history", h.History)
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
	want := []string{"balance", "costs", "debt", "enforced", "gift", "lots", "plan", "planCredits", "storeAccountToken"}
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
	if costs["render"].(map[string]any)["stems"] != float64(62) || costs["lyrics"].(map[string]any)["rewrite"] != float64(1) || costs["master"] != float64(3) {
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
	if item["id"] != "mv_3" || item["kind"] != "pack" || item["operation"] != nil || item["projectId"] != nil || item["tier"] != nil || item["amount"] != float64(100) {
		t.Fatalf("item = %v", item)
	}
}

func TestCreditsHandler_HistoryBadCursor400(t *testing.T) {
	status, body := creditsRequest(t, newCreditsApp("u1"), http.MethodGet, "/api/credits/history?cursor=zzz")
	if status != http.StatusBadRequest || errorCode(body) != response.CodeValidationError {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestCreditsHandler_HistoryKindBadRequest(t *testing.T) {
	status, body := creditsRequest(t, newCreditsApp("u1"), http.MethodGet, "/api/credits/history?kind=all")
	if status != http.StatusBadRequest || errorCode(body) != response.CodeValidationError {
		t.Fatalf("status=%d body=%v", status, body)
	}
	status, body = creditsRequest(t, newCreditsApp("u1"), http.MethodGet, "/api/credits/history?kind=spend")
	if items, _ := body["items"].([]any); status != http.StatusOK || len(items) != 0 {
		t.Fatalf("spend: status=%d body=%v", status, body)
	}
}

func TestCreditsHandler_UnauthorizedWithoutUser(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/credits/"}, {http.MethodGet, "/api/credits/history"},
	} {
		status, body := creditsRequest(t, newCreditsApp(""), tc.method, tc.path)
		if status != http.StatusUnauthorized || errorCode(body) != response.CodeUnauthorized {
			t.Fatalf("%s %s: status=%d body=%v", tc.method, tc.path, status, body)
		}
	}
}

func TestInsufficientCredits_Envelope(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c *fiber.Ctx) error { return response.InsufficientCredits(c, 62, 10, 70) })
	status, body := creditsRequest(t, app, http.MethodGet, "/x")
	if status != http.StatusPaymentRequired || errorCode(body) != "INSUFFICIENT_CREDITS" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	e := body["error"].(map[string]any)
	d := e["details"].(map[string]any)
	if d["required"] != float64(62) || d["balance"] != float64(10) || d["debt"] != float64(70) || e["message"] != "Yeterli krediniz yok." {
		t.Fatalf("error = %v", e)
	}
}
