package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
)

// fakeDevices mimics the store: token is the key, the last writer owns it.
type fakeDevices struct {
	rows map[string]struct {
		owner string
		dev   model.Device
	}
}

func (f *fakeDevices) UpsertDevice(_ context.Context, userID string, d model.Device) error {
	f.rows[d.Token] = struct {
		owner string
		dev   model.Device
	}{userID, d}
	return nil
}

func (f *fakeDevices) DeleteDevice(_ context.Context, userID, token string) error {
	if r, ok := f.rows[token]; ok && r.owner == userID {
		delete(f.rows, token)
	}
	return nil
}

func devicesApp(store *fakeDevices, userID string) *fiber.App {
	h := NewDevicesHandler(store, validator.New())
	app := fiber.New()
	app.Put("/devices", asUser(userID), h.Put)
	app.Delete("/devices/:token", asUser(userID), h.Delete)
	return app
}

func doDevices(t *testing.T, app *fiber.App, method, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

const okDevice = `{"token":"tok:1","platform":"android","locale":"fr","appVersion":"1.2.3"}`

func newFakeDevices() *fakeDevices {
	return &fakeDevices{rows: map[string]struct {
		owner string
		dev   model.Device
	}{}}
}

func TestDevicesHandler_PutUpsertsAndMovesToken(t *testing.T) {
	store := newFakeDevices()
	if code := doDevices(t, devicesApp(store, "alice"), http.MethodPut, "/devices", okDevice); code != 204 {
		t.Fatalf("alice: %d", code)
	}
	r := store.rows["tok:1"]
	if r.owner != "alice" || r.dev.Platform != "android" || r.dev.Language != "fr" || r.dev.AppVersion != "1.2.3" {
		t.Errorf("row = %+v", r)
	}
	if code := doDevices(t, devicesApp(store, "bob"), http.MethodPut, "/devices", okDevice); code != 204 {
		t.Fatalf("bob: %d", code)
	}
	if store.rows["tok:1"].owner != "bob" {
		t.Errorf("token did not move: %+v", store.rows["tok:1"])
	}
}

func TestDevicesHandler_PutValidates(t *testing.T) {
	app := devicesApp(newFakeDevices(), "alice")
	bad := []string{
		`{"token":"","platform":"ios","locale":"tr","appVersion":"1"}`,
		`{"token":"t","platform":"web","locale":"tr","appVersion":"1"}`,
		`{"token":"t","platform":"ios","locale":"de","appVersion":"1"}`,
		`{"token":"t","platform":"ios","locale":"tr"}`,
		`{"token":"t","platform":"ios","locale":"tr","appVersion":"` + strings.Repeat("x", 33) + `"}`,
		`{"token":"` + strings.Repeat("x", 4097) + `","platform":"ios","locale":"tr","appVersion":"1"}`,
		`not json`,
	}
	for _, b := range bad {
		if code := doDevices(t, app, http.MethodPut, "/devices", b); code != 400 {
			t.Errorf("%.40s: status %d, want 400", b, code)
		}
	}
	if code := doDevices(t, app, http.MethodPut, "/devices", `{"token":"t","platform":"ios","locale":"tr","appVersion":""}`); code != 204 {
		t.Errorf("empty appVersion: %d", code)
	}
}

func TestDevicesHandler_RequiresUser(t *testing.T) {
	app := devicesApp(newFakeDevices(), "")
	if code := doDevices(t, app, http.MethodPut, "/devices", okDevice); code != 401 {
		t.Errorf("put: %d", code)
	}
	if code := doDevices(t, app, http.MethodDelete, "/devices/x", ""); code != 401 {
		t.Errorf("delete: %d", code)
	}
}

func TestDevicesHandler_DeleteOtherUsersTokenNoop(t *testing.T) {
	store := newFakeDevices()
	doDevices(t, devicesApp(store, "alice"), http.MethodPut, "/devices", okDevice)
	if code := doDevices(t, devicesApp(store, "bob"), http.MethodDelete, "/devices/tok:1", ""); code != 204 {
		t.Fatalf("status %d", code)
	}
	if _, ok := store.rows["tok:1"]; !ok {
		t.Fatal("bob removed alice's token")
	}
	// URL-encoded token, own: removed. Unknown: still 204.
	if code := doDevices(t, devicesApp(store, "alice"), http.MethodDelete, "/devices/tok%3A1", ""); code != 204 {
		t.Fatalf("status %d", code)
	}
	if _, ok := store.rows["tok:1"]; ok {
		t.Error("alice's encoded-token delete did not remove the row")
	}
	if code := doDevices(t, devicesApp(store, "alice"), http.MethodDelete, "/devices/nope", ""); code != 204 {
		t.Errorf("unknown token: %d", code)
	}
}
