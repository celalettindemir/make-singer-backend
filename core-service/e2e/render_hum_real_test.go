package e2e

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/model"
)

// TestRenderHum_RealSuno uploads a hum and runs a mix render on it through
// Suno Upload and Cover. Paid (about 12 Suno credits plus one OpenAI
// transcription); it needs R2, an audio-service (AUDIO_SERVICE_URL) and a
// hum file of 5-30 seconds. It checks that the vocal stem and lyricTimeline
// exist and logs the stem URLs; whether the words are really sung is a
// listening check.
func TestRenderHum_RealSuno(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Suno API test in short mode")
	}
	humFile := os.Getenv("HUM_E2E_FILE")
	if os.Getenv("SUNO_E2E_ONAY") != "1" || humFile == "" {
		t.Skip("paid Suno test skipped; set SUNO_E2E_ONAY=1 and HUM_E2E_FILE=<m4a or wav> to run it")
	}

	app, cleanup := setupRealApp(t)
	defer cleanup()
	headers := map[string]string{"Authorization": "Bearer " + generateTokenFor(t, "e2e-test-user")}
	projectID := uuid.New().String()

	raw, err := os.ReadFile(humFile)
	if err != nil {
		t.Fatalf("could not read HUM_E2E_FILE: %v", err)
	}
	contentType := "audio/wav"
	if filepath.Ext(humFile) == ".m4a" {
		contentType = "audio/mp4"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("projectId", projectID)
	_ = mw.WriteField("language", "tr")
	part, err := mw.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename=%q`, filepath.Base(humFile))},
		"Content-Type":        {contentType},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(raw)
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/hum", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", headers["Authorization"])
	resp, err := app.Test(req, 60_000)
	if err != nil {
		t.Fatalf("hum upload failed: %v", err)
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Skip("hum upload unavailable: R2 or audio-service is not configured")
	}
	assertStatus(t, resp, http.StatusCreated)
	up := parseJSON(t, resp)
	humID, _ := up["humId"].(string)
	if humID == "" {
		t.Fatalf("expected humId: %v", up)
	}
	t.Logf("hum uploaded: %v ms, transcript %v", up["durationMs"], up["transcript"])

	body := fmt.Sprintf(`{
		"projectId": "%s",
		"brief": {
			"genre": "pop",
			"vibes": ["warm"],
			"bpm": {"mode": "auto"},
			"key": {"mode": "auto"},
			"structure": [{"id": "v", "type": "verse", "bars": 8}, {"id": "c", "type": "chorus", "bars": 8}]
		},
		"arrangement": {"instruments": ["drums", "bass", "piano"], "density": "medium", "groove": "straight"},
		"vocal": {
			"voice": "female",
			"title": "Mırıltı",
			"sections": [
				{"id": "v", "type": "verse", "lines": ["Sabah oldu yine sensiz", "Pencereden doğdu güneş"]},
				{"id": "c", "type": "chorus", "lines": ["Gel bu gece yanıma", "Sar beni kollarına"]}
			]
		},
		"hum": {"humId": "%s", "fidelity": "faithful"}
	}`, projectID, humID)

	resp, err = doRequest(app, http.MethodPost, "/api/render/start", body, headers)
	if err != nil {
		t.Fatalf("start request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusAccepted)
	jobID, _ := parseJSON(t, resp)["jobId"].(string)
	if jobID == "" {
		t.Fatal("expected jobId in start response")
	}

	deadline := time.Now().Add(20 * time.Minute)
	for {
		if time.Now().After(deadline) {
			t.Fatal("job timed out after 20 minutes")
		}
		time.Sleep(5 * time.Second)
		resp, err = doRequest(app, http.MethodGet, "/api/render/status/"+jobID, "", headers)
		if err != nil {
			t.Fatalf("status request failed: %v", err)
		}
		st := parseJSON(t, resp)
		status := model.JobStatus(st["status"].(string))
		if status == model.JobStatusFailed || status == model.JobStatusCanceled {
			t.Fatalf("job %s: %v", status, st["error"])
		}
		if status == model.JobStatusSucceeded {
			break
		}
	}

	resp, err = doRequest(app, http.MethodGet, "/api/render/result/"+jobID, "", headers)
	if err != nil {
		t.Fatalf("result request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)
	result := parseJSON(t, resp)
	stems, _ := result["stems"].([]any)
	if len(stems) == 0 {
		t.Fatalf("expected stems in the result: %v", result)
	}
	for _, s := range stems {
		m, _ := s.(map[string]any)
		t.Logf("stem %v: %v", m["instrument"], m["fileUrl"])
	}
	first, _ := stems[0].(map[string]any)
	if first["instrument"] != "vocal" {
		t.Fatalf("expected the first stem to be the vocal: %v", first)
	}
	timeline, ok := result["lyricTimeline"].([]any)
	if !ok || len(timeline) < 1 {
		t.Fatalf("expected at least one section in lyricTimeline: %v", result["lyricTimeline"])
	}
	t.Logf("lyricTimeline sections: %d; listen to the vocal stem to confirm the words are sung", len(timeline))
}
