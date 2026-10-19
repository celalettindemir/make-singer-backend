package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func fakeOpenAI(t *testing.T, status int, reply string, capture *map[string]any, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		if capture != nil {
			_ = json.NewDecoder(r.Body).Decode(capture)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func openAIConfig(url string) *config.OpenAIConfig {
	return &config.OpenAIConfig{APIKey: "k", BaseURL: url, Model: "gpt-x", MaxTokens: 4000,
		ReasoningEffort: "low", TemperatureGenerate: 0.8, TemperatureRewrite: 0.6}
}

const openAIReply = `{"choices":[{"message":{"role":"assistant","content":"{\"lines\":[\"a\"]}"}}]}`

func TestOpenAI_SendsModelJSONModeAndMaxCompletionTokens(t *testing.T) {
	var body map[string]any
	var auth string
	srv := fakeOpenAI(t, 200, openAIReply, &body, &auth)
	c := NewOpenAIClient(openAIConfig(srv.URL))

	if _, err := c.ChatCompletion(context.Background(), "sys", "user", 0.8); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if auth != "Bearer k" {
		t.Errorf("Authorization = %q", auth)
	}
	if body["model"] != "gpt-x" {
		t.Errorf("model = %v", body["model"])
	}
	if body["max_completion_tokens"] != float64(4000) {
		t.Errorf("max_completion_tokens = %v", body["max_completion_tokens"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Error("max_tokens must not be sent")
	}
	if _, ok := body["temperature"]; ok {
		t.Error("temperature must not be sent (reasoning models reject it)")
	}
	if body["reasoning_effort"] != "low" {
		t.Errorf("reasoning_effort = %v", body["reasoning_effort"])
	}
	rf, _ := body["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format = %v", body["response_format"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
}

func TestOpenAI_OmitsEmptyReasoningEffort(t *testing.T) {
	var body map[string]any
	srv := fakeOpenAI(t, 200, openAIReply, &body, nil)
	cfg := openAIConfig(srv.URL)
	cfg.ReasoningEffort = ""
	if _, err := NewOpenAIClient(cfg).ChatCompletion(context.Background(), "s", "u", 0.8); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Error("reasoning_effort must be omitted when empty")
	}
}

func TestOpenAI_ReturnsFirstChoiceContent(t *testing.T) {
	srv := fakeOpenAI(t, 200, openAIReply, nil, nil)
	out, err := NewOpenAIClient(openAIConfig(srv.URL)).ChatCompletion(context.Background(), "s", "u", 0.8)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"lines":["a"]}` {
		t.Errorf("content = %q", out)
	}
}

func TestOpenAI_Non200IsError(t *testing.T) {
	srv := fakeOpenAI(t, 500, `{"error":{"message":"boom"}}`, nil, nil)
	_, err := NewOpenAIClient(openAIConfig(srv.URL)).ChatCompletion(context.Background(), "s", "u", 0.8)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want status 500", err)
	}
}

func TestOpenAI_NoChoicesIsError(t *testing.T) {
	srv := fakeOpenAI(t, 200, `{"choices":[]}`, nil, nil)
	if _, err := NewOpenAIClient(openAIConfig(srv.URL)).ChatCompletion(context.Background(), "s", "u", 0.8); err == nil {
		t.Fatal("want error for no choices")
	}
}

func TestOpenAI_IsConfiguredNeedsKey(t *testing.T) {
	if NewOpenAIClient(&config.OpenAIConfig{}).IsConfigured() {
		t.Error("no key must not be configured")
	}
	c := NewOpenAIClient(openAIConfig("http://x"))
	if !c.IsConfigured() {
		t.Error("key set must be configured")
	}
	if g, r := c.Temperatures(); g != 0.8 || r != 0.6 {
		t.Errorf("Temperatures = %v, %v", g, r)
	}
	var _ ChatClient = c
	var _ ChatClient = (*GroqClient)(nil)
}
