package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// importCleanupLookback bounds the job search: it must exceed the source
// retention so a retry that started after the failure is always seen.
const importCleanupLookback = 48 * time.Hour

// ImportCleanupWorker deletes an import source that was kept after a failed or
// canceled job, unless a retry still uses it.
type ImportCleanupWorker struct {
	repo      jobs.Repo
	storage   client.StorageClient
	retention time.Duration
	now       func() time.Time
}

// NewImportCleanupWorker: retention is the time a failed job's source is kept;
// a failure newer than that is cleaned up by its own, later task.
func NewImportCleanupWorker(repo jobs.Repo, storage client.StorageClient, retention time.Duration) *ImportCleanupWorker {
	return &ImportCleanupWorker{repo: repo, storage: storage, retention: retention, now: time.Now}
}

func (w *ImportCleanupWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p service.ImportCleanupPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("could not decode the import cleanup payload: %v: %w", err, asynq.SkipRetry)
	}
	// Never delete outside the owner's import prefix.
	if p.OwnerID == "" || !client.IsImportKey(p.SourceKey) ||
		!strings.HasPrefix(p.SourceKey, "imports/"+p.OwnerID+"/") || strings.Contains(p.SourceKey, "..") {
		return fmt.Errorf("import cleanup payload has a foreign source key: %w", asynq.SkipRetry)
	}

	list, err := w.repo.List(ctx, p.OwnerID, w.now().Add(-importCleanupLookback), 100)
	if err != nil {
		return fmt.Errorf("could not list import jobs: %w", err)
	}
	now := w.now()
	for _, j := range list {
		if j.Type != model.JobTypeImport {
			continue
		}
		switch j.Status {
		case model.JobStatusQueued, model.JobStatusRunning:
		case model.JobStatusFailed, model.JobStatusCanceled:
			// A recent failure keeps the source; its own cleanup task deletes it.
			if j.CompletedAt == nil || !j.CompletedAt.After(now.Add(-w.retention)) {
				continue
			}
		default:
			continue
		}
		var ip model.ImportPayload
		if json.Unmarshal(j.Payload, &ip) == nil && ip.SourceKey == p.SourceKey {
			log.Printf("import cleanup: source kept, job %s still uses it", j.ID)
			return nil
		}
	}

	// R2 deletes are idempotent: a missing object is not an error.
	if err := w.storage.Delete(ctx, p.SourceKey); err != nil {
		return fmt.Errorf("could not delete the import source: %w", err)
	}
	log.Printf("import cleanup: deleted %s", p.SourceKey)
	return nil
}
