package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
	"github.com/redis/go-redis/v9"
)

// MasterWorker processes mastering jobs
type MasterWorker struct {
	redis         *redis.Client
	audioClient   client.AudioProcessor
	r2Client      client.StorageClient
	masterService *service.MasterService
	hub           *websocket.Hub
	r2Cfg         *config.R2Config
}

// NewMasterWorker creates a new master worker
func NewMasterWorker(redisClient *redis.Client, audioClient client.AudioProcessor, r2Client client.StorageClient, masterService *service.MasterService, hub *websocket.Hub, r2Cfg *config.R2Config) *MasterWorker {
	return &MasterWorker{
		redis:         redisClient,
		audioClient:   audioClient,
		r2Client:      r2Client,
		masterService: masterService,
		hub:           hub,
		r2Cfg:         r2Cfg,
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
		w.failJob(ctx, jobID, fmt.Sprintf("Vokal URL cozulemedi: %v", err))
		return err
	}

	// Step 3b: Stem URL'lerini anahtara cevir. Istemciden gelen her URL
	// KeyFromURL ile anahtara donusturulur.
	stemKeys := make([]string, 0, len(payload.StemURLs))
	for _, u := range payload.StemURLs {
		k, err := client.KeyFromURL(u, w.r2Cfg)
		if err != nil {
			w.failJob(ctx, jobID, fmt.Sprintf("Stem URL cozulemedi: %v", err))
			return fmt.Errorf("stem URL cozulemedi: %w", err)
		}
		stemKeys = append(stemKeys, k)
	}

	// Step 4: Call audio service for mastering
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 20, "Starting mastering process...")

	outputKey := fmt.Sprintf("masters/%s/%s.wav", payload.ProjectID, uuid.New().String())

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
		w.failJob(ctx, jobID, fmt.Sprintf("Mastering failed: %v", err))
		return err
	}

	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 80, "Applying limiter...")
	w.updateJobStatus(ctx, jobID, model.JobStatusRunning, 95, "Finalizing...")

	// Step 6: Generate result. masterResp.OutputURL imzasiz oldugundan
	// (private masters/ nesnesi) dogrudan istemciye verilmez; adres
	// outputKey'den yeniden uretilir.
	fileURL, expiresAt, err := w.r2Client.URLFor(ctx, outputKey)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Master URL uretilemedi: %v", err))
		return fmt.Errorf("master URL uretilemedi: %w", err)
	}

	result := &model.MasterResultResponse{
		FileURL:   fileURL,
		Duration:  masterResp.Duration,
		Profile:   payload.Profile,
		PeakDb:    masterResp.PeakDb,
		LUFS:      int(masterResp.LUFS),
		ExpiresAt: model.ExpiresPtr(expiresAt),
	}

	// Complete the job
	w.completeJob(ctx, jobID, result)
	w.hub.BroadcastComplete(jobID, result)

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
			return ctx.Err()
		default:
		}

		w.updateJobStatus(ctx, jobID, model.JobStatusRunning, step.progress, step.step)
		w.hub.BroadcastProgress(jobID, step.progress, model.JobStatusRunning, step.step)
		time.Sleep(step.duration)
	}

	result := w.generateMockResult(payload)
	w.completeJob(ctx, jobID, result)
	w.hub.BroadcastComplete(jobID, result)

	log.Printf("Master job %s completed (mock)", jobID)
	return nil
}

func (w *MasterWorker) buildMixSettings(payload *model.MasterJobPayload) []client.MixChannel {
	var settings []client.MixChannel

	// If no channels in mix snapshot, use default settings
	if len(payload.MixSnapshot.Channels) == 0 {
		for _, url := range payload.StemURLs {
			settings = append(settings, client.MixChannel{
				StemURL: url,
				Volume:  1.0,
				Pan:     0.0,
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
			StemURL: payload.StemURLs[i],
			Volume:  volume,
			Pan:     0.0, // Pan is not in the model, default to center
			Mute:    channel.Mute,
			Solo:    channel.Solo,
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
		k, err := client.KeyFromURL(take.FileURL, w.r2Cfg)
		if err != nil {
			return nil, fmt.Errorf("vokal URL cozulemedi: %w", err)
		}
		takes = append(takes, client.VocalTakeInput{
			Key:    k,
			Volume: 1.0, // Default volume, could be configurable
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
		FileURL:  fmt.Sprintf("https://cdn.makeasinger.com/masters/%s.wav", uuid.New().String()),
		Duration: 180.5,
		Profile:  payload.Profile,
		PeakDb:   -0.3,
		LUFS:     -14,
		// Mock: masters/ private oldugu icin gercekte suresi olurdu; sabit
		// yazmamak icin config'teki PresignTTL kullanilir (varsa).
		ExpiresAt: model.ExpiresPtr(time.Now().Add(w.presignTTL())),
	}
}

// presignTTL, mock/dev yollarinda kullanilacak sure degerini dondurur.
// r2Cfg yoksa (mock modda calisiliyorsa) 1 saatlik varsayilana duser.
func (w *MasterWorker) presignTTL() time.Duration {
	if w.r2Cfg != nil && w.r2Cfg.PresignTTL > 0 {
		return w.r2Cfg.PresignTTL
	}
	return time.Hour
}
