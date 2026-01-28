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

// MasterService handles mastering operations
type MasterService struct {
	redis       *redis.Client
	asynqClient *asynq.Client
}

func NewMasterService(redisClient *redis.Client, asynqClient *asynq.Client) *MasterService {
	return &MasterService{
		redis:       redisClient,
		asynqClient: asynqClient,
	}
}

// Preview generates a 20-second master preview
func (s *MasterService) Preview(ctx context.Context, req *model.MasterPreviewRequest) (*model.MasterPreviewResponse, error) {
	// TODO: Implement actual preview generation
	// This would call your audio processing service

	previewID := uuid.New().String()

	return &model.MasterPreviewResponse{
		FileURL:   fmt.Sprintf("https://cdn.makeasinger.com/previews/%s.mp3", previewID),
		Duration:  20,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}, nil
}

// StartFinal queues a final mastering job
func (s *MasterService) StartFinal(ctx context.Context, req *model.MasterFinalRequest) (*model.MasterFinalResponse, error) {
	jobID := uuid.New().String()
	now := time.Now()

	// Create job record
	job := &model.Job{
		ID:        jobID,
		Type:      model.JobTypeMaster,
		Status:    model.JobStatusQueued,
		Progress:  0,
		CreatedAt: now,
	}

	// Create payload
	payload := &model.MasterJobPayload{
		ProjectID:   req.ProjectID,
		Profile:     req.Profile,
		StemURLs:    req.StemURLs,
		MixSnapshot: req.MixSnapshot,
		VocalTakes:  req.VocalTakes,
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
	task, err := newMasterTask(jobID, payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Enqueue the task
	_, err = s.asynqClient.Enqueue(task,
		asynq.Queue("master"),
		asynq.MaxRetry(3),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue task: %w", err)
	}

	return &model.MasterFinalResponse{
		JobID:             jobID,
		Status:            model.JobStatusQueued,
		EstimatedDuration: 60,
	}, nil
}

// GetStatus returns the current status of a master job
func (s *MasterService) GetStatus(ctx context.Context, jobID string) (*model.MasterStatusResponse, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	return &model.MasterStatusResponse{
		JobID:       job.ID,
		Status:      job.Status,
		Progress:    job.Progress,
		CurrentStep: job.CurrentStep,
	}, nil
}

// GetResult returns the result of a completed master job
func (s *MasterService) GetResult(ctx context.Context, jobID string) (*model.MasterResultResponse, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	if job.Status != model.JobStatusSucceeded {
		return nil, fmt.Errorf("job not completed")
	}

	var result model.MasterResultResponse
	if err := json.Unmarshal(job.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return &result, nil
}

// Helper methods

func (s *MasterService) saveJob(ctx context.Context, job *model.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, fmt.Sprintf("job:%s", job.ID), data, 24*time.Hour).Err()
}

func (s *MasterService) getJob(ctx context.Context, jobID string) (*model.Job, error) {
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

func newMasterTask(jobID string, payload []byte) (*asynq.Task, error) {
	taskPayload := map[string]interface{}{
		"jobId":   jobID,
		"payload": payload,
	}
	data, err := json.Marshal(taskPayload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskTypeMaster, data), nil
}
