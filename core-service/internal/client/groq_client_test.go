package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
