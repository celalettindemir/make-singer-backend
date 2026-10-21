package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
	"github.com/redis/go-redis/v9"
)

func creditRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 11})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	_ = rdb.FlushDB(context.Background()).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

func putJob(t *testing.T, rdb *redis.Client, status model.JobStatus) {
	t.Helper()
	raw, _ := json.Marshal(model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: status, CreatedAt: time.Now()})
	if err := rdb.Set(context.Background(), "job:is1", raw, time.Hour).Err(); err != nil {
		t.Fatalf("could not write the job: %v", err)
	}
}

func jobStatus(t *testing.T, svc *service.RenderService) model.JobStatus {
	t.Helper()
	st, err := svc.JobStatus(context.Background(), "is1")
	if err != nil {
		t.Fatalf("JobStatus: %v", err)
	}
	return st
}

func renderTask(t *testing.T, mode model.StemMode) *asynq.Task {
	t.Helper()
	p := testPayload()
	p.StemMode = mode
	pb, _ := json.Marshal(p)
	data, _ := json.Marshal(map[string]any{"jobId": "is1", "payload": json.RawMessage(pb)})
	return asynq.NewTask(service.TaskTypeRender, data)
}

type creditSetup struct {
	w    *RenderWorker
	svc  *service.RenderService
	fake *credit.FakeSpender
	suno *fakeSuno
	rdb  *redis.Client
}

// newCreditSetup builds a worker on a live Redis job in the given status.
// The attempt is not the last one unless the test says so.
func newCreditSetup(t *testing.T, status model.JobStatus, store client.StorageClient) *creditSetup {
	t.Helper()
	rdb := creditRedis(t)
	putJob(t, rdb, status)
	svc := service.NewRenderService(rdb, nil, store, &config.R2Config{})
	fake := &credit.FakeSpender{IsEnforced: true}
	suno := &fakeSuno{}
	w := NewRenderWorker(svc, suno, store, websocket.NewHub(), nil).WithCredits(fake)
	w.pollInterval = time.Millisecond
	w.isLastAttempt = func(context.Context) bool { return false }
	return &creditSetup{w: w, svc: svc, fake: fake, suno: suno, rdb: rdb}
}

func TestRenderWorker_CanceledJobSkipsSunoAndRefunds(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusCanceled, &fakeStorage{})

	if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeStems)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if s.suno.generateCalls != 0 {
		t.Errorf("GenerateMusic was called %d times for a canceled job", s.suno.generateCalls)
	}
	if len(renderRefunds(s.fake)) != 1 || renderRefunds(s.fake)[0].ID != "is1" || renderRefunds(s.fake)[0].Op != credit.OpRenderStems {
		t.Errorf("refunds = %+v, want one render_stems for is1", renderRefunds(s.fake))
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled", st)
	}
}

func TestRenderWorker_RetryableFailureKeepsRunning(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	s.suno.generateErr = errors.New("suno 503")

	err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix))
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want a retryable error, got: %v", err)
	}
	if st := jobStatus(t, s.svc); st == model.JobStatusFailed {
		t.Errorf("an intermediate failure marked the job failed")
	}
	if len(renderRefunds(s.fake)) != 0 {
		t.Errorf("refunds = %+v, want none before the last attempt", renderRefunds(s.fake))
	}
}

func TestRenderWorker_LastAttemptFailsAndRefunds(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	s.suno.generateErr = errors.New("suno 503")
	s.w.isLastAttempt = func(context.Context) bool { return true }

	if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeFocus)); err == nil {
		t.Fatal("want the error returned")
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(renderRefunds(s.fake)) != 1 || renderRefunds(s.fake)[0].ID != "is1" || renderRefunds(s.fake)[0].Op != credit.OpRenderFocus {
		t.Errorf("refunds = %+v, want one render_focus for is1", renderRefunds(s.fake))
	}
}

func TestRenderWorker_SkipRetryBeforeChargeFailsAndRefundsEarly(t *testing.T) {
	// No storage: generateWithSuno fails with SkipRetry before the paid call.
	s := newCreditSetup(t, model.JobStatusQueued, nil)
	s.w.r2Client = nil

	err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got: %v", err)
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(renderRefunds(s.fake)) != 1 {
		t.Errorf("refunds = %+v, want one", renderRefunds(s.fake))
	}
}

// deleteJobOnUpload removes the job record while the stems are uploaded, so
// CompleteJob fails after the paid generation.
type deleteJobOnUpload struct {
	fakeStorage
	rdb *redis.Client
}

func (d *deleteJobOnUpload) Upload(ctx context.Context, key string, body io.Reader, ct string) (string, time.Time, error) {
	_ = d.rdb.Del(context.Background(), "job:is1").Err()
	return d.fakeStorage.Upload(ctx, key, body, ct)
}

func TestRenderWorker_SkipRetryAfterChargeRefunds(t *testing.T) {
	srv := audioServer(t)
	rdb := creditRedis(t)
	putJob(t, rdb, model.JobStatusQueued)
	store := &deleteJobOnUpload{rdb: rdb}
	svc := service.NewRenderService(rdb, nil, store, &config.R2Config{})
	fake := &credit.FakeSpender{IsEnforced: true}
	suno := &fakeSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 60}}
	w := NewRenderWorker(svc, suno, store, websocket.NewHub(), nil).WithCredits(fake)
	w.pollInterval = time.Millisecond
	w.isLastAttempt = func(context.Context) bool { return false }

	err := w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry after the paid call, got: %v", err)
	}
	if suno.generateCalls != 1 {
		t.Fatalf("generateCalls = %d, want 1", suno.generateCalls)
	}
	if got := renderRefunds(fake); len(got) != 1 || got[0].ID != "is1" || got[0].Op != credit.OpRenderMix {
		t.Errorf("refunds = %+v, want one render_mix for is1", fake.Refunds)
	}
}

func TestRenderWorker_RefundFailureIsNotReturned(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusCanceled, &fakeStorage{})
	s.fake.Err = errors.New("db down")

	if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix)); err != nil {
		t.Errorf("a refund failure must not be returned (it would retry the render): %v", err)
	}
}

func TestRenderWorker_NilSpenderUnchanged(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	s.w.credits = nil
	s.suno.generateErr = errors.New("suno 503")
	s.w.isLastAttempt = func(context.Context) bool { return true }

	if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix)); err == nil {
		t.Fatal("want the error returned")
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
}

// cancelHook cancels the job (through a service that has its own spender)
// when the worker reaches the hooked step.
type cancelBeforeClaim struct{ cancel func() }

func (c cancelBeforeClaim) IsDeleted(context.Context, string) (bool, error) {
	c.cancel() // runs after the attempt-start check, before the claim
	return false, nil
}

func TestRenderWorker_CancelBeforeClaimSkipsSunoAndRefundsOnce(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	s.svc.WithCredits(s.fake) // the cancel's own refund is observed too
	s.w.deletionChecker = cancelBeforeClaim{cancel: func() {
		if _, err := s.svc.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
			t.Errorf("CancelRender: %v", err)
		}
	}}

	if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if s.suno.generateCalls != 0 {
		t.Errorf("GenerateMusic called %d times after a committed cancel", s.suno.generateCalls)
	}
	// The cancel refunds (queued) and the worker refunds again; the ledger
	// keys both on the jobId, so they are one refund row.
	// The cancel and the worker each refund the render (and try the absent
	// guide debit); the ledger keys the render refunds on the jobId.
	if got := renderRefunds(s.fake); len(got) != 2 || got[0].ID != "is1" || got[1].ID != "is1" {
		t.Errorf("refunds = %+v, want two render calls for is1", got)
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled", st)
	}
}

// cancelOnGenerate cancels the job once GenerateMusic has been reached,
// i.e. after the claim.
type cancelOnGenerate struct {
	*fakeSuno
	cancel func()
}

func (c cancelOnGenerate) GenerateMusic(ctx context.Context, r *client.GenerateMusicRequest) (string, error) {
	c.cancel()
	return c.fakeSuno.GenerateMusic(ctx, r)
}

func TestRenderWorker_CancelAfterClaimNoRefund(t *testing.T) {
	rdb := creditRedis(t)
	putJob(t, rdb, model.JobStatusQueued)
	cancelSpender := &credit.FakeSpender{IsEnforced: true}
	svc := service.NewRenderService(rdb, nil, &fakeStorage{}, &config.R2Config{}).WithCredits(cancelSpender)
	workerSpender := &credit.FakeSpender{IsEnforced: true}
	suno := &fakeSuno{generateErr: errors.New("suno 503")}
	music := cancelOnGenerate{fakeSuno: suno, cancel: func() {
		if _, err := svc.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
			t.Errorf("CancelRender: %v", err)
		}
	}}
	w := NewRenderWorker(svc, music, &fakeStorage{}, websocket.NewHub(), nil).WithCredits(workerSpender)
	w.isLastAttempt = func(context.Context) bool { return false }

	_ = w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix))

	if suno.generateCalls != 1 {
		t.Fatalf("generateCalls = %d, want 1", suno.generateCalls)
	}
	if len(cancelSpender.Refunds) != 0 || len(workerSpender.Refunds) != 0 {
		t.Errorf("refunds cancel=%+v worker=%+v, want none after the claim", cancelSpender.Refunds, workerSpender.Refunds)
	}
	if st := jobStatus(t, svc); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled", st)
	}
}

func TestRenderWorker_InvalidPayloadFailsAndRefunds(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	data, _ := json.Marshal(map[string]any{"jobId": "is1", "payload": []int{1}})

	err := s.w.ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeRender, data))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got: %v", err)
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(renderRefunds(s.fake)) != 1 || renderRefunds(s.fake)[0].ID != "is1" {
		t.Errorf("refunds = %+v, want one for is1", renderRefunds(s.fake))
	}
}

func TestRenderWorker_MockContextDoneOnLastAttemptRefunds(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	s.w.music = nil // mock path
	s.w.isLastAttempt = func(context.Context) bool { return true }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := s.w.ProcessTask(ctx, renderTask(t, model.StemModeMix)); err == nil {
		t.Fatal("want the context error")
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if len(renderRefunds(s.fake)) != 1 {
		t.Errorf("refunds = %+v, want one", renderRefunds(s.fake))
	}
}

func TestRenderWorker_MockCanceledBeforeClaimRefunds(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	s.w.music = nil // mock path
	s.w.afterStatusCheck = func() {
		if _, err := s.svc.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
			t.Errorf("CancelRender: %v", err)
		}
	}

	if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(renderRefunds(s.fake)) != 1 || renderRefunds(s.fake)[0].ID != "is1" {
		t.Errorf("refunds = %+v, want one for is1", renderRefunds(s.fake))
	}
	if st := jobStatus(t, s.svc); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled (the mock must not run)", st)
	}
}

func TestRenderWorker_RedeliveredFinishedJobNoRefundNoSuno(t *testing.T) {
	for _, st := range []model.JobStatus{model.JobStatusSucceeded, model.JobStatusFailed} {
		for _, mock := range []bool{false, true} {
			s := newCreditSetup(t, st, &fakeStorage{})
			if mock {
				s.w.music = nil
			}
			if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix)); err != nil {
				t.Fatalf("%s mock=%v: ProcessTask: %v", st, mock, err)
			}
			if s.suno.generateCalls != 0 {
				t.Errorf("%s mock=%v: GenerateMusic called", st, mock)
			}
			if len(renderRefunds(s.fake)) != 0 {
				t.Errorf("%s mock=%v: refunds = %+v, want none", st, mock, renderRefunds(s.fake))
			}
			if got := jobStatus(t, s.svc); got != st {
				t.Errorf("%s mock=%v: status changed to %s", st, mock, got)
			}
		}
	}
}

// TestRenderWorker_FinishedBeforeClaimNoRefund ends the job after the attempt-start check, so the
// claim sees a finished job.
func TestRenderWorker_FinishedBeforeClaimNoRefund(t *testing.T) {
	s := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	s.w.deletionChecker = cancelBeforeClaim{cancel: func() {
		if err := s.svc.CompleteJob(context.Background(), "is1", map[string]string{}); err != nil {
			t.Errorf("CompleteJob: %v", err)
		}
	}}
	if err := s.w.ProcessTask(context.Background(), renderTask(t, model.StemModeMix)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if s.suno.generateCalls != 0 || len(renderRefunds(s.fake)) != 0 {
		t.Errorf("generateCalls=%d refunds=%+v, want 0 and none", s.suno.generateCalls, renderRefunds(s.fake))
	}
}

// renderRefunds are the refunds of the render debit.
func renderRefunds(f *credit.FakeSpender) []credit.RefundCall { return f.Refunds }
