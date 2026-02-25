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

func TestAuthVerify_NoToken(t *testing.T) {
	ta := setupApp(t)

	resp, err := doRequest(ta.app, http.MethodGet, "/auth/verify", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestAuthVerify_ValidToken(t *testing.T) {
	ta := setupApp(t)

	token := generateToken(t)
	resp, err := doRequest(ta.app, http.MethodGet, "/auth/verify", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	if resp.Header.Get("X-User-Id") == "" {
		t.Error("expected X-User-Id header to be set")
	}
	if resp.Header.Get("X-User-Email") == "" {
		t.Error("expected X-User-Email header to be set")
	}
}
