package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

func creditRenderRequest(mode model.StemMode) *model.RenderStartRequest {
	return &model.RenderStartRequest{
		ProjectID:   "p1",
		StemMode:    mode,
		Arrangement: model.Arrangement{Instruments: []model.Instrument{model.InstrumentDrums}},
	}
}

func creditAsynqClient(t *testing.T) *asynq.Client {
	t.Helper()
	c := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func jobKeyCount(t *testing.T, rdb *redis.Client) int {
	t.Helper()
	keys, err := rdb.Keys(context.Background(), "job:*").Result()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	return len(keys)
}

func TestStartRender_DebitsByStemModeWithJobRef(t *testing.T) {
	cases := map[model.StemMode]credit.Operation{
		"":                  credit.OpRenderMix,
		model.StemModeMix:   credit.OpRenderMix,
		model.StemModeStems: credit.OpRenderStems,
	}
	for mode, want := range cases {
		rdb := liveRedis(t)
		fake := &credit.FakeSpender{Bal: 138}
		s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)

		resp, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(mode))
		if err != nil {
			t.Fatalf("mode %q: StartRender: %v", mode, err)
		}
		if len(fake.Spends) != 1 {
			t.Fatalf("mode %q: spends = %d, want 1", mode, len(fake.Spends))
		}
		got := fake.Spends[0]
		if got.Op != want || got.ID != resp.JobID || got.User != "kullanici-a" {
			t.Errorf("mode %q: spend = %+v, want op %s ref %s", mode, got, want, resp.JobID)
		}
		if resp.Balance != 138 {
			t.Errorf("mode %q: balance = %d, want 138", mode, resp.Balance)
		}
		if len(fake.Refunds) != 0 {
			t.Errorf("mode %q: unexpected refund %+v", mode, fake.Refunds)
		}
	}
}

func TestStartRender_FocusDebitsFocus(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{}
	s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	req := creditRenderRequest(model.StemModeFocus)
	focus := model.InstrumentDrums
	req.FocusInstrument = &focus
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	if len(fake.Spends) != 1 || fake.Spends[0].Op != credit.OpRenderFocus {
		t.Errorf("spends = %+v, want one render_focus", fake.Spends)
	}
}

func TestStartRender_InsufficientNoJob(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{Insufficient: true, Required: 62, Bal: 10}
	// A nil asynq client would panic if the enqueue were reached.
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)

	_, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(model.StemModeStems))
	var insufficient *credit.InsufficientError
	if !errors.As(err, &insufficient) || insufficient.Required != 62 || insufficient.Balance != 10 {
		t.Fatalf("err = %v, want InsufficientError{62,10}", err)
	}
	if n := jobKeyCount(t, rdb); n != 0 {
		t.Errorf("job records = %d, want 0", n)
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("refunds = %+v, want none", fake.Refunds)
	}
}

func TestStartRender_EnqueueFailureRefunds(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{}
	closed := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	_ = closed.Close()
	s := NewRenderService(rdb, closed, nil, testR2Cfg()).WithCredits(fake)

	if _, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(model.StemModeStems)); err == nil {
		t.Fatal("want an enqueue error")
	}
	if len(fake.Spends) != 1 || len(fake.Refunds) != 1 {
		t.Fatalf("spends=%d refunds=%d, want 1 and 1", len(fake.Spends), len(fake.Refunds))
	}
	if r := fake.Refunds[0]; r.Op != credit.OpRenderStems || r.ID != fake.Spends[0].ID {
		t.Errorf("refund = %+v, want render_stems with ref %s", r, fake.Spends[0].ID)
	}
}

func TestStartRender_NilSpenderUnchanged(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg())
	resp, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(""))
	if err != nil || resp.Balance != 0 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func storeRenderJob(t *testing.T, rdb *redis.Client, status model.JobStatus) {
	t.Helper()
	storeJob(t, rdb, &model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: status, CreatedAt: time.Now()})
}

func TestCancelRender_QueuedRefunds(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	storeRenderJob(t, rdb, model.JobStatusQueued)

	if _, err := s.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Fatalf("CancelRender: %v", err)
	}
	if len(fake.Refunds) != 1 || fake.Refunds[0].ID != "is1" {
		t.Errorf("refunds = %+v, want exactly one for is1", fake.Refunds)
	}
}

func TestCancelRender_RunningNoRefund(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	storeRenderJob(t, rdb, model.JobStatusRunning)

	if _, err := s.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Fatalf("CancelRender: %v", err)
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("refunds = %+v, want none for a running job", fake.Refunds)
	}
	if st, _ := s.JobStatus(context.Background(), "is1"); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled", st)
	}
}

func TestCancelRender_RefundFailureStillCancels(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{Err: errors.New("db down")}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	storeRenderJob(t, rdb, model.JobStatusQueued)

	if _, err := s.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Fatalf("a refund failure must not fail the cancel: %v", err)
	}
}

func TestRenderOperation(t *testing.T) {
	cases := map[model.StemMode]credit.Operation{
		model.StemModeMix: credit.OpRenderMix, model.StemModeFocus: credit.OpRenderFocus,
		model.StemModeStems: credit.OpRenderStems, "": credit.OpRenderMix, "bogus": credit.OpRenderMix,
	}
	for mode, want := range cases {
		if got := RenderOperation(mode); got != want {
			t.Errorf("RenderOperation(%q) = %s, want %s", mode, got, want)
		}
	}
}

func TestUpdateJobProgress_CanceledStaysCanceled(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	storeRenderJob(t, rdb, model.JobStatusCanceled)

	if err := s.UpdateJobProgress(context.Background(), "is1", 40, "x"); err != nil {
		t.Fatalf("UpdateJobProgress: %v", err)
	}
	job, _ := readJob(context.Background(), rdb, "is1")
	if job.Status != model.JobStatusCanceled || job.Progress != 0 {
		t.Errorf("job = %s/%d, want canceled/0", job.Status, job.Progress)
	}
}

func TestClaimRun(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())

	storeRenderJob(t, rdb, model.JobStatusQueued)
	ok, _, err := s.ClaimRun(context.Background(), "is1")
	if err != nil || !ok {
		t.Fatalf("claim queued = %v, %v; want true", ok, err)
	}
	if st, _ := s.JobStatus(context.Background(), "is1"); st != model.JobStatusRunning {
		t.Errorf("status = %s, want running", st)
	}

	storeRenderJob(t, rdb, model.JobStatusCanceled)
	ok, st, err := s.ClaimRun(context.Background(), "is1")
	if err != nil || ok || st != model.JobStatusCanceled {
		t.Fatalf("claim canceled = %v, %v; want false", ok, err)
	}
	if st, _ := s.JobStatus(context.Background(), "is1"); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled", st)
	}
}

func TestCancelThenClaim_NoRunAfterQueuedCancelRefund(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	storeRenderJob(t, rdb, model.JobStatusQueued)
	if _, err := s.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.ClaimRun(context.Background(), "is1"); ok {
		t.Error("a canceled job was claimed")
	}
	// Running then cancel: no refund.
	fake.Refunds = nil
	storeRenderJob(t, rdb, model.JobStatusQueued)
	if ok, _, _ := s.ClaimRun(context.Background(), "is1"); !ok {
		t.Fatal("claim failed")
	}
	if _, err := s.CancelRender(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("refunds after a post-claim cancel = %+v, want none", fake.Refunds)
	}
}

func TestUpdateJob_ConcurrentUpdatesLoseNothing(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	storeRenderJob(t, rdb, model.JobStatusRunning)

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.updateJob(context.Background(), "is1", func(j *model.Job) error {
				j.RetryCount++
				return nil
			})
			if err != nil {
				t.Errorf("updateJob: %v", err)
			}
		}()
	}
	wg.Wait()
	job, _ := readJob(context.Background(), rdb, "is1")
	if job.RetryCount != n {
		t.Errorf("RetryCount = %d, want %d (an update was lost)", job.RetryCount, n)
	}
}

func TestClaimRun_ConflictWithCancelReturnsFalse(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	storeRenderJob(t, rdb, model.JobStatusQueued)

	// The first transaction attempt has read "queued"; a cancel commits
	// before its EXEC, so EXEC fails and the retry reads "canceled".
	fired := false
	s.beforeExec = func() {
		if fired {
			return
		}
		fired = true
		job, err := readJob(context.Background(), rdb, "is1")
		if err != nil {
			t.Errorf("readJob: %v", err)
			return
		}
		job.Status = model.JobStatusCanceled
		if err := writeJob(context.Background(), rdb, job); err != nil {
			t.Errorf("writeJob: %v", err)
		}
	}

	ok, _, err := s.ClaimRun(context.Background(), "is1")
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if !fired {
		t.Fatal("the conflict hook did not run")
	}
	if ok {
		t.Error("ClaimRun returned true for a job canceled during the claim")
	}
	if st, _ := s.JobStatus(context.Background(), "is1"); st != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled", st)
	}
}

func TestClaimRun_FinishedJobsNotClaimed(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	for _, st := range []model.JobStatus{model.JobStatusFailed, model.JobStatusSucceeded} {
		storeRenderJob(t, rdb, st)
		if ok, seen, err := s.ClaimRun(context.Background(), "is1"); err != nil || ok || seen != st {
			t.Errorf("claim %s = %v, %v, %v; want false with the status", st, ok, seen, err)
		}
	}
}

func TestStartRender_ShadowReadFailureStillStarts(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: false, Err: errors.New("db down")}
	s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	resp, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(""))
	if err != nil || resp.Balance != 0 {
		t.Fatalf("resp=%+v err=%v, want a started job with balance 0", resp, err)
	}
}

func TestStartRender_EnforcedSpendErrorFails(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Err: errors.New("db down")}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	if _, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest("")); err == nil {
		t.Fatal("want the spend error in enforced mode")
	}
	if n := jobKeyCount(t, rdb); n != 0 {
		t.Errorf("job records = %d, want 0", n)
	}
}
