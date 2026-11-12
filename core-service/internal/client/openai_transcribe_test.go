package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

func newTestTranscriber(url string) *OpenAITranscriber {
	return NewOpenAITranscriber(
		&config.OpenAIConfig{APIKey: "sk-test", BaseURL: url},
		&config.HumConfig{TranscribeModel: "gpt-4o-transcribe", TranscribeFallbackModel: "whisper-1"},
	)
}

func TestTranscribe_SendsModelLanguageAndFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("path %q auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		for k, want := range map[string]string{"model": "gpt-4o-transcribe", "language": "tr", "response_format": "json"} {
			if got := r.FormValue(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(f)
		if hdr.Filename != "hum.wav" || string(data) != "RIFFdata" {
			t.Errorf("file %q %q", hdr.Filename, data)
		}
		_, _ = w.Write([]byte(`{"text":"Oy şeftaşım. Sarı sarı"}`))
	}))
	defer srv.Close()

	got, err := newTestTranscriber(srv.URL).Transcribe(context.Background(), []byte("RIFFdata"), "hum.wav", "tr")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Oy şeftaşım. Sarı sarı" || got.Segments != nil {
		t.Errorf("got %+v", got)
	}
}

func TestTranscribe_FallsBackToWhisperWithSegments(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		if atomic.AddInt32(&calls, 1) == 1 {
			if r.FormValue("model") != "gpt-4o-transcribe" {
				t.Errorf("first model = %q", r.FormValue("model"))
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("model") != "whisper-1" || r.FormValue("response_format") != "verbose_json" {
			t.Errorf("fallback model %q format %q", r.FormValue("model"), r.FormValue("response_format"))
		}
		_, _ = w.Write([]byte(`{"text":"x","segments":[{"text":" Oy şeftaşım "},{"text":"Sarı sarı"}]}`))
	}))
	defer srv.Close()

	got, err := newTestTranscriber(srv.URL).Transcribe(context.Background(), []byte("a"), "hum.wav", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Oy şeftaşım", "Sarı sarı"}; !reflect.DeepEqual(got.Segments, want) {
		t.Errorf("Segments = %q, want %q", got.Segments, want)
	}
}

func TestTranscribe_DeadlineDoesNotFallBack(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := newTestTranscriber(srv.URL).Transcribe(ctx, []byte("a"), "hum.wav", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("server saw %d requests, want 1", n)
	}
}
