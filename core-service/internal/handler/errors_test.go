package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
)

// TestInternalAndAIError_DoNotLeakErrorText: the service error text stays in
// the log; the body carries only the generic message and the fixed code.
func TestInternalAndAIError_DoNotLeakErrorText(t *testing.T) {
	const secret = "secret-db-dsn leaked"
	tests := []struct {
		name   string
		fn     func(*fiber.Ctx, error) error
		status int
		code   string
		key    i18n.Key
	}{
		{"internalError", internalError, http.StatusInternalServerError, "SERVICE_ERROR", i18n.KeyInternal},
		{"aiError", aiError, http.StatusBadGateway, "AI_ERROR", i18n.KeyAIUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/x", func(c *fiber.Ctx) error { return tt.fn(c, errors.New(secret)) })
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(raw), "secret") {
				t.Fatalf("body leaks the error text: %s", raw)
			}
			var body struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.status || body.Error.Code != tt.code || body.Error.Message != i18n.Message(i18n.TR, tt.key) {
				t.Fatalf("got %d %+v", resp.StatusCode, body.Error)
			}
		})
	}
}
