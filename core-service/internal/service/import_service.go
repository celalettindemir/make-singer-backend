package service

import (
	"bytes"
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
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

const (
	// TaskTypeImport is the asynq task that processes an import job.
	TaskTypeImport = "ice-aktar:isle"
	// TaskTypeImportSourceCleanup deletes an import source that was kept for a
	// retry; the maintenance queue runs it.
	TaskTypeImportSourceCleanup = "ice-aktar:kaynak-sil"
	// ImportQueue is the asynq queue of import jobs; only the import server
	// reads it, so waiting jobs show as queued.
	ImportQueue = "ice-aktar"

	importCleanupQueue  = "bakim"
	importTaskTimeout   = 25 * time.Minute
	importTaskRetention = 24 * time.Hour
	importActiveWindow  = 24 * time.Hour
	// importDurationSlack is the tolerance around the accepted duration.
	importDurationSlack = 500
)

var (
	ErrImportDisabled     = errors.New("import is not enabled")
	ErrImportUnreadable   = errors.New("import audio could not be read")
	ErrImportSourceGone   = errors.New("import source is gone")
	ErrImportNotRetryable = errors.New("import job is not retryable")
)

// ImportDurationError is returned when the measured length is out of range.
type ImportDurationError struct{ DurationMs int }

func (e *ImportDurationError) Error() string {
	return fmt.Sprintf("import duration %d ms is out of range", e.DurationMs)
}

// ImportTaskPayload is the payload of a TaskTypeImport task.
type ImportTaskPayload struct {
	JobID string `json:"jobId"`
}

// ImportCleanupPayload is the payload of a TaskTypeImportSourceCleanup task.
type ImportCleanupPayload struct {
	OwnerID   string `json:"ownerId"`
	SourceKey string `json:"sourceKey"`
}

// TaskEnqueuer is the part of *asynq.Client the services use.
type TaskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// ImportUpload is one uploaded track.
type ImportUpload struct {
	ProjectID, ProjectName, ContentType string
	Data                                []byte
}

// ImportDeps are the collaborators of ImportService.
type ImportDeps struct {
	Cfg     config.ImportConfig
	Repo    jobs.Repo
	Storage client.StorageClient
	Audio   client.ImportAudio
	Queue   TaskEnqueuer
	Credits credit.Spender
}

// ImportService starts import jobs; the import worker does the processing.
type ImportService struct{ d ImportDeps }

func NewImportService(d ImportDeps) *ImportService { return &ImportService{d: d} }

// Start stores the file, measures it, charges, creates the job and queues it.
// A refused file spends nothing and its source is deleted.
func (s *ImportService) Start(ctx context.Context, userID string, in ImportUpload) (*model.ImportStartResponse, error) {
	d := s.d
	if !d.Cfg.Enabled || d.Storage == nil || d.Audio == nil || !d.Audio.IsConfigured() {
		return nil, ErrImportDisabled
	}
	if _, err := client.UserKey("imports", userID, in.ProjectID); err != nil {
		return nil, err
	}
	if err := s.checkNoActive(ctx, userID); err != nil {
		return nil, err
	}

	jobID := uuid.NewString()
	ext := humExtByContentType[in.ContentType]
	if ext == "" {
		ext = "wav"
	}
	srcKey, err := client.UserKey("imports", userID, jobID, "source."+ext)
	if err != nil {
		return nil, err
	}
	if _, _, err := d.Storage.Upload(ctx, srcKey, bytes.NewReader(in.Data), in.ContentType); err != nil {
		return nil, fmt.Errorf("could not upload the import source: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			s.deleteSource(srcKey)
		}
	}()

	prep, err := d.Audio.PrepareImport(ctx, &client.ImportPrepareRequest{InputKey: srcKey})
	if err != nil {
		if errors.Is(err, client.ErrAudioUnprocessable) {
			return nil, ErrImportUnreadable
		}
		return nil, fmt.Errorf("could not measure the import: %w", err)
	}
	if prep.DurationMs < d.Cfg.MinMs-importDurationSlack || prep.DurationMs > d.Cfg.MaxMs+importDurationSlack {
		return nil, &ImportDurationError{DurationMs: prep.DurationMs}
	}

	if err := s.chargeAndQueue(ctx, userID, jobID, in.ProjectID, in.ProjectName, model.ImportPayload{
		SourceKey: srcKey, DurationMs: prep.DurationMs, ContentType: in.ContentType,
	}); err != nil {
		return nil, err
	}
	keep = true
	log.Printf("import start: user=%s job=%s project=%s type=%s durationMs=%d", userID, jobID, in.ProjectID, in.ContentType, prep.DurationMs)
	return &model.ImportStartResponse{JobID: jobID, DurationMs: prep.DurationMs}, nil
}

// chargeAndQueue charges the import, creates the queued job and enqueues its
// task. Any failure after the charge refunds it and leaves no active job.
// Start and Retry share it.
func (s *ImportService) chargeAndQueue(ctx context.Context, userID, jobID, projectID, projectName string, p model.ImportPayload) error {
	d := s.d
	if _, err := d.Credits.Spend(ctx, userID, credit.OpImport, jobID, projectID); err != nil {
		return err
	}
	refund := func() {
		rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.Credits.Refund(rctx, credit.OpImport, jobID); err != nil {
			log.Printf("import: refund job=%s: %v", jobID, err)
		}
	}

	payload, err := json.Marshal(p)
	if err != nil {
		refund()
		return err
	}
	job := &model.Job{
		ID: jobID, Type: model.JobTypeImport, OwnerID: userID, Status: model.JobStatusQueued,
		ProjectID: projectID, ProjectName: projectName, Payload: payload,
		CreatedAt: time.Now().UTC(),
	}
	if err := d.Repo.Create(ctx, job); err != nil {
		refund()
		return err
	}
	if err := EnqueueImport(d.Queue, jobID); err != nil {
		refund()
		// The queued row would block the user's single active import slot.
		_ = d.Repo.Update(context.Background(), jobID, func(j *model.Job) error {
			j.Status = model.JobStatusCanceled
			return nil
		})
		return fmt.Errorf("failed to enqueue import job: %w", err)
	}
	return nil
}

// Retry runs a failed or canceled import again as a new job from the same
// stored source and charges it again. A source that is already cleaned up
// is ErrImportSourceGone with nothing spent and no job created.
func (s *ImportService) Retry(ctx context.Context, userID, jobID string) (*model.ImportStartResponse, error) {
	d := s.d
	if !d.Cfg.Enabled || d.Storage == nil || d.Audio == nil || !d.Audio.IsConfigured() {
		return nil, ErrImportDisabled
	}
	old, err := d.Repo.GetOwned(ctx, userID, jobID)
	if err != nil {
		return nil, err
	}
	if old.Type != model.JobTypeImport || (old.Status != model.JobStatusFailed && old.Status != model.JobStatusCanceled) {
		return nil, ErrImportNotRetryable
	}
	if err := s.checkNoActive(ctx, userID); err != nil {
		return nil, err
	}
	var p model.ImportPayload
	if err := json.Unmarshal(old.Payload, &p); err != nil || p.SourceKey == "" {
		return nil, ErrImportSourceGone
	}
	if checker, ok := d.Storage.(client.ObjectChecker); ok {
		exists, err := checker.Exists(ctx, p.SourceKey)
		if err != nil {
			return nil, fmt.Errorf("could not check the import source: %w", err)
		}
		if !exists {
			return nil, ErrImportSourceGone
		}
	}

	newID := uuid.NewString()
	if err := s.chargeAndQueue(ctx, userID, newID, old.ProjectID, old.ProjectName, p); err != nil {
		return nil, err
	}
	log.Printf("import retry: user=%s old=%s job=%s", userID, jobID, newID)
	return &model.ImportStartResponse{JobID: newID, DurationMs: p.DurationMs}, nil
}

// Cancel cancels a queued or running import job of the user, refunds it in
// full whatever its state was and schedules the source cleanup.
func (s *ImportService) Cancel(ctx context.Context, userID, jobID string) error {
	var sourceKey string
	err := s.d.Repo.Update(ctx, jobID, func(job *model.Job) error {
		if !job.IsOwnedBy(userID) || job.Type != model.JobTypeImport {
			return jobs.ErrJobNotFound
		}
		if job.Status != model.JobStatusQueued && job.Status != model.JobStatusRunning {
			return jobs.ErrJobAlreadyCompleted
		}
		var p model.ImportPayload
		if json.Unmarshal(job.Payload, &p) == nil {
			sourceKey = p.SourceKey
		}
		job.Status = model.JobStatusCanceled
		now := time.Now()
		job.CompletedAt = &now
		return nil
	})
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.d.Credits.Refund(rctx, credit.OpImport, jobID); err != nil {
		log.Printf("import cancel: refund job=%s: %v", jobID, err)
	}
	if sourceKey != "" {
		if err := s.ScheduleSourceCleanup(userID, sourceKey); err != nil {
			log.Printf("import cancel: schedule cleanup job=%s: %v", jobID, err)
		}
	}
	return nil
}

// ScheduleSourceCleanup queues the delayed deletion of an import source. The
// worker skips it while an active import job still uses the source.
func (s *ImportService) ScheduleSourceCleanup(ownerID, sourceKey string) error {
	payload, err := json.Marshal(ImportCleanupPayload{OwnerID: ownerID, SourceKey: sourceKey})
	if err != nil {
		return err
	}
	_, err = s.d.Queue.Enqueue(asynq.NewTask(TaskTypeImportSourceCleanup, payload),
		asynq.Queue(importCleanupQueue),
		asynq.ProcessIn(s.d.Cfg.SourceRetention),
	)
	return err
}

// checkNoActive returns a JOB_ACTIVE error when the user has a queued or
// running import job.
func (s *ImportService) checkNoActive(ctx context.Context, userID string) error {
	list, err := s.d.Repo.List(ctx, userID, time.Now().Add(-importActiveWindow), 50)
	if err != nil {
		return fmt.Errorf("could not list jobs: %w", err)
	}
	for _, j := range list {
		if j.Type == model.JobTypeImport && (j.Status == model.JobStatusQueued || j.Status == model.JobStatusRunning) {
			return jobs.ActiveError(j.ID)
		}
	}
	return nil
}

func (s *ImportService) deleteSource(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.d.Storage.Delete(ctx, key); err != nil {
		log.Printf("import start: could not delete source %s: %v", key, err)
	}
}

// EnqueueImport queues the processing task of an import job. The retry
// service reuses it.
func EnqueueImport(q TaskEnqueuer, jobID string) error {
	payload, err := json.Marshal(ImportTaskPayload{JobID: jobID})
	if err != nil {
		return err
	}
	_, err = q.Enqueue(asynq.NewTask(TaskTypeImport, payload),
		asynq.Queue(ImportQueue),
		asynq.TaskID(jobID),
		asynq.MaxRetry(1),
		asynq.Timeout(importTaskTimeout),
		asynq.Retention(importTaskRetention),
	)
	return err
}
