package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExpiresPtr_ZeroTimeIsNil(t *testing.T) {
	if ExpiresPtr(time.Time{}) != nil {
		t.Error("sifir zaman icin nil bekleniyordu")
	}
	expiry := time.Now().Add(time.Hour)
	p := ExpiresPtr(expiry)
	if p == nil || !p.Equal(expiry) {
		t.Errorf("ExpiresPtr degeri kaybetti: %v", p)
	}
}

func TestUploadVocalResponse_ExpiringLinkHasExpiresAt(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	b, err := json.Marshal(UploadVocalResponse{ID: "t1", FileURL: "https://x/y", ExpiresAt: ExpiresPtr(expiry)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := out["expiresAt"]; !present {
		t.Errorf("expiresAt alani yok: %s", b)
	}
}

func TestExportResponse_PermanentLinkOmitsExpiresAt(t *testing.T) {
	b, err := json.Marshal(ExportMP3Response{FileURL: "https://cdn/exports/a.mp3", ExpiresAt: ExpiresPtr(time.Time{})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := out["expiresAt"]; present {
		t.Errorf("kalici link icin expiresAt hic olmamali: %s", b)
	}
}

func TestStemResult_ExpiresAtField(t *testing.T) {
	b, err := json.Marshal(StemResult{ID: "s1", FileURL: "https://x/y", ExpiresAt: ExpiresPtr(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := out["expiresAt"]; !present {
		t.Errorf("expiresAt alani yok: %s", b)
	}
}
