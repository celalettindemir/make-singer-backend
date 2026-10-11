package e2e

import (
	"net/http"
	"testing"
)

func TestBaseURL(t *testing.T) {
	ta := setupApp(t)

	resp, err := doRequest(ta.app, http.MethodGet, "/", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	body := parseJSON(t, resp)
	if _, ok := body["timestamp"]; !ok {
		t.Error("expected 'timestamp' field in response")
	}
}

func TestHealth(t *testing.T) {
	ta := setupApp(t)

	resp, err := doRequest(ta.app, http.MethodGet, "/health", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	body := parseJSON(t, resp)
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", body["status"])
	}
	if _, ok := body["services"]; !ok {
		t.Error("expected 'services' field in response")
	}
}
