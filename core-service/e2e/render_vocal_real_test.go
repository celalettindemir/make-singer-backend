package e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/model"
)

// TestRenderVocal_RealSuno runs a vocal render end to end on the mix tier: one
// Suno generate with lyrics, vocal separation and the word timings. Paid
// (about 22.5 Suno credits); it needs R2 and an audio-service
// (AUDIO_SERVICE_URL) on the same bucket.
func TestRenderVocal_RealSuno(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Suno API test in short mode")
	}
	if os.Getenv("SUNO_E2E_ONAY") != "1" {
		t.Skip("paid Suno test skipped; set SUNO_E2E_ONAY=1 to run it")
	}

	app, cleanup := setupRealApp(t)
	defer cleanup()
	headers := map[string]string{"Authorization": "Bearer " + generateTokenFor(t, "e2e-test-user")}

	body := fmt.Sprintf(`{
		"projectId": "%s",
		"brief": {
			"genre": "pop",
			"vibes": ["warm"],
			"bpm": {"mode": "fixed", "value": 100},
			"key": {"mode": "auto"},
			"structure": [{"id": "v", "type": "verse", "bars": 8}, {"id": "c", "type": "chorus", "bars": 8}]
		},
		"arrangement": {"instruments": ["drums", "bass", "piano"], "density": "medium", "groove": "straight"},
		"vocal": {
			"voice": "female",
			"title": "Sabah",
			"sections": [
				{"id": "v", "type": "verse", "lines": ["Sabah oldu yine sensiz", "Pencereden doğdu güneş"]},
				{"id": "c", "type": "chorus", "lines": ["Gel bu gece yanıma", "Sar beni kollarına"]}
			],
			"range": {"low": 55, "high": 72}
		}
	}`, uuid.New().String())

	resp, err := doRequest(app, http.MethodPost, "/api/render/start", body, headers)
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
	first, _ := stems[0].(map[string]any)
	if first["instrument"] != "vocal" {
		t.Fatalf("expected the first stem to be the vocal: %v", first)
	}
	if u, _ := first["fileUrl"].(string); u == "" {
		t.Fatal("expected a vocal stem url")
	} else if fr, err := http.Get(u); err != nil || fr.StatusCode != http.StatusOK {
		t.Fatalf("vocal stem not downloadable: %v %v", err, fr)
	} else {
		_ = fr.Body.Close()
	}

	timeline, ok := result["lyricTimeline"].([]any)
	if !ok || len(timeline) < 1 {
		t.Fatalf("expected at least one section in lyricTimeline: %v", result["lyricTimeline"])
	}
	t.Logf("vocal stem ok; lyricTimeline sections: %d", len(timeline))
}
