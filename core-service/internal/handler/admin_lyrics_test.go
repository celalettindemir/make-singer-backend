package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/middleware"
)

// Fixtures: native/api/examples getLyricsProvider.200.json, setLyricsProvider.400.json.
const (
	exLyricsProvider       = `{"provider": "anthropic", "available": ["anthropic", "groq", "openai"]}`
	exSetLyricsProvider400 = `{"error": {"code": "VALIDATION_ERROR", "message": "Validation failed.", "details": {"provider": "unavailable"}}}`
)

type fakeLyricsProvider struct {
	active lyricsprovider.Name
	setTo  lyricsprovider.Name
	setBy  string
	err    error
}

func (f *fakeLyricsProvider) Active(context.Context) (lyricsprovider.Name, client.ChatClient) {
	return f.active, nil
}

func (f *fakeLyricsProvider) Available() []lyricsprovider.Name {
	return []lyricsprovider.Name{lyricsprovider.Anthropic, lyricsprovider.Groq, lyricsprovider.OpenAI}
}

func (f *fakeLyricsProvider) Set(_ context.Context, name lyricsprovider.Name, by string) error {
	if f.err != nil {
		return f.err
	}
	f.setTo, f.setBy, f.active = name, by, name
	return nil
}

func newLyricsAdminApp(p *fakeLyricsProvider) *fiber.App {
	app := fiber.New()
	g := app.Group("/api/admin", func(c *fiber.Ctx) error {
		c.Locals(middleware.LocalAdminEmail, "celal258@gmail.com")
		return c.Next()
	})
	NewAdminHandler(&fakeCampaigns{}, &fakeAdmin{}).WithLyricsProvider(p).Routes(g)
	return app
}

func TestAdmin_GetLyricsProviderMatchesExample(t *testing.T) {
	code, body := adminDo(t, newLyricsAdminApp(&fakeLyricsProvider{active: lyricsprovider.Anthropic}), http.MethodGet, "/api/admin/lyrics-provider", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exLyricsProvider)
}

func TestAdmin_SetLyricsProvider(t *testing.T) {
	p := &fakeLyricsProvider{active: lyricsprovider.Groq}
	code, body := adminDo(t, newLyricsAdminApp(p), http.MethodPut, "/api/admin/lyrics-provider", `{"provider": "anthropic"}`)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exLyricsProvider)
	if p.setTo != lyricsprovider.Anthropic || p.setBy != "celal258@gmail.com" {
		t.Errorf("Set(%q) by %q", p.setTo, p.setBy)
	}
}

func TestAdmin_SetLyricsProviderUnavailableIs400(t *testing.T) {
	p := &fakeLyricsProvider{err: lyricsprovider.ErrUnavailable}
	code, body := adminDo(t, newLyricsAdminApp(p), http.MethodPut, "/api/admin/lyrics-provider", `{"provider": "anthropic"}`)
	if code != 400 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exSetLyricsProvider400)
}

func TestAdmin_SetLyricsProviderBadBodiesAre400(t *testing.T) {
	cases := map[string]string{
		`{"provider": "gemini"}`:         "provider",
		`{"provider": "openai", "x": 1}`: "body",
		`{}`:                             "provider",
		`not json`:                       "body",
	}
	for body, field := range cases {
		p := &fakeLyricsProvider{}
		code, out := adminDo(t, newLyricsAdminApp(p), http.MethodPut, "/api/admin/lyrics-provider", body)
		if code != 400 || adminErrorCode(t, out) != "VALIDATION_ERROR" {
			t.Errorf("%s: status %d %s", body, code, out)
		}
		if p.setTo != "" {
			t.Errorf("%s: Set must not be called", body)
		}
		if _, ok := adminDetails(t, out)[field]; !ok {
			t.Errorf("%s: details %v lack %q", body, adminDetails(t, out), field)
		}
	}
}

func TestAdmin_SetLyricsProviderStoreFailureIs500(t *testing.T) {
	p := &fakeLyricsProvider{err: errors.New("db down")}
	code, _ := adminDo(t, newLyricsAdminApp(p), http.MethodPut, "/api/admin/lyrics-provider", `{"provider": "openai"}`)
	if code != 500 {
		t.Fatalf("status %d, want 500", code)
	}
}

func TestAdmin_LyricsProviderRoutesAbsentWithoutProvider(t *testing.T) {
	app := fiber.New()
	NewAdminHandler(nil, nil).Routes(app.Group("/api/admin"))
	for _, r := range app.GetRoutes() {
		if r.Path == "/api/admin/lyrics-provider" {
			t.Errorf("route %s %s bound without a provider", r.Method, r.Path)
		}
	}
}

func adminDetails(t *testing.T, body string) map[string]any {
	t.Helper()
	var e struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return e.Error.Details
}
