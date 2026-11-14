package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

const (
	// ImportErrorCode is the error code of an import job that failed.
	ImportErrorCode = "IMPORT_FAILED"
	// importStaleAfterDefault applies when Cfg.StaleAfter is not positive, so
	// a bad value can never fail every active job.
	importStaleAfterDefault = 2 * time.Hour
)

// ImportOutputKeys are the keys a job's separation and notes step write under
// its own prefix. The source is not among them: it may live under an older
// job's prefix and is handled by the source cleanup task.
func ImportOutputKeys(ownerID, jobID string) []string {
	var keys []string
	for _, name := range []string{"vocal.mp3", "drums.mp3", "bass.mp3", "rest.mp3", "notes.json"} {
		if k, err := client.UserKey("imports", ownerID, jobID, name); err == nil {
			keys = append(keys, k)
		}
	}
	return keys
}

// DeleteImportOutputs deletes a job's output keys, best effort: a refunded or
// canceled job must not leave separated stems that can still be signed. It
// ignores a canceled context.
func DeleteImportOutputs(ctx context.Context, storage client.StorageClient, ownerID, jobID string) {
	if storage == nil {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, k := range ImportOutputKeys(ownerID, jobID) {
		if err := storage.Delete(dctx, k); err != nil {
			log.Printf("import job %s: could not delete output %s: %v", jobID, k, err)
		}
	}
}

// FailStale ends the import jobs nobody is working on (a lost worker, a DB
// error on the last attempt, the flag turned off with jobs queued): a queued
// or running job with no event for Cfg.StaleAfter becomes failed IMPORT_FAILED,
// is refunded, loses its outputs and schedules the source cleanup. It returns
// how many jobs it ended.
func (s *ImportService) FailStale(ctx context.Context, now time.Time) (int, error) {
	d := s.d
	after := d.Cfg.StaleAfter
	if after <= 0 {
		after = importStaleAfterDefault
	}
	ids, err := d.Repo.StaleImportJobs(ctx, now.Add(-after))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		var owner, sourceKey string
		failed := false
		err := d.Repo.UpdateWith(ctx, id, func(j *model.Job, _ jobs.Emitter) error {
			if j.Status != model.JobStatusQueued && j.Status != model.JobStatusRunning {
				return jobs.ErrSkipWrite
			}
			var p model.ImportPayload
			if json.Unmarshal(j.Payload, &p) == nil {
				sourceKey = p.SourceKey
			}
			msg := "Import failed"
			done := now.UTC()
			owner = j.OwnerID
			j.Status, j.Error, j.ErrorCode, j.CompletedAt = model.JobStatusFailed, &msg, ImportErrorCode, &done
			failed = true
			return nil
		})
		if err != nil || !failed {
			continue // terminal meanwhile (the other party refunded) or a store error: next sweep
		}
		n++
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if d.Credits != nil {
			if rerr := d.Credits.Refund(rctx, credit.OpImport, id); rerr != nil {
				log.Printf("import stale: refund job=%s: %v", id, rerr)
			}
		}
		cancel()
		DeleteImportOutputs(ctx, d.Storage, owner, id)
		if sourceKey != "" {
			if cerr := s.ScheduleSourceCleanup(owner, sourceKey, id); cerr != nil {
				log.Printf("import stale: schedule cleanup job=%s: %v", id, cerr)
			}
		}
	}
	return n, nil
}
