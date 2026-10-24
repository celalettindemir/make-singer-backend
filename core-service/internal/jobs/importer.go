package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// TaskTypeRedisImport is the asynq task (queue bakim) that runs
// ImportRedisJobs again, so jobs of pods still on the old release (which
// write to Redis) reach the store during a rolling deploy.
const TaskTypeRedisImport = "uretim:redis-aktar"

// NewRedisImportHandler returns the asynq handler of TaskTypeRedisImport.
func NewRedisImportHandler(rdb *redis.Client, repo *PostgresStore) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, _ *asynq.Task) error {
		copied, refreshed, err := ImportRedisJobs(ctx, rdb, repo)
		log.Printf("Redis job import: %d copied, %d refreshed", copied, refreshed)
		return err
	}
}

// ImportRedisJobs copies the legacy job:<id> records of Redis into the store.
// A missing row is inserted (expiry = created + 30 days, only job.queued as
// event). A row that is still non-terminal and differs from its Redis copy is
// overwritten (status, progress, step, result, error) through Update, so the
// derived events flow; a terminal row is never touched. One unreadable record
// is logged and skipped.
func ImportRedisJobs(ctx context.Context, rdb *redis.Client, repo *PostgresStore) (copied, refreshed int, err error) {
	var cursor uint64
	for {
		keys, next, serr := rdb.Scan(ctx, cursor, "job:*", 200).Result()
		if serr != nil {
			return copied, refreshed, serr
		}
		for _, key := range keys {
			c, r, ierr := importOne(ctx, rdb, repo, key)
			if ierr != nil {
				if ctx.Err() != nil {
					return copied, refreshed, ctx.Err()
				}
				log.Printf("Warning: could not import %s: %v", key, ierr)
				continue
			}
			copied += c
			refreshed += r
		}
		cursor = next
		if cursor == 0 {
			return copied, refreshed, nil
		}
	}
}

func importOne(ctx context.Context, rdb *redis.Client, repo *PostgresStore, key string) (copied, refreshed int, err error) {
	raw, err := rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return 0, 0, nil // expired between SCAN and GET
	}
	if err != nil {
		return 0, 0, err
	}
	var src model.Job
	if err := json.Unmarshal(raw, &src); err != nil {
		return 0, 0, err
	}
	if _, perr := uuid.Parse(src.ID); perr != nil || src.OwnerID == "" {
		return 0, 0, nil // not a job record we can store
	}
	if src.ErrorCode == "" && src.Status == model.JobStatusFailed {
		src.ErrorCode = legacyErrorCode(src.Type)
	}

	if _, err := repo.Get(ctx, src.ID); errors.Is(err, ErrJobNotFound) {
		if src.CreatedAt.IsZero() {
			src.CreatedAt = time.Now().UTC()
		}
		cp := src
		cp.Payload = nil
		if cerr := repo.Create(ctx, &cp); cerr == nil {
			return 1, 0, nil
		} else if _, gerr := repo.Get(ctx, src.ID); gerr != nil {
			return 0, 0, cerr // not a lost race with another importer
		}
	} else if err != nil {
		return 0, 0, err
	}

	wrote := false
	err = repo.Update(ctx, src.ID, func(j *model.Job) error {
		wrote = false
		if isTerminal(j.Status) || sameProgress(j, &src) {
			return ErrSkipWrite
		}
		j.Status = src.Status
		j.Progress = src.Progress
		j.CurrentStep = src.CurrentStep
		j.Result = src.Result
		j.Error = src.Error
		j.ErrorCode = src.ErrorCode
		if src.StartedAt != nil {
			j.StartedAt = src.StartedAt
		}
		if src.CompletedAt != nil {
			j.CompletedAt = src.CompletedAt
		}
		wrote = true
		return nil
	})
	if err != nil && !errors.Is(err, ErrSkipWrite) {
		return 0, 0, err
	}
	if wrote {
		refreshed = 1
	}
	return 0, refreshed, nil
}

func sameProgress(a, b *model.Job) bool {
	ea, eb := "", ""
	if a.Error != nil {
		ea = *a.Error
	}
	if b.Error != nil {
		eb = *b.Error
	}
	return a.Status == b.Status && a.Progress == b.Progress && a.CurrentStep == b.CurrentStep &&
		ea == eb && bytes.Equal(a.Result, b.Result)
}

func legacyErrorCode(jobType string) string {
	if jobType == model.JobTypeMaster {
		return "MASTER_FAILED"
	}
	return "RENDER_FAILED"
}
