package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

func testSuno(t *testing.T, h http.HandlerFunc) *SunoClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewSunoClient(&config.SunoConfig{
		APIKey: "anahtar", BaseURL: srv.URL, Model: "V6", CallbackURL: "https://ornek.test/geri",
	})
}

func writeEnvelope(w http.ResponseWriter, code int, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": "m", "data": data})
}

func TestGenerateMusic_ProviderContract(t *testing.T) {
	var body map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/generate" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer anahtar" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnvelope(w, 200, map[string]any{"taskId": "t1"})
	})

	id, err := c.GenerateMusic(context.Background(), &GenerateMusicRequest{Style: "pop, instrumental", Title: "pop instrumental", Instrumental: true})
	if err != nil {
		t.Fatalf("GenerateMusic: %v", err)
	}
	if id != "t1" {
		t.Errorf("taskID = %q", id)
	}
	want := map[string]any{
		"customMode": true, "instrumental": true, "model": "V6",
		"callBackUrl": "https://ornek.test/geri", "style": "pop, instrumental", "title": "pop instrumental",
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, body[k], v)
		}
	}
}

func TestGenerateMusic_EnvelopeErrorCode(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 429, "msg": "insufficient credits", "data": nil})
	})
	_, err := c.GenerateMusic(context.Background(), &GenerateMusicRequest{Style: "pop"})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected a 429 error, got: %v", err)
	}
}

func TestWaitMusic_SuccessReturnsFirstTrack(t *testing.T) {
	var n atomic.Int32
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/generate/record-info" || r.URL.Query().Get("taskId") != "t1" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if n.Add(1) == 1 {
			writeEnvelope(w, 200, map[string]any{"status": "PENDING"})
			return
		}
		writeEnvelope(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{"sunoData": []any{
			map[string]any{"id": "a1", "audio_url": "https://cdn.test/a1.mp3", "duration": 201.16},
			map[string]any{"id": "a2", "audio_url": "https://cdn.test/a2.mp3", "duration": 191.6},
		}}})
	})

	tr, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitMusic: %v", err)
	}
	if tr.ID != "a1" || tr.AudioURL != "https://cdn.test/a1.mp3" || tr.Duration != 201.16 {
		t.Errorf("track = %+v", tr)
	}
}

func TestWaitMusic_FailureStatusReturnsImmediately(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, map[string]any{"status": "SENSITIVE_WORD_ERROR", "errorMessage": "yasakli kelime"})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "yasakli kelime") {
		t.Fatalf("expected an error message, got: %v", err)
	}
}

func TestWaitMusic_ToleratesTwoTransientErrors(t *testing.T) {
	var n atomic.Int32
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeEnvelope(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{"sunoData": []any{
			map[string]any{"id": "a1", "audio_url": "u", "duration": 1.0},
		}}})
	})
	if _, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second); err != nil {
		t.Fatalf("two transient errors should have been tolerated: %v", err)
	}
}

func TestWaitMusic_GivesUpAfterThreeConsecutiveErrors(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second); err == nil {
		t.Fatal("should have given up after three consecutive errors")
	}
}

func TestWaitMusic_Timeout(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, map[string]any{"status": "TEXT_SUCCESS"})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout, got: %v", err)
	}
}

var _ MusicGenerator = (*SunoClient)(nil)

func TestSplitStems_ProviderContract(t *testing.T) {
	var body map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vocal-removal/generate" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnvelope(w, 200, map[string]any{"taskId": "s1"})
	})

	id, err := c.SplitStems(context.Background(), "t1", "a1")
	if err != nil {
		t.Fatalf("SplitStems: %v", err)
	}
	if id != "s1" {
		t.Errorf("stemTaskID = %q", id)
	}
	want := map[string]any{"taskId": "t1", "audioId": "a1", "type": "split_stem", "callBackUrl": "https://ornek.test/geri"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, body[k], v)
		}
	}
}

func TestWaitStems_MapsInstrumentsSkipsVocals(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vocal-removal/record-info" || r.URL.Query().Get("taskId") != "s1" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		writeEnvelope(w, 200, map[string]any{"successFlag": "SUCCESS", "response": map[string]any{
			"originData":       []any{map[string]any{"duration": 1.0}},
			"vocalUrl":         "https://cdn.test/vokal.mp3",
			"backingVocalsUrl": "https://cdn.test/geri.mp3",
			"keyboardUrl":      "https://cdn.test/klavye.mp3",
			"drumsUrl":         "https://cdn.test/davul.mp3",
			"bassUrl":          nil,
			"guitarUrl":        "",
			"woodwindsUrl":     "https://cdn.test/nefesli.mp3",
		}})
	})

	stems, err := c.WaitStems(context.Background(), "s1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitStems: %v", err)
	}
	want := []Stem{
		{Instrument: model.InstrumentDrums, URL: "https://cdn.test/davul.mp3"},
		{Instrument: model.InstrumentPiano, URL: "https://cdn.test/klavye.mp3"},
	}
	if len(stems) != len(want) {
		t.Fatalf("stems = %+v, want %+v", stems, want)
	}
	for i := range want {
		if stems[i] != want[i] {
			t.Errorf("stems[%d] = %+v, want %+v", i, stems[i], want[i])
		}
	}
}

func TestWaitStems_FailureFlag(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, map[string]any{"successFlag": "GENERATE_AUDIO_FAILED", "errorMessage": "bolunemedi"})
	})
	_, err := c.WaitStems(context.Background(), "s1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "bolunemedi") {
		t.Fatalf("expected an error, got: %v", err)
	}
}

func TestWaitMusic_EmptyTrackIDErrors(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{
			"sunoData": []any{map[string]any{"id": "", "audio_url": "u", "duration": 1.0}},
		}})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "track ID") {
		t.Fatalf("expected an error, got: %v", err)
	}
}

func advancedClient(t *testing.T, h http.HandlerFunc) *SunoClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewSunoClient(&config.SunoConfig{APIKey: "k", BaseURL: srv.URL, Model: "V6", CallbackURL: "https://cb.test/suno"})
}

func TestIsolateStem_ProviderContract(t *testing.T) {
	var body map[string]any
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vocal-removal/generate" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"taskId":"a1"}}`))
	})

	id, err := c.IsolateStem(context.Background(), "t1", "m1", "Drum Kit")
	if err != nil || id != "a1" {
		t.Fatalf("IsolateStem = %q, %v", id, err)
	}
	want := map[string]any{"taskId": "t1", "audioId": "m1", "type": "split_stem_advanced", "stemName": "Drum Kit", "callBackUrl": "https://cb.test/suno"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, body[k], v)
		}
	}
}

// advancedResponse is a shortened version of the real response from the
// 2026-10-11 live discovery.
const advancedResponse = `{"code":200,"msg":"success","data":{
 "taskId":"a1","successFlag":"SUCCESS","errorMessage":null,
 "response":{"originUrl":null,"drumsUrl":null,"originData":[
  {"extract":{"duration":141.48,"audio_url":"https://tempfile.test/r/x_Drum Kit.mp3","stem_type_group_name":"Drum Kit","id":"e1"},
   "remove":{"duration":141.48,"audio_url":"https://tempfile.test/r/y_Drum Kit.mp3","stem_type_group_name":"Drum Kit","id":"r1"}},
  {"extract":{"duration":141.48,"audio_url":"https://tempfile.test/r/ikinci-e.mp3","id":"e2"},
   "remove":{"duration":141.48,"audio_url":"https://tempfile.test/r/ikinci-r.mp3","id":"r2"}}
 ]}}}`

func TestWaitIsolated_FirstVariantExtractAndRemoveURLs(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vocal-removal/record-info" || r.URL.Query().Get("taskId") != "a1" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(advancedResponse))
	})

	s, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitIsolated: %v", err)
	}
	if s.ExtractURL != "https://tempfile.test/r/x_Drum Kit.mp3" || s.RemoveURL != "https://tempfile.test/r/y_Drum Kit.mp3" || s.Duration != 141.48 {
		t.Errorf("result = %+v", s)
	}
}

func TestWaitIsolated_EmptyOriginDataErrors(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"SUCCESS","response":{"originData":[]}}}`))
	})
	if _, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second); err == nil {
		t.Fatal("expected an error for empty originData")
	}
}

func TestWaitIsolated_MissingURLErrors(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"SUCCESS","response":{"originData":[{"extract":{"audio_url":"https://x.test/e.mp3"},"remove":{"audio_url":""}}]}}}`))
	})
	if _, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second); err == nil {
		t.Fatal("expected an error for empty remove URL")
	}
}

func TestWaitIsolated_FailureFlag(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"GENERATE_AUDIO_FAILED","errorMessage":"katalog eslesmesi"}}`))
	})
	_, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "katalog eslesmesi") {
		t.Fatalf("error message must be carried, got: %v", err)
	}
}

func TestSunoStemName(t *testing.T) {
	want := map[model.Instrument]string{
		model.InstrumentDrums: "Drum Kit", model.InstrumentBass: "Bass", model.InstrumentPiano: "Piano",
		model.InstrumentGuitar: "Guitar", model.InstrumentSynth: "Synth", model.InstrumentStrings: "String Section",
		model.InstrumentBrass: "Brass Section", model.InstrumentWoodwinds: "Woodwinds", model.InstrumentPercussion: "Percussion",
		model.InstrumentPads: "Synth Pad", model.InstrumentLead: "Synth Lead", model.InstrumentFX: "Sound Effects",
	}
	for i, name := range want {
		if got, ok := SunoStemName(i); !ok || got != name {
			t.Errorf("SunoStemAdi(%s) = %q, %v; want %q", i, got, ok, name)
		}
	}
	for _, i := range []model.Instrument{model.InstrumentMix, model.InstrumentRest, "kazoo"} {
		if _, ok := SunoStemName(i); ok {
			t.Errorf("SunoStemAdi(%s) should not have matched", i)
		}
	}
}

func TestAddVocals_ProviderContract(t *testing.T) {
	var body map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/generate/add-vocals" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnvelope(w, 200, map[string]any{"taskId": "av1"})
	})
	id, err := c.AddVocals(context.Background(), &AddVocalsRequest{
		UploadURL: "https://cdn.test/inst.mp3", Style: "pop, A minor", Title: "pop guide",
		Lyrics: "[Verse]\na", VocalGender: "f",
	})
	if err != nil || id != "av1" {
		t.Fatalf("AddVocals = %q, %v", id, err)
	}
	want := map[string]any{
		"uploadUrl": "https://cdn.test/inst.mp3", "title": "pop guide", "negativeTags": "rap, spoken word",
		"style": "pop, A minor", "lyrics": "[Verse]\na", "vocalGender": "f", "model": "V6",
		"callBackUrl": "https://ornek.test/geri",
	}
	if len(body) != len(want) {
		t.Errorf("body keys = %v, want exactly %v", body, want)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, body[k], v)
		}
	}
}

func TestSeparateVocal_ProviderContract(t *testing.T) {
	var body map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vocal-removal/generate" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnvelope(w, 200, map[string]any{"taskId": "sv1"})
	})
	id, err := c.SeparateVocal(context.Background(), "av1", "aud1")
	if err != nil || id != "sv1" {
		t.Fatalf("SeparateVocal = %q, %v", id, err)
	}
	want := map[string]any{"taskId": "av1", "audioId": "aud1", "type": "separate_vocal", "callBackUrl": "https://ornek.test/geri"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, body[k], v)
		}
	}
}

func TestWaitSeparated_ReadsUrls(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vocal-removal/record-info" || r.URL.Query().Get("taskId") != "sv1" {
			t.Errorf("request = %s", r.URL)
		}
		writeEnvelope(w, 200, map[string]any{"successFlag": "SUCCESS", "response": map[string]any{
			"vocalUrl": "https://cdn.test/v.mp3", "instrumentalUrl": "https://cdn.test/i.mp3",
		}})
	})
	got, err := c.WaitSeparated(context.Background(), "sv1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitSeparated: %v", err)
	}
	if got.VocalURL != "https://cdn.test/v.mp3" || got.InstrumentalURL != "https://cdn.test/i.mp3" {
		t.Errorf("got %+v", got)
	}
}

func TestWaitSeparated_EmptyVocalIsError(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, map[string]any{"successFlag": "SUCCESS", "response": map[string]any{"instrumentalUrl": "https://cdn.test/i.mp3"}})
	})
	if _, err := c.WaitSeparated(context.Background(), "sv1", time.Millisecond, time.Second); err == nil {
		t.Fatal("want an error for a missing vocalUrl")
	}
}

func TestTimestampedLyrics_ProviderContract(t *testing.T) {
	var body map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/generate/get-timestamped-lyrics" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnvelope(w, 200, map[string]any{"alignedWords": []map[string]any{
			{"word": "[Verse]\nSabah ", "startS": 27.7, "endS": 28.4, "success": true, "palign": 0},
			{"word": "oldu ", "startS": 28.5, "endS": 29.3, "success": false},
		}, "hootCer": 0.3})
	})
	words, err := c.TimestampedLyrics(context.Background(), "av1", "aud1")
	if err != nil {
		t.Fatalf("TimestampedLyrics: %v", err)
	}
	if body["taskId"] != "av1" || body["audioId"] != "aud1" {
		t.Errorf("body = %v", body)
	}
	want := []AlignedWord{{Word: "[Verse]\nSabah ", StartS: 27.7, EndS: 28.4, Success: true}, {Word: "oldu ", StartS: 28.5, EndS: 29.3}}
	if len(words) != 2 || words[0] != want[0] || words[1] != want[1] {
		t.Errorf("words = %+v, want %+v", words, want)
	}
}
