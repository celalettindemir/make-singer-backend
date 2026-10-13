package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/identity"
	"github.com/makeasinger/api/internal/service"
	"github.com/redis/go-redis/v9"
)

type hSahteKimlik struct{ hata error }

func (k hSahteKimlik) DeleteAccount(ctx context.Context, userID string) error { return k.hata }

type hBosKuyruk struct{}

func (hBosKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{}, nil
}

func hesapUygulamasi(kimlikHata error, userID string) *fiber.App {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	svc := service.NewAccountService(hSahteKimlik{hata: kimlikHata}, nil, rdb, hBosKuyruk{}, nil)
	app := fiber.New()
	app.Delete("/api/account", kullaniciOlarak(userID), NewHesapHandler(svc).Sil)
	return app
}

func hesapSilDurum(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodDelete, "/api/account", nil))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	return resp.StatusCode
}

func TestHesapHandler_Sil204(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(nil, "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusNoContent {
		t.Fatalf("durum = %d, beklenen 204", got)
	}
}

func TestHesapHandler_Yok404(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(identity.ErrUserNotFound, "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusNotFound {
		t.Fatalf("durum = %d, beklenen 404", got)
	}
}

func TestHesapHandler_KimliksizIstek401(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(nil, "")); got != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401", got)
	}
}

func TestHesapHandler_KimlikHatasi500(t *testing.T) {
	if got := hesapSilDurum(t, hesapUygulamasi(errors.New("pg"), "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusInternalServerError {
		t.Fatalf("durum = %d, beklenen 500", got)
	}
}
