package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestPrepareHum_Contract(t *testing.T) {
	var body map[string]any
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mirilti-hazirla" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"duration_ms": 12600}`))
	})
	got, err := c.PrepareHum(context.Background(), &HumPrepareRequest{
		InputKey: "hums/u/p/h-src.wav", OutputKey: "hums/u/p/h.mp3",
	})
	if err != nil {
		t.Fatalf("PrepareHum: %v", err)
	}
	if got.DurationMs != 12600 {
		t.Errorf("DurationMs = %d", got.DurationMs)
	}
	if body["input_key"] != "hums/u/p/h-src.wav" || body["output_key"] != "hums/u/p/h.mp3" || len(body) != 2 {
		t.Errorf("body = %v", body)
	}
}

func TestPrepareHum_422IsUnprocessable(t *testing.T) {
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"undecodable"}`))
	})
	_, err := c.PrepareHum(context.Background(), &HumPrepareRequest{})
	if !errors.Is(err, ErrAudioUnprocessable) {
		t.Fatalf("err = %v, want ErrAudioUnprocessable", err)
	}
}
