package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExpiresPtr_SifirZamanNil(t *testing.T) {
	if ExpiresPtr(time.Time{}) != nil {
		t.Error("sifir zaman icin nil bekleniyordu")
	}
	son := time.Now().Add(time.Hour)
	p := ExpiresPtr(son)
	if p == nil || !p.Equal(son) {
		t.Errorf("ExpiresPtr degeri kaybetti: %v", p)
	}
}

func TestUploadVocalResponse_SureliLinkteExpiresAtVar(t *testing.T) {
	son := time.Now().Add(time.Hour)
	b, err := json.Marshal(UploadVocalResponse{ID: "t1", FileURL: "https://x/y", ExpiresAt: ExpiresPtr(son)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, varmi := cikti["expiresAt"]; !varmi {
		t.Errorf("expiresAt alani yok: %s", b)
	}
}

func TestExportResponse_KaliciLinkteExpiresAtHicYok(t *testing.T) {
	b, err := json.Marshal(ExportMP3Response{FileURL: "https://cdn/exports/a.mp3", ExpiresAt: ExpiresPtr(time.Time{})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, varmi := cikti["expiresAt"]; varmi {
		t.Errorf("kalici link icin expiresAt hic olmamali: %s", b)
	}
}

func TestStemResult_ExpiresAtAlani(t *testing.T) {
	b, err := json.Marshal(StemResult{ID: "s1", FileURL: "https://x/y", ExpiresAt: ExpiresPtr(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cikti map[string]interface{}
	if err := json.Unmarshal(b, &cikti); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, varmi := cikti["expiresAt"]; !varmi {
		t.Errorf("expiresAt alani yok: %s", b)
	}
}
