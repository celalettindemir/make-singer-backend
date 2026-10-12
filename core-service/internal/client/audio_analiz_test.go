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

func TestAnaliz_IstekVeYanit(t *testing.T) {
	var gelen map[string]any
	var yol string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		yol = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gelen)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"peaks":[0.5,1.0],"duration":12.5,"preview_url":"https://x/sosyal/y1/onizleme.mp3"}`))
	}))
	defer srv.Close()

	c := NewAudioClient(&config.AudioConfig{ServiceURL: srv.URL, Timeout: 5})
	yanit, err := c.Analiz(context.Background(), &AnalizRequest{
		InputKey: "sosyal/y1/master.wav", PreviewKey: "sosyal/y1/onizleme.mp3", PeakCount: 64, PreviewSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Analiz: %v", err)
	}
	if yol != "/analiz" {
		t.Fatalf("yol = %q", yol)
	}
	beklenen := map[string]any{"input_key": "sosyal/y1/master.wav", "preview_key": "sosyal/y1/onizleme.mp3", "peak_count": float64(64), "preview_seconds": float64(30)}
	for k, v := range beklenen {
		if gelen[k] != v {
			t.Errorf("%s = %v, beklenen %v", k, gelen[k], v)
		}
	}
	if len(gelen) != len(beklenen) {
		t.Errorf("fazladan alan var (Python extra=forbid reddeder): %v", gelen)
	}
	if len(yanit.Peaks) != 2 || yanit.Peaks[1] != 1.0 || yanit.Duration != 12.5 {
		t.Fatalf("yanit = %+v", yanit)
	}
}

func TestAnaliz_ServisHatasiDoner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"bozuk"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewAudioClient(&config.AudioConfig{ServiceURL: srv.URL, Timeout: 5})
	if _, err := c.Analiz(context.Background(), &AnalizRequest{InputKey: "k", PreviewKey: "p", PeakCount: 64, PreviewSeconds: 30}); err == nil {
		t.Fatal("500 hata olarak donmeli")
	}
}
