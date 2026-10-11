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
	key, err := client.KullaniciAnahtari("previews", userID, previewID+".mp3")
	if err != nil {
		return nil, err
	}

	// Mock adres bile ayni kurallarla uretilir: istemci bunu geri
	// gonderdiginde KeyFromURL cozebilsin.
	fileURL := client.UnsignedURL(key, s.r2Cfg)

	return &model.MasterPreviewResponse{
		FileURL:   fileURL,
		Duration:  20,
		ExpiresAt: model.ExpiresPtr(time.Now().Add(s.r2Cfg.PresignTTLOrDefault())),
	}, nil
}

// StartFinal queues a final mastering job
func (s *MasterService) StartFinal(ctx context.Context, userID string, req *model.MasterFinalRequest) (*model.MasterFinalResponse, error) {
	// Girdi denetimi is kaydindan ONCE: worker bu anahtarlari okuyacak,
	// yabanci bir anahtar baskasinin dosyasini master'a sokardi.
	for _, u := range req.StemURLs {
		if _, err := client.SahipliAnahtarCoz(u, userID, s.r2Cfg); err != nil {
			return nil, fmt.Errorf("stem URL: %w", err)
		}
	}
	for _, take := range req.VocalTakes {
		if _, err := client.SahipliAnahtarCoz(take.FileURL, userID, s.r2Cfg); err != nil {
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
	job, err := sahipliJobOku(ctx, s.redis, userID, jobID)
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
	job, err := sahipliJobOku(ctx, s.redis, userID, jobID)
	if err != nil {
		return nil, err
	}

	if job.Status != model.JobStatusSucceeded {
		return nil, fmt.Errorf("job not completed")
	}

	return s.BuildResult(ctx, job.Result)
}

// BuildResult, Redis'te saklanan ham sonuc kaydindan istemci yanitini
// uretir. Adres HER ISTEKTE yeniden uretilir: kayitta imzali URL
// tutulmaz, boylece 24 saatlik is kaydi 1 saatlik imzayi tekrar
// oynatmaz (ve gecmis bir expiresAt donmez).
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
	return jobYaz(ctx, s.redis, job)
}

func (s *MasterService) getJob(ctx context.Context, jobID string) (*model.Job, error) {
	return jobOku(ctx, s.redis, jobID)
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
