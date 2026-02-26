package e2e

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	"github.com/google/uuid"
)

// createMultipartVocalRequest builds a multipart/form-data request with a fake audio file.
func createMultipartVocalRequest(t *testing.T, token string) *http.Request {
	t.Helper()

	projectID := uuid.New().String()
	sectionID := uuid.New().String()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	_ = writer.WriteField("projectId", projectID)
	_ = writer.WriteField("sectionId", sectionID)
	_ = writer.WriteField("takeName", "Take 1")

	// Create a fake WAV file with correct Content-Type
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="vocal.wav"`)
	partHeader.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	// Minimal WAV header + some data
	wavHeader := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	fakeData := make([]byte, 1024)
	_, _ = part.Write(wavHeader)
	_, _ = part.Write(fakeData)

	writer.Close()

	req, err := http.NewRequest(http.MethodPost, "/api/upload/vocal", &buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req
}

func TestUploadVocal_Success(t *testing.T) {
	ta := setupApp(t)

	token := generateToken(t)
	req := createMultipartVocalRequest(t, token)

	resp, err := ta.app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusCreated)

	result := parseJSON(t, resp)
	if result["id"] == nil || result["id"] == "" {
		t.Error("expected 'id' in response")
	}
	if result["fileUrl"] == nil || result["fileUrl"] == "" {
		t.Error("expected 'fileUrl' in response")
	}
}

func TestUploadVocal_NoAuth(t *testing.T) {
	ta := setupApp(t)

	req := createMultipartVocalRequest(t, "")

	resp, err := ta.app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestUploadVocal_MissingFile(t *testing.T) {
	ta := setupApp(t)

	token := generateToken(t)
	projectID := uuid.New().String()
	sectionID := uuid.New().String()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("projectId", projectID)
	_ = writer.WriteField("sectionId", sectionID)
	writer.Close()

	req, _ := http.NewRequest(http.MethodPost, "/api/upload/vocal", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := ta.app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusBadRequest)
}

func TestDeleteVocal_Success(t *testing.T) {
	ta := setupApp(t)

	takeID := uuid.New().String()
	path := fmt.Sprintf("/api/upload/vocal/%s", takeID)

	resp, err := doAuthRequest(t, ta.app, http.MethodDelete, path, "")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusNoContent)
}
