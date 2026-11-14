package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// Import job steps (CurrentStep) and their progress.
const (
	importStepSeparating   = "separating"
	importStepAnalyzing    = "analyzing"
	importStepTranscribing = "transcribing"
	importStepNotes        = "notes"
	importStepDone         = "done"

	importProgressSeparating   = 10
	importProgressAnalyzing    = 60
	importProgressTranscribing = 70
	importProgressNotes        = 90
	importProgressDone         = 100

	// ImportErrorCode is the error code of an import job that failed.
	ImportErrorCode = service.ImportErrorCode

	// maxImportVocalFetch bounds the vocal stem read back for transcription
	// (an 8 minute mp3 is about 15 MB).
	maxImportVocalFetch = 40 << 20
	// importDefaultBPM is the tempo the notes use when none was measured.
	importDefaultBPM = 100.0
)

// importStemOrder is the order of the result's stems.
var importStemOrder = []string{"vocal", "drums", "bass", "rest"}

// ImportWorkerDeps are the collaborators of ImportWorker.
type ImportWorkerDeps struct {
	Repo      jobs.Repo
	Storage   client.StorageClient
	Separator client.Separator
	Audio     client.ImportAudio
	// Transcriber may be nil: the result then has no lyrics.
	Transcriber client.TimedTranscriber
	Credits     credit.Spender
	// Deletion may be nil: no deleted-owner check.
	Deletion deletionmark.Checker
	// ScheduleCleanup queues the delayed deletion of a failed job's source
	// (ImportService.ScheduleSourceCleanup).
	ScheduleCleanup   func(ownerID, sourceKey, jobID string) error
	TranscribeTimeout time.Duration

	// IsLastAttempt and Fetch default to asynq's retry state and an HTTP GET
	// of the signed URL; tests replace them.
	IsLastAttempt func(context.Context) bool
	Fetch         func(ctx context.Context, url string) ([]byte, error)
}

// ImportWorker processes import jobs: separation, measurement, timed
// transcription and the guide notes.
type ImportWorker struct{ d ImportWorkerDeps }

func NewImportWorker(d ImportWorkerDeps) *ImportWorker {
	if d.IsLastAttempt == nil {
		d.IsLastAttempt = asynqIsLastAttempt
	}
	if d.Fetch == nil {
		d.Fetch = fetchImportObject
	}
	return &ImportWorker{d: d}
}

func fetchImportObject(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxImportVocalFetch))
}

func importTerminal(s model.JobStatus) bool {
	return s == model.JobStatusSucceeded || s == model.JobStatusFailed || s == model.JobStatusCanceled
}

// ProcessTask runs one TaskTypeImport task.
func (w *ImportWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var tp service.ImportTaskPayload
	if err := json.Unmarshal(t.Payload(), &tp); err != nil {
		return fmt.Errorf("could not decode the import task payload: %v: %w", err, asynq.SkipRetry)
	}
	d := w.d
	job, err := d.Repo.Get(ctx, tp.JobID)
	if errors.Is(err, jobs.ErrJobNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if importTerminal(job.Status) {
		// A cancel or failure between attempts leaves the earlier attempt's
		// outputs behind.
		w.dropOutputs(ctx, job, job.Status)
		return nil
	}
	jobID, owner := job.ID, job.OwnerID
	var p model.ImportPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.SourceKey == "" {
		err = fmt.Errorf("import job %s: bad input: %w", jobID, asynq.SkipRetry)
		w.failFinal(ctx, job, p.SourceKey, true, err)
		return err
	}

	// A deleted account gets nothing written and no paid separation.
	if err := w.checkOwner(ctx, jobID, owner); err != nil {
		w.failFinal(ctx, job, p.SourceKey, !errors.Is(err, errOwnerDeleted), err)
		return err
	}
	if ok, err := w.step(ctx, job, p.SourceKey, importStepSeparating, importProgressSeparating, true); err != nil || !ok {
		return err
	}

	outPrefix, err := client.UserKey("imports", owner, jobID)
	if err != nil {
		err = fmt.Errorf("import key: %v: %w", err, asynq.SkipRetry)
		w.failFinal(ctx, job, p.SourceKey, true, err)
		return err
	}
	sep, err := d.Separator.Separate(ctx, &client.SeparateRequest{InputKey: p.SourceKey, OutputPrefix: outPrefix + "/"})
	if err != nil {
		err = fmt.Errorf("separation failed: %w", err)
		w.failFinal(ctx, job, p.SourceKey, true, err)
		return err
	}
	stems, err := orderImportStems(sep)
	if err != nil {
		w.failFinal(ctx, job, p.SourceKey, true, err)
		return err
	}
	durationMs := sep.DurationMs
	if durationMs <= 0 {
		durationMs = p.DurationMs
	}

	if ok, err := w.step(ctx, job, p.SourceKey, importStepAnalyzing, importProgressAnalyzing, false); err != nil || !ok {
		return err
	}
	meas, err := d.Audio.MeasureImport(ctx, &client.ImportMeasureRequest{InputKey: p.SourceKey})
	if err != nil {
		err = fmt.Errorf("measurement failed: %w", err)
		w.failFinal(ctx, job, p.SourceKey, true, err)
		return err
	}

	// From here on nothing fails the job: lyrics and notes are best effort.
	if ok, err := w.step(ctx, job, p.SourceKey, importStepTranscribing, importProgressTranscribing, false); err != nil || !ok {
		return err
	}
	lines, words := w.transcribe(ctx, jobID, stems[0].Key)
	lyr := service.BuildImportLyrics(lines, durationMs, meas.BPM)

	var guide *model.GuideRecord
	if len(lines) > 0 {
		if ok, err := w.step(ctx, job, p.SourceKey, importStepNotes, importProgressNotes, false); err != nil || !ok {
			return err
		}
		guide = w.notes(ctx, jobID, owner, outPrefix, stems[0].Key, lyr, words, meas.BPM)
	}

	rec := &model.ImportRecord{
		DurationMs: durationMs, BPM: meas.BPM, Key: meas.Key,
		Structure: lyr.Structure, Lyrics: lyr.Lyrics, LyricTimeline: lyr.Timeline, Guide: guide,
		Stems: make([]model.ImportStemRecord, len(stems)),
	}
	for i, s := range stems {
		rec.Stems[i] = model.ImportStemRecord{
			ID: s.Instrument, Instrument: model.Instrument(s.Instrument), Key: s.Key,
			Duration: float64(durationMs) / 1000,
		}
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	if err := w.checkOwner(ctx, jobID, owner); err != nil {
		w.failFinal(ctx, job, p.SourceKey, !errors.Is(err, errOwnerDeleted), err)
		return err
	}
	done := false
	var ended model.JobStatus
	err = d.Repo.UpdateWith(ctx, jobID, func(j *model.Job, _ jobs.Emitter) error {
		if importTerminal(j.Status) {
			ended = j.Status
			return jobs.ErrSkipWrite
		}
		now := time.Now().UTC()
		j.Status, j.Progress, j.CurrentStep = model.JobStatusSucceeded, importProgressDone, importStepDone
		j.Result, j.CompletedAt = raw, &now
		done = true
		return nil
	})
	if err != nil && !errors.Is(err, jobs.ErrSkipWrite) {
		w.failFinal(ctx, job, p.SourceKey, true, err)
		return err
	}
	if !done {
		w.dropOutputs(ctx, job, ended)
		return nil
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := d.Storage.Delete(dctx, p.SourceKey); err != nil {
		log.Printf("import job %s: could not delete the source: %v", jobID, err)
	}
	return nil
}

func (w *ImportWorker) checkOwner(ctx context.Context, jobID, owner string) error {
	err := ownerCanWrite(ctx, w.d.Deletion, owner)
	if errors.Is(err, errOwnerDeleted) {
		return fmt.Errorf("import was not written: %w: %w", err, asynq.SkipRetry)
	}
	if err != nil {
		log.Printf("import job %s: owner check failed: %v", jobID, err)
	}
	return err
}

// step is setStep with the last-attempt rule: a repo error on the final
// attempt fails and refunds the job (earlier attempts return it for the retry).
// When another writer already ended the job, step drops the outputs and ok is
// false: the caller stops without error.
func (w *ImportWorker) step(ctx context.Context, job *model.Job, sourceKey, step string, progress int, start bool) (bool, error) {
	ended, ok, err := w.setStep(ctx, job.ID, step, progress, start)
	if err != nil {
		w.failFinal(ctx, job, sourceKey, true, err)
		return false, err
	}
	if !ok {
		w.dropOutputs(ctx, job, ended)
	}
	return ok, nil
}

// deleteOutputs removes the job's separated stems and notes (best effort).
func (w *ImportWorker) deleteOutputs(ctx context.Context, job *model.Job) {
	service.DeleteImportOutputs(ctx, w.d.Storage, job.OwnerID, job.ID)
}

// dropOutputs handles a job that another writer ended while this attempt
// worked: a cancel or a failure refunded it, so its stems must not stay
// signable; a job that ended succeeded (an overlapping run) keeps them.
func (w *ImportWorker) dropOutputs(ctx context.Context, job *model.Job, ended model.JobStatus) {
	if ended == model.JobStatusSucceeded {
		return
	}
	w.deleteOutputs(ctx, job)
}

// setStep moves the job to running at the given step. When the job is
// already terminal it writes nothing and returns that state with ok false.
func (w *ImportWorker) setStep(ctx context.Context, jobID, step string, progress int, start bool) (model.JobStatus, bool, error) {
	var ended model.JobStatus
	err := w.d.Repo.UpdateWith(ctx, jobID, func(j *model.Job, _ jobs.Emitter) error {
		if importTerminal(j.Status) {
			ended = j.Status
			return jobs.ErrSkipWrite
		}
		j.Status, j.CurrentStep, j.Progress = model.JobStatusRunning, step, progress
		if start && j.StartedAt == nil {
			now := time.Now().UTC()
			j.StartedAt = &now
		}
		return nil
	})
	if errors.Is(err, jobs.ErrSkipWrite) {
		return ended, false, nil
	}
	return ended, err == nil, err
}

// transcribe returns the lyric lines and the words of the vocal stem. Any
// failure gives no lines: the song is imported without lyrics.
func (w *ImportWorker) transcribe(ctx context.Context, jobID, vocalKey string) ([]service.ImportLine, []client.TimedWordIn) {
	d := w.d
	if d.Transcriber == nil || !d.Transcriber.IsConfigured() {
		return nil, nil
	}
	tctx := ctx
	if d.TranscribeTimeout > 0 {
		var cancel context.CancelFunc
		tctx, cancel = context.WithTimeout(ctx, d.TranscribeTimeout)
		defer cancel()
	}
	url, err := d.Storage.GetSignedURL(tctx, vocalKey, 15*time.Minute)
	if err != nil {
		log.Printf("import job %s: could not sign the vocal stem: %v", jobID, err)
		return nil, nil
	}
	audio, err := d.Fetch(tctx, url)
	if err != nil {
		log.Printf("import job %s: could not read the vocal stem: %v", jobID, err)
		return nil, nil
	}
	tr, err := d.Transcriber.TranscribeTimed(tctx, audio, "vocal.mp3")
	if err != nil {
		log.Printf("import job %s: transcription failed: %v", jobID, err)
		return nil, nil
	}
	lines := service.ImportLines(tr)
	var words []client.TimedWordIn
	for _, l := range lines {
		for _, wd := range l.Words {
			words = append(words, client.TimedWordIn{Word: wd.Word, StartS: wd.Start, EndS: wd.End, Success: true})
		}
	}
	return lines, words
}

// notes makes the guide note strip for a Plus user; nil when not entitled or
// when the strip could not be made.
func (w *ImportWorker) notes(ctx context.Context, jobID, owner, outPrefix, vocalKey string, lyr service.ImportLyrics, words []client.TimedWordIn, bpm *float64) *model.GuideRecord {
	d := w.d
	if d.Credits == nil || d.Audio == nil {
		return nil
	}
	plus, err := d.Credits.HasPlan(ctx, owner, credit.TierPlus)
	if err != nil {
		log.Printf("import job %s: could not check the plan: %v", jobID, err)
		return nil
	}
	if !plus {
		return nil
	}
	notesKey := outPrefix + "/notes.json"
	req := &client.ImportNotesRequest{VocalKey: vocalKey, Words: words, BPM: importDefaultBPM, NotesKey: notesKey}
	if bpm != nil && *bpm > 0 {
		req.BPM = *bpm
	}
	for i, sec := range lyr.Structure {
		texts := make([]string, len(lyr.SectionLines[i]))
		for j, l := range lyr.SectionLines[i] {
			texts[j] = l.Text
		}
		req.Sections = append(req.Sections, client.NotesSection{ID: sec.ID, Lines: texts})
	}
	if _, err := d.Audio.ImportNotes(ctx, req); err != nil {
		log.Printf("import job %s: notes failed: %v", jobID, err)
		return nil
	}
	return &model.GuideRecord{NotesKey: &notesKey}
}

// orderImportStems returns the stems as vocal, drums, bass, rest; a missing
// stem is an error.
func orderImportStems(sep *client.SeparateResponse) ([]client.SeparatedStem, error) {
	by := map[string]client.SeparatedStem{}
	for _, s := range sep.Stems {
		by[s.Instrument] = s
	}
	out := make([]client.SeparatedStem, 0, len(importStemOrder))
	for _, name := range importStemOrder {
		s, ok := by[name]
		if !ok || s.Key == "" {
			return nil, fmt.Errorf("separation returned no %s stem", name)
		}
		out = append(out, s)
	}
	return out, nil
}

// failFinal ends a failed attempt. Only the final failure (SkipRetry or the
// last asynq attempt) writes failed, refunds (when refund is true) and
// schedules the source cleanup; earlier attempts leave the job as it is.
func (w *ImportWorker) failFinal(ctx context.Context, job *model.Job, sourceKey string, refund bool, err error) {
	d := w.d
	if !errors.Is(err, asynq.SkipRetry) && !d.IsLastAttempt(ctx) {
		log.Printf("import job %s: attempt failed, will retry: %v", job.ID, err)
		return
	}
	log.Printf("import job %s: failed: %v", job.ID, err)
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	failed := false
	var ended model.JobStatus
	uerr := d.Repo.UpdateWith(fctx, job.ID, func(j *model.Job, _ jobs.Emitter) error {
		if importTerminal(j.Status) {
			ended = j.Status
			return jobs.ErrSkipWrite
		}
		msg := "Import failed"
		now := time.Now().UTC()
		j.Status, j.Error, j.ErrorCode, j.CompletedAt = model.JobStatusFailed, &msg, ImportErrorCode, &now
		failed = true
		return nil
	})
	if uerr != nil && !errors.Is(uerr, jobs.ErrSkipWrite) {
		log.Printf("import job %s: could not mark the job failed: %v", job.ID, uerr)
	}
	if !failed {
		// Ended meanwhile: the cancel or the stale sweep gave the refund and
		// scheduled the cleanup. A DB error leaves the job to the stale sweep.
		if errors.Is(uerr, jobs.ErrSkipWrite) {
			w.dropOutputs(ctx, job, ended)
		}
		return
	}
	if refund {
		w.deleteOutputs(ctx, job)
	}
	if refund && d.Credits != nil {
		if rerr := d.Credits.Refund(fctx, credit.OpImport, job.ID); rerr != nil {
			log.Printf("import job %s: could not refund: %v", job.ID, rerr)
		}
	}
	if refund && sourceKey != "" && d.ScheduleCleanup != nil {
		if cerr := d.ScheduleCleanup(job.OwnerID, sourceKey, job.ID); cerr != nil {
			log.Printf("import job %s: could not schedule the source cleanup: %v", job.ID, cerr)
		}
	}
}
