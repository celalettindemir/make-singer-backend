package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/makeasinger/api/internal/model"
)

const (
	TaskTypeRender = "render:process"
	TaskTypeMaster = "master:process"
)

// RenderService handles render job management
type RenderService struct {
	redis       *redis.Client
	asynqClient *asynq.Client
}

func NewRenderService(redisClient *redis.Client, asynqClient *asynq.Client) *RenderService {
	return &RenderService{
		redis:       redisClient,
		asynqClient: asynqClient,
	}
}

// StartRender queues a new render job
func (s *RenderService) StartRender(ctx context.Context, req *model.RenderStartRequest) (*model.RenderStartResponse, error) {
	jobID := uuid.New().String()
	now := time.Now()

	// Create job record
	job := &model.Job{
		ID:        jobID,
		Type:      model.JobTypeRender,
		Status:    model.JobStatusQueued,
		Progress:  0,
		CreatedAt: now,
	}

	// Create payload
	payload := &model.RenderJobPayload{
		ProjectID:   req.ProjectID,
		Brief:       req.Brief,
		Arrangement: req.Arrangement,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	job.Payload = payloadBytes

	// Save job to Redis
	if err := s.saveJob(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to save job: %w", err)
	}

	// Create Asynq task
	task, err := newRenderTask(jobID, payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Enqueue the task
	_, err = s.asynqClient.Enqueue(task,
		asynq.Queue("render"),
		asynq.MaxRetry(3),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue task: %w", err)
	}

	return &model.RenderStartResponse{
		JobID:             jobID,
		Status:            model.JobStatusQueued,
		EstimatedDuration: 45, // seconds - this would be calculated based on song length
		CreatedAt:         now,
	}, nil
}

// GetStatus returns the current status of a render job
func (s *RenderService) GetStatus(ctx context.Context, jobID string) (*model.RenderStatusResponse, error) {
	job, err := s.getJob(ctx, jobID)
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
func (s *RenderService) GetResult(ctx context.Context, jobID string) (*model.RenderResultResponse, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	if job.Status != model.JobStatusSucceeded {
		return nil, fmt.Errorf("job not completed")
	}

	var result model.RenderResultResponse
	if err := json.Unmarshal(job.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return &result, nil
}

// CancelRender cancels a render job
func (s *RenderService) CancelRender(ctx context.Context, jobID string) (*model.RenderCancelResponse, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	if job.Status == model.JobStatusSucceeded || job.Status == model.JobStatusFailed {
		return nil, fmt.Errorf("job already completed")
	}

	job.Status = model.JobStatusCanceled
	now := time.Now()
	job.CompletedAt = &now

	if err := s.saveJob(ctx, job); err != nil {
		return nil, err
	}

	return &model.RenderCancelResponse{
		Success: true,
		JobID:   jobID,
		Status:  model.JobStatusCanceled,
	}, nil
}

// UpdateJobProgress updates job progress (called by worker)
func (s *RenderService) UpdateJobProgress(ctx context.Context, jobID string, progress int, step string) error {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return err
	}

	job.Progress = progress
	job.CurrentStep = step

	if job.Status == model.JobStatusQueued {
		job.Status = model.JobStatusRunning
		now := time.Now()
		job.StartedAt = &now
	}

	return s.saveJob(ctx, job)
}

// CompleteJob marks job as completed (called by worker)
func (s *RenderService) CompleteJob(ctx context.Context, jobID string, result interface{}) error {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return err
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}

	job.Status = model.JobStatusSucceeded
	job.Progress = 100
	job.Result = resultBytes
	now := time.Now()
	job.CompletedAt = &now

	return s.saveJob(ctx, job)
}

// FailJob marks job as failed (called by worker)
func (s *RenderService) FailJob(ctx context.Context, jobID string, errMsg string) error {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return err
	}

	job.Status = model.JobStatusFailed
	job.Error = &errMsg
	now := time.Now()
	job.CompletedAt = &now

	return s.saveJob(ctx, job)
}

// Helper methods

func (s *RenderService) saveJob(ctx context.Context, job *model.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, fmt.Sprintf("job:%s", job.ID), data, 24*time.Hour).Err()
}

func (s *RenderService) getJob(ctx context.Context, jobID string) (*model.Job, error) {
	data, err := s.redis.Get(ctx, fmt.Sprintf("job:%s", jobID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("job not found")
		}
		return nil, err
	}

	var job model.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}

	return &job, nil
}

func newRenderTask(jobID string, payload []byte) (*asynq.Task, error) {
	taskPayload := map[string]interface{}{
		"jobId":   jobID,
		"payload": payload,
	}
	data, err := json.Marshal(taskPayload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskTypeRender, data), nil
}
