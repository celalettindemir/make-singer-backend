package social

import (
	"context"
	"fmt"
	"time"

	"github.com/makeasinger/api/internal/client"
)

// previewURLTTL is the validity of the onizlemeUrl signature (spec §4.8: 1
// hour). It is independent of R2_PRESIGN_TTL: the contract says a fixed
// 1 hour.
const previewURLTTL = time.Hour

// Preview is the GET /api/public/s/{slug} response. PreviewURL is VISIBLE as
// null when no preview could be produced; Waveform is never null.
type Preview struct {
	Title       string         `json:"baslik"`
	Owner       ProfileSummary `json:"sahip"`
	DurationSec int            `json:"sureSn"`
	Waveform    []float64      `json:"dalgaFormu"`
	PreviewURL  *string        `json:"onizlemeUrl"`
	Genre       string         `json:"tur"`
}

// PreviewRecord is the preview read from the store plus its internal fields.
type PreviewRecord struct {
	Preview
	OwnerID    string
	PreviewKey string // "" means no preview
}

// Preview returns the unauthenticated preview. A malformed slug, a
// followers-only publication, or a publication that is being prepared,
// failed or deleted is ErrNotFound.
func (s *Service) Preview(ctx context.Context, slug string) (*Preview, error) {
	if !ValidSlug(slug) {
		return nil, ErrNotFound
	}
	rec, err := s.store.GetPreview(ctx, slug)
	if err != nil {
		return nil, err
	}
	preview := rec.Preview
	if rec.PreviewKey != "" {
		url, err := s.previewURL(ctx, rec.PreviewKey)
		if err != nil {
			return nil, err
		}
		preview.PreviewURL = &url
	}
	return &preview, nil
}

// previewURL produces a signed address valid for 1 hour; without R2 (dev)
// it returns an unsigned address.
func (s *Service) previewURL(ctx context.Context, key string) (string, error) {
	if s.storage == nil {
		return client.UnsignedURL(key, s.r2), nil
	}
	url, err := s.storage.GetSignedURL(ctx, key, previewURLTTL)
	if err != nil {
		return "", fmt.Errorf("onizleme adresi imzalanamadi: %w", err)
	}
	return url, nil
}
