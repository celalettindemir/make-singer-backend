package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

const (
	lyricsJobID  = "6f1c2a10-0000-4000-8000-0000000000b1"
	lyricsOwner  = "kullanici-a"
	lyricsAction = "11111111-1111-4111-8111-111111111111"
)

// step is one scripted provider call: it streams lines, then returns err; a
// blocking step waits for the context instead.
type step struct {
	lines []string
	err   error
	block bool
}

type lyricsFake struct {
	mu      sync.Mutex
	steps   []step
	calls   int
	genReqs []*model.LyricsGenerateRequest
	rewReqs []*model.LyricsRewriteRequest
}

func (f *lyricsFake) next() step {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	f.calls++
	if i >= len(f.steps) {
		return step{err: errors.New("script exhausted")}
	}
	return f.steps[i]
}

func (f *lyricsFake) run(ctx context.Context, onLine func(string) error) (int, error) {
	st := f.next()
	if st.block {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	n := 0
	for _, l := range st.lines {
		if err := onLine(l); err != nil {
			return n, err
		}
		n++
	}
	return n, st.err
}

func (f *lyricsFake) Generate(context.Context, *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	return nil, errors.New("unused")
}
func (f *lyricsFake) Rewrite(context.Context, *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	return nil, errors.New("unused")
}
func (f *lyricsFake) IsMock() bool { return false }
func (f *lyricsFake) GenerateStream(ctx context.Context, r *model.LyricsGenerateRequest, onLine func(string) error) (int, error) {
	f.mu.Lock()
	f.genReqs = append(f.genReqs, r)
	f.mu.Unlock()
	return f.run(ctx, onLine)
}
func (f *lyricsFake) RewriteStream(ctx context.Context, r *model.LyricsRewriteRequest, onLine func(string) error) (int, error) {
	f.mu.Lock()
	f.rewReqs = append(f.rewReqs, r)
	f.mu.Unlock()
	return f.run(ctx, onLine)
}

type lyricsEnv struct {
	w    *LyricsWorker
	repo *jobs.MemoryStore
	gen  *lyricsFake
	sp   *credit.FakeSpender
}

// newLyricsEnv stores a queued job over a three-section song writing the
// given song indexes; result overrides the initial all-pending result.
func newLyricsEnv(t *testing.T, mode string, indexes []int, result *model.LyricsJobResult, charges []model.LyricsCharge, steps ...step) *lyricsEnv {
	t.Helper()
	req := model.LyricsJobRequest{
		ProjectID: "p1", Mode: mode, Genre: model.GenrePop, Vibes: []string{"happy"},
		Song: []model.LyricsSongSection{
			{Type: model.SectionVerse, Lyrics: "old zero"},
			{Type: model.SectionChorus, Lyrics: "old one"},
			{Type: model.SectionVerse, Lyrics: "old two"},
		},
	}
	for _, i := range indexes {
		req.Sections = append(req.Sections, model.LyricsJobSection{Index: i})
	}
	if result == nil {
		result = model.NewLyricsJobResult(req.Sections)
	}
	in := model.LyricsJobInput{LyricsJobRequest: req, ActionID: lyricsAction, Charges: charges}
	payload, _ := json.Marshal(in)
	raw, _ := json.Marshal(result)
	repo := jobs.NewMemoryStore(nil)
	job := &model.Job{ID: lyricsJobID, Type: model.JobTypeLyrics, OwnerID: lyricsOwner, ProjectID: "p1",
		Status: model.JobStatusQueued, Payload: payload, Result: raw, CreatedAt: time.Now()}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatalf("create: %v", err)
	}
	gen := &lyricsFake{steps: steps}
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	return &lyricsEnv{w: NewLyricsWorker(repo, gen, sp), repo: repo, gen: gen, sp: sp}
}

func (e *lyricsEnv) process(t *testing.T) error {
	t.Helper()
	payload, _ := json.Marshal(service.LyricsTaskPayload{JobID: lyricsJobID})
	return e.w.ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeLyrics, payload))
}

func (e *lyricsEnv) job(t *testing.T) (*model.Job, *model.LyricsJobResult) {
	t.Helper()
	j, err := e.repo.Get(context.Background(), lyricsJobID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	res, err := model.ParseLyricsJobResult(j.Result)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	return j, res
}

func (e *lyricsEnv) kinds(t *testing.T) []string {
	t.Helper()
	evs, err := e.repo.EventsAfter(context.Background(), lyricsOwner, lyricsJobID, 0, 1000)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Kind
	}
	return out
}

func count(kinds []string, kind string) int {
	n := 0
	for _, k := range kinds {
		if k == kind {
			n++
		}
	}
	return n
}

func TestLyricsWorker_WritesSectionsInOrderAndCarriesEarlierLines(t *testing.T) {
	e := newLyricsEnv(t, "generate", []int{0, 1}, nil, nil,
		step{lines: []string{"first line", "second line"}}, step{lines: []string{"chorus line"}})
	if err := e.process(t); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(e.gen.genReqs) != 2 {
		t.Fatalf("generate calls = %d", len(e.gen.genReqs))
	}
	if got := e.gen.genReqs[1].Song[0].Lyrics; got != "first line\nsecond line" {
		t.Errorf("section 1 sees song[0].lyrics = %q, want section 0's lines", got)
	}
	if *e.gen.genReqs[1].SectionIndex != 1 || e.gen.genReqs[1].SectionType != model.SectionChorus {
		t.Errorf("section 1 request = %+v", e.gen.genReqs[1])
	}
	job, res := e.job(t)
	if job.Status != model.JobStatusSucceeded || job.Progress != 100 || job.CompletedAt == nil {
		t.Errorf("job = %+v", job)
	}
	if res.Sections[0].Status != "done" || len(res.Sections[0].Lines) != 2 || res.Sections[1].Status != "done" {
		t.Errorf("result = %+v", res)
	}
	kinds := e.kinds(t)
	if count(kinds, jobs.KindLine) != 3 || count(kinds, jobs.KindSectionStarted) != 2 ||
		count(kinds, jobs.KindSectionDone) != 2 || count(kinds, jobs.KindSucceeded) != 1 {
		t.Errorf("events = %v", kinds)
	}
	if len(e.sp.ActionRefunds) != 0 {
		t.Errorf("unexpected refunds %+v", e.sp.ActionRefunds)
	}
}

func TestLyricsWorker_SectionFailsTwiceThenContinues(t *testing.T) {
	charges := []model.LyricsCharge{
		{Section: 0, ActionID: "aaaaaaaa-0000-4000-8000-000000000000", Op: string(credit.OpLyricsRewrite)},
		{Section: 1, ActionID: "aaaaaaaa-0000-4000-8000-000000000001", Op: string(credit.OpLyricsRewrite)},
	}
	e := newLyricsEnv(t, "rewrite", []int{0, 1}, nil, charges,
		step{err: errors.New("boom")}, step{lines: []string{"half"}, err: errors.New("cut")}, // section 0 twice
		step{lines: []string{"ok line"}})
	if err := e.process(t); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	job, res := e.job(t)
	if job.Status != model.JobStatusSucceeded {
		t.Errorf("status = %s, want succeeded (one section done)", job.Status)
	}
	if res.Sections[0].Status != "failed" || len(res.Sections[0].Lines) != 0 || res.Sections[1].Status != "done" {
		t.Errorf("result = %+v", res.Sections)
	}
	kinds := e.kinds(t)
	if count(kinds, jobs.KindSectionFailed) != 1 || count(kinds, jobs.KindSectionStarted) != 3 {
		t.Errorf("events = %v", kinds)
	}
	if len(e.sp.ActionRefunds) != 1 || e.sp.ActionRefunds[0].ID != charges[0].ActionID {
		t.Errorf("refunds = %+v, want the failed section's charge", e.sp.ActionRefunds)
	}
	evs, _ := e.repo.EventsAfter(context.Background(), lyricsOwner, lyricsJobID, 0, 1000)
	last := evs[len(evs)-1]
	if last.Kind != jobs.KindSucceeded || string(last.Data) != `{"failedSections":[0]}` {
		t.Errorf("last event = %s %s", last.Kind, last.Data)
	}
}

func TestLyricsWorker_AllFailRefundsGenerate(t *testing.T) {
	charges := []model.LyricsCharge{{Section: -1, ActionID: lyricsAction, Op: string(credit.OpLyricsGenerate)}}
	e := newLyricsEnv(t, "generate", []int{0, 1}, nil, charges) // empty script: every call errors
	if err := e.process(t); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	job, _ := e.job(t)
	if job.Status != model.JobStatusFailed || job.ErrorCode != "AI_ERROR" {
		t.Errorf("job = %s %q, want failed AI_ERROR", job.Status, job.ErrorCode)
	}
	if len(e.sp.ActionRefunds) != 1 || e.sp.ActionRefunds[0].ID != lyricsAction || e.sp.ActionRefunds[0].Op != credit.OpLyricsGenerate {
		t.Errorf("refunds = %+v, want the generate charge", e.sp.ActionRefunds)
	}
	if e.gen.calls != 4 {
		t.Errorf("provider calls = %d, want 2 per section", e.gen.calls)
	}
}

func TestLyricsWorker_RetryResumesAtUnfinishedSection(t *testing.T) {
	res := &model.LyricsJobResult{Sections: []model.LyricsJobSectionResult{
		{Index: 0, Status: "done", Lines: []string{"kept"}},
		{Index: 1, Status: "writing", Lines: []string{"stale one", "stale two"}},
	}}
	e := newLyricsEnv(t, "generate", []int{0, 1}, res, nil, step{lines: []string{"fresh"}})
	if err := e.process(t); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(e.gen.genReqs) != 1 || *e.gen.genReqs[0].SectionIndex != 1 {
		t.Fatalf("calls = %d, want only section 1", len(e.gen.genReqs))
	}
	if got := e.gen.genReqs[0].Song[0].Lyrics; got != "kept" {
		t.Errorf("song[0].lyrics = %q, want the lines already written", got)
	}
	_, out := e.job(t)
	if out.Sections[1].Status != "done" || len(out.Sections[1].Lines) != 1 || out.Sections[1].Lines[0] != "fresh" {
		t.Errorf("section 1 = %+v", out.Sections[1])
	}
	if kinds := e.kinds(t); count(kinds, jobs.KindSectionStarted) != 1 {
		t.Errorf("events = %v, want one section_started", kinds)
	}
	if len(e.sp.Actions) != 0 || len(e.sp.Spends) != 0 {
		t.Errorf("worker must never spend: %+v", e.sp.Actions)
	}
}

func TestLyricsWorker_CancelStopsProviderQuickly(t *testing.T) {
	e := newLyricsEnv(t, "generate", []int{0}, nil, nil, step{block: true})
	ctx, cancel := context.WithCancel(context.Background())
	payload, _ := json.Marshal(service.LyricsTaskPayload{JobID: lyricsJobID})
	done := make(chan error, 1)
	go func() {
		done <- e.w.ProcessTask(ctx, asynq.NewTask(service.TaskTypeLyrics, payload))
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	_ = e.repo.Update(context.Background(), lyricsJobID, func(j *model.Job) error {
		j.Status = model.JobStatusCanceled
		return nil
	})
	cancel() // what the asynq inspector does to the running task
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err = %v, want nil for a canceled job", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("stopped after %s", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
	if job, _ := e.job(t); job.Status != model.JobStatusCanceled {
		t.Errorf("status = %s, want canceled kept", job.Status)
	}
}

// flakyRepo fails the failAt-th UpdateWith call (1 = mark running, 2 = the
// first section_started), then works again.
type flakyRepo struct {
	jobs.Repo
	failAt, calls int
}

func (f *flakyRepo) UpdateWith(ctx context.Context, id string, fn func(*model.Job, jobs.Emitter) error) error {
	f.calls++
	if f.calls == f.failAt {
		return errors.New("store down")
	}
	return f.Repo.UpdateWith(ctx, id, fn)
}

func TestLyricsWorker_StoreErrorOnLastAttemptEndsTheJob(t *testing.T) {
	charges := []model.LyricsCharge{{Section: -1, ActionID: lyricsAction, Op: string(credit.OpLyricsGenerate)}}
	e := newLyricsEnv(t, "generate", []int{0}, nil, charges, step{lines: []string{"a"}})
	e.w.repo = &flakyRepo{Repo: e.repo, failAt: 2}
	e.w.isLastAttempt = func(context.Context) bool { return true }
	if err := e.process(t); err == nil {
		t.Fatal("want the store error returned")
	}
	job, _ := e.job(t)
	if job.Status != model.JobStatusFailed || job.ErrorCode != "AI_ERROR" {
		t.Errorf("job = %s %q, want failed AI_ERROR (slot freed)", job.Status, job.ErrorCode)
	}
	if len(e.sp.ActionRefunds) != 1 {
		t.Errorf("refunds = %+v, want the generate charge", e.sp.ActionRefunds)
	}
}

func TestLyricsWorker_StoreErrorBeforeLastAttemptLeavesJobForRetry(t *testing.T) {
	e := newLyricsEnv(t, "generate", []int{0}, nil, nil, step{lines: []string{"a"}})
	e.w.repo = &flakyRepo{Repo: e.repo, failAt: 2}
	e.w.isLastAttempt = func(context.Context) bool { return false }
	if err := e.process(t); err == nil {
		t.Fatal("want the store error returned so asynq retries")
	}
	if job, _ := e.job(t); job.Status != model.JobStatusRunning {
		t.Errorf("status = %s, want running until the last attempt", job.Status)
	}
}

func TestLyricsWorker_UnreadableInputEndsTheJobWithoutRetry(t *testing.T) {
	e := newLyricsEnv(t, "generate", []int{0}, nil, nil)
	_ = e.repo.Update(context.Background(), lyricsJobID, func(j *model.Job) error {
		j.Payload = []byte("{broken")
		return nil
	})
	err := e.process(t)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("err = %v, want SkipRetry", err)
	}
	if job, _ := e.job(t); job.Status != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", job.Status)
	}
}
