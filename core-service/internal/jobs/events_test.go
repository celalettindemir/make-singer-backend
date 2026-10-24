package jobs

import (
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func TestDeriveEvents_StatusAndProgress(t *testing.T) {
	queued := model.Job{Status: model.JobStatusQueued}
	running := model.Job{Status: model.JobStatusRunning, Progress: 5, CurrentStep: "start"}

	got := DeriveEvents(queued, running)
	if len(got) != 1 || got[0].Kind != KindProgress {
		t.Fatalf("queued->running with progress: want one job.progress, got %+v", got)
	}

	failed := running
	failed.Status = model.JobStatusFailed
	failed.ErrorCode = "RENDER_FAILED"
	got = DeriveEvents(running, failed)
	if len(got) != 1 || got[0].Kind != KindFailed {
		t.Fatalf("running->failed: got %+v", got)
	}
	if d, _ := got[0].Data.(map[string]any); d["code"] != "RENDER_FAILED" {
		t.Errorf("failed data: %+v", got[0].Data)
	}

	if got := DeriveEvents(running, running); len(got) != 0 {
		t.Errorf("no change: want none, got %+v", got)
	}

	canceled := running
	canceled.Status = model.JobStatusCanceled
	if got := DeriveEvents(running, canceled); len(got) != 1 || got[0].Kind != KindCanceled {
		t.Errorf("running->canceled: got %+v", got)
	}
	done := running
	done.Status = model.JobStatusSucceeded
	if got := DeriveEvents(running, done); len(got) != 1 || got[0].Kind != KindSucceeded {
		t.Errorf("running->succeeded: got %+v", got)
	}
	if got := DeriveEvents(queued, model.Job{Status: model.JobStatusRunning}); len(got) != 0 {
		t.Errorf("queued->running without progress: want none, got %+v", got)
	}
}
