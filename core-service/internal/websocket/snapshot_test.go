package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func snapshotOf(t *testing.T, job *model.Job, err error) map[string]any {
	t.Helper()
	hub := NewHub(nil).WithSnapshot(func(context.Context, string) (*model.Job, error) { return job, err })
	data := hub.snapshot(context.Background(), "job-1")
	if data == nil {
		return nil
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("snapshot is not JSON: %v", err)
	}
	return got
}

// A client that connects after the job ended still learns the outcome (the socket stayed silent before).
func TestSnapshot_SucceededJobSendsComplete(t *testing.T) {
	got := snapshotOf(t, &model.Job{ID: "job-1", Type: model.JobTypeRender, Status: model.JobStatusSucceeded,
		Progress: 100, Result: []byte(`{"stems":[]}`)}, nil)
	if got["type"] != model.WSMessageTypeComplete || got["jobId"] != "job-1" {
		t.Fatalf("got %v, want a complete message", got)
	}
	if got["result"] != nil {
		t.Fatalf("result = %v, want null: the clients fetch the result endpoint for fresh links", got["result"])
	}
}

func TestSnapshot_FailedJobSendsError(t *testing.T) {
	msg := "suno down"
	got := snapshotOf(t, &model.Job{ID: "job-1", Type: model.JobTypeMaster, Status: model.JobStatusFailed, Error: &msg}, nil)
	if got["type"] != model.WSMessageTypeError {
		t.Fatalf("got %v, want an error message", got)
	}
	e := got["error"].(map[string]any)
	if e["code"] != "MASTER_FAILED" || e["message"] != msg {
		t.Fatalf("error = %v, want MASTER_FAILED / %q", e, msg)
	}
}

func TestSnapshot_StoredErrorCodeWins(t *testing.T) {
	got := snapshotOf(t, &model.Job{ID: "job-1", Type: model.JobTypeRender, Status: model.JobStatusFailed, ErrorCode: "SUNO_TIMEOUT"}, nil)
	if e := got["error"].(map[string]any); e["code"] != "SUNO_TIMEOUT" || e["message"] != "" {
		t.Fatalf("error = %v, want SUNO_TIMEOUT with an empty message", e)
	}
}

func TestSnapshot_RunningJobSendsProgress(t *testing.T) {
	got := snapshotOf(t, &model.Job{ID: "job-1", Type: model.JobTypeRender, Status: model.JobStatusRunning,
		Progress: 40, CurrentStep: "vocal"}, nil)
	if got["type"] != model.WSMessageTypeProgress || got["progress"] != float64(40) ||
		got["status"] != string(model.JobStatusRunning) || got["currentStep"] != "vocal" {
		t.Fatalf("got %v, want progress 40 running vocal", got)
	}
}

func TestSnapshot_NothingWithoutAJob(t *testing.T) {
	if got := snapshotOf(t, nil, errors.New("not found")); got != nil {
		t.Fatalf("got %v, want no snapshot when the job cannot be read", got)
	}
	if got := snapshotOf(t, &model.Job{ID: "job-1", Status: model.JobStatusCanceled}, nil); got != nil {
		t.Fatalf("got %v, want no snapshot for a canceled job (clients treat it as non-terminal)", got)
	}
	if data := NewHub(nil).snapshot(context.Background(), "job-1"); data != nil {
		t.Fatalf("got %s, want no snapshot without a source", data)
	}
}
