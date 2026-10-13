package social

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
)

// CoverStem is a stem handed to the cover project: fileUrl is the address of
// the copy under the user's own stems/ prefix (/api/files/sign re-signs it).
type CoverStem struct {
	ID         string  `json:"id"`
	Instrument string  `json:"enstruman"`
	FileURL    string  `json:"fileUrl"`
	Duration   float64 `json:"sure"`
}

// CoverStart is the POST /yayinlar/{id}/cover-baslat response (spec §4.6).
type CoverStart struct {
	Source      SourceSummary   `json:"kaynak"`
	Genre       string          `json:"tur"`
	BPM         int             `json:"bpm"`
	MusicalKey  MusicalKey      `json:"ton"`
	DurationSec int             `json:"sureSn"`
	Structure   []StructureItem `json:"yapi"`
	Lyrics      Lyrics          `json:"sozler"`
	Stems       []CoverStem     `json:"stemler"`
}

// WithCopier attaches the stem copier of cover-baslat and returns the
// service. If nil (R2 not configured, dev), copying is skipped and the
// addresses are still produced. main passes only the real client to avoid
// the typed-nil trap.
func (s *Service) WithCopier(copier client.Copier) *Service {
	s.copier = copier
	return s
}

// CoverStemDestKey is the key of a stem copied for a cover:
// stems/{userId}/{folder}/{stemId}.{ext}. It matches the render stem layout;
// the extension comes from the source key.
func CoverStemDestKey(userID, folder, stemID, source string) (string, error) {
	return client.UserKey("stems", userID, folder, stemID+extension(source))
}

// Covers is GET /yayinlar/{id}/coverlar. ErrNotFound if the source is not
// visible; an empty page if its backing is disabled (cover publications
// included).
func (s *Service) Covers(ctx context.Context, userID, pubID, rawCursor string, limit int) (*Page[PublicationSummary], error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeTimeCursor(rawCursor)
	if err != nil {
		return nil, err
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if !rec.BackingEnabled {
		return buildTimePage(nil, limit), nil
	}
	items, err := s.store.ListCovers(ctx, rec.ID, id, cursor, limit)
	if err != nil {
		return nil, err
	}
	return buildTimePage(items, limit), nil
}

// StartCover is POST /yayinlar/{id}/cover-baslat: it copies the
// publication's stems concurrently to the caller's stems/ prefix and returns
// the start of the cover project. A publication that is not visible is
// ErrNotFound; a cover publication, one with backing disabled or one without
// stem copies is ErrBackingDisabled; the owner's own publication that is not
// yet published is ErrInvalidStatus.
func (s *Service) StartCover(ctx context.Context, userID, pubID string) (*CoverStart, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if rec.SourcePublicationID != "" || !rec.BackingEnabled {
		return nil, ErrBackingDisabled
	}
	if rec.Status != StatusPublished {
		return nil, ErrInvalidStatus
	}
	sourceStems, err := s.store.ListStems(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	if len(sourceStems) == 0 {
		return nil, ErrBackingDisabled
	}

	folder := uuid.NewString()
	stems := make([]CoverStem, 0, len(sourceStems))
	for _, stem := range sourceStems {
		dest, err := CoverStemDestKey(id, folder, stem.ID, stem.Key)
		if err != nil {
			return nil, fmt.Errorf("cover stem anahtari kurulamadi: %w", err)
		}
		if s.copier != nil {
			if err := s.copier.Copy(ctx, stem.Key, dest); err != nil {
				return nil, fmt.Errorf("cover stem kopyalanamadi: %w", err)
			}
		}
		url, _, err := client.ResolveURL(ctx, s.storage, s.r2, dest)
		if err != nil {
			return nil, fmt.Errorf("cover stem adresi uretilemedi: %w", err)
		}
		stems = append(stems, CoverStem{ID: stem.ID, Instrument: stem.Instrument, FileURL: url, Duration: stem.Duration})
	}
	return &CoverStart{
		Source: SourceSummary{PublicationID: rec.ID, Title: rec.Title, OwnerUsername: rec.Owner.Username},
		Genre:  rec.Genre, BPM: rec.BPM, MusicalKey: rec.MusicalKey, DurationSec: rec.DurationSec,
		Structure: rec.Structure, Lyrics: rec.Lyrics, Stems: stems,
	}, nil
}
