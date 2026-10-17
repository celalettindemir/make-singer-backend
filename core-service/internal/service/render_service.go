package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

const (
	TaskTypeRender = "render:process"
	TaskTypeMaster = "master:process"
)

// RenderService handles render job management
type RenderService struct {
	redis       *redis.Client
	asynqClient *asynq.Client
	r2Client    client.StorageClient
	r2Cfg       *config.R2Config
	credits     credit.Spender // nil: no credit accounting

	// beforeExec is a test seam: called after updateJob read the record and
	// before the transaction executes, to force a WATCH conflict.
	beforeExec func()
}

func NewRenderService(redisClient *redis.Client, asynqClient *asynq.Client, r2Client client.StorageClient, r2Cfg *config.R2Config) *RenderService {
	return &RenderService{
		redis:       redisClient,
		asynqClient: asynqClient,
		r2Client:    r2Client,
		r2Cfg:       r2Cfg,
	}
}

// WithCredits turns on credit accounting: StartRender debits, and a cancel
// while the job is still queued refunds. A nil spender keeps the service
// free of credits.
func (s *RenderService) WithCredits(sp credit.Spender) *RenderService {
	s.credits = sp
	return s
}

// RenderOperation maps a stem mode to its credit operation. An empty or
// unknown mode is mix (the cheap side), like stemPlan in the worker.
func RenderOperation(mode model.StemMode) credit.Operation {
	switch mode {
	case model.StemModeStems:
		return credit.OpRenderStems
	case model.StemModeFocus:
		return credit.OpRenderFocus
	default:
		return credit.OpRenderMix
	}
}

// refundJob reverses a render debit. It uses a detached context so a
// canceled request cannot skip the refund; a failure is logged, never
// returned (the ledger refund is idempotent per jobId, so it can be redone).
func (s *RenderService) refundJob(ctx context.Context, op credit.Operation, jobID string) {
	if s.credits == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.credits.Refund(rctx, op, jobID); err != nil {
		log.Printf("Job %s: could not refund %s: %v", jobID, op, err)
	}
}

// BuildResult produces the client response from the stored record: each
// stem's URL and expiry are regenerated ON EVERY CALL; the record holds no
// signed URL.
func (s *RenderService) BuildResult(ctx context.Context, record *model.RenderResultRecord) (*model.RenderResultResponse, error) {
	stems := make([]model.StemResult, 0, len(record.Stems))
	for _, st := range record.Stems {
		if st.FileKey == "" {
			return nil, fmt.Errorf("the stem record has no object key: %s", st.ID)
		}
		fileURL, expiresAt, err := client.ResolveURL(ctx, s.r2Client, s.r2Cfg, st.FileKey)
		if err != nil {
			return nil, fmt.Errorf("could not generate the stem URL: %w", err)
		}
		stems = append(stems, model.StemResult{
			ID:           st.ID,
			Instrument:   st.Instrument,
			FileURL:      fileURL,
			Duration:     st.Duration,
			WaveformData: st.WaveformData,
			ExpiresAt:    model.ExpiresPtr(expiresAt),
		})
	}

	return &model.RenderResultResponse{
		ID:        record.ID,
		BPM:       record.BPM,
		Duration:  record.Duration,
		Key:       record.Key,
		CreatedAt: record.CreatedAt,
		StemMode:  record.StemMode,
		Stems:     stems,
	}, nil
}

// StartRender queues a new render job
func (s *RenderService) StartRender(ctx context.Context, userID string, req *model.RenderStartRequest) (*model.RenderStartResponse, error) {
	// The stem settings are validated BEFORE the job record: an inconsistent request creates no job.
	stemMode, focus, err := req.StemSettings()
	if err != nil {
		return nil, err
	}

	jobID := uuid.New().String()
	now := time.Now()

	// Create job record
	job := &model.Job{
		ID:        jobID,
		Type:      model.JobTypeRender,
		OwnerID:   userID,
		Status:    model.JobStatusQueued,
		Progress:  0,
		CreatedAt: now,
	}

	// Create payload
	payload := &model.RenderJobPayload{
		OwnerID:         userID,
		ProjectID:       req.ProjectID,
		Brief:           req.Brief,
		Arrangement:     req.Arrangement,
		StemMode:        stemMode,
		FocusInstrument: focus,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	job.Payload = payloadBytes

	// Debit at the start (reservation); every failure below refunds it.
	// An *credit.InsufficientError is returned as is: no job, no enqueue.
	op := RenderOperation(stemMode)
	balance := 0
	if s.credits != nil {
		balance, err = s.credits.Spend(ctx, userID, op, jobID)
		if err != nil {
			var insufficient *credit.InsufficientError
			if errors.As(err, &insufficient) || s.credits.Enforced() {
				return nil, err
			}
			// Shadow mode: nothing is debited, so a failed balance read
			// must not fail the render.
			log.Printf("Job %s: shadow credit read failed: %v", jobID, err)
			balance = 0
		}
	}

	// Save job to Redis
	if err := s.saveJob(ctx, job); err != nil {
		s.refundJob(ctx, op, jobID)
		return nil, fmt.Errorf("failed to save job: %w", err)
	}

	// Create Asynq task
	task, err := newRenderTask(jobID, payloadBytes)
	if err != nil {
		s.refundJob(ctx, op, jobID)
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Enqueue the task
	_, err = s.asynqClient.Enqueue(task,
		asynq.Queue("render"),
		asynq.MaxRetry(3),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		s.refundJob(ctx, op, jobID)
		return nil, fmt.Errorf("failed to enqueue task: %w", err)
	}

	return &model.RenderStartResponse{
		JobID:             jobID,
		Status:            model.JobStatusQueued,
		EstimatedDuration: 45, // seconds - this would be calculated based on song length
		CreatedAt:         now,
		Balance:           balance,
	}, nil
}

// JobStatus returns a job's current status without an owner check (called by
// the worker).
func (s *RenderService) JobStatus(ctx context.Context, jobID string) (model.JobStatus, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	return job.Status, nil
}

// GetStatus returns the current status of a render job
func (s *RenderService) GetStatus(ctx context.Context, userID, jobID string) (*model.RenderStatusResponse, error) {
	job, err := readOwnedJob(ctx, s.redis, userID, jobID)
	if err != nil {
		return nil, err
	}

	return &model.RenderStatusResponse{
		JobID:       job.ID,
		Status:      job.Status,
		Progress:    job.Progress,
		CurrentStep: job.CurrentStep,
		Error:       job.Error,
		CreatedAt:   job.CreatedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
		RetryCount:  job.RetryCount,
	}, nil
}

// GetResult returns the result of a completed render job
func (s *RenderService) GetResult(ctx context.Context, userID, jobID string) (*model.RenderResultResponse, error) {
	job, err := readOwnedJob(ctx, s.redis, userID, jobID)
	if err != nil {
		return nil, err
	}

	if job.Status != model.JobStatusSucceeded {
		return nil, ErrJobNotCompleted
	}

	var record model.RenderResultRecord
	if err := json.Unmarshal(job.Result, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return s.BuildResult(ctx, &record)
}

// CancelRender cancels a render job. The status check and the write are one
// transaction, so a worker claiming the job at the same time either sees
// canceled (and never calls Suno) or wins (and the cancel sees running).
func (s *RenderService) CancelRender(ctx context.Context, userID, jobID string) (*model.RenderCancelResponse, error) {
	// Only a job that never left the queue is refunded: no Suno call was
	// made. A running job keeps its charge (6-G2).
	wasQueued := false
	err := s.updateJob(ctx, jobID, func(job *model.Job) error {
		if !job.IsOwnedBy(userID) {
			return ErrJobNotFound
		}
		if job.Status == model.JobStatusSucceeded || job.Status == model.JobStatusFailed {
			return ErrJobAlreadyCompleted
		}
		wasQueued = job.Status == model.JobStatusQueued
		job.Status = model.JobStatusCanceled
		now := time.Now()
		job.CompletedAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	if wasQueued {
		// The stored job keeps no stem mode (Job.Payload is not persisted),
		// and the ledger refund is keyed by jobId within the render family,
		// so any render operation names the right debit.
		s.refundJob(ctx, credit.OpRenderMix, jobID)
	}

	return &model.RenderCancelResponse{
		Success: true,
		JobID:   jobID,
		Status:  model.JobStatusCanceled,
	}, nil
}

// UpdateJobProgress updates job progress (called by worker). A canceled job
// stays canceled.
func (s *RenderService) UpdateJobProgress(ctx context.Context, jobID string, progress int, step string) error {
	err := s.updateJob(ctx, jobID, func(job *model.Job) error {
		if job.Status == model.JobStatusCanceled {
			return errSkipWrite
		}
		job.Progress = progress
		job.CurrentStep = step
		if job.Status == model.JobStatusQueued {
			markRunning(job)
		}
		return nil
	})
	if errors.Is(err, errSkipWrite) {
		return nil
	}
	return err
}

// ClaimRun is the worker's last check before the paid Suno call. It returns
// false (with the status it saw) if the job was canceled or is already finished; otherwise it marks the job running, which
// makes a later cancel a running-cancel (no refund). Both outcomes are decided
// in one transaction against CancelRender.
func (s *RenderService) ClaimRun(ctx context.Context, jobID string) (claimed bool, status model.JobStatus, err error) {
	err = s.updateJob(ctx, jobID, func(job *model.Job) error {
		// The callback re-runs on a WATCH conflict: decide from scratch each
		// time, so a stale true from an earlier read cannot survive.
		claimed, status = false, job.Status
		switch job.Status {
		case model.JobStatusCanceled, model.JobStatusFailed, model.JobStatusSucceeded:
			return errSkipWrite
		case model.JobStatusQueued:
			markRunning(job)
		}
		claimed = true
		return nil
	})
	if err != nil && !errors.Is(err, errSkipWrite) {
		return false, "", err
	}
	return claimed, status, nil
}

func markRunning(job *model.Job) {
	job.Status = model.JobStatusRunning
	now := time.Now()
	job.StartedAt = &now
}

// CompleteJob marks job as completed (called by worker)
func (s *RenderService) CompleteJob(ctx context.Context, jobID string, result interface{}) error {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.updateJob(ctx, jobID, func(job *model.Job) error {
		job.Status = model.JobStatusSucceeded
		job.Progress = 100
		job.Result = resultBytes
		now := time.Now()
		job.CompletedAt = &now
		return nil
	})
}

// FailJob marks job as failed (called by worker)
func (s *RenderService) FailJob(ctx context.Context, jobID string, errMsg string) error {
	return s.updateJob(ctx, jobID, func(job *model.Job) error {
		job.Status = model.JobStatusFailed
		job.Error = &errMsg
		now := time.Now()
		job.CompletedAt = &now
		return nil
	})
}

// errSkipWrite: updateJob callbacks return it to leave the record untouched;
// updateJob returns it to the caller, which treats it as success.
var errSkipWrite = errors.New("skip job write")

const updateJobAttempts = 20

// updateJob read-modify-writes a job under WATCH, so concurrent updates (the
// worker and a cancel) cannot overwrite each other. fn runs again on every
// conflict and must be free of side effects.
func (s *RenderService) updateJob(ctx context.Context, jobID string, fn func(*model.Job) error) error {
	key := jobKey(jobID)
	for i := 0; i < updateJobAttempts; i++ {
		err := s.redis.Watch(ctx, func(tx *redis.Tx) error {
			data, err := tx.Get(ctx, key).Bytes()
			if err != nil {
				if errors.Is(err, redis.Nil) {
					return ErrJobNotFound
				}
				return err
			}
			var job model.Job
			if err := json.Unmarshal(data, &job); err != nil {
				return err
			}
			if err := fn(&job); err != nil {
				return err
			}
			out, err := json.Marshal(&job)
			if err != nil {
				return err
			}
			if s.beforeExec != nil {
				s.beforeExec()
			}
			_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
				return p.Set(ctx, key, out, jobTTL).Err()
			})
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return redis.TxFailedErr
}

// Helper methods

func (s *RenderService) saveJob(ctx context.Context, job *model.Job) error {
	return writeJob(ctx, s.redis, job)
}

func (s *RenderService) getJob(ctx context.Context, jobID string) (*model.Job, error) {
	return readJob(ctx, s.redis, jobID)
}

func newRenderTask(jobID string, payload []byte) (*asynq.Task, error) {
	taskPayload := map[string]interface{}{
		"jobId":   jobID,
		"payload": json.RawMessage(payload),
	}
	data, err := json.Marshal(taskPayload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskTypeRender, data), nil
}
