package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

type importStorage struct {
	fakeStorage
	mu      sync.Mutex
	events  []string
	deleted []string
	gone    bool
}

func (s *importStorage) Exists(_ context.Context, _ string) (bool, error) { return !s.gone, nil }

func (s *importStorage) Upload(ctx context.Context, key string, body io.Reader, ct string) (string, time.Time, error) {
	_, _ = io.Copy(io.Discard, body)
	s.mu.Lock()
	s.events = append(s.events, "upload:"+key)
	s.mu.Unlock()
	return "", time.Time{}, nil
}

func (s *importStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, key)
	return nil
}

type importAudio struct {
	client.ImportAudio
	ms     int
	err    error
	inputs []string
}

func (a *importAudio) PrepareImport(_ context.Context, req *client.ImportPrepareRequest) (*client.ImportPrepareResponse, error) {
	a.inputs = append(a.inputs, req.InputKey)
	if a.err != nil {
		return nil, a.err
	}
	return &client.ImportPrepareResponse{DurationMs: a.ms}, nil
}
func (a *importAudio) IsConfigured() bool { return true }

type importFakeQueue struct {
	tasks []*asynq.Task
	opts  [][]asynq.Option
	err   error
	// failType, when set, fails only the tasks of that type.
	failType string
}

func (q *importFakeQueue) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.err != nil || (q.failType != "" && t.Type() == q.failType) {
		if q.err == nil {
			return nil, errors.New("redis down")
		}
		return nil, q.err
	}
	q.tasks = append(q.tasks, t)
	q.opts = append(q.opts, opts)
	return &asynq.TaskInfo{}, nil
}

type importEnv struct {
	svc     *ImportService
	st      *importStorage
	audio   *importAudio
	queue   *importFakeQueue
	repo    *jobs.MemoryStore
	spender *credit.FakeSpender
}

func newImportEnv(ms int) *importEnv {
	e := &importEnv{
		st: &importStorage{}, audio: &importAudio{ms: ms}, queue: &importFakeQueue{},
		repo: jobs.NewMemoryStore(nil), spender: &credit.FakeSpender{IsEnforced: true, Bal: 100},
	}
	e.svc = NewImportService(ImportDeps{
		Cfg:  config.ImportConfig{Enabled: true, SeparationURL: "http://separation:8085", MinMs: 30000, MaxMs: 480000, StaleAfter: 2 * time.Hour},
		Repo: e.repo, Storage: e.st, Audio: e.audio, Queue: e.queue, Credits: e.spender,
	})
	return e
}

func importUpload() ImportUpload {
	return ImportUpload{ProjectID: "p1", ProjectName: "Gece", ContentType: "audio/mpeg", Data: []byte("x")}
}

func TestImportService_Start_HappyPath(t *testing.T) {
	e := newImportEnv(95000)
	resp, err := e.svc.Start(context.Background(), "u1", importUpload())
	if err != nil {
		t.Fatal(err)
	}
	if resp.DurationMs != 95000 || resp.JobID == "" {
		t.Fatalf("resp = %+v", resp)
	}
	wantKey := "imports/u1/" + resp.JobID + "/source.mp3"
	if len(e.st.events) != 1 || e.st.events[0] != "upload:"+wantKey || e.audio.inputs[0] != wantKey {
		t.Fatalf("storage %v audio %v", e.st.events, e.audio.inputs)
	}
	if len(e.spender.Spends) != 1 || e.spender.Spends[0].Op != credit.OpImport || e.spender.Spends[0].ID != resp.JobID || e.spender.Spends[0].Project != "p1" {
		t.Fatalf("spends = %+v", e.spender.Spends)
	}
	job, err := e.repo.Get(context.Background(), resp.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Type != model.JobTypeImport || job.OwnerID != "u1" || job.Status != model.JobStatusQueued || job.ProjectName != "Gece" || job.ProjectID != "p1" {
		t.Fatalf("job = %+v", job)
	}
	if !strings.Contains(string(job.Payload), wantKey) || !strings.Contains(string(job.Payload), `"durationMs":95000`) {
		t.Fatalf("payload = %s", job.Payload)
	}
	if len(e.queue.tasks) != 1 || e.queue.tasks[0].Type() != TaskTypeImport {
		t.Fatalf("tasks = %v", e.queue.tasks)
	}
	if !strings.Contains(string(e.queue.tasks[0].Payload()), resp.JobID) {
		t.Fatalf("task payload = %s", e.queue.tasks[0].Payload())
	}
	if len(e.st.deleted) != 0 {
		t.Fatalf("source deleted on success: %v", e.st.deleted)
	}
}

func TestImportService_DurationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		ms int
		ok bool
	}{{29499, false}, {29500, true}, {480500, true}, {480501, false}} {
		e := newImportEnv(tc.ms)
		_, err := e.svc.Start(context.Background(), "u1", importUpload())
		var de *ImportDurationError
		if tc.ok {
			if err != nil {
				t.Fatalf("%d: %v", tc.ms, err)
			}
			continue
		}
		if !errors.As(err, &de) || de.DurationMs != tc.ms {
			t.Fatalf("%d: err = %v", tc.ms, err)
		}
		if len(e.spender.Spends) != 0 || len(e.st.deleted) != 1 || len(e.queue.tasks) != 0 {
			t.Fatalf("%d: spends %v deleted %v tasks %d", tc.ms, e.spender.Spends, e.st.deleted, len(e.queue.tasks))
		}
	}
}

func TestImportService_Unreadable_DeletesSourceNoDebit(t *testing.T) {
	e := newImportEnv(0)
	e.audio.err = client.ErrAudioUnprocessable
	_, err := e.svc.Start(context.Background(), "u1", importUpload())
	if !errors.Is(err, ErrImportUnreadable) {
		t.Fatalf("err = %v", err)
	}
	if len(e.spender.Spends) != 0 || len(e.st.deleted) != 1 {
		t.Fatalf("spends %v deleted %v", e.spender.Spends, e.st.deleted)
	}
}

func TestImportService_ActiveImport_Conflict(t *testing.T) {
	e := newImportEnv(95000)
	err := e.repo.Create(context.Background(), &model.Job{
		ID: "11111111-1111-4111-8111-aaaaaaaaaaaa", Type: model.JobTypeImport, OwnerID: "u1", Status: model.JobStatusRunning, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Start(context.Background(), "u1", importUpload())
	if !errors.Is(err, jobs.ErrActiveLyricsJob) || jobs.ActiveJobID(err) != "11111111-1111-4111-8111-aaaaaaaaaaaa" {
		t.Fatalf("err = %v", err)
	}
	if len(e.st.events) != 0 || len(e.spender.Spends) != 0 {
		t.Fatalf("uploaded %v spent %v", e.st.events, e.spender.Spends)
	}
}

func TestImportService_EnqueueFails_RefundsAndCancels(t *testing.T) {
	e := newImportEnv(95000)
	e.queue.err = errors.New("redis down")
	_, err := e.svc.Start(context.Background(), "u1", importUpload())
	if err == nil {
		t.Fatal("expected error")
	}
	if len(e.spender.Refunds) != 1 || e.spender.Refunds[0].Op != credit.OpImport || e.spender.Refunds[0].ID != e.spender.Spends[0].ID {
		t.Fatalf("refunds = %+v", e.spender.Refunds)
	}
	list, _ := e.repo.List(context.Background(), "u1", time.Now().Add(-time.Hour), 10)
	if len(list) != 1 || list[0].Status != model.JobStatusCanceled {
		t.Fatalf("jobs = %+v", list)
	}
	if len(e.st.deleted) != 1 {
		t.Fatalf("source not deleted: %v", e.st.deleted)
	}
}

func TestImportService_InsufficientCredits_DeletesSource(t *testing.T) {
	e := newImportEnv(95000)
	e.spender.Insufficient = true
	_, err := e.svc.Start(context.Background(), "u1", importUpload())
	var ie *credit.InsufficientError
	if !errors.As(err, &ie) || len(e.st.deleted) != 1 || len(e.queue.tasks) != 0 {
		t.Fatalf("err %v deleted %v", err, e.st.deleted)
	}
}

func TestImportService_Disabled(t *testing.T) {
	e := newImportEnv(95000)
	e.svc = NewImportService(ImportDeps{Cfg: config.ImportConfig{Enabled: false}, Repo: e.repo, Storage: e.st, Audio: e.audio, Queue: e.queue, Credits: e.spender})
	_, err := e.svc.Start(context.Background(), "u1", importUpload())
	if !errors.Is(err, ErrImportDisabled) || len(e.st.events) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestImportService_Disabled_WhenSeparatorUnconfigured(t *testing.T) {
	e := newImportEnv(95000)
	e.svc.d.Cfg.SeparationURL = ""
	if e.svc.Available() {
		t.Fatal("Available with no separator")
	}
	if _, err := e.svc.Start(context.Background(), "u1", importUpload()); !errors.Is(err, ErrImportDisabled) {
		t.Fatalf("Start err = %v", err)
	}
	if _, err := e.svc.Retry(context.Background(), "u1", oldImportID); !errors.Is(err, ErrImportDisabled) {
		t.Fatalf("Retry err = %v", err)
	}
	if len(e.st.events) != 0 || len(e.spender.Spends) != 0 {
		t.Fatalf("touched storage %v or credits %v", e.st.events, e.spender.Spends)
	}
}

const (
	oldImportID    = "11111111-1111-4111-8111-111111111111"
	liveImportID   = "22222222-2222-4222-8222-222222222222"
	cancelImportID = "33333333-3333-4333-8333-333333333333"
)

const importSrcKey = "imports/u1/old/source.mp3"

func seedImportJob(t *testing.T, e *importEnv, id string, status model.JobStatus) {
	t.Helper()
	payload := `{"sourceKey":"` + importSrcKey + `","durationMs":95000,"contentType":"audio/mpeg"}`
	err := e.repo.Create(context.Background(), &model.Job{
		ID: id, Type: model.JobTypeImport, OwnerID: "u1", Status: status,
		ProjectID: "p1", ProjectName: "Gece", Payload: []byte(payload), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportService_Retry_NewJobSameSource(t *testing.T) {
	e := newImportEnv(95000)
	seedImportJob(t, e, oldImportID, model.JobStatusFailed)
	resp, err := e.svc.Retry(context.Background(), "u1", oldImportID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.JobID == "" || resp.JobID == oldImportID || resp.DurationMs != 95000 {
		t.Fatalf("resp = %+v", resp)
	}
	job, err := e.repo.Get(context.Background(), resp.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != model.JobStatusQueued || job.ProjectID != "p1" || job.ProjectName != "Gece" ||
		!strings.Contains(string(job.Payload), importSrcKey) || !strings.Contains(string(job.Payload), `"contentType":"audio/mpeg"`) {
		t.Fatalf("job = %+v payload %s", job, job.Payload)
	}
	if len(e.spender.Spends) != 1 || e.spender.Spends[0].ID != resp.JobID || e.spender.Spends[0].Op != credit.OpImport {
		t.Fatalf("spends = %+v", e.spender.Spends)
	}
	if len(e.queue.tasks) != 1 || e.queue.tasks[0].Type() != TaskTypeImport || !strings.Contains(string(e.queue.tasks[0].Payload()), resp.JobID) {
		t.Fatalf("tasks = %v", e.queue.tasks)
	}
	if len(e.st.deleted) != 0 || len(e.st.events) != 0 {
		t.Fatalf("storage touched: %v %v", e.st.deleted, e.st.events)
	}
}

func TestImportService_Retry_CanceledAllowed(t *testing.T) {
	e := newImportEnv(95000)
	seedImportJob(t, e, oldImportID, model.JobStatusCanceled)
	if _, err := e.svc.Retry(context.Background(), "u1", oldImportID); err != nil {
		t.Fatal(err)
	}
}

func TestImportService_RetrySourceGone_NoDebit(t *testing.T) {
	e := newImportEnv(95000)
	e.st.gone = true
	seedImportJob(t, e, oldImportID, model.JobStatusFailed)
	_, err := e.svc.Retry(context.Background(), "u1", oldImportID)
	if !errors.Is(err, ErrImportSourceGone) {
		t.Fatalf("err = %v", err)
	}
	if len(e.spender.Spends) != 0 || len(e.queue.tasks) != 0 {
		t.Fatalf("spends %v tasks %v", e.spender.Spends, e.queue.tasks)
	}
	list, _ := e.repo.List(context.Background(), "u1", time.Now().Add(-time.Hour), 50)
	if len(list) != 0 { // the seeded failed job has no completion time, so List skips it
		t.Fatalf("a job was created: %+v", list)
	}
}

func TestImportService_Retry_NotFailed_NotRetryable(t *testing.T) {
	for _, st := range []model.JobStatus{model.JobStatusSucceeded, model.JobStatusRunning, model.JobStatusQueued} {
		e := newImportEnv(95000)
		seedImportJob(t, e, oldImportID, st)
		_, err := e.svc.Retry(context.Background(), "u1", oldImportID)
		if !errors.Is(err, ErrImportNotRetryable) {
			t.Fatalf("%s: err = %v", st, err)
		}
		if len(e.spender.Spends) != 0 {
			t.Fatalf("%s: spent", st)
		}
	}
}

func TestImportService_Retry_OtherOwner_NotFound(t *testing.T) {
	e := newImportEnv(95000)
	seedImportJob(t, e, oldImportID, model.JobStatusFailed)
	if _, err := e.svc.Retry(context.Background(), "u2", oldImportID); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestImportService_Retry_ActiveImport_Conflict(t *testing.T) {
	e := newImportEnv(95000)
	seedImportJob(t, e, oldImportID, model.JobStatusFailed)
	seedImportJob(t, e, liveImportID, model.JobStatusRunning)
	_, err := e.svc.Retry(context.Background(), "u1", oldImportID)
	if jobs.ActiveJobID(err) != liveImportID {
		t.Fatalf("err = %v", err)
	}
	if len(e.spender.Spends) != 0 {
		t.Fatal("spent")
	}
}

func TestJobCenter_CancelImport_RefundsAndSchedulesCleanup(t *testing.T) {
	for _, st := range []model.JobStatus{model.JobStatusQueued, model.JobStatusRunning} {
		e := newImportEnv(95000)
		e.svc.d.Cfg.SourceRetention = 24 * time.Hour
		seedImportJob(t, e, cancelImportID, st)
		center := NewJobCenter(e.repo, nil, nil).WithImport(e.svc)
		if err := center.Cancel(context.Background(), "u1", cancelImportID); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		job, _ := e.repo.Get(context.Background(), cancelImportID)
		if job.Status != model.JobStatusCanceled || job.CompletedAt == nil {
			t.Fatalf("%s: job = %+v", st, job)
		}
		if len(e.spender.Refunds) != 1 || e.spender.Refunds[0].Op != credit.OpImport || e.spender.Refunds[0].ID != cancelImportID {
			t.Fatalf("%s: refunds = %+v", st, e.spender.Refunds)
		}
		if len(e.queue.tasks) != 1 || e.queue.tasks[0].Type() != TaskTypeImportSourceCleanup {
			t.Fatalf("%s: tasks = %v", st, e.queue.tasks)
		}
		want := `{"ownerId":"u1","sourceKey":"` + importSrcKey + `","jobId":"` + cancelImportID + `"}`
		if string(e.queue.tasks[0].Payload()) != want {
			t.Fatalf("payload = %s", e.queue.tasks[0].Payload())
		}
		var queue string
		var delay time.Duration
		for _, o := range e.queue.opts[0] {
			switch o.Type() {
			case asynq.QueueOpt:
				queue = o.Value().(string)
			case asynq.ProcessInOpt:
				delay = o.Value().(time.Duration)
			}
		}
		if queue != "bakim" || delay != 24*time.Hour {
			t.Fatalf("%s: queue %q delay %v", st, queue, delay)
		}
		if err := center.Cancel(context.Background(), "u1", cancelImportID); !errors.Is(err, ErrJobAlreadyCompleted) {
			t.Fatalf("second cancel = %v", err)
		}
		if len(e.spender.Refunds) != 1 {
			t.Fatal("refunded twice")
		}
		if err := center.Cancel(context.Background(), "u2", cancelImportID); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("foreign cancel = %v", err)
		}
	}
}

func TestJobCenter_RetryImport_NotRetryable(t *testing.T) {
	e := newImportEnv(95000)
	seedImportJob(t, e, cancelImportID, model.JobStatusFailed)
	center := NewJobCenter(e.repo, nil, nil).WithImport(e.svc)
	if err := center.Retry(context.Background(), "u1", cancelImportID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("err = %v", err)
	}
	if len(e.spender.Spends) != 0 || len(e.queue.tasks) != 0 {
		t.Fatal("generic retry started something")
	}
}

func TestImportService_RetryEnqueueFails_SchedulesCleanup(t *testing.T) {
	e := newImportEnv(95000)
	seedImportJob(t, e, oldImportID, model.JobStatusFailed)
	// The first enqueue (the retry's task) fails; the cleanup task goes through.
	e.queue.failType = TaskTypeImport
	if _, err := e.svc.Retry(context.Background(), "u1", oldImportID); err == nil {
		t.Fatal("expected the enqueue error")
	}
	if len(e.spender.Refunds) != 1 {
		t.Fatalf("refunds = %+v", e.spender.Refunds)
	}
	if len(e.queue.tasks) != 1 || e.queue.tasks[0].Type() != TaskTypeImportSourceCleanup {
		t.Fatalf("tasks = %v", e.queue.tasks)
	}
	var p ImportCleanupPayload
	if err := json.Unmarshal(e.queue.tasks[0].Payload(), &p); err != nil || p.SourceKey != importSrcKey || p.OwnerID != "u1" || p.JobID == "" || p.JobID == oldImportID {
		t.Fatalf("cleanup payload = %+v, %v", p, err)
	}
}

// staleImportJob seeds a job of the given status whose only activity (its
// creation) was age ago.
func staleImportJob(t *testing.T, e *importEnv, id string, status model.JobStatus, age time.Duration) {
	t.Helper()
	payload := `{"sourceKey":"` + importSrcKey + `","durationMs":95000,"contentType":"audio/mpeg"}`
	err := e.repo.Create(context.Background(), &model.Job{
		ID: id, Type: model.JobTypeImport, OwnerID: "u1", Status: status,
		ProjectID: "p1", ProjectName: "Gece", Payload: []byte(payload), CreatedAt: time.Now().UTC().Add(-age),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportStaleSweep_FailsRefundsAndSchedulesCleanup(t *testing.T) {
	for _, st := range []model.JobStatus{model.JobStatusQueued, model.JobStatusRunning} {
		e := newImportEnv(95000)
		e.svc.d.Cfg.SourceRetention = 24 * time.Hour
		staleImportJob(t, e, liveImportID, st, 3*time.Hour)
		// The event of creation is recent in the memory store; age it by
		// sweeping at a later "now".
		n, err := e.svc.FailStale(context.Background(), time.Now().Add(3*time.Hour))
		if err != nil || n != 1 {
			t.Fatalf("%s: n=%d err=%v", st, n, err)
		}
		job, _ := e.repo.Get(context.Background(), liveImportID)
		if job.Status != model.JobStatusFailed || job.ErrorCode != "IMPORT_FAILED" || job.CompletedAt == nil {
			t.Fatalf("%s: job = %+v", st, job)
		}
		if len(e.spender.Refunds) != 1 || e.spender.Refunds[0].Op != credit.OpImport || e.spender.Refunds[0].ID != liveImportID {
			t.Fatalf("%s: refunds = %+v", st, e.spender.Refunds)
		}
		if len(e.st.deleted) != 5 || e.st.deleted[0] != "imports/u1/"+liveImportID+"/vocal.mp3" {
			t.Fatalf("%s: deleted outputs = %v", st, e.st.deleted)
		}
		for _, k := range e.st.deleted {
			if k == importSrcKey {
				t.Fatalf("%s: the source was deleted by the sweep", st)
			}
		}
		if len(e.queue.tasks) != 1 || e.queue.tasks[0].Type() != TaskTypeImportSourceCleanup ||
			!strings.Contains(string(e.queue.tasks[0].Payload()), `"jobId":"`+liveImportID+`"`) {
			t.Fatalf("%s: tasks = %v", st, e.queue.tasks)
		}
		// A second sweep finds nothing and refunds nothing more.
		if n, _ := e.svc.FailStale(context.Background(), time.Now().Add(3*time.Hour)); n != 0 || len(e.spender.Refunds) != 1 {
			t.Fatalf("%s: second sweep n=%d refunds=%d", st, n, len(e.spender.Refunds))
		}
	}
}

func TestImportStaleSweep_SkipsFreshAndTerminal(t *testing.T) {
	e := newImportEnv(95000)
	staleImportJob(t, e, liveImportID, model.JobStatusRunning, time.Minute)
	staleImportJob(t, e, cancelImportID, model.JobStatusCanceled, 5*time.Hour)
	staleImportJob(t, e, oldImportID, model.JobStatusSucceeded, 5*time.Hour)
	n, err := e.svc.FailStale(context.Background(), time.Now())
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(e.spender.Refunds) != 0 || len(e.st.deleted) != 0 || len(e.queue.tasks) != 0 {
		t.Fatalf("refunds %v deleted %v tasks %v", e.spender.Refunds, e.st.deleted, e.queue.tasks)
	}
	if job, _ := e.repo.Get(context.Background(), liveImportID); job.Status != model.JobStatusRunning {
		t.Fatalf("fresh job = %v", job.Status)
	}
}
