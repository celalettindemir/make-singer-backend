package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/service"
)

func signApp(userID string) *fiber.App {
	cfg := &config.R2Config{
		AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
		PublicURL: "https://makesinger-cdn.celalettindemir.dev", PresignTTL: time.Hour,
	}
	app := fiber.New()
	app.Post("/api/files/sign", asUser(userID), NewFileHandler(service.NewFileService(nil, cfg), validator.New()).Sign)
	return app
}

func signStatus(t *testing.T, app *fiber.App, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/files/sign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestFileHandler_Signs(t *testing.T) {
	status, body := signStatus(t, signApp("u1"),
		`{"urls":["https://makesinger-cdn.celalettindemir.dev/exports/u1/e1.mp3"]}`)
	if status != http.StatusOK || !strings.Contains(body, `"files"`) {
		t.Fatalf("durum=%d govde=%s", status, body)
	}
}

func TestFileHandler_Foreign404(t *testing.T) {
	status, _ := signStatus(t, signApp("u1"),
		`{"urls":["https://makesinger-cdn.celalettindemir.dev/exports/u2/e1.mp3"]}`)
	if status != http.StatusNotFound {
		t.Fatalf("durum=%d, beklenen 404", status)
	}
}

func TestFileHandler_InvalidInput400(t *testing.T) {
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = `"https://makesinger-cdn.celalettindemir.dev/exports/u1/e.mp3"`
	}
	for _, body := range []string{
		`{"urls":[]}`,
		`{}`,
		`{"urls":["https://kotu.ornek/exports/u1/e.mp3"]}`,
		`{"urls":[` + strings.Join(tooMany, ",") + `]}`,
	} {
		if status, _ := signStatus(t, signApp("u1"), body); status != http.StatusBadRequest {
			t.Errorf("%.40s... icin durum=%d, beklenen 400", body, status)
		}
	}
}

func TestFileHandler_Unauthenticated401(t *testing.T) {
	if status, _ := signStatus(t, signApp(""), `{"urls":["x"]}`); status != http.StatusUnauthorized {
		t.Fatalf("durum=%d, beklenen 401", status)
	}
}
