package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
)

type fakeVocalRanges struct {
	rows  map[string]model.VocalRangeRecord
	saves int
}

func (f *fakeVocalRanges) GetVocalRange(_ context.Context, userID string) (*model.VocalRangeRecord, error) {
	if r, ok := f.rows[userID]; ok {
		return &r, nil
	}
	return nil, nil
}

func (f *fakeVocalRanges) SaveVocalRange(_ context.Context, userID string, r model.VocalRangeRecord) error {
	f.saves++
	f.rows[userID] = r
	return nil
}

func vocalRangeApp(store *fakeVocalRanges, userID string) *fiber.App {
	h := NewVocalRangeHandler(store, validator.New())
	app := fiber.New()
	app.Get("/profile/vocal-range", asUser(userID), h.Get)
	app.Put("/profile/vocal-range", asUser(userID), h.Put)
	return app
}

func doVocalRange(t *testing.T, app *fiber.App, method, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, "/profile/vocal-range", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const okRange = `{"low":45,"high":64,"comfortCenter":55,"measuredAt":"2026-11-12T10:00:00Z","source":"measured"}`

func newFakeVocalRanges() *fakeVocalRanges {
	return &fakeVocalRanges{rows: map[string]model.VocalRangeRecord{}}
}

func TestVocalRange_GetNoneIsNull(t *testing.T) {
	code, body := doVocalRange(t, vocalRangeApp(newFakeVocalRanges(), "u1"), http.MethodGet, "")
	if code != 200 || strings.ReplaceAll(body, " ", "") != `{"range":null}` {
		t.Errorf("got %d %s", code, body)
	}
}

func TestVocalRange_PutThenGet(t *testing.T) {
	app := vocalRangeApp(newFakeVocalRanges(), "u1")
	code, body := doVocalRange(t, app, http.MethodPut, okRange)
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	var put, get struct {
		Range model.VocalRangeRecord `json:"range"`
	}
	if err := json.Unmarshal([]byte(body), &put); err != nil || put.Range.High != 64 || put.Range.Source != "measured" {
		t.Errorf("put echo: %s", body)
	}
	code, body = doVocalRange(t, app, http.MethodGet, "")
	if code != 200 {
		t.Fatalf("get: %d", code)
	}
	if err := json.Unmarshal([]byte(body), &get); err != nil || get.Range != put.Range {
		t.Errorf("get = %+v, want %+v", get.Range, put.Range)
	}
}

func TestVocalRange_PutRejectsInvertedRange(t *testing.T) {
	store := newFakeVocalRanges()
	code, body := doVocalRange(t, vocalRangeApp(store, "u1"), http.MethodPut,
		`{"low":45,"high":40,"comfortCenter":42,"measuredAt":"2026-11-12T10:00:00Z","source":"measured"}`)
	if code != 400 || !strings.Contains(body, "VALIDATION_ERROR") || store.saves != 0 {
		t.Errorf("got %d %s saves=%d", code, body, store.saves)
	}
}

func TestVocalRange_PutRejectsCenterOutside(t *testing.T) {
	store := newFakeVocalRanges()
	code, body := doVocalRange(t, vocalRangeApp(store, "u1"), http.MethodPut,
		`{"low":45,"high":64,"comfortCenter":70,"measuredAt":"2026-11-12T10:00:00Z","source":"measured"}`)
	if code != 400 || !strings.Contains(body, `"comfortCenter":"range"`) || store.saves != 0 {
		t.Errorf("got %d %s saves=%d", code, body, store.saves)
	}
}

func TestVocalRange_PutRejectsUnknownSource(t *testing.T) {
	store := newFakeVocalRanges()
	code, _ := doVocalRange(t, vocalRangeApp(store, "u1"), http.MethodPut,
		`{"low":45,"high":64,"comfortCenter":55,"measuredAt":"2026-11-12T10:00:00Z","source":"guess"}`)
	if code != 400 || store.saves != 0 {
		t.Errorf("got %d saves=%d", code, store.saves)
	}
}

func TestVocalRange_NoOwnerIs401(t *testing.T) {
	app := vocalRangeApp(newFakeVocalRanges(), "")
	if code, _ := doVocalRange(t, app, http.MethodGet, ""); code != 401 {
		t.Errorf("get: %d", code)
	}
	if code, _ := doVocalRange(t, app, http.MethodPut, okRange); code != 401 {
		t.Errorf("put: %d", code)
	}
}
