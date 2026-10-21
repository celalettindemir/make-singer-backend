package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

func testAudio(t *testing.T, h http.HandlerFunc) *AudioClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewAudioClient(&config.AudioConfig{ServiceURL: srv.URL, Timeout: 5})
}

func TestAnalyzeGuide_Contract(t *testing.T) {
	var body map[string]any
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rehber-analiz" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"aligned_url": "https://r2/g.mp3", "notes_url": "https://r2/n.json", "offset_ms": 140,
			"range_midi": []int{57, 64}, "note_count": 120, "lines_matched": 16,
		})
	})
	got, err := c.AnalyzeGuide(context.Background(), &GuideAnalyzeRequest{
		GuideKey: "g-ham.mp3", ReferenceKey: "g-ref.mp3", InstrumentalKey: "s.mp3",
		Words:       []GuideWord{{Word: "la", StartS: 1, EndS: 2, Success: true}},
		Sections:    []GuideSectionInput{{ID: "v", Type: "verse", Lines: []string{"la"}}},
		VocalGender: "f", BPM: 100, AlignedKey: "g.mp3", NotesKey: "n.json",
	})
	if err != nil {
		t.Fatalf("AnalyzeGuide: %v", err)
	}
	if got.OffsetMs != 140 || got.RangeMidi == nil || *got.RangeMidi != [2]int{57, 64} || got.LinesMatched != 16 {
		t.Errorf("result = %+v", got)
	}
	for _, k := range []string{"guide_key", "reference_key", "instrumental_key", "words", "sections", "vocal_gender", "bpm", "aligned_key", "notes_key"} {
		if _, ok := body[k]; !ok {
			t.Errorf("body misses %q: %v", k, body)
		}
	}
	w := body["words"].([]any)[0].(map[string]any)
	if w["start_s"] != 1.0 || w["end_s"] != 2.0 || w["success"] != true {
		t.Errorf("word = %v", w)
	}
}

func TestAnalyzeGuide_NullRange(t *testing.T) {
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"aligned_url":"a","notes_url":"n","offset_ms":0,"range_midi":null,"note_count":0,"lines_matched":0}`))
	})
	got, err := c.AnalyzeGuide(context.Background(), &GuideAnalyzeRequest{})
	if err != nil || got.RangeMidi != nil {
		t.Fatalf("got %+v, %v; want a nil range", got, err)
	}
}

func TestPitchShift_OneRequestPerItemNotesFirst(t *testing.T) {
	var bodies []map[string]any
	c := testAudio(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/perde-kaydir" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		_, _ = w.Write([]byte(`{"outputs":["u"],"notes_url":null}`))
	})
	err := c.PitchShift(context.Background(), []PitchShiftItem{{InputKey: "a", OutputKey: "a2"}, {InputKey: "b", OutputKey: "b2"}}, -3,
		&PitchShiftNotes{InputKey: "n", OutputKey: "n2"})
	if err != nil {
		t.Fatalf("PitchShift: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want one per item", len(bodies))
	}
	if bodies[0]["semitones"] != -3.0 || bodies[0]["notes_key"] != "n" || bodies[0]["notes_output_key"] != "n2" {
		t.Errorf("first body = %v", bodies[0])
	}
	if _, ok := bodies[1]["notes_key"]; ok {
		t.Errorf("second body carries the notes: %v", bodies[1])
	}
	items := bodies[1]["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["input_key"] != "b" {
		t.Errorf("second items = %v", items)
	}
}
