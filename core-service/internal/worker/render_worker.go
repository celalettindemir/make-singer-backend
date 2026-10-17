package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
)

// errJobCanceled: the job was canceled before the paid call; the worker
// refunds and ends the task without an error.
var errJobCanceled = errors.New("the job was canceled")

// maxStemSize: download limit for a single stem file.
const maxStemSize = 50 << 20

// errOwnerDeleted: the job owner's account was deleted; the job stops
// without charging and without writing files.
var errOwnerDeleted = errors.New("the job owner's account was deleted")

// ownerDeleted checks the job owner's deletion mark. If the checker is nil
// no check is made.
func ownerDeleted(ctx context.Context, d deletionmark.Checker, ownerID string) (bool, error) {
	if d == nil {
		return false, nil
	}
	return d.IsDeleted(ctx, ownerID)
}

// ownerCanWrite returns nil if the job owner's account still exists. It
// returns errOwnerDeleted if the account is deleted, or the check error if
// the check fails.
func ownerCanWrite(ctx context.Context, d deletionmark.Checker, ownerID string) error {
	deleted, err := ownerDeleted(ctx, d, ownerID)
	if err != nil {
		return fmt.Errorf("could not check the account status: %w", err)
	}
	if deleted {
		return errOwnerDeleted
	}
	return nil
}

// RenderWorker processes render jobs
type RenderWorker struct {
	renderService   *service.RenderService
	music           client.MusicGenerator
	r2Client        client.StorageClient
	hub             *websocket.Hub
	downloader      *http.Client
	pollInterval    time.Duration
	deletionChecker deletionmark.Checker // nil: no check
	credits         credit.Spender       // nil: no credit accounting
	isLastAttempt   func(context.Context) bool
}

// NewRenderWorker creates a new render worker
func NewRenderWorker(renderService *service.RenderService, music client.MusicGenerator, r2Client client.StorageClient, hub *websocket.Hub, deletionChecker deletionmark.Checker) *RenderWorker {
	return &RenderWorker{
		renderService:   renderService,
		music:           music,
		r2Client:        r2Client,
		hub:             hub,
		downloader:      &http.Client{Timeout: 30 * time.Second},
		pollInterval:    5 * time.Second,
		deletionChecker: deletionChecker,
		isLastAttempt:   asynqIsLastAttempt,
	}
}

// WithCredits turns on credit accounting: the final failure of a job and a
// job canceled before its first attempt refund the render debit. A nil
// spender keeps the worker free of credits.
func (w *RenderWorker) WithCredits(s credit.Spender) *RenderWorker {
	w.credits = s
	return w
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
		// A payload that cannot be decoded never will; the op is unknown
		// but the ledger refund ignores it.
		err = fmt.Errorf("failed to unmarshal render payload: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, credit.OpRenderMix, "Invalid payload", err)
		return err
	}
	op := service.RenderOperation(payload.StemMode)

	// A job canceled while queued must not reach Suno. Its cancel already
	// refunded; the refund here is a no-op then (one row per jobId), and it
	// covers a cancel whose own refund failed.
	status, err := w.renderService.JobStatus(ctx, jobID)
	if err != nil {
		log.Printf("Job %s: could not read the status: %v", jobID, err)
		w.failFinal(ctx, jobID, op, "Render failed", err)
		return fmt.Errorf("could not read the job status: %w", err)
	}
	if status == model.JobStatusCanceled {
		log.Printf("Job %s was canceled before it started; skipping", jobID)
		w.refund(ctx, op, jobID)
		return nil
	}

	// Check if Suno client is configured
	if w.music == nil || !w.music.IsConfigured() {
		return w.processWithMock(ctx, jobID, op, &payload)
	}

	return w.processWithSuno(ctx, jobID, op, &payload)
}

// processWithSuno handles real rendering using Suno API
func (w *RenderWorker) processWithSuno(ctx context.Context, jobID string, op credit.Operation, payload *model.RenderJobPayload) error {
	result, err := w.generateWithSuno(ctx, payload, func(p int, step string) {
		w.updateProgress(ctx, jobID, p, step)
	}, func() (bool, error) {
		return w.renderService.ClaimRun(ctx, jobID)
	})
	if errors.Is(err, errJobCanceled) {
		log.Printf("Job %s was canceled before the Suno call; skipping", jobID)
		w.refund(ctx, op, jobID)
		return nil
	}
	if err != nil {
		log.Printf("Job %s: render failed: %v", jobID, err)
		w.failFinal(ctx, jobID, op, "Render failed", err)
		return err
	}

	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		// The Suno fee was paid; a retry would rerun the job and charge again.
		err = fmt.Errorf("could not save the result: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, op, "Failed to save result", err)
		return err
	}

	// URLs are generated fresh for publishing; the record keeps the key.
	resp, err := w.renderService.BuildResult(ctx, result)
	if err != nil {
		log.Printf("Job %s: could not generate the stem URL: %v", jobID, err)
		// Same reason: the paid generation finished and a retry would charge again.
		err = fmt.Errorf("could not generate the stem URL: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, op, "Could not generate the stem URL", err)
		return err
	}
	w.hub.BroadcastComplete(jobID, resp)
	log.Printf("Render job %s completed", jobID)
	return nil
}

// generateWithSuno generates on Suno and transfers the stems to R2. It does
// not touch renderService or the hub; progress is reported via a callback.
//
// Retry: once GenerateMusic succeeds, credits are spent. Every later error is
// wrapped with asynq.SkipRetry; otherwise asynq would rerun the job and
// charge Suno again (4 x 62 credits with MaxRetry(3)).
func (w *RenderWorker) generateWithSuno(ctx context.Context, payload *model.RenderJobPayload, progress func(int, string), claim func() (bool, error)) (result *model.RenderResultRecord, retErr error) {
	if w.r2Client == nil {
		return nil, fmt.Errorf("storage is not configured, Suno was not called: %w", asynq.SkipRetry)
	}

	// BEFORE the paid call: if the owner/project cannot form a key, the
	// stems cannot be uploaded and 62 credits are wasted.
	if _, err := client.UserKey("stems", payload.OwnerID, payload.ProjectID, "x.mp3"); err != nil {
		return nil, fmt.Errorf("job owner/project does not fit the key, Suno was not called: %w: %w", err, asynq.SkipRetry)
	}

	// The mode is resolved BEFORE the paid call: if the focus instrument
	// cannot be mapped, 12 credits must not be spent and then fail.
	mode, focusName, err := stemPlan(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid stem setting, Suno was not called: %w: %w", err, asynq.SkipRetry)
	}

	// BEFORE the paid call: Suno is not charged for a deleted account.
	// A check error can be retried (nothing is charged yet); a deleted
	// owner is permanent.
	if err := ownerCanWrite(ctx, w.deletionChecker, payload.OwnerID); err != nil {
		if errors.Is(err, errOwnerDeleted) {
			return nil, fmt.Errorf("suno was not called: %w: %w", err, asynq.SkipRetry)
		}
		return nil, fmt.Errorf("suno was not called: %w", err)
	}

	progress(5, "Building music prompt...")
	req := &client.GenerateMusicRequest{
		Style:        buildMusicStyle(payload),
		Title:        fmt.Sprintf("%s instrumental", payload.Brief.Genre),
		Instrumental: true,
	}

	progress(10, "Generating music...")
	// The last check before the paid call. It is atomic against a cancel:
	// after it returns true, a cancel counts as a running-cancel.
	claimed, err := claim()
	if err != nil {
		return nil, fmt.Errorf("could not claim the job, suno was not called: %w", err)
	}
	if !claimed {
		return nil, errJobCanceled
	}
	taskID, err := w.music.GenerateMusic(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("music generation failed: %w", err)
	}
	charged := func(what string, err error) error {
		return fmt.Errorf("%s: %w: %w", what, err, asynq.SkipRetry)
	}
	// A panic is also an error: asynq catches it and RETRIES the job, and
	// Suno is charged again. A panic after the fee is paid is converted to
	// SkipRetry.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			retErr = charged("render panic", fmt.Errorf("%v", r))
		}
	}()

	progress(30, "Waiting for music generation...")
	track, err := w.music.WaitMusic(ctx, taskID, w.pollInterval, 10*time.Minute)
	if err != nil {
		return nil, charged("music generation failed", err)
	}

	// If Suno returns no duration, it is estimated from the structure.
	duration := track.Duration
	if duration <= 0 {
		duration = estimateDuration(payload)
	}

	var stems []client.Stem
	switch mode {
	case model.StemModeMix:
		// The generation is instrumental; the track itself is the single stem, no splitting.
		if track.AudioURL == "" {
			return nil, charged("mix render failed", errors.New("suno did not return a track URL"))
		}
		stems = []client.Stem{{Instrument: model.InstrumentMix, URL: track.AudioURL}}

	case model.StemModeFocus:
		progress(60, "Isolating instrument...")
		isolateID, err := w.music.IsolateStem(ctx, taskID, track.ID, focusName)
		if err != nil {
			return nil, charged("instrument isolation failed", err)
		}
		progress(75, "Waiting for instrument isolation...")
		isolated, err := w.music.WaitIsolated(ctx, isolateID, w.pollInterval, 5*time.Minute)
		if err != nil {
			return nil, charged("instrument isolation failed", err)
		}
		stems = []client.Stem{
			{Instrument: *payload.FocusInstrument, URL: isolated.ExtractURL},
			{Instrument: model.InstrumentRest, URL: isolated.RemoveURL},
		}

	default: // stems
		progress(60, "Splitting stems...")
		stemTaskID, err := w.music.SplitStems(ctx, taskID, track.ID)
		if err != nil {
			return nil, charged("stem splitting failed", err)
		}
		progress(75, "Waiting for stem separation...")
		stems, err = w.music.WaitStems(ctx, stemTaskID, w.pollInterval, 5*time.Minute)
		if err != nil {
			return nil, charged("stem splitting failed", err)
		}
		if len(stems) == 0 {
			return nil, charged("stem splitting failed", errors.New("suno returned no instrument stems"))
		}
	}

	progress(90, "Uploading stems...")
	records, err := w.uploadStems(ctx, payload.OwnerID, payload.ProjectID, stems, duration)
	if err != nil {
		return nil, charged("stem upload failed", err)
	}

	progress(95, "Finalizing...")
	return w.generateResult(payload, mode, duration, records), nil
}

// stemPlan resolves the payload's stem mode. An empty or unknown mode is
// treated as mix (the cheap side; including tasks queued before the
// feature). For focus it returns the instrument's Suno stemName.
func stemPlan(payload *model.RenderJobPayload) (model.StemMode, string, error) {
	switch payload.StemMode {
	case model.StemModeStems:
		return model.StemModeStems, "", nil
	case model.StemModeFocus:
		if payload.FocusInstrument == nil {
			return "", "", errors.New("focus mode requires an instrument")
		}
		name, ok := client.SunoStemName(*payload.FocusInstrument)
		if !ok {
			return "", "", fmt.Errorf("instrument cannot be separated: %s", *payload.FocusInstrument)
		}
		return model.StemModeFocus, name, nil
	default:
		return model.StemModeMix, "", nil
	}
}

// processWithMock handles rendering with mock data for development
func (w *RenderWorker) processWithMock(ctx context.Context, jobID string, op credit.Operation, payload *model.RenderJobPayload) error {
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

	claimed, err := w.renderService.ClaimRun(ctx, jobID)
	if err != nil {
		w.failFinal(ctx, jobID, op, "Render failed", err)
		return fmt.Errorf("could not claim the job: %w", err)
	}
	if !claimed {
		log.Printf("Job %s was canceled before it started; skipping (mock)", jobID)
		w.refund(ctx, op, jobID)
		return nil
	}

	for _, step := range steps {
		select {
		case <-ctx.Done():
			log.Printf("Render job %s cancelled", jobID)
			err := ctx.Err()
			w.failFinal(ctx, jobID, op, "Render failed", err)
			return err
		default:
		}

		w.updateProgress(ctx, jobID, step.progress, step.step)
		time.Sleep(step.duration)
	}

	result, err := w.generateMockResult(payload)
	if err != nil {
		err = fmt.Errorf("mock result: %w: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, jobID, op, "Invalid job owner or project", err)
		return err
	}

	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		w.failFinal(ctx, jobID, op, "Failed to save result", err)
		return err
	}

	resp, err := w.renderService.BuildResult(ctx, result)
	if err != nil {
		log.Printf("Job %s: could not generate the stem URL: %v", jobID, err)
		w.failFinal(ctx, jobID, op, "Could not generate the stem URL", err)
		return err
	}
	w.hub.BroadcastComplete(jobID, resp)
	log.Printf("Render job %s completed (mock)", jobID)
	return nil
}

// uploadStems downloads the Suno stems and writes them to R2. The record
// stores the KEY, not a signed URL; the URL is generated fresh at read time
// (RenderService.GetResult). The job owner is checked before every upload:
// if the account was deleted while the job ran (or the check fails), no
// file is written. The caller wraps the error with SkipRetry like the paid
// path; Suno is not charged again.
func (w *RenderWorker) uploadStems(ctx context.Context, ownerID, projectID string, stems []client.Stem, duration float64) ([]model.StemResultRecord, error) {
	results := make([]model.StemResultRecord, 0, len(stems))
	for _, stem := range stems {
		stemID := uuid.New().String()
		key, err := client.UserKey("stems", ownerID, projectID, stemID+".mp3")
		if err != nil {
			return nil, err
		}
		if err := ownerCanWrite(ctx, w.deletionChecker, ownerID); err != nil {
			return nil, err
		}
		if err := w.transferStem(ctx, stem.URL, key); err != nil {
			return nil, fmt.Errorf("could not transfer the %s stem: %w", stem.Instrument, err)
		}
		results = append(results, model.StemResultRecord{
			ID:           stemID,
			Instrument:   stem.Instrument,
			FileKey:      key,
			Duration:     duration,
			WaveformData: generateWaveform(100),
		})
	}
	return results, nil
}

// transferStem downloads the URL and uploads it to the key. The body is
// read into memory: S3 PutObject needs a content length and a file over the
// cap must not be uploaded halfway.
func (w *RenderWorker) transferStem(ctx context.Context, rawURL, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := w.downloader.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxStemSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxStemSize {
		return fmt.Errorf("stem exceeds the %d byte cap", maxStemSize)
	}
	_, _, err = w.r2Client.Upload(ctx, key, bytes.NewReader(data), "audio/mpeg")
	return err
}

func (w *RenderWorker) generateResult(payload *model.RenderJobPayload, mode model.StemMode, duration float64, stems []model.StemResultRecord) *model.RenderResultRecord {
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

	return &model.RenderResultRecord{
		ID:        uuid.New().String(),
		BPM:       bpm,
		Duration:  duration,
		Key:       model.KeyResult{Tonic: tonic, Scale: scale},
		StemMode:  mode,
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

// failFinal ends a failed attempt. Only the job's FINAL failure (SkipRetry,
// or asynq's last attempt) writes the failed status and refunds; an
// intermediate failure leaves the job as it is, so a later retry that
// succeeds is not a free render and a visible "failed" always comes with its
// refund (6-G16).
func (w *RenderWorker) failFinal(ctx context.Context, jobID string, op credit.Operation, errMsg string, err error) {
	if !errors.Is(err, asynq.SkipRetry) && !w.lastAttempt(ctx) {
		log.Printf("Job %s: attempt failed, will retry: %v", jobID, err)
		return
	}
	// Detached: the final failure must be written even when ctx is the
	// reason for it (shutdown, deadline).
	w.failJob(context.WithoutCancel(ctx), jobID, errMsg)
	w.refund(ctx, op, jobID)
}

func (w *RenderWorker) lastAttempt(ctx context.Context) bool {
	if w.isLastAttempt == nil {
		return asynqIsLastAttempt(ctx)
	}
	return w.isLastAttempt(ctx)
}

// refund reverses the render debit on a detached context, so a shutting-down
// worker still refunds. A failure is logged, never returned: returning it
// would retry the whole render for a bookkeeping error.
func (w *RenderWorker) refund(ctx context.Context, op credit.Operation, jobID string) {
	if w.credits == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := w.credits.Refund(rctx, op, jobID); err != nil {
		log.Printf("Job %s: could not refund %s: %v", jobID, op, err)
	}
}

func (w *RenderWorker) generateMockResult(payload *model.RenderJobPayload) (*model.RenderResultRecord, error) {
	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}
	duration := estimateDuration(payload)

	tonic := model.TonicC
	scale := model.ScaleMajor
	if payload.Brief.Key.Tonic != nil {
		tonic = *payload.Brief.Key.Tonic
	}
	if payload.Brief.Key.Scale != nil {
		scale = *payload.Brief.Key.Scale
	}

	mode, _, err := stemPlan(payload)
	if err != nil {
		return nil, err
	}
	var instruments []model.Instrument
	switch mode {
	case model.StemModeMix:
		instruments = []model.Instrument{model.InstrumentMix}
	case model.StemModeFocus:
		instruments = []model.Instrument{*payload.FocusInstrument, model.InstrumentRest}
	default:
		instruments = payload.Arrangement.Instruments
	}

	var stems []model.StemResultRecord
	for _, instrument := range instruments {
		stemID := uuid.New().String()
		key, err := client.UserKey("stems", payload.OwnerID, payload.ProjectID, string(instrument)+".wav")
		if err != nil {
			return nil, err
		}
		stems = append(stems, model.StemResultRecord{
			ID:           stemID,
			Instrument:   instrument,
			FileKey:      key,
			Duration:     duration,
			WaveformData: generateWaveform(100),
		})
	}

	return &model.RenderResultRecord{
		ID:        uuid.New().String(),
		BPM:       bpm,
		Duration:  duration,
		Key:       model.KeyResult{Tonic: tonic, Scale: scale},
		StemMode:  mode,
		CreatedAt: time.Now(),
		Stems:     stems,
	}, nil
}

// estimateDuration estimates seconds as total bars x 4 beats / BPM x 60.
func estimateDuration(payload *model.RenderJobPayload) float64 {
	var totalBars int
	for _, section := range payload.Brief.Structure {
		totalBars += section.Bars
	}
	bpm := 120
	if payload.Brief.BPM.Value != nil {
		bpm = *payload.Brief.BPM.Value
	}
	return float64(totalBars*4) / float64(bpm) * 60
}

func generateWaveform(points int) []float64 {
	waveform := make([]float64, points)
	for i := range waveform {
		waveform[i] = 0.1 + float64(i%10)/15.0
	}
	return waveform
}
