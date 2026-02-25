package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestExportMP3_Success(t *testing.T) {
	ta := setupApp(t)

	projectID := uuid.New().String()
	body := fmt.Sprintf(`{
		"projectId": "%s",
		"masterFileUrl": "https://cdn.example.com/master/final.wav",
		"quality": 320
	}`, projectID)

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/export/mp3", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	if result["fileUrl"] == nil || result["fileUrl"] == "" {
		t.Error("expected 'fileUrl' in response")
	}
	if result["format"] != "mp3" {
		t.Errorf("expected format 'mp3', got %v", result["format"])
	}
	// quality is returned as float64 from JSON
	if result["quality"] != float64(320) {
		t.Errorf("expected quality 320, got %v", result["quality"])
	}
}

func TestExportMP3_NoAuth(t *testing.T) {
	ta := setupApp(t)

	projectID := uuid.New().String()
	body := fmt.Sprintf(`{
		"projectId": "%s",
		"masterFileUrl": "https://cdn.example.com/master/final.wav"
	}`, projectID)

	resp, err := doRequest(ta.app, http.MethodPost, "/api/export/mp3", body, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestExportMP3_InvalidBody(t *testing.T) {
	ta := setupApp(t)

	// Missing masterFileUrl
	body := `{"projectId": "not-a-uuid"}`

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/export/mp3", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusBadRequest)
}

func TestExportWAV_Success(t *testing.T) {
	ta := setupApp(t)

	projectID := uuid.New().String()
	body := fmt.Sprintf(`{
		"projectId": "%s",
		"masterFileUrl": "https://cdn.example.com/master/final.wav",
		"bitDepth": 24,
		"sampleRate": 48000
	}`, projectID)

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/export/wav", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	if result["format"] != "wav" {
		t.Errorf("expected format 'wav', got %v", result["format"])
	}
	if result["bitDepth"] != float64(24) {
		t.Errorf("expected bitDepth 24, got %v", result["bitDepth"])
	}
	if result["sampleRate"] != float64(48000) {
		t.Errorf("expected sampleRate 48000, got %v", result["sampleRate"])
	}
}

func TestExportStems_Success(t *testing.T) {
	ta := setupApp(t)

	projectID := uuid.New().String()
	body := fmt.Sprintf(`{
		"projectId": "%s",
		"stemUrls": [
			"https://cdn.example.com/stems/drums.wav",
			"https://cdn.example.com/stems/bass.wav",
			"https://cdn.example.com/stems/piano.wav"
		]
	}`, projectID)

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/export/stems", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	if result["fileUrl"] == nil || result["fileUrl"] == "" {
		t.Error("expected 'fileUrl' in response")
	}
	fileCount, ok := result["fileCount"].(float64)
	if !ok || fileCount < 1 {
		t.Errorf("expected fileCount >= 1, got %v", result["fileCount"])
	}
}
