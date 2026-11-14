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

const cleanupRetention = 24 * time.Hour

func cleanupTask(owner, key string) *asynq.Task {
	b, _ := json.Marshal(service.ImportCleanupPayload{OwnerID: owner, SourceKey: key})
	return asynq.NewTask(service.TaskTypeImportSourceCleanup, b)
}

func cleanupTaskFor(owner, key, jobID string) *asynq.Task {
	b, _ := json.Marshal(service.ImportCleanupPayload{OwnerID: owner, SourceKey: key, JobID: jobID})
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
	if err := NewImportCleanupWorker(repo, st, cleanupRetention).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
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
		if err := NewImportCleanupWorker(repo, st, cleanupRetention).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
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
	if err := NewImportCleanupWorker(jobs.NewMemoryStore(nil), st, cleanupRetention).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
		t.Fatalf("missing object must not fail: %v", err)
	}
}

func TestImportCleanup_BadPayloadSkipRetry(t *testing.T) {
	w := NewImportCleanupWorker(jobs.NewMemoryStore(nil), &impStorage{}, cleanupRetention)
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

func failedImportJob(t *testing.T, repo jobs.Repo, completedAgo time.Duration) {
	t.Helper()
	addImportJob(t, repo, impJobID, impSrc, model.JobStatusRunning)
	done := time.Now().Add(-completedAgo)
	err := repo.Update(context.Background(), impJobID, func(j *model.Job) error {
		j.Status, j.CompletedAt = model.JobStatusFailed, &done
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportCleanup_SkipsWhenRecentFailedJobUsesSource(t *testing.T) {
	repo, st := jobs.NewMemoryStore(nil), &impStorage{}
	failedImportJob(t, repo, 3*time.Hour)
	if err := NewImportCleanupWorker(repo, st, cleanupRetention).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
		t.Fatal(err)
	}
	if len(st.deleted) != 0 {
		t.Fatalf("source deleted despite recent failure: %v", st.deleted)
	}
}

func TestImportCleanup_DeletesWhenFailedJobOlderThanRetention(t *testing.T) {
	repo, st := jobs.NewMemoryStore(nil), &impStorage{}
	failedImportJob(t, repo, 25*time.Hour)
	if err := NewImportCleanupWorker(repo, st, cleanupRetention).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
		t.Fatal(err)
	}
	if len(st.deleted) != 1 {
		t.Fatalf("deleted = %v", st.deleted)
	}
}

func TestImportCleanup_DeletesWhenOwnJobCompletedJustAtRetention(t *testing.T) {
	repo, st := jobs.NewMemoryStore(nil), &impStorage{}
	failedImportJob(t, repo, cleanupRetention-time.Second)
	if err := NewImportCleanupWorker(repo, st, cleanupRetention).ProcessTask(context.Background(), cleanupTask(impOwner, impSrc)); err != nil {
		t.Fatal(err)
	}
	if len(st.deleted) != 1 {
		t.Fatalf("own job at the retention edge must not keep the source: %v", st.deleted)
	}
}

func TestImportCleanup_ExcludesOwnJob(t *testing.T) {
	// The task's own job failed a moment ago, well inside the retention window.
	repo, st := jobs.NewMemoryStore(nil), &impStorage{}
	failedImportJob(t, repo, time.Hour)
	if err := NewImportCleanupWorker(repo, st, cleanupRetention).ProcessTask(context.Background(), cleanupTaskFor(impOwner, impSrc, impJobID)); err != nil {
		t.Fatal(err)
	}
	if len(st.deleted) != 1 {
		t.Fatalf("own job must not keep the source: %v", st.deleted)
	}

	// Another recent failed job on the same source still keeps it.
	repo, st = jobs.NewMemoryStore(nil), &impStorage{}
	failedImportJob(t, repo, time.Hour)
	if err := NewImportCleanupWorker(repo, st, cleanupRetention).ProcessTask(context.Background(), cleanupTaskFor(impOwner, impSrc, "other-job")); err != nil {
		t.Fatal(err)
	}
	if len(st.deleted) != 0 {
		t.Fatalf("another recent failed job must keep the source: %v", st.deleted)
	}
}
