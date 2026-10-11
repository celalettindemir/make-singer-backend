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

func zarfYaz(w http.ResponseWriter, code int, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": "m", "data": data})
}

func TestGenerateMusic_SaglayiciSozlesmesi(t *testing.T) {
	var govde map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/generate" {
			t.Errorf("istek = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer anahtar" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&govde)
		zarfYaz(w, 200, map[string]any{"taskId": "t1"})
	})

	id, err := c.GenerateMusic(context.Background(), &GenerateMusicRequest{Style: "pop, instrumental", Title: "pop instrumental", Instrumental: true})
	if err != nil {
		t.Fatalf("GenerateMusic: %v", err)
	}
	if id != "t1" {
		t.Errorf("taskID = %q", id)
	}
	beklenen := map[string]any{
		"customMode": true, "instrumental": true, "model": "V6",
		"callBackUrl": "https://ornek.test/geri", "style": "pop, instrumental", "title": "pop instrumental",
	}
	for k, v := range beklenen {
		if govde[k] != v {
			t.Errorf("govde[%q] = %v, beklenen %v", k, govde[k], v)
		}
	}
}

func TestGenerateMusic_ZarfHataKodu(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 429, "msg": "insufficient credits", "data": nil})
	})
	_, err := c.GenerateMusic(context.Background(), &GenerateMusicRequest{Style: "pop"})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("429 hatasi bekleniyordu, geldi: %v", err)
	}
}

func TestWaitMusic_SuccessIlkParcayiDoner(t *testing.T) {
	var n atomic.Int32
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/generate/record-info" || r.URL.Query().Get("taskId") != "t1" {
			t.Errorf("istek = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if n.Add(1) == 1 {
			zarfYaz(w, 200, map[string]any{"status": "PENDING"})
			return
		}
		zarfYaz(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{"sunoData": []any{
			map[string]any{"id": "a1", "audio_url": "https://cdn.test/a1.mp3", "duration": 201.16},
			map[string]any{"id": "a2", "audio_url": "https://cdn.test/a2.mp3", "duration": 191.6},
		}}})
	})

	tr, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitMusic: %v", err)
	}
	if tr.ID != "a1" || tr.AudioURL != "https://cdn.test/a1.mp3" || tr.Duration != 201.16 {
		t.Errorf("parca = %+v", tr)
	}
}

func TestWaitMusic_HataDurumuAnindaDoner(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		zarfYaz(w, 200, map[string]any{"status": "SENSITIVE_WORD_ERROR", "errorMessage": "yasakli kelime"})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "yasakli kelime") {
		t.Fatalf("hata mesaji bekleniyordu, geldi: %v", err)
	}
}

func TestWaitMusic_IkiGeciciHatayiTolereEder(t *testing.T) {
	var n atomic.Int32
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		zarfYaz(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{"sunoData": []any{
			map[string]any{"id": "a1", "audio_url": "u", "duration": 1.0},
		}}})
	})
	if _, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second); err != nil {
		t.Fatalf("iki gecici hata tolere edilmeliydi: %v", err)
	}
}

func TestWaitMusic_UcArdisikHatadaVazgecer(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second); err == nil {
		t.Fatal("uc ardisik hatada vazgecmeliydi")
	}
}

func TestWaitMusic_ZamanAsimi(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		zarfYaz(w, 200, map[string]any{"status": "TEXT_SUCCESS"})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "zaman asimi") {
		t.Fatalf("zaman asimi bekleniyordu, geldi: %v", err)
	}
}

var _ MusicGenerator = (*SunoClient)(nil)

func TestSplitStems_SaglayiciSozlesmesi(t *testing.T) {
	var govde map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vocal-removal/generate" {
			t.Errorf("istek = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&govde)
		zarfYaz(w, 200, map[string]any{"taskId": "s1"})
	})

	id, err := c.SplitStems(context.Background(), "t1", "a1")
	if err != nil {
		t.Fatalf("SplitStems: %v", err)
	}
	if id != "s1" {
		t.Errorf("stemTaskID = %q", id)
	}
	beklenen := map[string]any{"taskId": "t1", "audioId": "a1", "type": "split_stem", "callBackUrl": "https://ornek.test/geri"}
	for k, v := range beklenen {
		if govde[k] != v {
			t.Errorf("govde[%q] = %v, beklenen %v", k, govde[k], v)
		}
	}
}

func TestWaitStems_EnstrumanlariEslerVokaliAtlar(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vocal-removal/record-info" || r.URL.Query().Get("taskId") != "s1" {
			t.Errorf("istek = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		zarfYaz(w, 200, map[string]any{"successFlag": "SUCCESS", "response": map[string]any{
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
	beklenen := []Stem{
		{Instrument: model.InstrumentDrums, URL: "https://cdn.test/davul.mp3"},
		{Instrument: model.InstrumentPiano, URL: "https://cdn.test/klavye.mp3"},
	}
	if len(stems) != len(beklenen) {
		t.Fatalf("stems = %+v, beklenen %+v", stems, beklenen)
	}
	for i := range beklenen {
		if stems[i] != beklenen[i] {
			t.Errorf("stems[%d] = %+v, beklenen %+v", i, stems[i], beklenen[i])
		}
	}
}

func TestWaitStems_HataBayragi(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		zarfYaz(w, 200, map[string]any{"successFlag": "GENERATE_AUDIO_FAILED", "errorMessage": "bolunemedi"})
	})
	_, err := c.WaitStems(context.Background(), "s1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "bolunemedi") {
		t.Fatalf("hata bekleniyordu, geldi: %v", err)
	}
}

func TestWaitMusic_BosParcaKimligiHata(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		zarfYaz(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{
			"sunoData": []any{map[string]any{"id": "", "audio_url": "u", "duration": 1.0}},
		}})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "kimligi") {
		t.Fatalf("hata bekleniyordu, geldi: %v", err)
	}
}

func advancedClient(t *testing.T, h http.HandlerFunc) *SunoClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewSunoClient(&config.SunoConfig{APIKey: "k", BaseURL: srv.URL, Model: "V6", CallbackURL: "https://cb.test/suno"})
}

func TestIsolateStem_SaglayiciSozlesmesi(t *testing.T) {
	var govde map[string]any
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vocal-removal/generate" {
			t.Errorf("istek = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&govde)
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"taskId":"a1"}}`))
	})

	id, err := c.IsolateStem(context.Background(), "t1", "m1", "Drum Kit")
	if err != nil || id != "a1" {
		t.Fatalf("IsolateStem = %q, %v", id, err)
	}
	beklenen := map[string]any{"taskId": "t1", "audioId": "m1", "type": "split_stem_advanced", "stemName": "Drum Kit", "callBackUrl": "https://cb.test/suno"}
	for k, v := range beklenen {
		if govde[k] != v {
			t.Errorf("govde[%s] = %v, beklenen %v", k, govde[k], v)
		}
	}
}

// advancedYanit, 2026-10-11 canli kesifteki gercek yanitin kisaltilmis hali.
const advancedYanit = `{"code":200,"msg":"success","data":{
 "taskId":"a1","successFlag":"SUCCESS","errorMessage":null,
 "response":{"originUrl":null,"drumsUrl":null,"originData":[
  {"extract":{"duration":141.48,"audio_url":"https://tempfile.test/r/x_Drum Kit.mp3","stem_type_group_name":"Drum Kit","id":"e1"},
   "remove":{"duration":141.48,"audio_url":"https://tempfile.test/r/y_Drum Kit.mp3","stem_type_group_name":"Drum Kit","id":"r1"}},
  {"extract":{"duration":141.48,"audio_url":"https://tempfile.test/r/ikinci-e.mp3","id":"e2"},
   "remove":{"duration":141.48,"audio_url":"https://tempfile.test/r/ikinci-r.mp3","id":"r2"}}
 ]}}}`

func TestWaitIsolated_IlkVaryantinExtractVeRemoveAdresleri(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vocal-removal/record-info" || r.URL.Query().Get("taskId") != "a1" {
			t.Errorf("istek = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(advancedYanit))
	})

	s, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitIsolated: %v", err)
	}
	if s.ExtractURL != "https://tempfile.test/r/x_Drum Kit.mp3" || s.RemoveURL != "https://tempfile.test/r/y_Drum Kit.mp3" || s.Duration != 141.48 {
		t.Errorf("sonuc = %+v", s)
	}
}

func TestWaitIsolated_BosOriginDataHata(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"SUCCESS","response":{"originData":[]}}}`))
	})
	if _, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second); err == nil {
		t.Fatal("bos originData icin hata bekleniyordu")
	}
}

func TestWaitIsolated_EksikAdresHata(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"SUCCESS","response":{"originData":[{"extract":{"audio_url":"https://x.test/e.mp3"},"remove":{"audio_url":""}}]}}}`))
	})
	if _, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second); err == nil {
		t.Fatal("bos remove adresi icin hata bekleniyordu")
	}
}

func TestWaitIsolated_HataBayragi(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"GENERATE_AUDIO_FAILED","errorMessage":"katalog eslesmesi"}}`))
	})
	_, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "katalog eslesmesi") {
		t.Fatalf("hata mesaji tasinmali, geldi: %v", err)
	}
}

func TestSunoStemAdi(t *testing.T) {
	beklenen := map[model.Instrument]string{
		model.InstrumentDrums: "Drum Kit", model.InstrumentBass: "Bass", model.InstrumentPiano: "Piano",
		model.InstrumentGuitar: "Guitar", model.InstrumentSynth: "Synth", model.InstrumentStrings: "String Section",
		model.InstrumentBrass: "Brass Section", model.InstrumentWoodwinds: "Woodwinds", model.InstrumentPercussion: "Percussion",
		model.InstrumentPads: "Synth Pad", model.InstrumentLead: "Synth Lead", model.InstrumentFX: "Sound Effects",
	}
	for i, ad := range beklenen {
		if got, ok := SunoStemAdi(i); !ok || got != ad {
			t.Errorf("SunoStemAdi(%s) = %q, %v; beklenen %q", i, got, ok, ad)
		}
	}
	for _, i := range []model.Instrument{model.InstrumentMix, model.InstrumentRest, "kazoo"} {
		if _, ok := SunoStemAdi(i); ok {
			t.Errorf("SunoStemAdi(%s) eslesmemeliydi", i)
		}
	}
}
