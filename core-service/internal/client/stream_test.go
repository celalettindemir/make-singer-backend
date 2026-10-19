package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

var errSentinel = errors.New("sentinel")

func collect(out *[]string) func(string) error {
	return func(s string) error { *out = append(*out, s); return nil }
}

// sseServer serves the file's events (split on blank lines) with a flush
// after each, capturing the request body.
func sseServer(t *testing.T, file string, capture *map[string]any, hdr *http.Header) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			_ = json.NewDecoder(r.Body).Decode(capture)
		}
		if hdr != nil {
			*hdr = r.Header.Clone()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		for _, ev := range strings.SplitAfter(string(raw), "\n\n") {
			if ev == "" {
				continue
			}
			_, _ = w.Write([]byte(ev))
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAIStream_SendsStreamWithoutJSONMode(t *testing.T) {
	var body map[string]any
	srv := sseServer(t, "openai_stream.txt", &body, nil)
	c := NewOpenAIClient(openAIConfig(srv.URL))
	var got []string
	if err := c.ChatStream(context.Background(), "sys", "user", 0.8, collect(&got)); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v", body["stream"])
	}
	if _, ok := body["response_format"]; ok {
		t.Error("response_format must be absent")
	}
	if body["reasoning_effort"] != "low" {
		t.Errorf("reasoning_effort = %v", body["reasoning_effort"])
	}
	if want := []string{"Gece ", "boyunca\n", "Şehir"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deltas = %q, want %q", got, want)
	}
}

func TestOpenAIStream_SplitRuneAcrossChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		ev := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"şehir\"}}]}\n\ndata: [DONE]\n\n")
		i := strings.Index(string(ev), "ş") + 1 // between the two bytes of ş
		_, _ = w.Write(ev[:i])
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(ev[i:])
	}))
	defer srv.Close()
	c := NewOpenAIClient(openAIConfig(srv.URL))
	var got []string
	if err := c.ChatStream(context.Background(), "s", "u", 0, collect(&got)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "") != "şehir" {
		t.Errorf("got %q", got)
	}
}

func TestOpenAIStream_Non200IsError(t *testing.T) {
	srv := fakeOpenAI(t, 429, `{"error":"slow down"}`, nil, nil)
	c := NewOpenAIClient(openAIConfig(srv.URL))
	err := c.ChatStream(context.Background(), "s", "u", 0, collect(&[]string{}))
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAIStream_OnTextErrorStops(t *testing.T) {
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	c := NewOpenAIClient(openAIConfig(srv.URL))
	err := c.ChatStream(context.Background(), "s", "u", 0, func(string) error { return errSentinel })
	if !errors.Is(err, errSentinel) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Error("server did not see the request cancelled")
	}
}

func TestGroqStream_SendsTemperature(t *testing.T) {
	var body map[string]any
	srv := sseServer(t, "openai_stream.txt", &body, nil)
	c := NewGroqClient(&config.GroqConfig{APIKey: "k", BaseURL: srv.URL, Model: "m", MaxTokens: 100})
	var got []string
	if err := c.ChatStream(context.Background(), "s", "u", 0.8, collect(&got)); err != nil {
		t.Fatal(err)
	}
	if body["temperature"] != 0.8 || body["stream"] != true {
		t.Errorf("body = %v", body)
	}
	if _, ok := body["response_format"]; ok {
		t.Error("response_format must be absent")
	}
	if len(got) != 3 {
		t.Errorf("got %q", got)
	}
}

func TestAnthropicStream_CollectsTextDeltas(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	srv := sseServer(t, "anthropic_stream.txt", &body, &hdr)
	c := NewAnthropicClient(anthropicConfig(srv.URL))
	var got []string
	if err := c.ChatStream(context.Background(), "sys", "user", 0, collect(&got)); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if want := []string{"Gece ", "boyunca\n", "Şehir"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deltas = %q, want %q", got, want)
	}
	if !strings.Contains(hdr.Get("anthropic-beta"), anthropicFallbackBeta) {
		t.Errorf("anthropic-beta = %q", hdr.Get("anthropic-beta"))
	}
	if body["stream"] != true || body["fallbacks"] != "default" {
		t.Errorf("body = %v", body)
	}
}

func TestAnthropicStream_OnTextErrorStops(t *testing.T) {
	srv := sseServer(t, "anthropic_stream.txt", nil, nil)
	c := NewAnthropicClient(anthropicConfig(srv.URL))
	err := c.ChatStream(context.Background(), "s", "u", 0, func(string) error { return errSentinel })
	if !errors.Is(err, errSentinel) {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAIStream_NoClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	c := NewOpenAIClient(openAIConfig(srv.URL))
	if c.streamHTTP.Timeout != 0 {
		t.Fatalf("streamHTTP.Timeout = %v", c.streamHTTP.Timeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got []string
	if err := c.ChatStream(ctx, "s", "u", 0, collect(&got)); err != nil || len(got) != 3 {
		t.Errorf("err=%v got=%q", err, got)
	}
}

// rawSSE serves body as one event stream.
func rawSSE(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAIStream_TruncatedWithoutDoneIsError(t *testing.T) {
	raw, err := os.ReadFile("testdata/openai_stream.txt")
	if err != nil {
		t.Fatal(err)
	}
	trunc := strings.Split(string(raw), "data: [DONE]")[0]
	c := NewOpenAIClient(openAIConfig(rawSSE(t, trunc).URL))
	var got []string
	if err := c.ChatStream(context.Background(), "s", "u", 0, collect(&got)); err == nil {
		t.Fatal("want error for stream without [DONE]")
	}
}

func TestOpenAIStream_ErrorChunkIsError(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"Gece\\n\"}}]}\n\n" +
		"data: {\"error\":{\"message\":\"upstream overloaded\"}}\n\n"
	c := NewOpenAIClient(openAIConfig(rawSSE(t, body).URL))
	var got []string
	err := c.ChatStream(context.Background(), "s", "u", 0, collect(&got))
	if err == nil || !strings.Contains(err.Error(), "upstream overloaded") {
		t.Fatalf("err = %v", err)
	}
}
