package response

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
)

func TestError_ShapeAndLanguage(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		return ValidationError(c, i18n.KeyFileTooLarge, map[string]any{"maxSize": 1})
	})
	for _, tt := range []struct{ header, lang, msg string }{
		{"", "tr", "Dosya boyutu 50 MB sınırını aşıyor."},
		{"en-GB,en;q=0.8", "en", "The file exceeds the 50 MB limit."},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Language", tt.header)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		var body map[string]map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		e := body["error"]
		if resp.StatusCode != http.StatusBadRequest || e["code"] != CodeValidationError || e["message"] != tt.msg || e["details"] == nil || len(e) != 3 {
			t.Errorf("%q: %d %s", tt.header, resp.StatusCode, raw)
		}
		if got := resp.Header.Get(fiber.HeaderContentLanguage); got != tt.lang {
			t.Errorf("%q: Content-Language = %q", tt.header, got)
		}
	}
}

func TestLang_CachedInLocals(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		first := Lang(c)
		c.Request().Header.Set(fiber.HeaderAcceptLanguage, "tr")
		if second := Lang(c); second != first {
			t.Errorf("second Lang = %q, want cached %q", second, first)
		}
		return c.SendString(string(first))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := io.ReadAll(resp.Body); string(raw) != "en" {
		t.Fatalf("Lang = %q", raw)
	}
}
