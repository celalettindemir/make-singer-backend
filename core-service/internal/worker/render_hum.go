package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// coverURLTTL is how long the signed URL of the hum stays valid. It is
// signed again on every attempt.
const coverURLTTL = 2 * time.Hour

// CoverWeights are the Suno Upload and Cover weights per fidelity.
type CoverWeights struct {
	AudioFaithful, AudioFree, StyleFaithful, StyleFree float64
}

// For returns the audio and style weight for the fidelity. Anything but
// faithful is free.
func (w CoverWeights) For(f model.HumFidelity) (audio, style float64) {
	if f == model.HumFaithful {
		return w.AudioFaithful, w.StyleFaithful
	}
	return w.AudioFree, w.StyleFree
}

// WithCoverWeights sets the hum cover weights (from config).
func (w *RenderWorker) WithCoverWeights(cw CoverWeights) *RenderWorker {
	w.coverWeights = cw
	return w
}

// coverPlan checks everything a hum render needs BEFORE the paid call and
// returns the cover capability and the signed URL of the hum. Permanent
// problems are SkipRetry; a signing error can be retried (nothing is charged).
func (w *RenderWorker) coverPlan(ctx context.Context, payload *model.RenderJobPayload, vocal bool) (client.CoverGenerator, string, error) {
	cover, ok := w.music.(client.CoverGenerator)
	if !ok {
		return nil, "", fmt.Errorf("cover unavailable, Suno was not called: %w", asynq.SkipRetry)
	}
	if !vocal {
		return nil, "", fmt.Errorf("a hum render needs a vocal, Suno was not called: %w", asynq.SkipRetry)
	}
	url, err := w.r2Client.GetSignedURL(ctx, payload.Hum.Key, coverURLTTL)
	if err != nil {
		return nil, "", fmt.Errorf("could not sign the hum URL, Suno was not called: %w", err)
	}
	return cover, url, nil
}
