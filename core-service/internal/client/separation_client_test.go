package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

func newTestSeparator(t *testing.T, h http.HandlerFunc) *SeparationClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewSeparationClient(&config.ImportConfig{SeparationURL: srv.URL, SeparationTimeout: 5 * time.Second})
}

func TestSeparationClient_PostsAyir(t *testing.T) {
	var body map[string]any
	c := newTestSeparator(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ayir" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"duration_ms": 200000, "stems": [{"instrument":"vocal","key":"imports/u/j/vocal.mp3"},{"instrument":"rest","key":"imports/u/j/rest.mp3"}]}`))
	})
	got, err := c.Separate(context.Background(), &SeparateRequest{InputKey: "imports/u/j/source.mp3", OutputPrefix: "imports/u/j/"})
	if err != nil {
		t.Fatalf("Separate: %v", err)
	}
	if body["input_key"] != "imports/u/j/source.mp3" || body["output_prefix"] != "imports/u/j/" || len(body) != 2 {
		t.Errorf("body = %v", body)
	}
	if got.DurationMs != 200000 || len(got.Stems) != 2 || got.Stems[0].Instrument != "vocal" || got.Stems[1].Key != "imports/u/j/rest.mp3" {
		t.Errorf("got = %+v", got)
	}
}

func TestSeparationClient_422IsUnprocessable(t *testing.T) {
	c := newTestSeparator(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"undecodable"}`))
	})
	if _, err := c.Separate(context.Background(), &SeparateRequest{}); !errors.Is(err, ErrAudioUnprocessable) {
		t.Errorf("err = %v, want ErrAudioUnprocessable", err)
	}
}

func TestSeparationClient_503IsError(t *testing.T) {
	c := newTestSeparator(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	_, err := c.Separate(context.Background(), &SeparateRequest{})
	if err == nil || errors.Is(err, ErrAudioUnprocessable) {
		t.Errorf("err = %v", err)
	}
}

func TestSeparationClient_IsConfigured(t *testing.T) {
	if NewSeparationClient(&config.ImportConfig{}).IsConfigured() {
		t.Error("empty URL must not be configured")
	}
	if !NewSeparationClient(&config.ImportConfig{SeparationURL: "http://x"}).IsConfigured() {
		t.Error("URL set must be configured")
	}
}
