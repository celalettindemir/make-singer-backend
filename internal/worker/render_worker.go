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
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
)

// RenderWorker processes render jobs
type RenderWorker struct {
	renderService *service.RenderService
	hub           *websocket.Hub
}

// NewRenderWorker creates a new render worker
func NewRenderWorker(renderService *service.RenderService, hub *websocket.Hub) *RenderWorker {
	return &RenderWorker{
		renderService: renderService,
		hub:           hub,
	}
}

// ProcessTask handles render task processing
func (w *RenderWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var taskPayload struct {
		JobID   string          `json:"jobId"`
		Payload json.RawMessage `json:"payload"`
	}

	if err := json.Unmarshal(t.Payload(), &taskPayload); err != nil {
		return fmt.Errorf("failed to unmarshal task payload: %w", err)
	}

	jobID := taskPayload.JobID
	log.Printf("Starting render job: %s", jobID)

	var payload model.RenderJobPayload
	if err := json.Unmarshal(taskPayload.Payload, &payload); err != nil {
		w.failJob(ctx, jobID, "Invalid payload")
		return fmt.Errorf("failed to unmarshal render payload: %w", err)
	}

	// Simulate render process with progress updates
	steps := []struct {
		progress int
		step     string
		duration time.Duration
	}{
		{10, "Analyzing song structure...", 2 * time.Second},
		{20, "Generating drums track...", 3 * time.Second},
		{35, "Generating bass track...", 3 * time.Second},
		{50, "Generating piano track...", 3 * time.Second},
		{65, "Generating guitar track...", 3 * time.Second},
		{80, "Generating synth track...", 3 * time.Second},
		{90, "Mixing stems...", 2 * time.Second},
		{95, "Finalizing...", 1 * time.Second},
	}

	for _, step := range steps {
		// Check for cancellation
		select {
		case <-ctx.Done():
			log.Printf("Render job %s cancelled", jobID)
			return ctx.Err()
		default:
		}

		// Update progress
		if err := w.renderService.UpdateJobProgress(ctx, jobID, step.progress, step.step); err != nil {
			log.Printf("Failed to update progress: %v", err)
		}

		// Broadcast progress via WebSocket
		w.hub.BroadcastProgress(jobID, step.progress, model.JobStatusRunning, step.step)

		// Simulate work
		time.Sleep(step.duration)
	}

	// Generate mock result
	result := w.generateMockResult(&payload)

	// Complete the job
	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		w.failJob(ctx, jobID, "Failed to save result")
		return err
	}

	// Broadcast completion
	w.hub.BroadcastComplete(jobID, result)

	log.Printf("Render job %s completed", jobID)
	return nil
}

func (w *RenderWorker) failJob(ctx context.Context, jobID, errMsg string) {
	if err := w.renderService.FailJob(ctx, jobID, errMsg); err != nil {
		log.Printf("Failed to mark job as failed: %v", err)
	}
	w.hub.BroadcastError(jobID, "RENDER_FAILED", errMsg)
}

func (w *RenderWorker) generateMockResult(payload *model.RenderJobPayload) *model.RenderResultResponse {
	// Calculate total duration based on structure
	var totalBars int
	for _, section := range payload.Brief.Structure {
		totalBars += section.Bars
	}

	// Assume 4 beats per bar at given BPM
	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}
	duration := float64(totalBars*4) / float64(bpm) * 60

	// Determine key
	tonic := model.TonicC
	scale := model.ScaleMajor
	if payload.Brief.Key.Tonic != nil {
		tonic = *payload.Brief.Key.Tonic
	}
	if payload.Brief.Key.Scale != nil {
		scale = *payload.Brief.Key.Scale
	}

	// Generate stems
	var stems []model.StemResult
	for _, instrument := range payload.Arrangement.Instruments {
		stemID := uuid.New().String()
		stems = append(stems, model.StemResult{
			ID:           stemID,
			Instrument:   instrument,
			FileURL:      fmt.Sprintf("https://cdn.makeasinger.com/stems/%s/%s.wav", payload.ProjectID, instrument),
			Duration:     duration,
			WaveformData: generateWaveform(100),
		})
	}

	return &model.RenderResultResponse{
		ID:        uuid.New().String(),
		BPM:       bpm,
		Duration:  duration,
		Key:       model.KeyResult{Tonic: tonic, Scale: scale},
		CreatedAt: time.Now(),
		Stems:     stems,
	}
}

func generateWaveform(points int) []float64 {
	waveform := make([]float64, points)
	for i := range waveform {
		// Simple pseudo-random waveform
		waveform[i] = 0.1 + float64(i%10)/15.0
	}
	return waveform
}
