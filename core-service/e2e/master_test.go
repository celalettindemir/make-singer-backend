package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func validMasterPreviewBody(t *testing.T) string {
	projectID := uuid.New().String()
	stemID := uuid.New().String()
	return fmt.Sprintf(`{
		"projectId": "%s",
		"profile": "clean",
		"stemUrls": ["%s"],
		"mixSnapshot": {
			"channels": [
				{"stemId": "%s", "volumeDb": 0, "mute": false, "solo": false}
			],
			"preset": "default"
		}
	}`, projectID, e2eURL(t, "stems", testUserID, projectID, "drums.mp3"), stemID)
}

func validMasterFinalBody(t *testing.T) string {
	projectID := uuid.New().String()
	stemID := uuid.New().String()
	return fmt.Sprintf(`{
		"projectId": "%s",
		"profile": "warm",
		"stemUrls": ["%s", "%s"],
		"mixSnapshot": {
			"channels": [
				{"stemId": "%s", "volumeDb": -3, "mute": false, "solo": false}
			],
			"preset": "vocal_friendly"
		}
	}`, projectID, e2eURL(t, "stems", testUserID, projectID, "drums.mp3"), e2eURL(t, "stems", testUserID, projectID, "bass.mp3"), stemID)
}

func TestMasterPreview_Success(t *testing.T) {
	ta := setupApp(t)

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/master/preview", validMasterPreviewBody(t))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	if result["fileUrl"] == nil || result["fileUrl"] == "" {
		t.Error("expected 'fileUrl' in response")
	}
	if result["duration"] == nil {
		t.Error("expected 'duration' in response")
	}
}

func TestMasterPreview_NoAuth(t *testing.T) {
	ta := setupApp(t)

	resp, err := doRequest(ta.app, http.MethodPost, "/api/master/preview", validMasterPreviewBody(t), nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestMasterFinal_Success(t *testing.T) {
	ta := setupApp(t)

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/master/final", validMasterFinalBody(t))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusAccepted)

	result := parseJSON(t, resp)
	if result["jobId"] == nil || result["jobId"] == "" {
		t.Error("expected 'jobId' in response")
	}
	if result["status"] != "queued" {
		t.Errorf("expected status 'queued', got %v", result["status"])
	}
}

func TestMasterStatus_Success(t *testing.T) {
	ta := setupApp(t)

	// Start a final master job first
	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/master/final", validMasterFinalBody(t))
	if err != nil {
		t.Fatalf("final request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusAccepted)
	finalResult := parseJSON(t, resp)
	jobID := finalResult["jobId"].(string)

	// Check status
	resp, err = doAuthRequest(t, ta.app, http.MethodGet, "/api/master/status/"+jobID, "")
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	statusResult := parseJSON(t, resp)
	if statusResult["jobId"] != jobID {
		t.Errorf("expected jobId %s, got %v", jobID, statusResult["jobId"])
	}
	if statusResult["status"] != "queued" {
		t.Errorf("expected status 'queued', got %v", statusResult["status"])
	}
}

func TestMasterStatus_NotFound(t *testing.T) {
	ta := setupApp(t)

	fakeJobID := uuid.New().String()
	resp, err := doAuthRequest(t, ta.app, http.MethodGet, "/api/master/status/"+fakeJobID, "")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusNotFound)

	result := parseJSON(t, resp)
	errObj := result["error"].(map[string]interface{})
	if errObj["code"] != "NOT_FOUND" {
		t.Errorf("expected error code NOT_FOUND, got %v", errObj["code"])
	}
}

func TestMasterFinal_YabanciStem404(t *testing.T) {
	ta := setupApp(t)

	projectID := uuid.New().String()
	body := fmt.Sprintf(`{
		"projectId": "%s",
		"profile": "warm",
		"stemUrls": ["%s"],
		"mixSnapshot": {
			"channels": [
				{"stemId": "%s", "volumeDb": 0, "mute": false, "solo": false}
			],
			"preset": "default"
		}
	}`, projectID, e2eURL(t, "stems", "baska-kullanici", projectID, "drums.mp3"), uuid.New().String())

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/master/final", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusNotFound)
}

func TestMasterFinal_TaninmayanKonak400(t *testing.T) {
	ta := setupApp(t)

	projectID := uuid.New().String()
	body := fmt.Sprintf(`{
		"projectId": "%s",
		"profile": "warm",
		"stemUrls": ["https://cdn.example.com/stems/drums.wav"],
		"mixSnapshot": {
			"channels": [
				{"stemId": "%s", "volumeDb": 0, "mute": false, "solo": false}
			],
			"preset": "default"
		}
	}`, projectID, uuid.New().String())

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/master/final", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusBadRequest)
}
