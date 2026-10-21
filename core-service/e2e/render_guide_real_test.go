package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/model"
)

// TestRenderGuide_RealSuno runs a guided render end to end: the instrumental,
// the guide singer over it, the separation, the word timings and the
// audio-service analysis. Paid (about 35 Suno credits: generate 12 +
// add-vocals 12 + separate_vocal 10 + word timings 0.5); it needs R2 and an
// audio-service (AUDIO_SERVICE_URL) on the same bucket.
func TestRenderGuide_RealSuno(t *testing.T) {
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
		"guide": {
			"vocalGender": "f",
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
	guide, ok := result["guide"].(map[string]any)
	if !ok {
		t.Fatalf("expected a guide in the result: %v", result)
	}
	t.Logf("guide: offsetMs=%v pitchShift=%v key=%v", guide["offsetMs"], guide["pitchShiftSemitones"], result["key"])

	fileURL, _ := guide["fileUrl"].(string)
	if fr, err := http.Get(fileURL); err != nil || fr.StatusCode != http.StatusOK {
		t.Fatalf("guide file not downloadable: %v %v", err, fr)
	} else {
		_ = fr.Body.Close()
	}
	notesURL, _ := guide["notesUrl"].(string)
	if notesURL == "" {
		t.Fatal("expected notesUrl")
	}
	nr, err := http.Get(notesURL)
	if err != nil {
		t.Fatalf("notes download: %v", err)
	}
	defer nr.Body.Close()
	raw, _ := io.ReadAll(nr.Body)
	var notes struct {
		Version int `json:"version"`
		Lines   []struct {
			StartMs *int `json:"startMs"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(raw, &notes); err != nil {
		t.Fatalf("notes JSON: %v", err)
	}
	matched := 0
	for _, l := range notes.Lines {
		if l.StartMs != nil {
			matched++
		}
	}
	if notes.Version != 1 || len(notes.Lines) != 4 || matched == 0 {
		t.Fatalf("notes = version %d, %d lines, %d matched", notes.Version, len(notes.Lines), matched)
	}
	t.Logf("notes: %d/%d lines matched", matched, len(notes.Lines))
}
