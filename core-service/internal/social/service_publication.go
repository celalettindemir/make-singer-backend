package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/makeasinger/api/internal/i18n"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
)

// Queue tasks. The constants are task type names; main binds them to the
// handlers.
const (
	// TaskTypePrepare copies the publication's files, analyzes them and
	// publishes it (social queue).
	TaskTypePrepare = "sosyal:hazirla"
	// TaskTypeDeleteFiles cleans the deleted publication's sosyal/{id}/ prefix
	// after 24 hours (maintenance queue).
	TaskTypeDeleteFiles = "sosyal:dosya-sil"

	// TaskTypeSweepStale fails publications stuck in the preparing status
	// (social queue, scheduled periodically).
	TaskTypeSweepStale = "sosyal:bayat-tara"

	// QueueSocial is the asynq queue of the prepare tasks.
	QueueSocial = "sosyal"

	prepareMaxRetry     = 3
	prepareRetention    = 24 * time.Hour
	deleteFilesDelay    = 24 * time.Hour
	deleteFilesMaxRetry = 10
	slugAttempts        = 5
)

// TaskPayload is the payload of both social tasks.
type TaskPayload struct {
	PublicationID string `json:"yayinId"`
}

func encodeTaskPayload(pubID string) []byte {
	payload, _ := json.Marshal(TaskPayload{PublicationID: pubID})
	return payload
}

func (s *Service) enqueuePrepare(pubID string) error {
	_, err := s.queue.Enqueue(asynq.NewTask(TaskTypePrepare, encodeTaskPayload(pubID)),
		asynq.Queue(QueueSocial),
		asynq.MaxRetry(prepareMaxRetry),
		asynq.Retention(prepareRetention),
	)
	return err
}

// resolveKey turns the address the client sent (a full URL or a bare key)
// into a key, with an ownership check. A file in the public bucket
// (exports/) is rejected: the copy is made within the same bucket.
func (s *Service) resolveKey(raw, userID, field string) (string, error) {
	key, err := client.ResolveOwnedKeyOrURL(raw, userID, s.r2)
	switch {
	case errors.Is(err, client.ErrKeyNotOwned):
		return "", ErrOwnership
	case err != nil:
		return "", invalid(i18n.KeyFieldFileURLInvalid, field)
	case client.IsPublicKey(key):
		return "", invalid(i18n.KeyFieldFileNotPrivate, field)
	}
	return key, nil
}

// resolveSource validates a cover's original: it must be visible to the
// viewer, published, not a cover itself and have backing enabled.
func (s *Service) resolveSource(ctx context.Context, rawID, viewerID string) (*PublicationRecord, error) {
	id, ok := parseID(rawID)
	if !ok {
		return nil, ErrSourceNotFound
	}
	rec, err := s.store.GetPublication(ctx, id, viewerID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrSourceNotFound
	}
	if err != nil {
		return nil, err
	}
	if !CanSeeDetail(rec.visibilityInput(viewerID)) || rec.Status != StatusPublished || rec.SourcePublicationID != "" {
		return nil, ErrSourceNotFound
	}
	if !rec.BackingEnabled {
		return nil, ErrBackingDisabled
	}
	return rec, nil
}

// CreatePublication saves the publication in the preparing status and
// enqueues the sosyal:hazirla task (POST /yayinlar, 202).
func (s *Service) CreatePublication(ctx context.Context, userID string, req PublicationRequest) (*PublicationResponse, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.GetProfile(ctx, id); err != nil {
		return nil, err
	}
	if err := normalizePublicationRequest(&req); err != nil {
		return nil, err
	}

	insert := PublicationInsert{
		ID: uuid.NewString(), OwnerID: id,
		Title: req.Title, Description: req.Description, Genre: req.Genre, BPM: req.BPM, MusicalKey: req.MusicalKey,
		DurationSec: req.DurationSec, Visibility: req.Visibility, BackingEnabled: req.BackingEnabled,
		CommentsEnabled: req.CommentsEnabled, Lyrics: req.Lyrics, Structure: req.Structure,
		Preparation: Preparation{Stems: []PreparationStem{}},
	}
	if req.SourcePublicationID != nil {
		source, err := s.resolveSource(ctx, *req.SourcePublicationID, id)
		if err != nil {
			return nil, err
		}
		insert.SourcePublicationID = source.ID
		insert.SourceTitle = source.Title
		insert.SourceOwnerUsername = source.Owner.Username
	}

	if insert.Preparation.MasterKey, err = s.resolveKey(req.MasterURL, id, "masterUrl"); err != nil {
		return nil, err
	}
	for _, stem := range req.Stems {
		key, err := s.resolveKey(stem.URL, id, "stem url")
		if err != nil {
			return nil, err
		}
		insert.Preparation.Stems = append(insert.Preparation.Stems, PreparationStem{
			ID: stem.ID, Instrument: stem.Instrument, Duration: stem.Duration, Key: key,
		})
	}

	for attempt := 1; ; attempt++ {
		if insert.Slug, err = generateSlug(); err != nil {
			return nil, fmt.Errorf("could not generate a slug: %w", err)
		}
		err = s.store.InsertPublication(ctx, insert)
		if !errors.Is(err, errSlugConflict) || attempt == slugAttempts {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	if err := s.enqueuePrepare(insert.ID); err != nil {
		// A publication that is not enqueued is never prepared; it is rolled
		// back instead of leaving an orphaned "hazirlaniyor" row. The rollback
		// must run even if the request context was cancelled (e.g. on
		// shutdown).
		if rollbackErr := s.store.RemovePublication(context.WithoutCancel(ctx), insert.ID); rollbackErr != nil {
			log.Printf("Warning: could not roll back the publication that was not enqueued (%s): %v", insert.ID, rollbackErr)
		}
		return nil, fmt.Errorf("could not build the preparation job: %w", err)
	}
	return &PublicationResponse{ID: insert.ID, Slug: insert.Slug, Status: StatusPreparing}, nil
}

// visiblePublication loads the publication and applies detail visibility.
func (s *Service) visiblePublication(ctx context.Context, viewerID, rawPubID string) (*PublicationRecord, error) {
	pubID, ok := parseID(rawPubID)
	if !ok {
		return nil, ErrNotFound
	}
	rec, err := s.store.GetPublication(ctx, pubID, viewerID)
	if err != nil {
		return nil, err
	}
	if !CanSeeDetail(rec.visibilityInput(viewerID)) {
		return nil, ErrNotFound
	}
	return rec, nil
}

// ownedPublication loads the publication and checks that the caller owns it
// and that it is not deleted. Otherwise it is ErrNotFound: the existence of
// someone else's publication is not leaked either.
func (s *Service) ownedPublication(ctx context.Context, ownerID, rawPubID string) (*PublicationRecord, error) {
	pubID, ok := parseID(rawPubID)
	if !ok {
		return nil, ErrNotFound
	}
	rec, err := s.store.GetPublication(ctx, pubID, ownerID)
	if err != nil {
		return nil, err
	}
	if rec.OwnerID != ownerID || rec.Deleted {
		return nil, ErrNotFound
	}
	return rec, nil
}

// Publication is GET /yayinlar/{id}.
func (s *Service) Publication(ctx context.Context, userID, pubID string) (*PublicationDetail, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	return &rec.PublicationDetail, nil
}

// PublicationBySlug is GET /s/{slug}.
func (s *Service) PublicationBySlug(ctx context.Context, userID, slug string) (*PublicationDetail, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	if !ValidSlug(slug) {
		return nil, ErrNotFound
	}
	rec, err := s.store.GetPublicationBySlug(ctx, slug, id)
	if err != nil {
		return nil, err
	}
	if !CanSeeDetail(rec.visibilityInput(id)) {
		return nil, ErrNotFound
	}
	return &rec.PublicationDetail, nil
}

// UpdatePublication is PATCH /yayinlar/{id}. Backing can be re-enabled only
// on originals that have stem copies (backing was enabled when they were
// published).
func (s *Service) UpdatePublication(ctx context.Context, userID, pubID string, patch PublicationPatch) (*PublicationDetail, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	if err := normalizePatch(&patch); err != nil {
		return nil, err
	}
	rec, err := s.ownedPublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if patch.BackingEnabled != nil && *patch.BackingEnabled && !rec.BackingEnabled && (!rec.HasStems || rec.SourcePublicationID != "") {
		return nil, ErrNoStems
	}
	if err := s.store.UpdatePublication(ctx, rec.ID, id, patch); err != nil {
		return nil, err
	}
	return s.Publication(ctx, id, rec.ID)
}

// DeletePublication is DELETE /yayinlar/{id}: it soft-deletes and schedules
// the file cleanup 24 hours later. A queue error is logged; account deletion
// cleans the same prefix again.
func (s *Service) DeletePublication(ctx context.Context, userID, pubID string) error {
	id, err := parseUserID(userID)
	if err != nil {
		return err
	}
	pubUUID, ok := parseID(pubID)
	if !ok {
		return ErrNotFound
	}
	if err := s.store.DeletePublication(ctx, pubUUID, id); err != nil {
		return err
	}
	if _, err := s.queue.Enqueue(asynq.NewTask(TaskTypeDeleteFiles, encodeTaskPayload(pubUUID)),
		asynq.Queue("bakim"),
		asynq.ProcessIn(deleteFilesDelay),
		asynq.MaxRetry(deleteFilesMaxRetry),
	); err != nil {
		log.Printf("Warning: could not build the publication file cleanup (%s): %v", pubUUID, err)
	}
	return nil
}

// Retry is POST /yayinlar/{id}/tekrar-dene: it puts a publication in the
// failed status back into preparation.
func (s *Service) Retry(ctx context.Context, userID, pubID string) (*PublicationResponse, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	rec, err := s.ownedPublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if err := s.store.ResetForRetry(ctx, rec.ID, id); err != nil {
		return nil, err
	}
	if err := s.enqueuePrepare(rec.ID); err != nil {
		// The publication must not stay in "hazirlaniyor" even when the request
		// context was cancelled.
		if failErr := s.store.MarkFailed(context.WithoutCancel(ctx), rec.ID); failErr != nil {
			log.Printf("Warning: could not mark the publication that was not enqueued as failed (%s): %v", rec.ID, failErr)
		}
		return nil, fmt.Errorf("could not build the preparation job: %w", err)
	}
	return &PublicationResponse{ID: rec.ID, Slug: rec.Slug, Status: StatusPreparing}, nil
}

// Files is POST /yayinlar/{id}/dosyalar: a signed listening address for the
// master copy (R2_PRESIGN_TTL, default 1 hour).
func (s *Service) Files(ctx context.Context, userID, pubID string) (*PublicationFiles, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if rec.Status != StatusPublished || rec.MasterKey == "" {
		return nil, ErrInvalidStatus
	}
	url, expiresAt, err := client.ResolveURL(ctx, s.storage, s.r2, rec.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("could not generate the listen URL: %w", err)
	}
	address := FileLink{URL: url}
	if !expiresAt.IsZero() {
		address.ExpiresAt = &expiresAt
	}
	return &PublicationFiles{Master: address}, nil
}
