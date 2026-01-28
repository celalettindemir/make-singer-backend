package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/websocket"
	"github.com/redis/go-redis/v9"
)

// MasterWorker processes mastering jobs
type MasterWorker struct {
	redis *redis.Client
	hub   *websocket.Hub
}

// NewMasterWorker creates a new master worker
func NewMasterWorker(redisClient *redis.Client, hub *websocket.Hub) *MasterWorker {
	return &MasterWorker{
		redis: redisClient,
		hub:   hub,
	}
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
		w.failJob(ctx, jobID, "Invalid payload")
		return fmt.Errorf("failed to unmarshal master payload: %w", err)
	}

	// Update job status to running
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 0, "Starting mastering...")

	// Simulate mastering process with progress updates
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
		// Check for cancellation
		select {
		case <-ctx.Done():
			log.Printf("Master job %s cancelled", jobID)
			return ctx.Err()
		default:
		}

		// Update progress
		w.updateJobStatus(ctx, jobID, model.JobStatusRunning, step.progress, step.step)

		// Broadcast progress via WebSocket
		w.hub.BroadcastProgress(jobID, step.progress, model.JobStatusRunning, step.step)

		// Simulate work
		time.Sleep(step.duration)
	}

	// Generate mock result
	result := w.generateMockResult(&payload)

	// Complete the job
	w.completeJob(ctx, jobID, result)

	// Broadcast completion
	w.hub.BroadcastComplete(jobID, result)

	log.Printf("Master job %s completed", jobID)
	return nil
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
}

func (w *MasterWorker) completeJob(ctx context.Context, jobID string, result *model.MasterResultResponse) {
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

func (w *MasterWorker) generateMockResult(payload *model.MasterJobPayload) *model.MasterResultResponse {
	return &model.MasterResultResponse{
		FileURL:   fmt.Sprintf("https://cdn.makeasinger.com/masters/%s.wav", uuid.New().String()),
		Duration:  180.5, // Mock duration
		Profile:   payload.Profile,
		PeakDb:    -0.3,
		LUFS:      -14,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
}
