package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
	"github.com/redis/go-redis/v9"
)

// MasterWorker processes mastering jobs
type MasterWorker struct {
	redis           *redis.Client
	audioClient     client.AudioProcessor
	r2Client        client.StorageClient
	masterService   *service.MasterService
	hub             *websocket.Hub
	r2Cfg           *config.R2Config
	deletionChecker deletionmark.Checker // nil: no check
	credits         credit.Spender       // nil: no credit accounting
	isLastAttempt   func(context.Context) bool
}

// NewMasterWorker creates a new master worker
func NewMasterWorker(redisClient *redis.Client, audioClient client.AudioProcessor, r2Client client.StorageClient, masterService *service.MasterService, hub *websocket.Hub, r2Cfg *config.R2Config, deletionChecker deletionmark.Checker) *MasterWorker {
	return &MasterWorker{
		redis:           redisClient,
		audioClient:     audioClient,
		r2Client:        r2Client,
		masterService:   masterService,
		hub:             hub,
		r2Cfg:           r2Cfg,
		deletionChecker: deletionChecker,
		isLastAttempt:   asynqIsLastAttempt,
	}
}

// WithCredits turns on credit accounting: a master job that ends failed is
// refunded once. A nil spender keeps the worker free of credits.
func (w *MasterWorker) WithCredits(sp credit.Spender) *MasterWorker {
	w.credits = sp
	return w
}

// ProcessTask handles mastering task processing
func (w *MasterWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var taskPayload struct {
		JobID   string          `json:"jobId"`
		Payload json.RawMessage `json:"payload"`
	}

	if err := json.Unmarshal(t.Payload(), &taskPayload); err != nil {
		return fmt.Errorf("failed to unmarshal task payload: %w", err)
	}

	jobID := taskPayload.JobID
	log.Printf("Starting master job: %s", jobID)

	var payload model.MasterJobPayload
	if err := json.Unmarshal(taskPayload.Payload, &payload); err != nil {
		err = fmt.Errorf("failed to unmarshal master payload: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, "Invalid payload", err)
		return err
	}

	// Check if audio client is configured
	if w.audioClient == nil {
		return w.processWithMock(ctx, jobID, &payload)
	}

	return w.processWithAudioService(ctx, jobID, &payload)
}

// processWithAudioService handles real mastering using the Python microservice
func (w *MasterWorker) processWithAudioService(ctx context.Context, jobID string, payload *model.MasterJobPayload) error {
	// Step 1: Update status
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 5, "Preparing stems...")

	// Step 2: Build mix settings from payload
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 10, "Building mix settings...")
	mixSettings := w.buildMixSettings(payload)

	// Step 3: Build vocal takes if present
	vocalTakes, err := w.buildVocalTakes(payload)
	if err != nil {
		log.Printf("Job %s: could not resolve the vocal URL: %v", jobID, err)
		err = fmt.Errorf("could not resolve the vocal URL: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, "Could not resolve the vocal URL", err)
		return err
	}

	// Step 3b: Convert stem URLs to keys. Every URL coming from the client
	// is converted to a key with KeyFromURL.
	stemKeys := make([]string, 0, len(payload.StemURLs))
	for _, u := range payload.StemURLs {
		k, err := client.ResolveOwnedKey(u, payload.OwnerID, w.r2Cfg)
		if err != nil {
			log.Printf("Job %s: could not resolve the stem URL: %v", jobID, err)
			err = fmt.Errorf("could not resolve the stem URL: %w: %w", err, asynq.SkipRetry)
			w.failFinal(ctx, jobID, "Could not resolve the stem URL", err)
			return err
		}
		stemKeys = append(stemKeys, k)
	}

	// Step 4: Call audio service for mastering
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 20, "Starting mastering process...")

	outputKey, err := client.UserKey("masters", payload.OwnerID, payload.ProjectID, uuid.New().String()+".wav")
	if err != nil {
		err = fmt.Errorf("master key: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, "Invalid job owner or project", err)
		return err
	}

	// The audio service writes the output to R2 itself; the Master call is
	// this job's R2 upload. No file is written for a deleted account. Master
	// is not a paid step: a check error can be retried.
	if err := ownerCanWrite(ctx, w.deletionChecker, payload.OwnerID); err != nil {
		log.Printf("Job %s: owner check failed: %v", jobID, err)
		if errors.Is(err, errOwnerDeleted) {
			err = fmt.Errorf("master was not written: %w: %w", err, asynq.SkipRetry)
			w.failFinal(ctx, jobID, errOwnerDeleted.Error(), err)
			return err
		}
		err = fmt.Errorf("master was not written: %w", err)
		w.failFinal(ctx, jobID, "Could not check the account status", err)
		return err
	}

	masterReq := &client.MasterRequest{
		StemKeys:    stemKeys,
		MixSettings: mixSettings,
		Profile:     string(payload.Profile),
		VocalTakes:  vocalTakes,
		OutputKey:   outputKey,
	}

	// Step 5: Wait for mastering to complete (with progress updates)
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 40, "Applying EQ and compression...")

	masterResp, err := w.audioClient.Master(ctx, masterReq)
	if err != nil {
		log.Printf("Job %s: mastering failed: %v", jobID, err)
		w.failFinal(ctx, jobID, "Mastering failed", err)
		return err
	}

	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 80, "Applying limiter...")
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 95, "Finalizing...")

	// Step 6: Generate result. The record stores the KEY, not a signed URL:
	// the job record lives 24 hours, the signature 1 hour. The URL is
	// generated fresh at read time (GetResult) and here for publishing.
	record := &model.MasterResultRecord{
		FileKey:  outputKey,
		Duration: masterResp.Duration,
		Profile:  payload.Profile,
		PeakDb:   masterResp.PeakDb,
		LUFS:     int(masterResp.LUFS),
	}

	fileURL, expiresAt, err := client.ResolveURL(ctx, w.r2Client, w.r2Cfg, outputKey)
	if err != nil {
		log.Printf("Job %s: could not generate the master URL: %v", jobID, err)
		err = fmt.Errorf("could not generate the master URL: %w", err)
		w.failFinal(ctx, jobID, "Could not generate the master URL", err)
		return err
	}

	// Complete the job
	w.completeJob(ctx, jobID, record)
	w.hub.BroadcastComplete(jobID, record.Response(fileURL, expiresAt))

	log.Printf("Master job %s completed", jobID)
	return nil
}

// processWithMock handles mastering with mock data for development
func (w *MasterWorker) processWithMock(ctx context.Context, jobID string, payload *model.MasterJobPayload) error {
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 0, "Starting mastering...")

	steps := []struct {
		progress int
		step     string
		duration time.Duration
	}{
		{10, "Loading stems...", 2 * time.Second},
		{25, "Applying EQ...", 3 * time.Second},
		{40, "Applying compression...", 3 * time.Second},
		{55, "Stereo widening...", 2 * time.Second},
		{70, "Applying limiter...", 2 * time.Second},
		{85, "Final adjustments...", 2 * time.Second},
		{95, "Rendering output...", 3 * time.Second},
	}

	for _, step := range steps {
		select {
		case <-ctx.Done():
			log.Printf("Master job %s cancelled", jobID)
			err := ctx.Err()
			w.failFinal(ctx, jobID, "Mastering failed", err)
			return err
		default:
		}

		w.updateJobStatus(ctx, jobID, model.JobStatusRunning, step.progress, step.step)
		w.hub.BroadcastProgress(jobID, step.progress, model.JobStatusRunning, step.step)
		time.Sleep(step.duration)
	}

	record, err := w.generateMockRecord(payload)
	if err != nil {
		err = fmt.Errorf("mock record: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, "Invalid job owner or project", err)
		return err
	}
	fileURL, expiresAt, err := client.ResolveURL(ctx, w.r2Client, w.r2Cfg, record.FileKey)
	if err != nil {
		log.Printf("Job %s: could not generate the master URL: %v", jobID, err)
		err = fmt.Errorf("could not generate the master URL: %w", err)
		w.failFinal(ctx, jobID, "Could not generate the master URL", err)
		return err
	}

	w.completeJob(ctx, jobID, record)
	w.hub.BroadcastComplete(jobID, record.Response(fileURL, expiresAt))

	log.Printf("Master job %s completed (mock)", jobID)
	return nil
}

func (w *MasterWorker) buildMixSettings(payload *model.MasterJobPayload) []client.MixChannel {
	var settings []client.MixChannel

	// If no channels in mix snapshot, use default settings
	if len(payload.MixSnapshot.Channels) == 0 {
		for range payload.StemURLs {
			settings = append(settings, client.MixChannel{
				Volume: 1.0,
				Pan:    0.0,
			})
		}
		return settings
	}

	// Use mix snapshot settings
	// Convert VolumeDb (dB) to linear volume
	for i, channel := range payload.MixSnapshot.Channels {
		if i >= len(payload.StemURLs) {
			break
		}
		// Convert dB to linear: volume = 10^(dB/20)
		volume := dbToLinear(channel.VolumeDb)
		settings = append(settings, client.MixChannel{
			Volume: volume,
			Pan:    0.0, // Pan is not in the model, default to center
			Mute:   channel.Mute,
			Solo:   channel.Solo,
		})
	}

	return settings
}

// dbToLinear converts decibels to linear volume (0-1 range)
func dbToLinear(db float64) float64 {
	if db <= -60 {
		return 0.0
	}
	return math.Pow(10, db/20)
}

func (w *MasterWorker) buildVocalTakes(payload *model.MasterJobPayload) ([]client.VocalTakeInput, error) {
	var takes []client.VocalTakeInput

	for _, take := range payload.VocalTakes {
		k, err := client.ResolveOwnedKey(take.FileURL, payload.OwnerID, w.r2Cfg)
		if err != nil {
			return nil, fmt.Errorf("could not resolve the vocal URL: %w", err)
		}
		takes = append(takes, client.VocalTakeInput{
			Key:     k,
			Volume:  1.0, // Default volume, could be configurable
			StartMs: take.StartMs,
			EndMs:   take.EndMs,
		})
	}

	return takes, nil
}

func (w *MasterWorker) updateJobStatus(ctx context.Context, jobID string, status model.JobStatus, progress int, step string) {
	job, err := w.getJob(ctx, jobID)
	if err != nil {
		log.Printf("Failed to get job: %v", err)
		return
	}

	job.Status = status
	job.Progress = progress
	job.CurrentStep = step

	if status == model.JobStatusRunning && job.StartedAt == nil {
		now := time.Now()
		job.StartedAt = &now
	}

	w.saveJob(ctx, job)
	w.hub.BroadcastProgress(jobID, progress, status, step)
}

func (w *MasterWorker) completeJob(ctx context.Context, jobID string, result *model.MasterResultRecord) {
	job, err := w.getJob(ctx, jobID)
	if err != nil {
		log.Printf("Failed to get job: %v", err)
		return
	}

	resultBytes, _ := json.Marshal(result)
	job.Status = model.JobStatusSucceeded
	job.Progress = 100
	job.Result = resultBytes
	now := time.Now()
	job.CompletedAt = &now

	w.saveJob(ctx, job)
}

// failFinal ends a failed attempt. Only the job's FINAL failure (SkipRetry,
// or asynq's last attempt) writes the failed status and refunds; an
// intermediate failure leaves the job running, so a retry that succeeds is
// not a free master and a visible "failed" always comes with its refund.
func (w *MasterWorker) failFinal(ctx context.Context, jobID, errMsg string, err error) {
	last := asynqIsLastAttempt
	if w.isLastAttempt != nil {
		last = w.isLastAttempt
	}
	if !errors.Is(err, asynq.SkipRetry) && !last(ctx) {
		log.Printf("Job %s: attempt failed, will retry: %v", jobID, err)
		return
	}
	// Detached: the final failure must be written even when ctx is the
	// reason for it (shutdown, deadline).
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	w.failJob(fctx, jobID, errMsg)
	w.refund(fctx, jobID)
}

// refund reverses the master debit; a failure is logged, never returned:
// returning it would retry the whole master for a bookkeeping error.
func (w *MasterWorker) refund(ctx context.Context, jobID string) {
	if w.credits == nil {
		return
	}
	if err := w.credits.Refund(ctx, credit.OpMaster, jobID); err != nil {
		log.Printf("Job %s: could not refund %s: %v", jobID, credit.OpMaster, err)
	}
}

func (w *MasterWorker) failJob(ctx context.Context, jobID, errMsg string) {
	job, err := w.getJob(ctx, jobID)
	if err != nil {
		log.Printf("Failed to get job: %v", err)
		return
	}

	job.Status = model.JobStatusFailed
	job.Error = &errMsg
	now := time.Now()
	job.CompletedAt = &now

	w.saveJob(ctx, job)
	w.hub.BroadcastError(jobID, "MASTER_FAILED", errMsg)
}

func (w *MasterWorker) getJob(ctx context.Context, jobID string) (*model.Job, error) {
	data, err := w.redis.Get(ctx, fmt.Sprintf("job:%s", jobID)).Bytes()
	if err != nil {
		return nil, err
	}

	var job model.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}

	return &job, nil
}

func (w *MasterWorker) saveJob(ctx context.Context, job *model.Job) {
	data, err := json.Marshal(job)
	if err != nil {
		log.Printf("Failed to marshal job: %v", err)
		return
	}
	w.redis.Set(ctx, fmt.Sprintf("job:%s", job.ID), data, 24*time.Hour)
}

// generateMockRecord builds the record stored on the mock/dev path. Like
// the real path it holds the KEY; since the URL is built with ResolveURL,
// the mock output can also be resolved with KeyFromURL (the old fixed
// "cdn.makeasinger.com" URL could not be resolved).
func (w *MasterWorker) generateMockRecord(payload *model.MasterJobPayload) (*model.MasterResultRecord, error) {
	key, err := client.UserKey("masters", payload.OwnerID, payload.ProjectID, uuid.New().String()+".wav")
	if err != nil {
		return nil, err
	}
	return &model.MasterResultRecord{
		FileKey:  key,
		Duration: 180.5,
		Profile:  payload.Profile,
		PeakDb:   -0.3,
		LUFS:     -14,
	}, nil
}
