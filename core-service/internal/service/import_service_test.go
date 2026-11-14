package service

import (
	"context"
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
}

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
	events *[]string
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
}

func (q *importFakeQueue) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.err != nil {
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
		Cfg:  config.ImportConfig{Enabled: true, MinMs: 30000, MaxMs: 480000},
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
