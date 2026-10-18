package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/websocket"
	"github.com/redis/go-redis/v9"
)

func masterJobStatus(t *testing.T, rdb *redis.Client) model.JobStatus {
	t.Helper()
	raw, err := rdb.Get(context.Background(), "job:m1").Bytes()
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	var job model.Job
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return job.Status
}

func TestMasterWorker_RefundsOnlyOnFinalFailure(t *testing.T) {
	rdb := creditRedis(t)
	raw, _ := json.Marshal(model.Job{ID: "m1", Type: model.JobTypeMaster, OwnerID: "kullanici-a", Status: model.JobStatusQueued, CreatedAt: time.Now()})
	if err := rdb.Set(context.Background(), "job:m1", raw, time.Hour).Err(); err != nil {
		t.Fatalf("job: %v", err)
	}
	fake := &credit.FakeSpender{IsEnforced: true}
	audio := &fakeAudio{err: errors.New("audio 503")}
	w := NewMasterWorker(rdb, audio, &fakeStorage{}, nil, websocket.NewHub(), testR2Cfg(), nil).WithCredits(fake)

	// Attempt 1 of 3: no refund, the job is not failed.
	w.isLastAttempt = func(context.Context) bool { return false }
	if err := w.processWithAudioService(context.Background(), "m1", masterPayload()); err == nil {
		t.Fatal("want the error returned")
	}
	if st := masterJobStatus(t, rdb); st == model.JobStatusFailed {
		t.Errorf("an intermediate failure marked the job failed")
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("refunds = %+v, want none before the last attempt", fake.Refunds)
	}

	// Last attempt: failed, one refund.
	w.isLastAttempt = func(context.Context) bool { return true }
	if err := w.processWithAudioService(context.Background(), "m1", masterPayload()); err == nil {
		t.Fatal("want the error returned")
	}
	if st := masterJobStatus(t, rdb); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(fake.Refunds) != 1 || fake.Refunds[0].Op != credit.OpMaster || fake.Refunds[0].ID != "m1" {
		t.Errorf("refunds = %+v, want one master refund for m1", fake.Refunds)
	}
}

func TestMasterWorker_SkipRetryFailsAndRefundsAtOnce(t *testing.T) {
	rdb := creditRedis(t)
	raw, _ := json.Marshal(model.Job{ID: "m1", Type: model.JobTypeMaster, OwnerID: "kullanici-a", Status: model.JobStatusQueued, CreatedAt: time.Now()})
	if err := rdb.Set(context.Background(), "job:m1", raw, time.Hour).Err(); err != nil {
		t.Fatalf("job: %v", err)
	}
	fake := &credit.FakeSpender{IsEnforced: true}
	w := NewMasterWorker(rdb, &fakeAudio{}, &fakeStorage{}, nil, websocket.NewHub(), testR2Cfg(), nil).WithCredits(fake)
	w.isLastAttempt = func(context.Context) bool { return false }

	p := masterPayload()
	p.StemURLs = []string{"https://example.com/not-ours.mp3"}
	err := w.processWithAudioService(context.Background(), "m1", p)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got: %v", err)
	}
	if st := masterJobStatus(t, rdb); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(fake.Refunds) != 1 {
		t.Errorf("refunds = %+v, want one", fake.Refunds)
	}
}
