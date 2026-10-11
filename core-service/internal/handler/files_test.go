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

func imzaUygulamasi(userID string) *fiber.App {
	cfg := &config.R2Config{
		AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
		PublicURL: "https://makesinger-cdn.celalettindemir.dev", PresignTTL: time.Hour,
	}
	app := fiber.New()
	app.Post("/api/files/sign", kullaniciOlarak(userID), NewFileHandler(service.NewFileService(nil, cfg), validator.New()).Sign)
	return app
}

func imzaDurum(t *testing.T, app *fiber.App, govde string) (int, string) {
	t.Helper()
	istek := httptest.NewRequest(http.MethodPost, "/api/files/sign", strings.NewReader(govde))
	istek.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(istek)
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestFileHandler_Imzalar(t *testing.T) {
	durum, govde := imzaDurum(t, imzaUygulamasi("u1"),
		`{"urls":["https://makesinger-cdn.celalettindemir.dev/exports/u1/e1.mp3"]}`)
	if durum != http.StatusOK || !strings.Contains(govde, `"files"`) {
		t.Fatalf("durum=%d govde=%s", durum, govde)
	}
}

func TestFileHandler_Yabanci404(t *testing.T) {
	durum, _ := imzaDurum(t, imzaUygulamasi("u1"),
		`{"urls":["https://makesinger-cdn.celalettindemir.dev/exports/u2/e1.mp3"]}`)
	if durum != http.StatusNotFound {
		t.Fatalf("durum=%d, beklenen 404", durum)
	}
}

func TestFileHandler_GecersizGirdi400(t *testing.T) {
	cok := make([]string, 51)
	for i := range cok {
		cok[i] = `"https://makesinger-cdn.celalettindemir.dev/exports/u1/e.mp3"`
	}
	for _, govde := range []string{
		`{"urls":[]}`,
		`{}`,
		`{"urls":["https://kotu.ornek/exports/u1/e.mp3"]}`,
		`{"urls":[` + strings.Join(cok, ",") + `]}`,
	} {
		if durum, _ := imzaDurum(t, imzaUygulamasi("u1"), govde); durum != http.StatusBadRequest {
			t.Errorf("%.40s... icin durum=%d, beklenen 400", govde, durum)
		}
	}
}

func TestFileHandler_Kimliksiz401(t *testing.T) {
	if durum, _ := imzaDurum(t, imzaUygulamasi(""), `{"urls":["x"]}`); durum != http.StatusUnauthorized {
		t.Fatalf("durum=%d, beklenen 401", durum)
	}
}
