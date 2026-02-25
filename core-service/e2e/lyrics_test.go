package e2e

import (
	"net/http"
	"testing"
)

func TestLyricsGenerate_Success(t *testing.T) {
	ta := setupApp(t)

	body := `{
		"genre": "pop",
		"sectionType": "chorus",
		"vibes": ["happy", "energetic"]
	}`

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/lyrics/generate", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	drafts, ok := result["drafts"].([]interface{})
	if !ok {
		t.Fatal("expected 'drafts' to be an array")
	}
	if len(drafts) != 2 {
		t.Errorf("expected 2 drafts, got %d", len(drafts))
	}

	// Each draft should have 4 lines (mock response)
	for i, d := range drafts {
		draft, ok := d.([]interface{})
		if !ok {
			t.Fatalf("draft[%d] is not an array", i)
		}
		if len(draft) != 4 {
			t.Errorf("draft[%d]: expected 4 lines, got %d", i, len(draft))
		}
	}
}

func TestLyricsGenerate_NoAuth(t *testing.T) {
	ta := setupApp(t)

	body := `{
		"genre": "pop",
		"sectionType": "chorus",
		"vibes": ["happy"]
	}`

	resp, err := doRequest(ta.app, http.MethodPost, "/api/lyrics/generate", body, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusUnauthorized)

	result := parseJSON(t, resp)
	errObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "UNAUTHORIZED" {
		t.Errorf("expected error code UNAUTHORIZED, got %v", errObj["code"])
	}
}

func TestLyricsGenerate_InvalidBody(t *testing.T) {
	ta := setupApp(t)

	// Missing required fields
	body := `{"genre": "pop"}`

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/lyrics/generate", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusBadRequest)

	result := parseJSON(t, resp)
	errObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("expected error code VALIDATION_ERROR, got %v", errObj["code"])
	}
}

func TestLyricsRewrite_Success(t *testing.T) {
	ta := setupApp(t)

	body := `{
		"currentLyrics": "Hello world\nThis is a test",
		"genre": "rock",
		"sectionType": "verse",
		"vibes": ["melancholy"]
	}`

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/lyrics/rewrite", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	lines, ok := result["lines"].([]interface{})
	if !ok {
		t.Fatal("expected 'lines' to be an array")
	}
	if len(lines) == 0 {
		t.Error("expected at least one line in response")
	}
}

func TestLyricsRewrite_ValidationError(t *testing.T) {
	ta := setupApp(t)

	// Missing genre
	body := `{
		"currentLyrics": "Hello world",
		"sectionType": "verse",
		"vibes": ["happy"]
	}`

	resp, err := doAuthRequest(t, ta.app, http.MethodPost, "/api/lyrics/rewrite", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusBadRequest)

	result := parseJSON(t, resp)
	errObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("expected error code VALIDATION_ERROR, got %v", errObj["code"])
	}
}
