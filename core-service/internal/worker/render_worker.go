package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
)

// RenderWorker processes render jobs
type RenderWorker struct {
	renderService *service.RenderService
	sunoClient    *client.SunoClient
	r2Client      client.StorageClient
	hub           *websocket.Hub
}

// NewRenderWorker creates a new render worker
func NewRenderWorker(renderService *service.RenderService, sunoClient *client.SunoClient, r2Client client.StorageClient, hub *websocket.Hub) *RenderWorker {
	return &RenderWorker{
		renderService: renderService,
		sunoClient:    sunoClient,
		r2Client:      r2Client,
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

	// Check if Suno client is configured
	if w.sunoClient == nil || !w.sunoClient.IsConfigured() {
		return w.processWithMock(ctx, jobID, &payload)
	}

	return w.processWithSuno(ctx, jobID, &payload)
}

// processWithSuno handles real rendering using Suno API
func (w *RenderWorker) processWithSuno(ctx context.Context, jobID string, payload *model.RenderJobPayload) error {
	// Step 1: Build prompt from brief
	w.updateProgress(ctx, jobID, 5, "Building music prompt...")
	prompt := w.buildMusicPrompt(payload)

	// Step 2: Generate music via Suno
	w.updateProgress(ctx, jobID, 10, "Generating music...")
	musicReq := &client.GenerateMusicRequest{
		Prompt:           prompt,
		Style:            string(payload.Brief.Genre),
		MakeInstrumental: true,
	}

	musicResp, err := w.sunoClient.GenerateMusic(ctx, musicReq)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Music generation failed: %v", err))
		return err
	}

	// Step 3: Poll for music completion
	w.updateProgress(ctx, jobID, 30, "Waiting for music generation...")
	musicResult, err := w.sunoClient.PollMusicStatus(ctx, musicResp.TaskID, 5*time.Second, 10*time.Minute)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Music generation timed out: %v", err))
		return err
	}

	// Step 4: Split stems
	w.updateProgress(ctx, jobID, 60, "Splitting stems...")
	stemResp, err := w.sunoClient.SplitStems(ctx, musicResult.AudioURL)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Stem splitting failed: %v", err))
		return err
	}

	// Step 5: Poll for stem split completion
	w.updateProgress(ctx, jobID, 75, "Waiting for stem separation...")
	stemResult, err := w.sunoClient.PollStemSplitStatus(ctx, stemResp.TaskID, 5*time.Second, 5*time.Minute)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Stem splitting timed out: %v", err))
		return err
	}

	// Step 6: Upload stems to R2
	w.updateProgress(ctx, jobID, 90, "Uploading stems...")
	stems, err := w.uploadStems(ctx, payload.ProjectID, stemResult.Stems)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Stem upload failed: %v", err))
		return err
	}

	// Step 7: Generate result
	w.updateProgress(ctx, jobID, 95, "Finalizing...")
	result := w.generateResult(payload, musicResult, stems)

	// Complete the job
	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		w.failJob(ctx, jobID, "Failed to save result")
		return err
	}

	w.hub.BroadcastComplete(jobID, result)
	log.Printf("Render job %s completed", jobID)
	return nil
}

// processWithMock handles rendering with mock data for development
func (w *RenderWorker) processWithMock(ctx context.Context, jobID string, payload *model.RenderJobPayload) error {
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
		select {
		case <-ctx.Done():
			log.Printf("Render job %s cancelled", jobID)
			return ctx.Err()
		default:
		}

		w.updateProgress(ctx, jobID, step.progress, step.step)
		time.Sleep(step.duration)
	}

	result := w.generateMockResult(payload)

	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		w.failJob(ctx, jobID, "Failed to save result")
		return err
	}

	w.hub.BroadcastComplete(jobID, result)
	log.Printf("Render job %s completed (mock)", jobID)
	return nil
}

func (w *RenderWorker) buildMusicPrompt(payload *model.RenderJobPayload) string {
	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}

	key := "C major"
	if payload.Brief.Key.Tonic != nil && payload.Brief.Key.Scale != nil {
		key = fmt.Sprintf("%s %s", *payload.Brief.Key.Tonic, *payload.Brief.Key.Scale)
	}

	return fmt.Sprintf("Create a %s instrumental track at %d BPM in %s. Style: %s. Vibes: %v",
		payload.Brief.Genre,
		bpm,
		key,
		payload.Brief.Genre,
		payload.Brief.Vibes,
	)
}

func (w *RenderWorker) uploadStems(ctx context.Context, projectID string, stems []client.Stem) ([]model.StemResult, error) {
	var results []model.StemResult

	for _, stem := range stems {
		stemID := uuid.New().String()

		// If R2 client is available, we could download from Suno and re-upload to R2
		// For now, we'll use the Suno URLs directly
		fileURL := stem.URL
		if w.r2Client != nil {
			// In a real implementation, download from stem.URL and upload to R2
			key := fmt.Sprintf("stems/%s/%s.wav", projectID, stemID)
			fileURL = w.r2Client.GetPublicURL(key)
		}

		results = append(results, model.StemResult{
			ID:           stemID,
			Instrument:   model.Instrument(stem.Name),
			FileURL:      fileURL,
			Duration:     stem.Duration,
			WaveformData: generateWaveform(100),
		})
	}

	return results, nil
}

func (w *RenderWorker) generateResult(payload *model.RenderJobPayload, musicResult *client.MusicResult, stems []model.StemResult) *model.RenderResultResponse {
	tonic := model.TonicC
	scale := model.ScaleMajor
	if payload.Brief.Key.Tonic != nil {
		tonic = *payload.Brief.Key.Tonic
	}
	if payload.Brief.Key.Scale != nil {
		scale = *payload.Brief.Key.Scale
	}

	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}

	return &model.RenderResultResponse{
		ID:        uuid.New().String(),
		BPM:       bpm,
		Duration:  musicResult.Duration,
		Key:       model.KeyResult{Tonic: tonic, Scale: scale},
		CreatedAt: time.Now(),
		Stems:     stems,
	}
}

func (w *RenderWorker) updateProgress(ctx context.Context, jobID string, progress int, step string) {
	if err := w.renderService.UpdateJobProgress(ctx, jobID, progress, step); err != nil {
		log.Printf("Failed to update progress: %v", err)
	}
	w.hub.BroadcastProgress(jobID, progress, model.JobStatusRunning, step)
}

func (w *RenderWorker) failJob(ctx context.Context, jobID, errMsg string) {
	if err := w.renderService.FailJob(ctx, jobID, errMsg); err != nil {
		log.Printf("Failed to mark job as failed: %v", err)
	}
	w.hub.BroadcastError(jobID, "RENDER_FAILED", errMsg)
}

func (w *RenderWorker) generateMockResult(payload *model.RenderJobPayload) *model.RenderResultResponse {
	var totalBars int
	for _, section := range payload.Brief.Structure {
		totalBars += section.Bars
	}

	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}
	duration := float64(totalBars*4) / float64(bpm) * 60

	tonic := model.TonicC
	scale := model.ScaleMajor
	if payload.Brief.Key.Tonic != nil {
		tonic = *payload.Brief.Key.Tonic
	}
	if payload.Brief.Key.Scale != nil {
		scale = *payload.Brief.Key.Scale
	}

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
		waveform[i] = 0.1 + float64(i%10)/15.0
	}
	return waveform
}
