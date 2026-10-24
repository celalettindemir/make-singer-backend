package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

const (
	lyricsAction = "11111111-1111-4111-8111-111111111111"
	lyricsUser   = "kullanici-a"
)

// jobGen is a LyricsGenerator whose only used method is IsMock.
type jobGen struct{ mock bool }

func (g *jobGen) Generate(context.Context, *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	return nil, errors.New("unused")
}
func (g *jobGen) Rewrite(context.Context, *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	return nil, errors.New("unused")
}
func (g *jobGen) IsMock() bool { return g.mock }
func (g *jobGen) GenerateStream(context.Context, *model.LyricsGenerateRequest, func(string) error) (int, error) {
	return 0, errors.New("unused")
}
func (g *jobGen) RewriteStream(context.Context, *model.LyricsRewriteRequest, func(string) error) (int, error) {
	return 0, errors.New("unused")
}

type lyricsJobEnv struct {
	svc       *LyricsJobService
	repo      *jobs.MemoryStore
	spender   *credit.FakeSpender
	inspector *asynq.Inspector
}

func newLyricsJobEnv(t *testing.T, enforced bool) *lyricsJobEnv {
	t.Helper()
	liveRedis(t)
	opt := asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13}
	client := asynq.NewClient(opt)
	inspector := asynq.NewInspector(opt)
	t.Cleanup(func() { _ = client.Close(); _ = inspector.Close() })
	repo := jobs.NewMemoryStore(nil)
	sp := &credit.FakeSpender{IsEnforced: enforced, ChargedFirst: true, Bal: 40}
	svc := NewLyricsJobService(repo, client, inspector, &jobGen{}, validator.New()).WithCredits(sp)
	return &lyricsJobEnv{svc: svc, repo: repo, spender: sp, inspector: inspector}
}

func lyricsJobRequest(mode string, indexes ...int) *model.LyricsJobRequest {
	song := []model.LyricsSongSection{
		{Type: model.SectionVerse, Lyrics: "old one"},
		{Type: model.SectionChorus, Lyrics: "old two"},
		{Type: model.SectionVerse, Lyrics: "old three"},
	}
	req := &model.LyricsJobRequest{
		ProjectID: "p1", ProjectName: "Gece", Mode: mode, Genre: model.GenrePop,
		Vibes: []string{"romantic"}, Song: song,
	}
	for _, i := range indexes {
		req.Sections = append(req.Sections, model.LyricsJobSection{Index: i})
	}
	return req
}

func jobInput(t *testing.T, repo jobs.Repo, id string) (*model.Job, model.LyricsJobInput) {
	t.Helper()
	job, err := repo.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	var in model.LyricsJobInput
	if err := json.Unmarshal(job.Payload, &in); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	return job, in
}

// setResult overwrites the stored result and status like a worker would.
func setResult(t *testing.T, repo jobs.Repo, id string, status model.JobStatus, secs ...model.LyricsJobSectionResult) {
	t.Helper()
	err := repo.Update(context.Background(), id, func(j *model.Job) error {
		j.Status = status
		raw, _ := json.Marshal(model.LyricsJobResult{Sections: secs})
		j.Result = raw
		return nil
	})
	if err != nil {
		t.Fatalf("set result: %v", err)
	}
}

func sec(idx int, status string, lines ...string) model.LyricsJobSectionResult {
	if lines == nil {
		lines = []string{}
	}
	return model.LyricsJobSectionResult{Index: idx, Status: status, Lines: lines}
}

type nthSpender struct {
	*credit.FakeSpender
	failAt, calls int
}

func (n *nthSpender) SpendAction(ctx context.Context, u string, op credit.Operation, id string) (int, bool, error) {
	n.calls++
	if n.calls == n.failAt {
		return 0, false, &credit.InsufficientError{Required: 1, Balance: 0}
	}
	return n.FakeSpender.SpendAction(ctx, u, op, id)
}

func TestLyricsJob_StartChargesGenerateOnce(t *testing.T) {
	e := newLyricsJobEnv(t, true)
	resp, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("generate", 0, 1))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(e.spender.Actions) != 1 || e.spender.Actions[0].Op != credit.OpLyricsGenerate || e.spender.Actions[0].ID != lyricsAction {
		t.Fatalf("actions = %+v, want one generate charge with the action id", e.spender.Actions)
	}
	if resp.Status != model.JobStatusQueued || resp.Balance != 40 {
		t.Errorf("resp = %+v", resp)
	}
	job, in := jobInput(t, e.repo, resp.JobID)
	if job.Type != model.JobTypeLyrics || job.ProjectID != "p1" || job.Status != model.JobStatusQueued {
		t.Errorf("job = %+v", job)
	}
	if len(in.Charges) != 1 || in.Charges[0].Section != -1 || in.Attempt != 0 {
		t.Errorf("input = %+v", in)
	}
	res, _ := model.ParseLyricsJobResult(job.Result)
	if len(res.Sections) != 2 || res.Sections[0].Status != model.LyricsSectionPending {
		t.Errorf("result = %+v", res)
	}
	if _, err := e.inspector.GetTaskInfo("soz", resp.JobID+":0"); err != nil {
		t.Errorf("task %s:0 not enqueued: %v", resp.JobID, err)
	}
}

func TestLyricsJob_StartChargesRewritePerSectionAndRollsBackOn402(t *testing.T) {
	e := newLyricsJobEnv(t, true)
	resp, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("rewrite", 0, 1, 2))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(e.spender.Actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(e.spender.Actions))
	}
	for i, a := range e.spender.Actions {
		if a.Op != credit.OpLyricsRewrite || a.ID != sectionActionID(lyricsAction, i, 0) {
			t.Errorf("action %d = %+v", i, a)
		}
	}
	_ = resp

	e2 := newLyricsJobEnv(t, true)
	ns := &nthSpender{FakeSpender: e2.spender, failAt: 3}
	e2.svc.WithCredits(ns)
	_, err = e2.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("rewrite", 0, 1, 2))
	var insufficient *credit.InsufficientError
	if !errors.As(err, &insufficient) {
		t.Fatalf("err = %v, want InsufficientError", err)
	}
	if len(e2.spender.ActionRefunds) != 2 {
		t.Fatalf("refunds = %+v, want the 2 charged sections", e2.spender.ActionRefunds)
	}
	if jobCount(t, e2.repo) != 0 {
		t.Error("no job may exist after a 402")
	}
}

func TestLyricsJob_StartRejectsSecondActiveJob409(t *testing.T) {
	e := newLyricsJobEnv(t, true)
	first, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("generate", 0))
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	other := "22222222-2222-4222-8222-222222222222"
	_, err = e.svc.Start(context.Background(), lyricsUser, other, lyricsJobRequest("generate", 1))
	if !errors.Is(err, jobs.ErrActiveLyricsJob) || jobs.ActiveJobID(err) != first.JobID {
		t.Fatalf("err = %v (%q), want ErrActiveLyricsJob for %s", err, jobs.ActiveJobID(err), first.JobID)
	}
	if len(e.spender.ActionRefunds) != 1 || e.spender.ActionRefunds[0].ID != other {
		t.Errorf("refunds = %+v, want the second start's charge refunded", e.spender.ActionRefunds)
	}
}

func TestLyricsJob_ShadowModeChargesNothing(t *testing.T) {
	e := newLyricsJobEnv(t, false)
	resp, err := e.svc.Start(context.Background(), lyricsUser, "", lyricsJobRequest("rewrite", 0, 1))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(e.spender.Actions) != 0 || len(e.spender.Spends) != 0 {
		t.Errorf("shadow mode must not charge: %+v", e.spender.Actions)
	}
	if resp.Balance != 40 {
		t.Errorf("balance = %d, want the read balance 40", resp.Balance)
	}
}

func TestLyricsJob_StartValidatesBeforeCharging(t *testing.T) {
	e := newLyricsJobEnv(t, true)
	bad := []*model.LyricsJobRequest{
		lyricsJobRequest("generate", 3),    // out of range
		lyricsJobRequest("generate", 1, 1), // duplicate
		lyricsJobRequest("generate"),       // no sections
	}
	empty := lyricsJobRequest("rewrite", 0)
	empty.Song[0].Lyrics = ""
	bad = append(bad, empty)
	for i, req := range bad {
		_, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, req)
		var inv *LyricsJobInvalidError
		if !errors.As(err, &inv) {
			t.Errorf("case %d: err = %v, want LyricsJobInvalidError", i, err)
		}
	}
	if len(e.spender.Actions) != 0 {
		t.Errorf("charged before validation: %+v", e.spender.Actions)
	}
	if _, err := e.svc.Start(context.Background(), lyricsUser, "", lyricsJobRequest("generate", 0)); !errors.Is(err, ErrActionIDRequired) {
		t.Errorf("missing action id: err = %v", err)
	}
}

func TestLyricsJob_CancelRefundRules(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		idx    []int
		result []model.LyricsJobSectionResult
		want   []string // refunded action ids
	}{
		{"generate no lines refunds all", "generate", []int{0, 1},
			[]model.LyricsJobSectionResult{sec(0, "writing"), sec(1, "pending")}, []string{lyricsAction}},
		{"generate with lines refunds nothing", "generate", []int{0, 1},
			[]model.LyricsJobSectionResult{sec(0, "writing", "a line"), sec(1, "pending")}, nil},
		{"rewrite 1 done refunds the unstarted", "rewrite", []int{0, 1, 2},
			[]model.LyricsJobSectionResult{sec(0, "done", "x"), sec(1, "pending"), sec(2, "pending")},
			[]string{sectionActionID(lyricsAction, 1, 0), sectionActionID(lyricsAction, 2, 0)}},
		{"rewrite failed and unstarted sections refunded, done kept", "rewrite", []int{0, 1, 2},
			[]model.LyricsJobSectionResult{sec(0, "done", "x"), sec(1, "failed"), sec(2, "pending")},
			[]string{sectionActionID(lyricsAction, 1, 0), sectionActionID(lyricsAction, 2, 0)}},
		{"rewrite no lines refunds all", "rewrite", []int{0, 1},
			[]model.LyricsJobSectionResult{sec(0, "writing"), sec(1, "pending")},
			[]string{sectionActionID(lyricsAction, 0, 0), sectionActionID(lyricsAction, 1, 0)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLyricsJobEnv(t, true)
			resp, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest(tc.mode, tc.idx...))
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			setResult(t, e.repo, resp.JobID, model.JobStatusRunning, tc.result...)
			if err := e.svc.Cancel(context.Background(), lyricsUser, resp.JobID); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			job, _ := e.repo.Get(context.Background(), resp.JobID)
			if job.Status != model.JobStatusCanceled {
				t.Errorf("status = %s", job.Status)
			}
			got := map[string]bool{}
			for _, r := range e.spender.ActionRefunds {
				got[r.ID] = true
			}
			if len(got) != len(tc.want) {
				t.Fatalf("refunds = %+v, want %v", e.spender.ActionRefunds, tc.want)
			}
			for _, id := range tc.want {
				if !got[id] {
					t.Errorf("missing refund %s", id)
				}
			}
			if err := e.svc.Cancel(context.Background(), lyricsUser, resp.JobID); !errors.Is(err, ErrJobAlreadyCompleted) {
				t.Errorf("second cancel err = %v, want ErrJobAlreadyCompleted", err)
			}
			if err := e.svc.Cancel(context.Background(), "baska", resp.JobID); !errors.Is(err, ErrJobNotFound) {
				t.Errorf("foreign cancel err = %v, want ErrJobNotFound", err)
			}
		})
	}
}

func TestLyricsJob_RetryOnlyFailedSectionsWithin24h(t *testing.T) {
	e := newLyricsJobEnv(t, true)
	resp, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("rewrite", 0, 1, 2))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id := resp.JobID

	// Not succeeded yet.
	if err := e.svc.Retry(context.Background(), lyricsUser, id); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("running job: err = %v, want ErrNotRetryable", err)
	}
	setResult(t, e.repo, id, model.JobStatusSucceeded,
		sec(0, "done", "a"), sec(1, "failed"), sec(2, "failed"))
	_ = e.repo.Update(context.Background(), id, func(j *model.Job) error {
		now := time.Now().Add(-time.Hour)
		j.CompletedAt = &now
		return nil
	})
	before := len(e.spender.Actions)
	if err := e.svc.Retry(context.Background(), lyricsUser, id); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	charged := e.spender.Actions[before:]
	if len(charged) != 2 || charged[0].ID != sectionActionID(lyricsAction, 1, 1) || charged[1].ID != sectionActionID(lyricsAction, 2, 1) {
		t.Fatalf("retry charges = %+v, want sections 1 and 2 with attempt 1", charged)
	}
	job, in := jobInput(t, e.repo, id)
	res, _ := model.ParseLyricsJobResult(job.Result)
	if job.Status != model.JobStatusQueued || in.Attempt != 1 || job.CompletedAt != nil {
		t.Errorf("job = %+v attempt %d", job, in.Attempt)
	}
	if res.Sections[0].Status != "done" || res.Sections[1].Status != "pending" || res.Sections[2].Status != "pending" {
		t.Errorf("sections = %+v", res.Sections)
	}
	if _, err := e.inspector.GetTaskInfo("soz", id+":1"); err != nil {
		t.Errorf("task %s:1 not enqueued: %v", id, err)
	}

	// Older than 24 h, or nothing failed: not retryable.
	e2 := newLyricsJobEnv(t, true)
	r2, _ := e2.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("generate", 0, 1))
	setResult(t, e2.repo, r2.JobID, model.JobStatusSucceeded, sec(0, "done", "a"), sec(1, "failed"))
	_ = e2.repo.Update(context.Background(), r2.JobID, func(j *model.Job) error {
		old := time.Now().Add(-25 * time.Hour)
		j.CompletedAt = &old
		return nil
	})
	if err := e2.svc.Retry(context.Background(), lyricsUser, r2.JobID); !errors.Is(err, ErrNotRetryable) {
		t.Errorf("old job: err = %v", err)
	}
	setResult(t, e2.repo, r2.JobID, model.JobStatusSucceeded, sec(0, "done", "a"), sec(1, "done", "b"))
	_ = e2.repo.Update(context.Background(), r2.JobID, func(j *model.Job) error {
		now := time.Now()
		j.CompletedAt = &now
		return nil
	})
	if err := e2.svc.Retry(context.Background(), lyricsUser, r2.JobID); !errors.Is(err, ErrNotRetryable) {
		t.Errorf("no failed section: err = %v", err)
	}
	// Generate retries are free.
	setResult(t, e2.repo, r2.JobID, model.JobStatusSucceeded, sec(0, "done", "a"), sec(1, "failed"))
	n := len(e2.spender.Actions)
	if err := e2.svc.Retry(context.Background(), lyricsUser, r2.JobID); err != nil {
		t.Fatalf("generate retry: %v", err)
	}
	if len(e2.spender.Actions) != n {
		t.Errorf("generate retry charged: %+v", e2.spender.Actions[n:])
	}
}

func TestLyricsJob_FailStaleEndsIdleJobsWithRefundRules(t *testing.T) {
	e := newLyricsJobEnv(t, true)
	resp, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("rewrite", 0, 1, 2))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	setResult(t, e.repo, resp.JobID, model.JobStatusRunning,
		sec(0, "done", "x"), sec(1, "writing", "half"), sec(2, "pending"))

	// Fresh activity: nothing is stale.
	if n, err := e.svc.FailStale(context.Background(), time.Now()); err != nil || n != 0 {
		t.Fatalf("fresh: n=%d err=%v", n, err)
	}
	n, err := e.svc.FailStale(context.Background(), time.Now().Add(20*time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("stale: n=%d err=%v", n, err)
	}
	job, _ := e.repo.Get(context.Background(), resp.JobID)
	res, _ := model.ParseLyricsJobResult(job.Result)
	if job.Status != model.JobStatusSucceeded || res.Sections[1].Status != "failed" || res.Sections[2].Status != "failed" {
		t.Errorf("job = %s %+v, want succeeded with 1 and 2 failed", job.Status, res.Sections)
	}
	if len(e.spender.ActionRefunds) != 2 {
		t.Errorf("refunds = %+v, want the two failed sections", e.spender.ActionRefunds)
	}

	// No section done: the job fails with AI_ERROR and generate is refunded.
	e2 := newLyricsJobEnv(t, true)
	r2, _ := e2.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("generate", 0))
	e2.svc.inspector = &fakeInspector{err: asynq.ErrTaskNotFound}
	if n, _ := e2.svc.FailStale(context.Background(), time.Now().Add(20*time.Minute)); n != 1 {
		t.Fatalf("n = %d", n)
	}
	j2, _ := e2.repo.Get(context.Background(), r2.JobID)
	if j2.Status != model.JobStatusFailed || j2.ErrorCode != "AI_ERROR" {
		t.Errorf("job = %s %q", j2.Status, j2.ErrorCode)
	}
	if len(e2.spender.ActionRefunds) != 1 || e2.spender.ActionRefunds[0].ID != lyricsAction {
		t.Errorf("refunds = %+v", e2.spender.ActionRefunds)
	}
}

// uniqueRepo makes the next UpdateWith fail like the partial unique index.
type uniqueRepo struct {
	jobs.Repo
	fail bool
}

func (u *uniqueRepo) UpdateWith(ctx context.Context, id string, fn func(*model.Job, jobs.Emitter) error) error {
	if u.fail {
		return &pgconn.PgError{Code: "23505", ConstraintName: "isler_sozler_aktif"}
	}
	return u.Repo.UpdateWith(ctx, id, fn)
}

func TestLyricsJob_RetryUniqueViolationNamesTheActiveJob(t *testing.T) {
	e := newLyricsJobEnv(t, false)
	first, err := e.svc.Start(context.Background(), lyricsUser, "", lyricsJobRequest("generate", 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	setResult(t, e.repo, first.JobID, model.JobStatusSucceeded, sec(0, "done", "a"), sec(1, "failed"))
	_ = e.repo.Update(context.Background(), first.JobID, func(j *model.Job) error {
		now := time.Now()
		j.CompletedAt = &now
		return nil
	})
	second, err := e.svc.Start(context.Background(), lyricsUser, "", lyricsJobRequest("generate", 0))
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	e.svc.repo = &uniqueRepo{Repo: e.repo, fail: true}
	err = e.svc.Retry(context.Background(), lyricsUser, first.JobID)
	if !errors.Is(err, jobs.ErrActiveLyricsJob) || jobs.ActiveJobID(err) != second.JobID {
		t.Fatalf("err = %v (%q), want ErrActiveLyricsJob naming %s", err, jobs.ActiveJobID(err), second.JobID)
	}
}

type fakeInspector struct {
	state asynq.TaskState
	err   error
	asked []string
}

func (f *fakeInspector) CancelProcessing(string) error { return nil }
func (f *fakeInspector) GetTaskInfo(queue, id string) (*asynq.TaskInfo, error) {
	f.asked = append(f.asked, queue+"/"+id)
	if f.err != nil {
		return nil, f.err
	}
	return &asynq.TaskInfo{State: f.state}, nil
}

func TestLyricsJob_FailStaleQueuedJobAsksTheInspector(t *testing.T) {
	cases := []struct {
		name     string
		insp     *fakeInspector
		wantFail bool
	}{
		{"pending waits", &fakeInspector{state: asynq.TaskStatePending}, false},
		{"scheduled waits", &fakeInspector{state: asynq.TaskStateScheduled}, false},
		{"retry waits", &fakeInspector{state: asynq.TaskStateRetry}, false},
		{"active waits", &fakeInspector{state: asynq.TaskStateActive}, false},
		{"archived fails", &fakeInspector{state: asynq.TaskStateArchived}, true},
		{"missing fails", &fakeInspector{err: asynq.ErrTaskNotFound}, true},
		{"other error waits", &fakeInspector{err: errors.New("redis down")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLyricsJobEnv(t, true)
			resp, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("generate", 0))
			if err != nil {
				t.Fatal(err)
			}
			e.svc.inspector = tc.insp
			n, err := e.svc.FailStale(context.Background(), time.Now().Add(20*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.insp.asked) != 1 || tc.insp.asked[0] != "soz/"+resp.JobID+":0" {
				t.Errorf("asked = %v, want the job's current task id", tc.insp.asked)
			}
			job, _ := e.repo.Get(context.Background(), resp.JobID)
			failed := job.Status == model.JobStatusFailed
			if failed != tc.wantFail || (n == 1) != tc.wantFail {
				t.Errorf("status = %s n = %d, want failed=%v", job.Status, n, tc.wantFail)
			}
			if !tc.wantFail && len(e.spender.ActionRefunds) != 0 {
				t.Errorf("refunded a waiting job: %+v", e.spender.ActionRefunds)
			}
		})
	}
}

// racingRepo commits a worker change right before the finish update runs, as
// a live worker could between a stale read and the finish.
type racingRepo struct {
	jobs.Repo
	before func()
}

func (r *racingRepo) UpdateWith(ctx context.Context, id string, fn func(*model.Job, jobs.Emitter) error) error {
	if r.before != nil {
		b := r.before
		r.before = nil
		b()
	}
	return r.Repo.UpdateWith(ctx, id, fn)
}

func TestLyricsJob_FinishKeepsAWorkerCommitAndDoesNotRefundThatSection(t *testing.T) {
	e := newLyricsJobEnv(t, true)
	resp, err := e.svc.Start(context.Background(), lyricsUser, lyricsAction, lyricsJobRequest("rewrite", 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	setResult(t, e.repo, resp.JobID, model.JobStatusRunning, sec(0, "done", "x"), sec(1, "writing", "half"))
	racing := &racingRepo{Repo: e.repo}
	racing.before = func() { // the worker finishes section 1 meanwhile
		setResult(t, e.repo, resp.JobID, model.JobStatusRunning, sec(0, "done", "x"), sec(1, "done", "half", "rest"))
	}
	if err := FinishLyricsJob(context.Background(), racing, e.spender, resp.JobID, true); err != nil {
		t.Fatal(err)
	}
	job, _ := e.repo.Get(context.Background(), resp.JobID)
	res, _ := model.ParseLyricsJobResult(job.Result)
	if res.Sections[1].Status != "done" || len(res.Sections[1].Lines) != 2 {
		t.Errorf("section 1 = %+v, want the worker's commit kept", res.Sections[1])
	}
	if job.Status != model.JobStatusSucceeded || len(res.FailedSections()) != 0 {
		t.Errorf("job = %s failed %v", job.Status, res.FailedSections())
	}
	if len(e.spender.ActionRefunds) != 0 {
		t.Errorf("refunds = %+v, want none (both sections done)", e.spender.ActionRefunds)
	}
}
