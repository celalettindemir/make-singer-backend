package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

// ErrPlusRequired: mastering with the app's AI vocal is for an active Plus (or
// Pro) plan only.
var ErrPlusRequired = errors.New("the app's vocal needs the Plus plan")

// usesAppVocal reports whether any key is the app's AI vocal (stored under
// guides/), as opposed to the user's own takes (vocals/) and stems (stems/).
func usesAppVocal(keys []string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, "guides/") {
			return true
		}
	}
	return false
}

// requirePlusForAppVocal refuses when the keys include the app's vocal and the
// user has no Plus plan. It is a product right, not a charge: it holds in
// shadow mode too and runs before anything is spent or saved.
func (s *MasterService) requirePlusForAppVocal(ctx context.Context, userID string, keys []string) error {
	if s.credits == nil || !usesAppVocal(keys) {
		return nil
	}
	plus, err := s.credits.HasPlan(ctx, userID, credit.TierPlus)
	if err != nil {
		return fmt.Errorf("could not check the plan: %w", err)
	}
	if !plus {
		return ErrPlusRequired
	}
	return nil
}

// MasterService handles mastering operations
type MasterService struct {
	repo        jobs.Repo
	asynqClient *asynq.Client
	r2Client    client.StorageClient
	r2Cfg       *config.R2Config
	credits     credit.Spender // nil: no credit accounting
}

func NewMasterService(repo jobs.Repo, asynqClient *asynq.Client, r2Client client.StorageClient, r2Cfg *config.R2Config) *MasterService {
	return &MasterService{
		repo:        repo,
		asynqClient: asynqClient,
		r2Client:    r2Client,
		r2Cfg:       r2Cfg,
	}
}

// WithCredits turns on credit accounting: StartFinal debits the master price
// and refunds when the job cannot be queued. A nil spender keeps the service
// free of credits.
func (s *MasterService) WithCredits(sp credit.Spender) *MasterService {
	s.credits = sp
	return s
}

// refundJob reverses a master debit on a detached context; a failure is
// logged, never returned (the ledger refund is idempotent per jobId).
func (s *MasterService) refundJob(ctx context.Context, jobID string) {
	if s.credits == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.credits.Refund(rctx, credit.OpMaster, jobID); err != nil {
		log.Printf("Job %s: could not refund %s: %v", jobID, credit.OpMaster, err)
	}
}

// Preview generates a 20-second master preview
func (s *MasterService) Preview(ctx context.Context, userID string, req *model.MasterPreviewRequest) (*model.MasterPreviewResponse, error) {
	// TODO: Implement actual preview generation
	// This would call your audio processing service

	keys := make([]string, 0, len(req.StemURLs))
	for _, u := range req.StemURLs {
		k, err := client.ResolveOwnedKey(u, userID, s.r2Cfg)
		if err != nil {
			return nil, fmt.Errorf("stem URL: %w", err)
		}
		keys = append(keys, k)
	}
	if err := s.requirePlusForAppVocal(ctx, userID, keys); err != nil {
		return nil, err
	}

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
	keys := make([]string, 0, len(req.StemURLs)+len(req.VocalTakes))
	for _, u := range req.StemURLs {
		k, err := client.ResolveOwnedKey(u, userID, s.r2Cfg)
		if err != nil {
			return nil, fmt.Errorf("stem URL: %w", err)
		}
		keys = append(keys, k)
	}
	for _, take := range req.VocalTakes {
		k, err := client.ResolveOwnedKey(take.FileURL, userID, s.r2Cfg)
		if err != nil {
			return nil, fmt.Errorf("vocal URL: %w", err)
		}
		keys = append(keys, k)
	}
	if err := s.requirePlusForAppVocal(ctx, userID, keys); err != nil {
		return nil, err
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

		ProjectID:   req.ProjectID,
		ProjectName: req.ProjectName,
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

	// Debit before the job record (P8); every failure below refunds it. An
	// *credit.InsufficientError is returned as is: no job, no enqueue.
	var balance int
	if s.credits != nil {
		b, err := s.credits.Spend(ctx, userID, credit.OpMaster, jobID, req.ProjectID)
		if err != nil {
			var insufficient *credit.InsufficientError
			if errors.As(err, &insufficient) || s.credits.Enforced() {
				return nil, err
			}
			// Shadow mode: nothing is debited, so a failed balance read
			// must not fail the master.
			log.Printf("Job %s: shadow credit read failed: %v", jobID, err)
			b = 0
		}
		balance = b
	}

	// Save the job
	if err := s.repo.Create(ctx, job); err != nil {
		s.refundJob(ctx, jobID)
		return nil, fmt.Errorf("failed to save job: %w", err)
	}

	// Create Asynq task
	task, err := newMasterTask(jobID, payloadBytes)
	if err != nil {
		s.refundJob(ctx, jobID)
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Enqueue the task
	_, err = s.asynqClient.Enqueue(task,
		asynq.Queue("master"),
		asynq.MaxRetry(3),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		s.refundJob(ctx, jobID)
		return nil, fmt.Errorf("failed to enqueue task: %w", err)
	}

	// Only an accepted request (job saved and enqueued) is logged.
	logMasterTakes(userID, jobID, req)

	return &model.MasterFinalResponse{
		JobID:             jobID,
		Status:            model.JobStatusQueued,
		EstimatedDuration: 60,
		Balance:           balance,
	}, nil
}

// GetStatus returns the current status of a master job
func (s *MasterService) GetStatus(ctx context.Context, userID, jobID string) (*model.MasterStatusResponse, error) {
	job, err := s.repo.GetOwned(ctx, userID, jobID)
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
	job, err := s.repo.GetOwned(ctx, userID, jobID)
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
		return nil, fmt.Errorf("the job record has no object key")
	}

	fileURL, expiresAt, err := client.ResolveURL(ctx, s.r2Client, s.r2Cfg, record.FileKey)
	if err != nil {
		return nil, fmt.Errorf("could not generate the master URL: %w", err)
	}

	return record.Response(fileURL, expiresAt), nil
}

// Cancel cancels a master job with the rules of RenderService.CancelRender:
// the status check and the write are one transaction, only a job that never
// left the queue is refunded, and a finished job is ErrJobAlreadyCompleted.
func (s *MasterService) Cancel(ctx context.Context, userID, jobID string) error {
	wasQueued := false
	err := s.repo.Update(ctx, jobID, func(job *model.Job) error {
		if !job.IsOwnedBy(userID) || job.Type != model.JobTypeMaster {
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
		return err
	}
	if wasQueued {
		s.refundJob(ctx, jobID)
	}
	return nil
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

// logMasterTakes records which takes a final master was requested with
// (R-VOC-17). The file URL is signed and therefore not logged.
func logMasterTakes(userID, jobID string, req *model.MasterFinalRequest) {
	ms := func(p *int) string {
		if p == nil {
			return "-"
		}
		return strconv.Itoa(*p)
	}
	for _, t := range req.VocalTakes {
		log.Printf("master take: job=%s user=%s project=%s section=%s take=%s startMs=%s endMs=%s",
			jobID, userID, req.ProjectID, t.SectionID, t.TakeID, ms(t.StartMs), ms(t.EndMs))
	}
}
