package client

import (
	"encoding/json"
	"testing"
)

func TestMasterRequest_AnahtarAlanlariJSON(t *testing.T) {
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

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, var_ := cikti["stem_keys"]; !var_ {
		t.Errorf("stem_keys alani yok: %s", b)
	}
	if _, yok := cikti["stem_urls"]; yok {
		t.Errorf("stem_urls alani hala duruyor: %s", b)
	}

	takes := cikti["vocal_takes"].([]interface{})
	ilk := takes[0].(map[string]interface{})
	if _, var_ := ilk["key"]; !var_ {
		t.Errorf("vocal_takes[].key alani yok: %s", b)
	}
}

func TestEncodeRequest_InputKeyJSON(t *testing.T) {
	req := EncodeRequest{InputKey: "masters/p1/m1.wav", Format: "mp3", OutputKey: "exports/e1.mp3"}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cikti["input_key"] != "masters/p1/m1.wav" {
		t.Errorf("input_key = %v", cikti["input_key"])
	}
	if _, yok := cikti["input_url"]; yok {
		t.Errorf("input_url alani hala duruyor: %s", b)
	}
}

func TestZipFileEntry_KeyJSON(t *testing.T) {
	b, err := json.Marshal(ZipFileEntry{Key: "stems/p1/s1.wav", Filename: "s1.wav"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cikti["key"] != "stems/p1/s1.wav" {
		t.Errorf("key = %v", cikti["key"])
	}
}
