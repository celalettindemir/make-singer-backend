package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAITranscriber_TranscribeTimed_SendsGranularities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("path %q auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("model"); got != "whisper-1" {
			t.Errorf("model = %q", got)
		}
		if got := r.FormValue("response_format"); got != "verbose_json" {
			t.Errorf("response_format = %q", got)
		}
		g := r.MultipartForm.Value["timestamp_granularities[]"]
		if len(g) != 2 || g[0] != "segment" || g[1] != "word" {
			t.Errorf("granularities = %v", g)
		}
		if _, ok := r.MultipartForm.Value["language"]; ok {
			t.Error("language must not be sent")
		}
		if _, hdr, err := r.FormFile("file"); err != nil || hdr.Filename != "vocal.mp3" {
			t.Errorf("file: %v", err)
		}
		_, _ = w.Write([]byte(`{"language":"turkish","text":"bir iki","segments":[{"start":1.0,"end":3.5,"text":" bir iki ","avg_logprob":-0.3,"no_speech_prob":0.05,"compression_ratio":1.2}],"words":[{"word":"bir","start":1.0,"end":1.5},{"word":"iki","start":2.0,"end":3.5}]}`))
	}))
	defer srv.Close()

	got, err := newTestTranscriber(srv.URL).TranscribeTimed(context.Background(), []byte("ID3"), "vocal.mp3")
	if err != nil {
		t.Fatalf("TranscribeTimed: %v", err)
	}
	if got.Language != "turkish" || len(got.Segments) != 1 || len(got.Words) != 2 {
		t.Fatalf("got = %+v", got)
	}
	s := got.Segments[0]
	if s.Start != 1.0 || s.End != 3.5 || s.Text != " bir iki " || s.AvgLogprob != -0.3 || s.NoSpeechProb != 0.05 || s.CompressionRatio != 1.2 {
		t.Errorf("segment = %+v", s)
	}
	if got.Words[1] != (TimedWord{Word: "iki", Start: 2.0, End: 3.5}) {
		t.Errorf("word = %+v", got.Words[1])
	}
}

func TestOpenAITranscriber_TranscribeTimed_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := newTestTranscriber(srv.URL).TranscribeTimed(context.Background(), []byte("x"), "vocal.mp3"); err == nil {
		t.Error("want error on 500")
	}
}
