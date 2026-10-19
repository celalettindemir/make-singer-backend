package client

import (
	"encoding/json"
	"testing"
)

func TestMasterRequest_KeyFieldsJSON(t *testing.T) {
	req := MasterRequest{
		StemKeys:   []string{"stems/p1/s1.wav"},
		Profile:    "clean",
		VocalTakes: []VocalTakeInput{{Key: "vocals/p1/s1/t1.wav", Volume: 1.0}},
		OutputKey:  "masters/p1/m1.wav",
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := out["stem_keys"]; !present {
		t.Errorf("stem_keys field missing: %s", b)
	}
	if _, absent := out["stem_urls"]; absent {
		t.Errorf("stem_urls field is still present: %s", b)
	}

	takes := out["vocal_takes"].([]interface{})
	first := takes[0].(map[string]interface{})
	if _, present := first["key"]; !present {
		t.Errorf("vocal_takes[].key field missing: %s", b)
	}
}

// TestMixChannel_HasNoURLField: the mix channel must not carry any URL field.
// The Python side now rejects stem_url too (extra forbid); both sides changed
// together.
func TestMixChannel_HasNoURLField(t *testing.T) {
	b, err := json.Marshal(MixChannel{Volume: 0.5, Pan: 0.1, Mute: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"stem_url", "stem_urls", "url"} {
		if _, absent := out[field]; absent {
			t.Errorf("%s field is still present: %s", field, b)
		}
	}
	if out["volume"] != 0.5 {
		t.Errorf("volume = %v", out["volume"])
	}
}

func TestEncodeRequest_InputKeyJSON(t *testing.T) {
	req := EncodeRequest{InputKey: "masters/p1/m1.wav", Format: "mp3", OutputKey: "exports/e1.mp3"}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["input_key"] != "masters/p1/m1.wav" {
		t.Errorf("input_key = %v", out["input_key"])
	}
	if _, absent := out["input_url"]; absent {
		t.Errorf("input_url field is still present: %s", b)
	}
}

func TestZipFileEntry_KeyJSON(t *testing.T) {
	b, err := json.Marshal(ZipFileEntry{Key: "stems/p1/s1.wav", Filename: "s1.wav"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["key"] != "stems/p1/s1.wav" {
		t.Errorf("key = %v", out["key"])
	}
}

func TestVocalTakeInput_TimelineJSON(t *testing.T) {
	start, end := 1000, 2500
	b, _ := json.Marshal(VocalTakeInput{Key: "k", Volume: 1, StartMs: &start, EndMs: &end})
	var out map[string]interface{}
	_ = json.Unmarshal(b, &out)
	if out["start_ms"] != float64(1000) || out["end_ms"] != float64(2500) {
		t.Errorf("start_ms/end_ms wrong: %s", b)
	}

	b, _ = json.Marshal(VocalTakeInput{Key: "k", Volume: 1})
	out = nil
	_ = json.Unmarshal(b, &out)
	if _, present := out["start_ms"]; present {
		t.Errorf("start_ms must be omitted when nil: %s", b)
	}
	if _, present := out["end_ms"]; present {
		t.Errorf("end_ms must be omitted when nil: %s", b)
	}
}
