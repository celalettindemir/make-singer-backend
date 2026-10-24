package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/websocket"
)

func masterJobStatus(t *testing.T, repo jobs.Repo) model.JobStatus {
	t.Helper()
	job, err := repo.Get(context.Background(), testMasterJobID)
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	return job.Status
}

func putMasterJob(t *testing.T, repo jobs.Repo, status model.JobStatus) {
	t.Helper()
	job := &model.Job{ID: testMasterJobID, Type: model.JobTypeMaster, OwnerID: "kullanici-a", Status: status, CreatedAt: time.Now()}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatalf("job: %v", err)
	}
}

func TestMasterWorker_RefundsOnlyOnFinalFailure(t *testing.T) {
	repo := jobs.NewMemoryStore(nil)
	putMasterJob(t, repo, model.JobStatusQueued)
	fake := &credit.FakeSpender{IsEnforced: true}
	audio := &fakeAudio{err: errors.New("audio 503")}
	w := NewMasterWorker(repo, audio, &fakeStorage{}, nil, websocket.NewHub(nil), testR2Cfg(), nil).WithCredits(fake)

	// Attempt 1 of 3: no refund, the job is not failed.
	w.isLastAttempt = func(context.Context) bool { return false }
	if err := w.processWithAudioService(context.Background(), testMasterJobID, masterPayload()); err == nil {
		t.Fatal("want the error returned")
	}
	if st := masterJobStatus(t, repo); st == model.JobStatusFailed {
		t.Errorf("an intermediate failure marked the job failed")
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("refunds = %+v, want none before the last attempt", fake.Refunds)
	}

	// Last attempt: failed, one refund.
	w.isLastAttempt = func(context.Context) bool { return true }
	if err := w.processWithAudioService(context.Background(), testMasterJobID, masterPayload()); err == nil {
		t.Fatal("want the error returned")
	}
	if st := masterJobStatus(t, repo); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(fake.Refunds) != 1 || fake.Refunds[0].Op != credit.OpMaster || fake.Refunds[0].ID != testMasterJobID {
		t.Errorf("refunds = %+v, want one master refund for m1", fake.Refunds)
	}
}

func TestMasterWorker_SkipRetryFailsAndRefundsAtOnce(t *testing.T) {
	repo := jobs.NewMemoryStore(nil)
	putMasterJob(t, repo, model.JobStatusQueued)
	fake := &credit.FakeSpender{IsEnforced: true}
	w := NewMasterWorker(repo, &fakeAudio{}, &fakeStorage{}, nil, websocket.NewHub(nil), testR2Cfg(), nil).WithCredits(fake)
	w.isLastAttempt = func(context.Context) bool { return false }

	p := masterPayload()
	p.StemURLs = []string{"https://example.com/not-ours.mp3"}
	err := w.processWithAudioService(context.Background(), testMasterJobID, p)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got: %v", err)
	}
	if st := masterJobStatus(t, repo); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(fake.Refunds) != 1 {
		t.Errorf("refunds = %+v, want one", fake.Refunds)
	}
}

func TestMasterWorker_CanceledJobSkipsAudioService(t *testing.T) {
	repo := jobs.NewMemoryStore(nil)
	putMasterJob(t, repo, model.JobStatusQueued)
	fake := &credit.FakeSpender{IsEnforced: true}
	audio := &fakeAudio{}
	w := NewMasterWorker(repo, audio, &fakeStorage{}, nil, websocket.NewHub(nil), testR2Cfg(), nil).WithCredits(fake)
	w.isLastAttempt = func(context.Context) bool { return true }

	// The cancel arrives while the job waits in the queue.
	if err := repo.Update(context.Background(), testMasterJobID, func(j *model.Job) error {
		j.Status = model.JobStatusCanceled
		return nil
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if err := w.processWithAudioService(context.Background(), testMasterJobID, masterPayload()); err != nil {
		t.Fatalf("processWithAudioService: %v", err)
	}
	if audio.masterCalls != 0 {
		t.Errorf("the audio service was called %d times for a canceled job", audio.masterCalls)
	}
	if st := masterJobStatus(t, repo); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled (the worker must not overwrite it)", st)
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("refunds = %+v, want none from the worker", fake.Refunds)
	}
}
