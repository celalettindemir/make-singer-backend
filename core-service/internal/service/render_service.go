package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// ErrJobNotFound: the job does not exist OR does not belong to the requesting
// user. The two cases are deliberately not told apart; a 403 would confirm
// that the job exists.
var ErrJobNotFound = jobs.ErrJobNotFound

// ErrJobNotCompleted is returned when a result is requested before the job
// has succeeded. Handlers answer it with 400.
var ErrJobNotCompleted = jobs.ErrJobNotCompleted

// ErrJobAlreadyCompleted is returned when a finished job is canceled.
// Handlers answer it with 400.
var ErrJobAlreadyCompleted = jobs.ErrJobAlreadyCompleted

const (
	TaskTypeRender = "render:process"
	TaskTypeMaster = "master:process"
)

// ErrPlanRequired: the stems render is for an active Pro plan only (R-CRD-16).
var ErrPlanRequired = errors.New("the stems render needs the Pro plan")

// RenderService handles render job management
type RenderService struct {
	repo        jobs.Repo
	asynqClient *asynq.Client
	r2Client    client.StorageClient
	r2Cfg       *config.R2Config
	credits     credit.Spender // nil: no credit accounting
}

func NewRenderService(repo jobs.Repo, asynqClient *asynq.Client, r2Client client.StorageClient, r2Cfg *config.R2Config) *RenderService {
	return &RenderService{
		repo:        repo,
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

	guide, err := s.buildGuide(ctx, record.Guide)
	if err != nil {
		return nil, err
	}
	return &model.RenderResultResponse{
		ID:            record.ID,
		BPM:           record.BPM,
		Duration:      record.Duration,
		Key:           record.Key,
		CreatedAt:     record.CreatedAt,
		StemMode:      record.StemMode,
		Stems:         stems,
		Guide:         guide,
		LyricTimeline: record.LyricTimeline,
	}, nil
}

// buildGuide signs the note strip; nil stays nil.
func (s *RenderService) buildGuide(ctx context.Context, rec *model.GuideRecord) (*model.GuideResult, error) {
	if rec == nil {
		return nil, nil
	}
	out := &model.GuideResult{PitchShiftSemitones: rec.PitchShiftSemitones}
	if rec.NotesKey != nil {
		notesURL, _, err := client.ResolveURL(ctx, s.r2Client, s.r2Cfg, *rec.NotesKey)
		if err != nil {
			return nil, fmt.Errorf("could not generate the guide notes URL: %w", err)
		}
		out.NotesURL = &notesURL
	}
	return out, nil
}

// StartRender queues a new render job
func (s *RenderService) StartRender(ctx context.Context, userID string, req *model.RenderStartRequest) (*model.RenderStartResponse, error) {
	// The stem settings are validated BEFORE the job record: an inconsistent request creates no job.
	stemMode, focus, err := req.StemSettings()
	if err != nil {
		return nil, err
	}

	// The stems tier is a product right of the Pro plan, not a charge: the
	// gate holds in shadow mode too (P10) and runs before anything is spent.
	if stemMode == model.StemModeStems && s.credits != nil {
		pro, err := s.credits.HasPro(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("could not check the plan: %w", err)
		}
		if !pro {
			return nil, ErrPlanRequired
		}
	}
	// Lyrics that cannot be sung are refused before any job or debit.
	if req.Vocal != nil {
		req.Vocal.Title = strings.TrimSpace(req.Vocal.Title)
		if err := req.Vocal.CheckLyrics(); err != nil {
			return nil, err
		}
	}
	// The note strip and key shift are a Plus right, decided here and never
	// a reason to refuse the render. A plan read failure means no analysis.
	guideAnalysis := false
	if req.Vocal != nil && s.credits != nil {
		plus, err := s.credits.HasPlan(ctx, userID, credit.TierPlus)
		if err != nil {
			log.Printf("User %s: could not check the plan, no note analysis: %v", userID, err)
		} else {
			guideAnalysis = plus
		}
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

		ProjectID:   req.ProjectID,
		ProjectName: req.ProjectName,
	}

	// Create payload
	payload := &model.RenderJobPayload{
		OwnerID:         userID,
		ProjectID:       req.ProjectID,
		Brief:           req.Brief,
		Arrangement:     req.Arrangement,
		StemMode:        stemMode,
		FocusInstrument: focus,
		Vocal:           req.Vocal,
		GuideAnalysis:   guideAnalysis,
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
		balance, err = s.credits.Spend(ctx, userID, op, jobID, req.ProjectID)
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

	// Save the job
	if err := s.repo.Create(ctx, job); err != nil {
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
	job, err := s.repo.Get(ctx, jobID)
	if err != nil {
		return "", err
	}
	return job.Status, nil
}

// GetStatus returns the current status of a render job
func (s *RenderService) GetStatus(ctx context.Context, userID, jobID string) (*model.RenderStatusResponse, error) {
	job, err := s.repo.GetOwned(ctx, userID, jobID)
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
	job, err := s.repo.GetOwned(ctx, userID, jobID)
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
	err := s.repo.Update(ctx, jobID, func(job *model.Job) error {
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
	err := s.repo.Update(ctx, jobID, func(job *model.Job) error {
		if job.Status == model.JobStatusCanceled {
			return jobs.ErrSkipWrite
		}
		job.Progress = progress
		job.CurrentStep = step
		if job.Status == model.JobStatusQueued {
			markRunning(job)
		}
		return nil
	})
	if errors.Is(err, jobs.ErrSkipWrite) {
		return nil
	}
	return err
}

// ClaimRun is the worker's last check before the paid Suno call. It returns
// false (with the status it saw) if the job was canceled or is already finished; otherwise it marks the job running, which
// makes a later cancel a running-cancel (no refund). Both outcomes are decided
// in one transaction against CancelRender.
func (s *RenderService) ClaimRun(ctx context.Context, jobID string) (claimed bool, status model.JobStatus, err error) {
	err = s.repo.Update(ctx, jobID, func(job *model.Job) error {
		// Decide from scratch on every run, so a stale true from an earlier
		// attempt cannot survive.
		claimed, status = false, job.Status
		switch job.Status {
		case model.JobStatusCanceled, model.JobStatusFailed, model.JobStatusSucceeded:
			return jobs.ErrSkipWrite
		case model.JobStatusQueued:
			markRunning(job)
		}
		claimed = true
		return nil
	})
	if err != nil && !errors.Is(err, jobs.ErrSkipWrite) {
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
	return s.repo.Update(ctx, jobID, func(job *model.Job) error {
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
	return s.repo.Update(ctx, jobID, func(job *model.Job) error {
		job.Status = model.JobStatusFailed
		job.Error = &errMsg
		job.ErrorCode = "RENDER_FAILED"
		now := time.Now()
		job.CompletedAt = &now
		return nil
	})
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
