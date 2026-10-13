package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

func TestVocalContentTypes_AcceptsIOSWav(t *testing.T) {
	for _, ct := range []string{"audio/wav", "audio/vnd.wave", "audio/x-m4a"} {
		if !vocalContentTypes[ct] {
			t.Errorf("%s must be accepted", ct)
		}
	}
	for _, ct := range []string{"", "text/plain", "application/octet-stream"} {
		if vocalContentTypes[ct] {
			t.Errorf("%q must be rejected", ct)
		}
	}
}

func TestUploadHandlerVocal_RejectsNonAudioType(t *testing.T) {
	h := NewUploadHandler(nil, validator.New())
	app := fiber.New()
	app.Post("/upload", func(c *fiber.Ctx) error {
		c.Locals("userId", "user-1")
		return h.Vocal(c)
	})

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("projectId", "p1")
	_ = w.WriteField("sectionId", "s1")
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="take.txt"`)
	header.Set("Content-Type", "text/plain")
	part, _ := w.CreatePart(header)
	_, _ = part.Write([]byte("not audio"))
	_ = w.Close()

	req := httptest.NewRequest("POST", "/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var got struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Error.Details["contentType"] != "text/plain" {
		t.Errorf("details = %v, want contentType text/plain", got.Error.Details)
	}
}
