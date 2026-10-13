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
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/social"
)

const (
	// waveformPeakCount and previewSeconds are the contract values: 64 peaks and a 30 s preview.
	waveformPeakCount = 64
	previewSeconds    = 30
)

// SocialWorker handles the sosyal:hazirla and sosyal:dosya-sil tasks.
type SocialWorker struct {
	store           social.PreparationStore
	copier          client.Copier        // nil: no R2, copying is skipped
	analyzer        client.AudioAnalyzer // nil: no waveform or preview
	prefixDeleter   client.PrefixDeleter // nil: no R2
	deletionChecker deletionmark.Checker // nil: no deletion check
	isLastAttempt   func(context.Context) bool
	sweeper         social.StaleSweeper // nil: the stale sweep is a no-op
	staleAfter      time.Duration
}

// NewSocialWorker builds the worker. Interface parameters may be nil; main
// passes only real clients so it does not fall into the typed-nil trap.
func NewSocialWorker(store social.PreparationStore, copier client.Copier, analyzer client.AudioAnalyzer,
	prefixDeleter client.PrefixDeleter, deletionChecker deletionmark.Checker) *SocialWorker {
	return &SocialWorker{
		store: store, copier: copier, analyzer: analyzer, prefixDeleter: prefixDeleter, deletionChecker: deletionChecker,
		isLastAttempt: asynqIsLastAttempt,
	}
}

// WithStaleSweep enables the sosyal:bayat-tara handler: publications that
// stayed in the preparing status for longer than staleAfter are failed.
func (w *SocialWorker) WithStaleSweep(sweeper social.StaleSweeper, staleAfter time.Duration) *SocialWorker {
	w.sweeper, w.staleAfter = sweeper, staleAfter
	return w
}

// SweepStale fails publications whose prepare job was lost, so their owners
// can retry. It logs the count only when something changed.
func (w *SocialWorker) SweepStale(ctx context.Context, _ *asynq.Task) error {
	if w.sweeper == nil || w.staleAfter <= 0 {
		return nil
	}
	n, err := w.sweeper.MarkStalePreparations(ctx, w.staleAfter)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("social: marked %d stale preparing publication(s) as failed", n)
	}
	return nil
}

// asynqIsLastAttempt reports whether this attempt is the job's last one.
// It returns false when the context carries no asynq data (tests, manual calls).
func asynqIsLastAttempt(ctx context.Context) bool {
	n, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	return ok1 && ok2 && n >= maxRetry
}

func decodeSocialTaskPayload(t *asynq.Task) (string, error) {
	var payload social.TaskPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return "", fmt.Errorf("could not decode the social task payload: %v: %w", err, asynq.SkipRetry)
	}
	id, err := uuid.Parse(payload.PublicationID)
	if err != nil {
		return "", fmt.Errorf("invalid publication ID in the social task payload: %w", asynq.SkipRetry)
	}
	return id.String(), nil
}

// Prepare copies the publication's files under sosyal/{id}/, analyzes them
// and publishes. Copy errors are retried; on the last attempt the publication
// is marked failed. An analysis error does not block publishing: the waveform
// stays empty.
func (w *SocialWorker) Prepare(ctx context.Context, t *asynq.Task) error {
	id, err := decodeSocialTaskPayload(t)
	if err != nil {
		return err
	}
	record, err := w.store.GetPreparation(ctx, id)
	if errors.Is(err, social.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.Deleted || record.Status != social.StatusPreparing {
		return nil
	}
	result, err := w.copyFiles(ctx, record)
	if err != nil {
		return w.fail(ctx, id, err)
	}
	if err := w.checkOwner(ctx, record.OwnerID); err != nil {
		return w.fail(ctx, id, err)
	}
	w.analyze(ctx, id, &result)
	if err := w.store.Publish(ctx, id, result); err != nil {
		return w.fail(ctx, id, err)
	}
	return nil
}

// checkOwner verifies before every R2 write that the job owner is not deleted.
// A deleted owner is a permanent error (SkipRetry); a check error is retried.
func (w *SocialWorker) checkOwner(ctx context.Context, ownerID string) error {
	if err := ownerCanWrite(ctx, w.deletionChecker, ownerID); err != nil {
		if errors.Is(err, errOwnerDeleted) {
			return fmt.Errorf("publication was not prepared: %w: %w", err, asynq.SkipRetry)
		}
		return err
	}
	return nil
}

// fail returns the error; on a permanent error or the last attempt it moves
// the publication to the failed state so the owner can see "Try again". If the
// context was canceled (shutdown) asynq requeues the job and the publication
// is not marked. MarkFailed is called with a context detached from
// cancellation so it still works when the deadline has expired.
func (w *SocialWorker) fail(ctx context.Context, id string, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return err
	}
	if errors.Is(err, asynq.SkipRetry) || w.isLastAttempt(ctx) {
		if markErr := w.store.MarkFailed(context.WithoutCancel(ctx), id); markErr != nil {
			log.Printf("Warning: could not mark the publication as failed (%s): %v", id, markErr)
		}
	}
	return err
}

// copyFiles copies the master and stems and fills the result's key fields.
// Copying to the same destination again is harmless, so the job can be retried.
func (w *SocialWorker) copyFiles(ctx context.Context, record *social.PreparationRecord) (social.PreparationResult, error) {
	var result social.PreparationResult
	master, err := social.MasterDestKey(record.ID, record.Preparation.MasterKey)
	if err != nil {
		return result, fmt.Errorf("could not build the master target: %v: %w", err, asynq.SkipRetry)
	}
	if err := w.copyOne(ctx, record.OwnerID, record.Preparation.MasterKey, master); err != nil {
		return result, fmt.Errorf("could not copy the master: %w", err)
	}
	result.MasterKey = master
	result.Stems = make([]social.StemRecord, 0, len(record.Preparation.Stems))
	for _, st := range record.Preparation.Stems {
		dst, err := social.StemDestKey(record.ID, st.ID, st.Key)
		if err != nil {
			return result, fmt.Errorf("could not build the stem target: %v: %w", err, asynq.SkipRetry)
		}
		if err := w.copyOne(ctx, record.OwnerID, st.Key, dst); err != nil {
			return result, fmt.Errorf("could not copy the stem: %w", err)
		}
		result.Stems = append(result.Stems, social.StemRecord{
			ID: st.ID, Instrument: st.Instrument, Duration: st.Duration, Key: dst,
		})
	}
	return result, nil
}

func (w *SocialWorker) copyOne(ctx context.Context, ownerID, src, dst string) error {
	if err := w.checkOwner(ctx, ownerID); err != nil {
		return err
	}
	if w.copier == nil {
		return nil
	}
	return w.copier.Copy(ctx, src, dst)
}

// analyze produces the waveform and preview. Every error is logged and the
// result continues with an empty waveform and no preview (spec §4.3).
func (w *SocialWorker) analyze(ctx context.Context, id string, result *social.PreparationResult) {
	result.Waveform = []float64{}
	if w.analyzer == nil {
		return
	}
	previewKey, err := social.PreviewDestKey(id)
	if err != nil {
		log.Printf("Warning: could not build the preview target (%s): %v", id, err)
		return
	}
	resp, err := w.analyzer.Analyze(ctx, &client.AnalysisRequest{
		InputKey: result.MasterKey, PreviewKey: previewKey,
		PeakCount: waveformPeakCount, PreviewSeconds: previewSeconds,
	})
	if err != nil {
		log.Printf("Warning: could not analyze the publication, the waveform will stay empty (%s): %v", id, err)
		return
	}
	if len(resp.Peaks) != waveformPeakCount {
		log.Printf("Warning: the analysis returned %d peaks, %d expected (%s)", len(resp.Peaks), waveformPeakCount, id)
		return
	}
	waveform := make([]float64, waveformPeakCount)
	for i, p := range resp.Peaks {
		if math.IsNaN(p) {
			p = 0
		}
		waveform[i] = math.Min(1, math.Max(0, p))
	}
	result.Waveform = waveform
	result.PreviewKey = previewKey
}

// DeleteFiles cleans the sosyal/{id}/ prefix of a deleted publication. If the
// row is still live (not deleted) nothing is removed.
func (w *SocialWorker) DeleteFiles(ctx context.Context, t *asynq.Task) error {
	id, err := decodeSocialTaskPayload(t)
	if err != nil {
		return err
	}
	prefix, err := client.SocialPrefix(id)
	if err != nil {
		return fmt.Errorf("could not build the social prefix: %v: %w", err, asynq.SkipRetry)
	}
	record, err := w.store.GetPreparation(ctx, id)
	switch {
	case errors.Is(err, social.ErrNotFound):
		// No row (account deleted): the files are still cleaned up.
	case err != nil:
		return err
	case !record.Deleted:
		return nil
	}
	if w.prefixDeleter == nil {
		return nil
	}
	if _, err := w.prefixDeleter.DeletePrefix(ctx, prefix); err != nil {
		return fmt.Errorf("could not delete the social files: %w", err)
	}
	return nil
}
