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

type fakeIdentity struct{ err error }

func (f fakeIdentity) DeleteAccount(ctx context.Context, userID string) error { return f.err }

type nopQueue struct{}

func (nopQueue) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{}, nil
}

func accountApp(identityErr error, userID string) *fiber.App {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
	svc := service.NewAccountService(fakeIdentity{err: identityErr}, nil, rdb, nopQueue{}, nil)
	app := fiber.New()
	app.Delete("/api/account", asUser(userID), NewAccountHandler(svc).Delete)
	return app
}

func accountDeleteStatus(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodDelete, "/api/account", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp.StatusCode
}

func TestAccountHandler_Delete204(t *testing.T) {
	if got := accountDeleteStatus(t, accountApp(nil, "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got)
	}
}

func TestAccountHandler_NotFound404(t *testing.T) {
	if got := accountDeleteStatus(t, accountApp(identity.ErrUserNotFound, "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got)
	}
}

func TestAccountHandler_Unauthenticated401(t *testing.T) {
	if got := accountDeleteStatus(t, accountApp(nil, "")); got != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
}

func TestAccountHandler_IdentityError500(t *testing.T) {
	if got := accountDeleteStatus(t, accountApp(errors.New("pg"), "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a")); got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", got)
	}
}
