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
