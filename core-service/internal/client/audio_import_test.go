package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestAudioClient_PrepareImport_422Unprocessable(t *testing.T) {
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ice-aktar-hazirla" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"undecodable"}`))
	})
	_, err := c.PrepareImport(context.Background(), &ImportPrepareRequest{InputKey: "imports/u/j/source.mp3"})
	if !errors.Is(err, ErrAudioUnprocessable) {
		t.Errorf("err = %v, want ErrAudioUnprocessable", err)
	}
}

func TestAudioClient_PrepareImport_Contract(t *testing.T) {
	var body map[string]any
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"duration_ms": 61000}`))
	})
	got, err := c.PrepareImport(context.Background(), &ImportPrepareRequest{InputKey: "k"})
	if err != nil || got.DurationMs != 61000 || body["input_key"] != "k" || len(body) != 1 {
		t.Errorf("got %+v err %v body %v", got, err, body)
	}
}

func TestAudioClient_MeasureImport_DecodesNullable(t *testing.T) {
	var path string
	resp := `{"bpm": 92.5, "key": {"tonic":"A","scale":"minor"}}`
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(resp))
	})
	got, err := c.MeasureImport(context.Background(), &ImportMeasureRequest{InputKey: "k"})
	if err != nil || path != "/ice-aktar-olc" {
		t.Fatalf("path %q err %v", path, err)
	}
	if got.BPM == nil || *got.BPM != 92.5 || got.Key == nil || got.Key.Tonic != "A" || got.Key.Scale != "minor" {
		t.Errorf("got = %+v", got)
	}
	resp = `{"bpm": null, "key": null}`
	got, err = c.MeasureImport(context.Background(), &ImportMeasureRequest{InputKey: "k"})
	if err != nil || got.BPM != nil || got.Key != nil {
		t.Errorf("null case: %+v err %v", got, err)
	}
}

func TestAudioClient_ImportNotes_Contract(t *testing.T) {
	var body map[string]any
	var path string
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"notes_url":"https://r2/notes.json","range_midi":[48,72],"lines_matched":3}`))
	})
	got, err := c.ImportNotes(context.Background(), &ImportNotesRequest{
		VocalKey: "imports/u/j/vocal.mp3",
		Words:    []TimedWordIn{{Word: "ben", StartS: 1.5, EndS: 1.9, Success: true}},
		Sections: []NotesSection{{ID: "s1", Lines: []string{"ben"}}},
		BPM:      100,
		NotesKey: "imports/u/j/notes.json",
	})
	if err != nil || path != "/ice-aktar-notalar" {
		t.Fatalf("path %q err %v", path, err)
	}
	if got.NotesURL != "https://r2/notes.json" || len(got.RangeMidi) != 2 || got.LinesMatched != 3 {
		t.Errorf("got = %+v", got)
	}
	for _, k := range []string{"vocal_key", "words", "sections", "bpm", "notes_key"} {
		if _, ok := body[k]; !ok {
			t.Errorf("body missing %s: %v", k, body)
		}
	}
	w0 := body["words"].([]any)[0].(map[string]any)
	if w0["word"] != "ben" || w0["start_s"] != 1.5 || w0["end_s"] != 1.9 || w0["success"] != true {
		t.Errorf("word = %v", w0)
	}
	s0 := body["sections"].([]any)[0].(map[string]any)
	if s0["id"] != "s1" || len(s0["lines"].([]any)) != 1 {
		t.Errorf("section = %v", s0)
	}
}
