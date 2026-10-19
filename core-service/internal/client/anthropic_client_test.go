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

type anthropicCapture struct {
	body    map[string]any
	headers http.Header
	calls   int
}

func fakeAnthropic(t *testing.T, status int, reply string, capture *anthropicCapture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if capture != nil {
			capture.calls++
			capture.headers = r.Header.Clone()
			_ = json.NewDecoder(r.Body).Decode(&capture.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func anthropicConfig(url string) *config.AnthropicConfig {
	return &config.AnthropicConfig{APIKey: "ak", BaseURL: url, Model: "claude-sonnet-5-5", MaxTokens: 4000,
		Effort: "low", TemperatureGenerate: 0.8, TemperatureRewrite: 0.6}
}

func anthropicReply(stop string, texts ...string) string {
	var blocks []map[string]any
	blocks = append(blocks, map[string]any{"type": "thinking", "thinking": "", "signature": "s"})
	for _, t := range texts {
		blocks = append(blocks, map[string]any{"type": "text", "text": t})
	}
	b, _ := json.Marshal(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
		"content": blocks, "stop_reason": stop,
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
	return string(b)
}

func TestAnthropic_SendsHeadersSystemAndModel(t *testing.T) {
	var cap anthropicCapture
	srv := fakeAnthropic(t, 200, anthropicReply("end_turn", `{"lines":["a"]}`), &cap)
	if _, err := NewAnthropicClient(anthropicConfig(srv.URL)).ChatCompletion(context.Background(), "sys", "user", 0.8); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if cap.headers.Get("X-Api-Key") != "ak" {
		t.Errorf("x-api-key = %q", cap.headers.Get("X-Api-Key"))
	}
	if cap.headers.Get("Anthropic-Version") == "" {
		t.Error("anthropic-version header missing")
	}
	if !strings.Contains(cap.headers.Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("anthropic-beta = %q, want server-side fallback", cap.headers.Get("Anthropic-Beta"))
	}
	if cap.body["fallbacks"] != "default" {
		t.Errorf("fallbacks = %v, want default", cap.body["fallbacks"])
	}
	if cap.body["model"] != "claude-sonnet-5-5" || cap.body["max_tokens"] != float64(4000) {
		t.Errorf("model/max_tokens = %v/%v", cap.body["model"], cap.body["max_tokens"])
	}
	if !strings.Contains(asJSON(cap.body["system"]), "sys") {
		t.Errorf("system = %v", cap.body["system"])
	}
	if _, ok := cap.body["temperature"]; ok {
		t.Error("temperature must not be sent (Sonnet 5.5 rejects non-default sampling)")
	}
	oc, _ := cap.body["output_config"].(map[string]any)
	if oc["effort"] != "low" {
		t.Errorf("output_config = %v, want effort low", cap.body["output_config"])
	}
	msgs, _ := cap.body["messages"].([]any)
	if len(msgs) != 1 || !strings.Contains(asJSON(msgs[0]), "user") {
		t.Errorf("messages = %v", msgs)
	}
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestAnthropic_JoinsTextBlocks(t *testing.T) {
	srv := fakeAnthropic(t, 200, anthropicReply("end_turn", `{"lines":`, `["a"]}`), nil)
	out, err := NewAnthropicClient(anthropicConfig(srv.URL)).ChatCompletion(context.Background(), "s", "u", 0.8)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"lines":["a"]}` {
		t.Errorf("content = %q", out)
	}
}

func TestAnthropic_RefusalIsError(t *testing.T) {
	srv := fakeAnthropic(t, 200, anthropicReply("refusal"), nil)
	if _, err := NewAnthropicClient(anthropicConfig(srv.URL)).ChatCompletion(context.Background(), "s", "u", 0.8); err == nil {
		t.Fatal("want error on refusal")
	}
}

func TestAnthropic_Non200IsErrorWithoutRetry(t *testing.T) {
	var cap anthropicCapture
	srv := fakeAnthropic(t, 500, `{"type":"error","error":{"type":"api_error","message":"boom"}}`, &cap)
	_, err := NewAnthropicClient(anthropicConfig(srv.URL)).ChatCompletion(context.Background(), "s", "u", 0.8)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want status 500", err)
	}
	if cap.calls != 1 {
		t.Errorf("calls = %d, want 1 (no SDK retries)", cap.calls)
	}
}

func TestAnthropic_IsConfiguredNeedsKey(t *testing.T) {
	if NewAnthropicClient(&config.AnthropicConfig{}).IsConfigured() {
		t.Error("no key must not be configured")
	}
	c := NewAnthropicClient(anthropicConfig("http://x"))
	if !c.IsConfigured() {
		t.Error("key set must be configured")
	}
	if g, r := c.Temperatures(); g != 0.8 || r != 0.6 {
		t.Errorf("Temperatures = %v, %v", g, r)
	}
	var _ ChatClient = c
}
