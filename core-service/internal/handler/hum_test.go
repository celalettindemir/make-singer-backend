package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/service"
)

type humTestStorage struct{ deleted []string }

func (m *humTestStorage) Upload(ctx context.Context, key string, body io.Reader, ct string) (string, time.Time, error) {
	return "https://x/" + key, time.Time{}, nil
}
func (m *humTestStorage) Delete(_ context.Context, k string) error {
	m.deleted = append(m.deleted, k)
	return nil
}
func (m *humTestStorage) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (m *humTestStorage) URLFor(_ context.Context, k string) (string, time.Time, error) {
	return "https://x/" + k, time.Time{}, nil
}

type humTestPreparer struct{ ms int }

func (p humTestPreparer) PrepareHum(context.Context, *client.HumPrepareRequest) (*client.HumPrepareResponse, error) {
	return &client.HumPrepareResponse{DurationMs: p.ms}, nil
}

func humApp(ms int) (*fiber.App, *humTestStorage) {
	st := &humTestStorage{}
	svc := service.NewHumService(st, &config.R2Config{PublicBucket: "pub", PrivateBucket: "priv", PublicURL: "https://cdn"},
		humTestPreparer{ms}, nil, time.Second)
	h := NewHumHandler(svc)
	app := fiber.New(fiber.Config{BodyLimit: 20 * 1024 * 1024})
	app.Post("/hum", func(c *fiber.Ctx) error { c.Locals("userId", "user-1"); return h.Upload(c) })
	app.Delete("/hum/:humId", func(c *fiber.Ctx) error { c.Locals("userId", "user-1"); return h.Delete(c) })
	return app, st
}

func humRequest(t *testing.T, app *fiber.App, contentType string, size int) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("projectId", "p1")
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="hum.wav"`)
	header.Set("Content-Type", contentType)
	part, _ := w.CreatePart(header)
	_, _ = part.Write(make([]byte, size))
	_ = w.Close()
	req := httptest.NewRequest("POST", "/hum", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	return resp.StatusCode, got
}

func TestHumHandler_Upload_201(t *testing.T) {
	app, _ := humApp(12000)
	code, got := humRequest(t, app, "audio/wav", 4)
	if code != 201 {
		t.Fatalf("status = %d: %v", code, got)
	}
	if _, ok := got["transcript"]; !ok {
		t.Error("transcript key missing")
	}
	if got["durationMs"] != float64(12000) {
		t.Errorf("durationMs = %v", got["durationMs"])
	}
}

func TestHumHandler_Upload_TooLarge(t *testing.T) {
	app, _ := humApp(12000)
	code, got := humRequest(t, app, "audio/wav", 10*1024*1024+1)
	if code != 400 {
		t.Fatalf("status = %d", code)
	}
	d := got["error"].(map[string]any)["details"].(map[string]any)
	if d["maxSize"] != float64(10485760) {
		t.Errorf("details = %v", d)
	}
}

func TestHumHandler_Upload_BadType(t *testing.T) {
	app, _ := humApp(12000)
	code, got := humRequest(t, app, "text/plain", 4)
	if code != 400 || got["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("status = %d: %v", code, got)
	}
}

func TestHumHandler_Upload_Duration400(t *testing.T) {
	app, _ := humApp(3200)
	code, got := humRequest(t, app, "audio/wav", 4)
	if code != 400 {
		t.Fatalf("status = %d", code)
	}
	d := got["error"].(map[string]any)["details"].(map[string]any)
	if d["minMs"] != float64(5000) || d["maxMs"] != float64(30000) || d["durationMs"] != float64(3200) {
		t.Errorf("details = %v", d)
	}
}

func TestHumHandler_Delete_204(t *testing.T) {
	app, st := humApp(12000)
	resp, err := app.Test(httptest.NewRequest("DELETE", "/hum/0b8f2c1e-1111-4222-8333-444455556666?projectId=p1", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "hums/user-1/p1/0b8f2c1e-1111-4222-8333-444455556666.mp3" {
		t.Errorf("deleted = %v", st.deleted)
	}
}

func TestHumHandler_Delete_MissingProject400(t *testing.T) {
	app, _ := humApp(12000)
	resp, err := app.Test(httptest.NewRequest("DELETE", "/hum/abc", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

type langProbe struct{ got []string }

func (p *langProbe) Transcribe(_ context.Context, _ []byte, _ string, language string) (*client.Transcription, error) {
	p.got = append(p.got, language)
	return &client.Transcription{Text: "hello there world"}, nil
}
func (p *langProbe) IsConfigured() bool { return true }

func humLangRequest(t *testing.T, app *fiber.App, language string) int {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("projectId", "p1")
	if language != "" {
		_ = w.WriteField("language", language)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="hum.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, _ := w.CreatePart(header)
	_, _ = part.Write([]byte("RIFF"))
	_ = w.Close()
	req := httptest.NewRequest("POST", "/hum", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

// An unknown language reaches the service as "" and is never sent to OpenAI.
func TestHumHandler_Upload_LanguageFiltering(t *testing.T) {
	probe := &langProbe{}
	svc := service.NewHumService(&humTestStorage{}, &config.R2Config{PublicBucket: "pub", PrivateBucket: "priv", PublicURL: "https://cdn"},
		humTestPreparer{12000}, probe, time.Second)
	h := NewHumHandler(svc)
	app := fiber.New()
	app.Post("/hum", func(c *fiber.Ctx) error { c.Locals("userId", "user-1"); return h.Upload(c) })
	for _, lang := range []string{"de", "TR", "tr", "fr", ""} {
		if code := humLangRequest(t, app, lang); code != 201 {
			t.Fatalf("language %q: status %d", lang, code)
		}
	}
	want := []string{"", "", "tr", "fr", ""}
	if !reflect.DeepEqual(probe.got, want) {
		t.Errorf("languages sent = %q, want %q", probe.got, want)
	}
}

func TestHumHandler_Delete_BadHumID400(t *testing.T) {
	app, st := humApp(12000)
	for _, id := range []string{"abc", "..%2Fx", "0b8f2c1e-1111-4222-8333"} {
		resp, err := app.Test(httptest.NewRequest("DELETE", "/hum/"+id+"?projectId=p1", nil))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if resp.StatusCode != 400 {
			t.Fatalf("%s: status = %d", id, resp.StatusCode)
		}
		if msg := got["error"].(map[string]any)["message"]; msg == nil || !strings.Contains(msg.(string), "humId") {
			t.Errorf("%s: no message in %v", id, got)
		}
	}
	if len(st.deleted) != 0 {
		t.Errorf("deleted = %v", st.deleted)
	}
}
