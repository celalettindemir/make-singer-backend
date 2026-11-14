package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

func cleanupTask(owner, key string) *asynq.Task {
	b, _ := json.Marshal(service.ImportCleanupPayload{OwnerID: owner, SourceKey: key})
	return asynq.NewTask(service.TaskTypeImportSourceCleanup, b)
}

func addImportJob(t *testing.T, repo jobs.Repo, id, key string, st model.JobStatus) {
	t.Helper()
	payload, _ := json.Marshal(model.ImportPayload{SourceKey: key})
	err := repo.Create(context.Background(), &model.Job{ID: id, Type: model.JobTypeImport, OwnerID: impOwner,
		ProjectID: "p1", ProjectName: "Yaz", Status: st, Payload: payload, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportCleanup_DeletesSource(t *testing.T) {
	repo, st := jobs.NewMemoryStore(nil), &impStorage{}
	addImportJob(t, repo, impJobID, impSrc, model.JobStatusFailed)
	if err := NewImportCleanupWorker(repo, st).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
		t.Fatal(err)
	}
	if len(st.deleted) != 1 || st.deleted[0] != impSrc {
		t.Fatalf("deleted = %v", st.deleted)
	}
}

func TestImportCleanup_SkipsWhenActiveRetryUsesSource(t *testing.T) {
	for _, status := range []model.JobStatus{model.JobStatusQueued, model.JobStatusRunning} {
		repo, st := jobs.NewMemoryStore(nil), &impStorage{}
		addImportJob(t, repo, impJobID, impSrc, status)
		if err := NewImportCleanupWorker(repo, st).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
			t.Fatal(err)
		}
		if len(st.deleted) != 0 {
			t.Fatalf("%s: source deleted despite active job: %v", status, st.deleted)
		}
	}
}

func TestImportCleanup_MissingObjectIsNotError(t *testing.T) {
	// No job and no object: storage Delete is idempotent and returns nil.
	st := &impStorage{}
	if err := NewImportCleanupWorker(jobs.NewMemoryStore(nil), st).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
		t.Fatalf("missing object must not fail: %v", err)
	}
}

func TestImportCleanup_BadPayloadSkipRetry(t *testing.T) {
	w := NewImportCleanupWorker(jobs.NewMemoryStore(nil), &impStorage{})
	bad := []*asynq.Task{
		asynq.NewTask(service.TaskTypeImportSourceCleanup, []byte("{")),
		cleanupTask(impOwner, "imports/u2/x/source.mp3"),
		cleanupTask(impOwner, "renders/u1/x.mp3"),
		cleanupTask("", "imports//x"),
	}
	for i, task := range bad {
		if err := w.ProcessTask(context.Background(), task); !errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("case %d: want SkipRetry, got %v", i, err)
		}
	}
}
