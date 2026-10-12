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

// ErrJobNotFound: is yok YA DA istegi yapan kullaniciya ait degil. Iki
// durum bilerek ayirt edilmez; 403 isin var oldugunu dogrulardi.
var ErrJobNotFound = errors.New("job not found")

const jobTTL = 24 * time.Hour

func jobAnahtari(jobID string) string { return fmt.Sprintf("job:%s", jobID) }

func jobOku(ctx context.Context, rdb *redis.Client, jobID string) (*model.Job, error) {
	data, err := rdb.Get(ctx, jobAnahtari(jobID)).Bytes()
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

func jobYaz(ctx context.Context, rdb *redis.Client, job *model.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, jobAnahtari(job.ID), data, jobTTL).Err()
}

// sahipliJobOku, isi yalnizca sahibine dondurur; digerlerine ErrJobNotFound.
func sahipliJobOku(ctx context.Context, rdb *redis.Client, userID, jobID string) (*model.Job, error) {
	job, err := jobOku(ctx, rdb, jobID)
	if err != nil {
		return nil, err
	}
	if !job.IsOwnedBy(userID) {
		return nil, ErrJobNotFound
	}
	return job, nil
}
