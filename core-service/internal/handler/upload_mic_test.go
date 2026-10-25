package handler

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/service"
)

type micStorage struct{ meta map[string]string }

func (m *micStorage) Upload(ctx context.Context, key string, body io.Reader, ct string) (string, time.Time, error) {
	return "https://x/" + key, time.Time{}, nil
}
func (m *micStorage) UploadWithMetadata(ctx context.Context, key string, body io.Reader, ct string, meta map[string]string) (string, time.Time, error) {
	m.meta = meta
	return m.Upload(ctx, key, body, ct)
}
func (m *micStorage) Delete(context.Context, string) error { return nil }
func (m *micStorage) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (m *micStorage) URLFor(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func uploadWithMic(t *testing.T, fields map[string]string) map[string]string {
	t.Helper()
	st := &micStorage{}
	svc := service.NewUploadService(st, &config.R2Config{PublicBucket: "pub", PrivateBucket: "priv", PublicURL: "https://cdn"})
	h := NewUploadHandler(svc, validator.New())
	app := fiber.New()
	app.Post("/upload", func(c *fiber.Ctx) error {
		c.Locals("userId", "user-1")
		return h.Vocal(c)
	})
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("projectId", "p1")
	_ = w.WriteField("sectionId", "s1")
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="take.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, _ := w.CreatePart(header)
	_, _ = part.Write([]byte("RIFF"))
	_ = w.Close()
	req := httptest.NewRequest("POST", "/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	return st.meta
}

func TestUploadVocal_MicFieldsPassed(t *testing.T) {
	m := uploadWithMic(t, map[string]string{"micKind": "bluetooth", "micName": "  AirPods Pro "})
	if m["mic-kind"] != "bluetooth" || m["mic-name"] != "AirPods%20Pro" {
		t.Errorf("meta = %v", m)
	}
}

func TestUploadVocal_UnknownMicKindIsOther(t *testing.T) {
	m := uploadWithMic(t, map[string]string{"micKind": "tin-can"})
	if m["mic-kind"] != "other" {
		t.Errorf("meta = %v", m)
	}
}

func TestUploadVocal_MicNameCleaned(t *testing.T) {
	long := strings.Repeat("ç", 100) + "\x00\n"
	m := uploadWithMic(t, map[string]string{"micName": "A\x07B\tC" + long})
	// control chars dropped, cut to 64 runes (not bytes)
	want := "ABC" + strings.Repeat("ç", 61)
	if got := cleanMicName("A\x07B\tC" + long); got != want {
		t.Errorf("cleanMicName = %q", got)
	}
	if m["mic-name"] == "" || strings.ContainsAny(m["mic-name"], "\x00\n\x07") {
		t.Errorf("meta = %v", m)
	}
}

func TestUploadVocal_NoMicFieldsNoMetadata(t *testing.T) {
	if m := uploadWithMic(t, nil); len(m) != 0 {
		t.Errorf("meta = %v, want none", m)
	}
	if m := uploadWithMic(t, map[string]string{"micKind": "", "micName": " \x01 "}); len(m) != 0 {
		t.Errorf("meta = %v, want none", m)
	}
}
