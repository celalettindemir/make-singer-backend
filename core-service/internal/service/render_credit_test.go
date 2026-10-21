package service

import (
	"context"
	"encoding/json"
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
		fake := &credit.FakeSpender{Bal: 138, Pro: true}
		s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)

		resp, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(mode))
		if err != nil {
			t.Fatalf("mode %q: StartRender: %v", mode, err)
		}
		if len(fake.Spends) != 1 {
			t.Fatalf("mode %q: spends = %d, want 1", mode, len(fake.Spends))
		}
		got := fake.Spends[0]
		if got.Op != want || got.ID != resp.JobID || got.User != "kullanici-a" || got.Project != "p1" {
			t.Errorf("mode %q: spend = %+v, want op %s ref %s project p1", mode, got, want, resp.JobID)
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
	fake := &credit.FakeSpender{Insufficient: true, Required: 62, Bal: 10, Pro: true}
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
	fake := &credit.FakeSpender{Pro: true}
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
	if len(fake.Refunds) == 0 || fake.Refunds[0].ID != "is1" {
		t.Errorf("refunds = %+v, want the render refund for is1", fake.Refunds)
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

func TestRenderService_StemsRequiresPro(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Bal: 500}
	// A nil asynq client would panic if the enqueue were reached.
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)

	_, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(model.StemModeStems))
	if !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("err = %v, want ErrPlanRequired", err)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %+v, want none", fake.Spends)
	}
	if n := jobKeyCount(t, rdb); n != 0 {
		t.Errorf("job records = %d, want 0", n)
	}

	// Other tiers do not need Pro.
	s = NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	if _, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(model.StemModeMix)); err != nil {
		t.Fatalf("mix without Pro: %v", err)
	}

	fake.Pro = true
	fake.Spends = nil
	if _, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(model.StemModeStems)); err != nil {
		t.Fatalf("stems with Pro: %v", err)
	}
	if len(fake.Spends) != 1 || fake.Spends[0].Op != credit.OpRenderStems {
		t.Errorf("spends = %+v, want one render_stems", fake.Spends)
	}
}

func TestRenderService_StemsGateAppliesInShadowMode(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: false}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)

	_, err := s.StartRender(context.Background(), "kullanici-a", creditRenderRequest(model.StemModeStems))
	if !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("err = %v, want ErrPlanRequired in shadow mode", err)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %+v, want none", fake.Spends)
	}
}

func vocalRenderRequest(mode model.StemMode) *model.RenderStartRequest {
	r := creditRenderRequest(mode)
	r.Vocal = &model.VocalRequest{
		Voice:    model.VoiceFemale,
		Sections: []model.GuideSection{{ID: "s1", Type: "verse", Lines: []string{"a b"}}},
	}
	return r
}

// storedPayload reads the render payload back from the queued asynq task.
func storedPayload(t *testing.T, jobID string) model.RenderJobPayload {
	t.Helper()
	insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	t.Cleanup(func() { _ = insp.Close() })
	tasks, err := insp.ListPendingTasks("render")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	for _, task := range tasks {
		var env struct {
			JobID   string                 `json:"jobId"`
			Payload model.RenderJobPayload `json:"payload"`
		}
		if err := json.Unmarshal(task.Payload, &env); err != nil {
			t.Fatalf("task json: %v", err)
		}
		if env.JobID == jobID {
			return env.Payload
		}
	}
	t.Fatalf("no queued task for job %s", jobID)
	return model.RenderJobPayload{}
}

func TestStartRender_Vocal_FreeUser_NoAnalysis(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Bal: 500}
	s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	resp, err := s.StartRender(context.Background(), "kullanici-a", vocalRenderRequest(model.StemModeMix))
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	p := storedPayload(t, resp.JobID)
	if p.Vocal == nil || p.GuideAnalysis {
		t.Errorf("payload vocal=%v analysis=%v, want vocal set and no analysis", p.Vocal, p.GuideAnalysis)
	}
}

func TestStartRender_Vocal_PlusUser_Analysis(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Bal: 500, Plus: true}
	s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	resp, err := s.StartRender(context.Background(), "kullanici-a", vocalRenderRequest(model.StemModeMix))
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	if p := storedPayload(t, resp.JobID); !p.GuideAnalysis {
		t.Error("GuideAnalysis = false, want true for a Plus user")
	}
}

func TestStartRender_PlanReadError_NoAnalysis(t *testing.T) {
	rdb := liveRedis(t)
	// Shadow mode: the spend error is tolerated, and so is the plan read.
	fake := &credit.FakeSpender{Err: errors.New("db down"), Plus: true}
	s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	resp, err := s.StartRender(context.Background(), "kullanici-a", vocalRenderRequest(model.StemModeMix))
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	if p := storedPayload(t, resp.JobID); p.GuideAnalysis {
		t.Error("GuideAnalysis = true, want false when the plan cannot be read")
	}
}

func TestStartRender_Vocal_SingleDebit(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Bal: 500, Plus: true}
	s := NewRenderService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	resp, err := s.StartRender(context.Background(), "kullanici-a", vocalRenderRequest(model.StemModeMix))
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	if len(fake.Spends) != 1 || fake.Spends[0].Op != credit.OpRenderMix || fake.Spends[0].ID != resp.JobID {
		t.Errorf("spends = %+v, want one render_mix for job %s", fake.Spends, resp.JobID)
	}
}

func TestStartRender_InvalidLyrics_NoJobNoDebit(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Bal: 500}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	req := vocalRenderRequest(model.StemModeMix)
	req.Vocal.Sections[0].Lines = []string{"  "}
	_, err := s.StartRender(context.Background(), "kullanici-a", req)
	if !errors.Is(err, model.ErrInvalidLyrics) {
		t.Fatalf("err = %v, want ErrInvalidLyrics", err)
	}
	if len(fake.Spends) != 0 || jobKeyCount(t, rdb) != 0 {
		t.Errorf("spends %+v / jobs %d, want none", fake.Spends, jobKeyCount(t, rdb))
	}
}

func TestStartRender_StemsWithVocalNeedsPro(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{Plus: true, Bal: 500}
	s := NewRenderService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	_, err := s.StartRender(context.Background(), "kullanici-a", vocalRenderRequest(model.StemModeStems))
	if !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("err = %v, want ErrPlanRequired", err)
	}
}
