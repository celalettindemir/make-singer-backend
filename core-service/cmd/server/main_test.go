package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
)

func TestCustomErrorHandler(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		key    i18n.Key
	}{
		{"not found", fiber.ErrNotFound, 404, i18n.KeyRouteNotFound},
		{"method not allowed", fiber.ErrMethodNotAllowed, 405, i18n.KeyMethodNotAllowed},
		{"body too large", fiber.ErrRequestEntityTooLarge, 413, i18n.KeyBodyTooLarge},
		{"upgrade required", fiber.ErrUpgradeRequired, 426, i18n.KeyUpgradeRequired},
		{"other fiber error", fiber.NewError(fiber.StatusTeapot), 418, i18n.KeyRequestFailed},
		{"wrapped fiber error", fmt.Errorf("wrap: %w", fiber.ErrNotFound), 404, i18n.KeyRouteNotFound},
		{"non-fiber error", errors.New("secret-db-dsn leaked"), 500, i18n.KeyInternal},
		// social.ValidationError is rendered by the social handler, not here;
		// at this layer it is an unknown error and must not leak.
		{"social validation error", &social.ValidationError{Key: i18n.KeyFieldFileURLInvalid}, 500, i18n.KeyInternal},
	}
	langs := []struct {
		header string
		lang   i18n.Lang
	}{{"", i18n.TR}, {"en", i18n.EN}}

	for _, tt := range tests {
		for _, l := range langs {
			t.Run(tt.name+"/"+string(l.lang), func(t *testing.T) {
				app := fiber.New(fiber.Config{ErrorHandler: customErrorHandler})
				app.Get("/x", func(c *fiber.Ctx) error { return tt.err })
				req := httptest.NewRequest(http.MethodGet, "/x", nil)
				if l.header != "" {
					req.Header.Set("Accept-Language", l.header)
				}
				resp, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := io.ReadAll(resp.Body)
				var body struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatalf("body %s: %v", raw, err)
				}
				want := i18n.Message(l.lang, tt.key)
				if resp.StatusCode != tt.status || body.Error.Code != "SERVICE_ERROR" || body.Error.Message != want {
					t.Fatalf("got %d %q %q, want %d SERVICE_ERROR %q", resp.StatusCode, body.Error.Code, body.Error.Message, tt.status, want)
				}
			})
		}
	}
}
