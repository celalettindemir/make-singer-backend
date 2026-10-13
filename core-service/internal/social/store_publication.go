package social

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PublicationStore covers the endpoint operations on sosyal.yayin.
type PublicationStore interface {
	// InsertPublication writes the publication in the preparing status. If the
	// slug belongs to another publication it returns errSlugConflict; the
	// service retries with a new slug.
	InsertPublication(ctx context.Context, input PublicationInsert) error
	// GetPublication and GetPublicationBySlug return the publication with
	// counters as seen by the viewer. A deleted publication is returned too
	// (Deleted true); if missing, ErrNotFound.
	GetPublication(ctx context.Context, id, viewerID string) (*PublicationRecord, error)
	GetPublicationBySlug(ctx context.Context, slug, viewerID string) (*PublicationRecord, error)
	// UpdatePublication changes only the filled fields of the owner's
	// non-deleted publication. If there is no row, ErrNotFound.
	UpdatePublication(ctx context.Context, id, ownerID string, patch PublicationPatch) error
	// DeletePublication soft-deletes (silinme = now()). If there is no row,
	// the caller is not the owner, or it is already deleted, ErrNotFound.
	DeletePublication(ctx context.Context, id, ownerID string) error
	// RemovePublication deletes the publication permanently. It exists only to
	// roll back a publication that never entered the queue.
	RemovePublication(ctx context.Context, id string) error
	// ResetForRetry puts the owner's failed publication back to preparing. If
	// the publication exists but is not failed, ErrInvalidStatus; if it does
	// not exist, ErrNotFound.
	ResetForRetry(ctx context.Context, id, ownerID string) error
}

// PreparationStore holds the operations used by the sosyal:hazirla job.
type PreparationStore interface {
	// GetPreparation returns the row, or ErrNotFound.
	GetPreparation(ctx context.Context, id string) (*PreparationRecord, error)
	// Publish moves a non-deleted publication in the preparing status to
	// published with the result. If the status has changed (deleted, already
	// published) it does nothing.
	Publish(ctx context.Context, id string, result PreparationResult) error
	// MarkFailed marks a publication in the preparing status as failed.
	MarkFailed(ctx context.Context, id string) error
}

// summarySelect are the columns of PublicationSummary. $1 is always the
// viewer. The owner profile is an INNER JOIN: a publication cannot exist
// without a profile, and account deletion removes both together. The source
// is a LEFT JOIN: if the original was deleted permanently, the cover's last
// known values are used.
const summarySelect = `
	y.id::text, y.slug, y.baslik, y.sahip_id::text, p.kullanici_adi, p.gorunen_ad,
	y.tur, y.bpm, y.ton_tonik, y.ton_mod, y.sure_sn, y.dalga_formu, y.altyapi_acik,
	y.kaynak_yayin_id::text,
	COALESCE(k.baslik, y.kaynak_baslik, ''),
	COALESCE(kp.kullanici_adi, y.kaynak_sahip_adi, ''),
	(k.id IS NULL OR k.silinme IS NOT NULL),
	y.olusturma,
	(SELECT count(*) FROM sosyal.begeni b WHERE b.yayin_id = y.id),
	(SELECT count(*) FROM sosyal.yorum c WHERE c.yayin_id = y.id AND c.silinme IS NULL),
	(SELECT count(*) FROM sosyal.yayin cv
	  WHERE cv.kaynak_yayin_id = y.id AND cv.durum = 'yayinda' AND cv.silinme IS NULL),
	EXISTS (SELECT 1 FROM sosyal.begeni b WHERE b.yayin_id = y.id AND b.kullanici_id = $1)`

const summaryFrom = `
	FROM sosyal.yayin y
	JOIN sosyal.profil p ON p.kullanici_id = y.sahip_id
	LEFT JOIN sosyal.yayin k ON k.id = y.kaynak_yayin_id
	LEFT JOIN sosyal.profil kp ON kp.kullanici_id = k.sahip_id`

// summaryScanner holds the scan destinations of the summarySelect columns.
type summaryScanner struct {
	base            PublicationSummary
	ownerID         string
	sourceID        *string
	sourceTitle     string
	sourceUsername  string
	sourceDeleted   bool
	waveform        []float64
	createdAt       time.Time
	likes, comments int
	covers          int
	likedByMe       bool
	tonic, mode     string
}

func (sc *summaryScanner) targets() []any {
	return []any{
		&sc.base.ID, &sc.base.Slug, &sc.base.Title, &sc.ownerID, &sc.base.Owner.Username, &sc.base.Owner.DisplayName,
		&sc.base.Genre, &sc.base.BPM, &sc.tonic, &sc.mode, &sc.base.DurationSec, &sc.waveform, &sc.base.BackingEnabled,
		&sc.sourceID, &sc.sourceTitle, &sc.sourceUsername, &sc.sourceDeleted,
		&sc.createdAt, &sc.likes, &sc.comments, &sc.covers, &sc.likedByMe,
	}
}

func (sc *summaryScanner) summary() PublicationSummary {
	out := sc.base
	out.Owner.AvatarColor = AvatarColor(sc.ownerID)
	out.MusicalKey = MusicalKey{Tonic: sc.tonic, Mode: sc.mode}
	out.Waveform = sc.waveform
	if out.Waveform == nil {
		out.Waveform = []float64{}
	}
	out.LikeCount, out.CommentCount, out.CoverCount, out.LikedByMe = sc.likes, sc.comments, sc.covers, sc.likedByMe
	out.CreatedAt = sc.createdAt.UTC()
	if sc.sourceID != nil {
		out.Source = &SourceSummary{
			PublicationID: *sc.sourceID, Title: sc.sourceTitle,
			OwnerUsername: sc.sourceUsername, Deleted: sc.sourceDeleted,
		}
	}
	return out
}

// getPublication runs the detail query with the given condition ($2).
func (s *PostgresStore) getPublication(ctx context.Context, condition, value, viewerID string) (*PublicationRecord, error) {
	var (
		sc        summaryScanner
		record    PublicationRecord
		lyrics    Lyrics
		structure []StructureItem
		master    *string
		hasStems  bool
	)
	dests := append(sc.targets(),
		&record.Description, &record.CommentsEnabled, &lyrics, &structure, &record.Status, &record.Visibility,
		&record.Deleted, &hasStems, &master, &record.ViewerFollows)
	err := s.pool.QueryRow(ctx, `
		SELECT `+summarySelect+`,
		  y.aciklama, y.yorum_acik, y.sozler, y.yapi, y.durum, y.gorunurluk,
		  y.silinme IS NOT NULL,
		  (jsonb_typeof(y.hazirlik->'stems') = 'array' AND jsonb_array_length(y.hazirlik->'stems') > 0),
		  y.master_anahtari,
		  EXISTS (SELECT 1 FROM sosyal.takip tk WHERE tk.takipci_id = $1 AND tk.takip_edilen_id = y.sahip_id)
		`+summaryFrom+`
		WHERE `+condition+` = $2`, viewerID, value).Scan(dests...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the publication: %w", err)
	}
	record.PublicationSummary = sc.summary()
	record.OwnerID = sc.ownerID
	if sc.sourceID != nil {
		record.SourcePublicationID = *sc.sourceID
	}
	record.Lyrics = lyrics
	if record.Lyrics.Sections == nil {
		record.Lyrics.Sections = []LyricsSection{}
	}
	for i := range record.Lyrics.Sections {
		if record.Lyrics.Sections[i].Lines == nil {
			record.Lyrics.Sections[i].Lines = []string{}
		}
	}
	record.Structure = structure
	if record.Structure == nil {
		record.Structure = []StructureItem{}
	}
	record.HasStems = hasStems
	if master != nil {
		record.MasterKey = *master
	}
	record.IsMine = record.OwnerID == viewerID
	return &record, nil
}

func (s *PostgresStore) GetPublication(ctx context.Context, id, viewerID string) (*PublicationRecord, error) {
	return s.getPublication(ctx, "y.id", id, viewerID)
}

func (s *PostgresStore) GetPublicationBySlug(ctx context.Context, slug, viewerID string) (*PublicationRecord, error) {
	return s.getPublication(ctx, "y.slug", slug, viewerID)
}

func (s *PostgresStore) InsertPublication(ctx context.Context, input PublicationInsert) error {
	var source any
	if input.SourcePublicationID != "" {
		source = input.SourcePublicationID
	}
	if input.Preparation.Stems == nil {
		input.Preparation.Stems = []PreparationStem{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, aciklama, tur, bpm, ton_tonik, ton_mod,
			sure_sn, gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik,
			kaynak_yayin_id, kaynak_baslik, kaynak_sahip_adi, durum)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, NULLIF($18, ''), NULLIF($19, ''), 'hazirlaniyor')`,
		input.ID, input.Slug, input.OwnerID, input.Title, input.Description, input.Genre, input.BPM, input.MusicalKey.Tonic, input.MusicalKey.Mode,
		input.DurationSec, input.Visibility, input.BackingEnabled, input.CommentsEnabled, input.Lyrics, input.Structure, input.Preparation,
		source, input.SourceTitle, input.SourceOwnerUsername)
	if constraint, ok := uniqueViolation(err); ok && constraint == "yayin_slug_key" {
		return errSlugConflict
	}
	if err != nil {
		return fmt.Errorf("could not add the publication: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdatePublication(ctx context.Context, id, ownerID string, patch PublicationPatch) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE sosyal.yayin
		   SET aciklama     = COALESCE($3, aciklama),
		       altyapi_acik = COALESCE($4, altyapi_acik),
		       gorunurluk   = COALESCE($5, gorunurluk),
		       yorum_acik   = COALESCE($6, yorum_acik)
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL`,
		id, ownerID, patch.Description, patch.BackingEnabled, patch.Visibility, patch.CommentsEnabled)
	if err != nil {
		return fmt.Errorf("could not update the publication: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeletePublication(ctx context.Context, id, ownerID string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE sosyal.yayin SET silinme = now()
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL`, id, ownerID)
	if err != nil {
		return fmt.Errorf("could not delete the publication: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) RemovePublication(ctx context.Context, id string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1 AND durum = 'hazirlaniyor'`, id); err != nil {
		return fmt.Errorf("could not unpublish the publication: %w", err)
	}
	return nil
}

func (s *PostgresStore) ResetForRetry(ctx context.Context, id, ownerID string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE sosyal.yayin SET durum = 'hazirlaniyor'
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL AND durum = 'hata'`, id, ownerID)
	if err != nil {
		return fmt.Errorf("could not queue the publication for retry: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM sosyal.yayin WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL)`,
		id, ownerID).Scan(&exists); err != nil {
		return fmt.Errorf("could not read the publication: %w", err)
	}
	if exists {
		return ErrInvalidStatus
	}
	return ErrNotFound
}

func (s *PostgresStore) GetPreparation(ctx context.Context, id string) (*PreparationRecord, error) {
	var record PreparationRecord
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, sahip_id::text, durum, silinme IS NOT NULL, hazirlik
		  FROM sosyal.yayin WHERE id = $1`, id,
	).Scan(&record.ID, &record.OwnerID, &record.Status, &record.Deleted, &record.Preparation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the preparation data: %w", err)
	}
	return &record, nil
}

func (s *PostgresStore) Publish(ctx context.Context, id string, result PreparationResult) error {
	stems := result.Stems
	if stems == nil {
		stems = []StemRecord{}
	}
	waveform := result.Waveform
	if waveform == nil {
		waveform = []float64{}
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE sosyal.yayin
		   SET durum = 'yayinda', master_anahtari = $2, stem_listesi = $3,
		       dalga_formu = $4, onizleme_anahtari = NULLIF($5, '')
		 WHERE id = $1 AND durum = 'hazirlaniyor' AND silinme IS NULL`,
		id, result.MasterKey, stems, waveform, result.PreviewKey)
	if err != nil {
		return fmt.Errorf("could not publish the publication: %w", err)
	}
	return nil
}

func (s *PostgresStore) MarkFailed(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sosyal.yayin SET durum = 'hata' WHERE id = $1 AND durum = 'hazirlaniyor' AND silinme IS NULL`, id)
	if err != nil {
		return fmt.Errorf("could not mark the publication as failed: %w", err)
	}
	return nil
}
