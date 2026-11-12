package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestUploadCover_ProviderContract(t *testing.T) {
	var body map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/generate/upload-cover" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer anahtar" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnvelope(w, 200, map[string]any{"taskId": "c1"})
	})

	id, err := c.UploadCover(context.Background(), &UploadCoverRequest{
		UploadURL: "https://imzali/h.mp3", Style: "pop, dreamy", Title: "Sabah",
		Lyrics: "[Chorus]\nla", VocalGender: "m", AudioWeight: 0.9, StyleWeight: 0.5,
	})
	if err != nil {
		t.Fatalf("UploadCover: %v", err)
	}
	if id != "c1" {
		t.Errorf("taskID = %q", id)
	}
	want := map[string]any{
		"uploadUrl": "https://imzali/h.mp3", "customMode": true, "instrumental": false, "model": "V6",
		"callBackUrl": "https://ornek.test/geri", "style": "pop, dreamy", "title": "Sabah",
		"lyrics": "[Chorus]\nla", "vocalGender": "m", "audioWeight": 0.9, "styleWeight": 0.5,
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, body[k], v)
		}
	}
}

func TestUploadCover_OmitsEmptyVocalGender(t *testing.T) {
	var body map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnvelope(w, 200, map[string]any{"taskId": "c1"})
	})
	if _, err := c.UploadCover(context.Background(), &UploadCoverRequest{UploadURL: "u"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["vocalGender"]; ok {
		t.Errorf("vocalGender sent when empty: %v", body)
	}
}

func TestUploadCover_EnvelopeError(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 400, nil)
	})
	if _, err := c.UploadCover(context.Background(), &UploadCoverRequest{UploadURL: "u"}); err == nil {
		t.Fatal("want an error for a code 400 envelope")
	}
}
