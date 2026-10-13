package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// fakeGroq is a fake Groq server that captures the incoming request and
// returns a fixed response.
func fakeGroq(t *testing.T, capture *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(capture); err != nil {
			t.Fatalf("could not read request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"lines\":[\"a\"]}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChatCompletion_SendsJSONModeTemperatureAndMaxTokens(t *testing.T) {
	var body map[string]any
	srv := fakeGroq(t, &body)
	c := NewGroqClient(&config.GroqConfig{APIKey: "k", BaseURL: srv.URL, Model: "openai/gpt-oss-120b", MaxTokens: 4000})

	out, err := c.ChatCompletion(context.Background(), "sys", "user", 0.6)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if out != `{"lines":["a"]}` {
		t.Errorf("content = %q", out)
	}
	if body["model"] != "openai/gpt-oss-120b" {
		t.Errorf("model = %v", body["model"])
	}
	if body["temperature"] != 0.6 {
		t.Errorf("temperature = %v, want 0.6", body["temperature"])
	}
	if body["max_tokens"] != float64(4000) {
		t.Errorf("max_tokens = %v, want 4000", body["max_tokens"])
	}
	rf, _ := body["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format = %v, want json_object", body["response_format"])
	}
}

func TestTemperatures_ReturnsConfigValues(t *testing.T) {
	c := NewGroqClient(&config.GroqConfig{TemperatureGenerate: 0.8, TemperatureRewrite: 0.6})
	gen, rw := c.Temperatures()
	if gen != 0.8 || rw != 0.6 {
		t.Errorf("Temperatures() = %v, %v", gen, rw)
	}
}

func TestChatCompletion_SendsZeroTemperature(t *testing.T) {
	var body map[string]any
	srv := fakeGroq(t, &body)
	c := NewGroqClient(&config.GroqConfig{APIKey: "k", BaseURL: srv.URL, Model: "m", MaxTokens: 10})
	if _, err := c.ChatCompletion(context.Background(), "sys", "user", 0); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	v, ok := body["temperature"]
	if !ok || v != float64(0) {
		t.Errorf("temperature = %v (present=%v), want 0", v, ok)
	}
}

const failedGenBody = `{"error":{"message":"Failed to generate JSON. Please adjust your prompt. See 'failed_generation' for more details.","type":"invalid_request_error","code":"json_validate_failed","failed_generation":"{\"drafts\":[[\"secret line\"]]}"}}`

// sequencedGroq answers with the given (status, body) pairs in order, repeating the last.
func sequencedGroq(t *testing.T, calls *int, steps ...[2]string) *GroqClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := *calls
		if i >= len(steps) {
			i = len(steps) - 1
		}
		*calls++
		status := http.StatusOK
		switch steps[i][0] {
		case "400":
			status = http.StatusBadRequest
		case "500":
			status = http.StatusInternalServerError
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(steps[i][1]))
	}))
	t.Cleanup(srv.Close)
	c := NewGroqClient(&config.GroqConfig{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	c.retryDelay = time.Millisecond
	return c
}

const okBody = `{"choices":[{"message":{"role":"assistant","content":"{\"drafts\":[[\"a\"]]}"}}]}`

func TestChatCompletion_RetriesJSONValidateFailed(t *testing.T) {
	var calls int
	c := sequencedGroq(t, &calls, [2]string{"400", failedGenBody}, [2]string{"200", okBody})
	out, err := c.ChatCompletion(context.Background(), "s", "u", 0.5)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if out != `{"drafts":[["a"]]}` || calls != 2 {
		t.Errorf("out = %q, calls = %d", out, calls)
	}
}

func TestChatCompletion_JSONValidateFailedPersistsAfterBoundedRetries(t *testing.T) {
	var calls int
	c := sequencedGroq(t, &calls, [2]string{"400", failedGenBody})
	_, err := c.ChatCompletion(context.Background(), "s", "u", 0.5)
	if err == nil || !strings.Contains(err.Error(), "groq API error (status 400)") {
		t.Fatalf("err = %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (1 + 2 retries)", calls)
	}
}

func TestChatCompletion_OtherErrorsAreNotRetried(t *testing.T) {
	var calls int
	c := sequencedGroq(t, &calls, [2]string{"500", `{"error":{"code":"boom"}}`})
	if _, err := c.ChatCompletion(context.Background(), "s", "u", 0.5); err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
