package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func validRenderStartBody() string {
	projectID := uuid.New().String()
	sectionID := uuid.New().String()
	return fmt.Sprintf(`{
		"projectId": "%s",
		"brief": {
			"genre": "pop",
			"vibes": ["happy", "energetic"],
			"bpm": {"mode": "auto"},
			"key": {"mode": "auto"},
			"structure": [
				{"id": "%s", "type": "verse", "bars": 8}
			]
		},
		"arrangement": {
			"instruments": ["drums", "bass", "piano"],
			"density": "medium",
			"groove": "straight"
		}
	}`, projectID, sectionID)
}

func TestRenderStart_Success(t *testing.T) {
	ta := setupApp(t)

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/render/start", validRenderStartBody())
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

func TestRenderStart_NoAuth(t *testing.T) {
	ta := setupApp(t)

	resp, err := doRequest(ta.app, http.MethodPost, "/api/render/start", validRenderStartBody(), nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestRenderStart_InvalidBody(t *testing.T) {
	ta := setupApp(t)

	// Missing required fields
	body := `{"projectId": "not-a-uuid"}`

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/render/start", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusBadRequest)
}

func TestRenderStatus_Success(t *testing.T) {
	ta := setupApp(t)

	// First, start a render to get a jobId
	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/render/start", validRenderStartBody())
	if err != nil {
		t.Fatalf("start request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusAccepted)
	startResult := parseJSON(t, resp)
	jobID := startResult["jobId"].(string)

	// Now check status
	resp, err = doAuthRequest(t, ta.app, http.MethodGet, "/api/render/status/"+jobID, "")
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	statusResult := parseJSON(t, resp)
	if statusResult["jobId"] != jobID {
		t.Errorf("expected jobId %s, got %v", jobID, statusResult["jobId"])
	}
	if statusResult["status"] == nil {
		t.Error("expected 'status' field in response")
	}
}

func TestRenderStatus_NotFound(t *testing.T) {
	ta := setupApp(t)

	fakeJobID := uuid.New().String()
	resp, err := doAuthRequest(t, ta.app, http.MethodGet, "/api/render/status/"+fakeJobID, "")
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

func TestRenderCancel_Success(t *testing.T) {
	ta := setupApp(t)

	// Start a render
	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/render/start", validRenderStartBody())
	if err != nil {
		t.Fatalf("start request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusAccepted)
	startResult := parseJSON(t, resp)
	jobID := startResult["jobId"].(string)

	// Cancel it
	resp, err = doAuthRequest(t, ta.app, http.MethodPost, "/api/render/cancel/"+jobID, "")
	if err != nil {
		t.Fatalf("cancel request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	cancelResult := parseJSON(t, resp)
	if cancelResult["success"] != true {
		t.Errorf("expected success true, got %v", cancelResult["success"])
	}
	if cancelResult["status"] != "canceled" {
		t.Errorf("expected status 'canceled', got %v", cancelResult["status"])
	}
}
