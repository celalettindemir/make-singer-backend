package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// ErrJobNotFound: the job does not exist OR does not belong to the requesting
// user. The two cases are deliberately not told apart; a 403 would confirm
// that the job exists.
var ErrJobNotFound = errors.New("job not found")

// ErrJobNotCompleted is returned when a result is requested before the job
// has succeeded. Handlers answer it with 400.
var ErrJobNotCompleted = errors.New("job not completed")

// ErrJobAlreadyCompleted is returned when a finished job is canceled.
// Handlers answer it with 400.
var ErrJobAlreadyCompleted = errors.New("job already completed")

const jobTTL = 24 * time.Hour

func jobKey(jobID string) string { return fmt.Sprintf("job:%s", jobID) }

func readJob(ctx context.Context, rdb *redis.Client, jobID string) (*model.Job, error) {
	data, err := rdb.Get(ctx, jobKey(jobID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	var job model.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

func writeJob(ctx context.Context, rdb *redis.Client, job *model.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, jobKey(job.ID), data, jobTTL).Err()
}

// readOwnedJob returns the job only to its owner; anyone else gets ErrJobNotFound.
func readOwnedJob(ctx context.Context, rdb *redis.Client, userID, jobID string) (*model.Job, error) {
	job, err := readJob(ctx, rdb, jobID)
	if err != nil {
		return nil, err
	}
	if !job.IsOwnedBy(userID) {
		return nil, ErrJobNotFound
	}
	return job, nil
}
