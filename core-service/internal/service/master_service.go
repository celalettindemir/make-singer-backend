package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// MasterService handles mastering operations
type MasterService struct {
	redis       *redis.Client
	asynqClient *asynq.Client
	r2Client    client.StorageClient
	r2Cfg       *config.R2Config
}

func NewMasterService(redisClient *redis.Client, asynqClient *asynq.Client, r2Client client.StorageClient, r2Cfg *config.R2Config) *MasterService {
	return &MasterService{
		redis:       redisClient,
		asynqClient: asynqClient,
		r2Client:    r2Client,
		r2Cfg:       r2Cfg,
	}
}

// Preview generates a 20-second master preview
func (s *MasterService) Preview(ctx context.Context, userID string, req *model.MasterPreviewRequest) (*model.MasterPreviewResponse, error) {
	// TODO: Implement actual preview generation
	// This would call your audio processing service

	previewID := uuid.New().String()
	key, err := client.UserKey("previews", userID, previewID+".mp3")
	if err != nil {
		return nil, err
	}

	// Even the mock URL is produced under the same rules: so that KeyFromURL
	// can resolve it when the client sends it back.
	fileURL := client.UnsignedURL(key, s.r2Cfg)

	return &model.MasterPreviewResponse{
		FileURL:   fileURL,
		Duration:  20,
		ExpiresAt: model.ExpiresPtr(time.Now().Add(s.r2Cfg.PresignTTLOrDefault())),
	}, nil
}

// StartFinal queues a final mastering job
func (s *MasterService) StartFinal(ctx context.Context, userID string, req *model.MasterFinalRequest) (*model.MasterFinalResponse, error) {
	// Input check BEFORE the job record: the worker will read these keys, and
	// a foreign key would pull someone else's file into the master.
	for _, u := range req.StemURLs {
		if _, err := client.ResolveOwnedKey(u, userID, s.r2Cfg); err != nil {
			return nil, fmt.Errorf("stem URL: %w", err)
		}
	}
	for _, take := range req.VocalTakes {
		if _, err := client.ResolveOwnedKey(take.FileURL, userID, s.r2Cfg); err != nil {
			return nil, fmt.Errorf("vokal URL: %w", err)
		}
	}

	jobID := uuid.New().String()
	now := time.Now()

	// Create job record
	job := &model.Job{
		ID:        jobID,
		Type:      model.JobTypeMaster,
		OwnerID:   userID,
		Status:    model.JobStatusQueued,
		Progress:  0,
		CreatedAt: now,
	}

	// Create payload
	payload := &model.MasterJobPayload{
		OwnerID:     userID,
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
func (s *MasterService) GetStatus(ctx context.Context, userID, jobID string) (*model.MasterStatusResponse, error) {
	job, err := readOwnedJob(ctx, s.redis, userID, jobID)
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
func (s *MasterService) GetResult(ctx context.Context, userID, jobID string) (*model.MasterResultResponse, error) {
	job, err := readOwnedJob(ctx, s.redis, userID, jobID)
	if err != nil {
		return nil, err
	}

	if job.Status != model.JobStatusSucceeded {
		return nil, ErrJobNotCompleted
	}

	return s.BuildResult(ctx, job.Result)
}

// BuildResult produces the client response from the raw result record stored
// in Redis. The URL is regenerated ON EVERY REQUEST: the record holds no
// signed URL, so the 24-hour job record cannot replay a 1-hour signature (and
// a past expiresAt is never returned).
func (s *MasterService) BuildResult(ctx context.Context, stored []byte) (*model.MasterResultResponse, error) {
	var record model.MasterResultRecord
	if err := json.Unmarshal(stored, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}
	if record.FileKey == "" {
		return nil, fmt.Errorf("is kaydinda nesne anahtari yok")
	}

	fileURL, expiresAt, err := client.ResolveURL(ctx, s.r2Client, s.r2Cfg, record.FileKey)
	if err != nil {
		return nil, fmt.Errorf("master URL uretilemedi: %w", err)
	}

	return record.Response(fileURL, expiresAt), nil
}

// Helper methods

func (s *MasterService) saveJob(ctx context.Context, job *model.Job) error {
	return writeJob(ctx, s.redis, job)
}

func newMasterTask(jobID string, payload []byte) (*asynq.Task, error) {
	taskPayload := map[string]interface{}{
		"jobId":   jobID,
		"payload": json.RawMessage(payload),
	}
	data, err := json.Marshal(taskPayload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskTypeMaster, data), nil
}
