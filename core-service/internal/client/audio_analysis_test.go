package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func TestAnalyze_RequestAndResponse(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"peaks":[0.5,1.0],"duration":12.5,"preview_url":"https://x/sosyal/y1/onizleme.mp3"}`))
	}))
	defer srv.Close()

	c := NewAudioClient(&config.AudioConfig{ServiceURL: srv.URL, Timeout: 5})
	resp, err := c.Analyze(context.Background(), &AnalysisRequest{
		InputKey: "sosyal/y1/master.wav", PreviewKey: "sosyal/y1/onizleme.mp3", PeakCount: 64, PreviewSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if path != "/analiz" {
		t.Fatalf("path = %q", path)
	}
	want := map[string]any{"input_key": "sosyal/y1/master.wav", "preview_key": "sosyal/y1/onizleme.mp3", "peak_count": float64(64), "preview_seconds": float64(30)}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra field present (Python extra=forbid rejects it): %v", got)
	}
	if len(resp.Peaks) != 2 || resp.Peaks[1] != 1.0 || resp.Duration != 12.5 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAnalyze_ReturnsServiceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"bozuk"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewAudioClient(&config.AudioConfig{ServiceURL: srv.URL, Timeout: 5})
	if _, err := c.Analyze(context.Background(), &AnalysisRequest{InputKey: "k", PreviewKey: "p", PeakCount: 64, PreviewSeconds: 30}); err == nil {
		t.Fatal("500 must be returned as an error")
	}
}
