package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// The accepted duration has a 500 ms tolerance: the app stops at 30 s and the
// mp3 encoding can add a few tens of ms.
const (
	humAcceptMinMs = model.HumMinMs - 500
	humAcceptMaxMs = model.HumMaxMs + 500
	mockHumMs      = 12000
)

var (
	ErrHumUnreadable  = errors.New("hum audio could not be read")
	ErrHumUnavailable = errors.New("hum preparation is not available")
)

// HumDurationError is returned when the measured length is out of range.
type HumDurationError struct{ DurationMs int }

func (e *HumDurationError) Error() string {
	return fmt.Sprintf("hum duration %d ms is out of range", e.DurationMs)
}

var humExtByContentType = map[string]string{
	"audio/wav": "wav", "audio/x-wav": "wav", "audio/wave": "wav", "audio/vnd.wave": "wav",
	"audio/mpeg": "mp3", "audio/mp3": "mp3",
	"audio/mp4": "m4a", "audio/x-m4a": "m4a",
	"audio/aac": "aac", "audio/x-aac": "aac",
}

// HumService stores a hum as mp3 in R2 and reads the words out of it.
type HumService struct {
	storage     client.StorageClient
	r2Cfg       *config.R2Config
	preparer    client.HumPreparer
	transcriber client.Transcriber
	timeout     time.Duration
}

func NewHumService(storage client.StorageClient, r2Cfg *config.R2Config, preparer client.HumPreparer, transcriber client.Transcriber, timeout time.Duration) *HumService {
	return &HumService{storage: storage, r2Cfg: r2Cfg, preparer: preparer, transcriber: transcriber, timeout: timeout}
}

// Upload writes the source, has audio-service measure and convert it to mp3,
// checks the length, then transcribes within the timeout. A transcription
// failure never fails the upload.
func (s *HumService) Upload(ctx context.Context, userID, projectID string, lang model.Language, contentType string, data []byte) (*model.HumUploadResponse, error) {
	humID := uuid.New().String()
	ext := humExtByContentType[contentType]
	if ext == "" {
		ext = "wav"
	}
	srcKey, err := client.UserKey("hums", userID, projectID, humID+"-src."+ext)
	if err != nil {
		return nil, err
	}
	mp3Key, err := client.UserKey("hums", userID, projectID, humID+".mp3")
	if err != nil {
		return nil, err
	}

	if s.storage == nil {
		log.Printf("hum upload: user=%s project=%s hum=%s durationMs=%d lines=0", userID, projectID, humID, mockHumMs)
		return &model.HumUploadResponse{
			HumID:      humID,
			FileURL:    client.UnsignedURL(mp3Key, s.r2Cfg),
			DurationMs: mockHumMs,
		}, nil
	}
	if s.preparer == nil {
		return nil, ErrHumUnavailable
	}

	if _, _, err := s.storage.Upload(ctx, srcKey, bytes.NewReader(data), contentType); err != nil {
		return nil, fmt.Errorf("could not upload the hum source: %w", err)
	}
	prep, err := s.preparer.PrepareHum(ctx, &client.HumPrepareRequest{InputKey: srcKey, OutputKey: mp3Key})
	// The source is not needed after conversion, whatever the outcome.
	if delErr := s.storage.Delete(ctx, srcKey); delErr != nil {
		log.Printf("hum upload: could not delete source %s: %v", srcKey, delErr)
	}
	if err != nil {
		if errors.Is(err, client.ErrAudioUnprocessable) {
			return nil, ErrHumUnreadable
		}
		return nil, fmt.Errorf("could not prepare the hum: %w", err)
	}
	if prep.DurationMs < humAcceptMinMs || prep.DurationMs > humAcceptMaxMs {
		if delErr := s.storage.Delete(ctx, mp3Key); delErr != nil {
			log.Printf("hum upload: could not delete %s: %v", mp3Key, delErr)
		}
		return nil, &HumDurationError{DurationMs: prep.DurationMs}
	}

	var transcript *model.HumTranscript
	if lines := s.transcribe(ctx, data, ext, lang); len(lines) > 0 {
		transcript = &model.HumTranscript{Lines: lines}
	}

	fileURL, _, err := s.storage.URLFor(ctx, mp3Key)
	if err != nil {
		return nil, fmt.Errorf("could not sign the hum URL: %w", err)
	}
	log.Printf("hum upload: user=%s project=%s hum=%s durationMs=%d lines=%d",
		userID, projectID, humID, prep.DurationMs, len(transcriptLines(transcript)))
	return &model.HumUploadResponse{HumID: humID, FileURL: fileURL, DurationMs: prep.DurationMs, Transcript: transcript}, nil
}

func transcriptLines(t *model.HumTranscript) []string {
	if t == nil {
		return nil
	}
	return t.Lines
}

// transcribe returns the lyric lines or nil when there are none, the
// transcriber is missing, or it fails or runs out of time.
func (s *HumService) transcribe(ctx context.Context, data []byte, ext string, lang model.Language) []string {
	if s.transcriber == nil || !s.transcriber.IsConfigured() {
		return nil
	}
	tctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	apiLang := ""
	switch lang {
	case model.LanguageTR, model.LanguageEN, model.LanguageFR:
		apiLang = string(lang)
	default:
		lang = ""
	}
	t, err := s.transcriber.Transcribe(tctx, data, "hum."+ext, apiLang)
	if err != nil {
		log.Printf("hum upload: transcription failed: %v", err)
		return nil
	}
	return HumLines(t, lang)
}

// Delete removes the hum's mp3. Missing objects are not an error; the key is
// built from the caller's own prefix, so another user's hum cannot be reached.
func (s *HumService) Delete(ctx context.Context, userID, projectID, humID string) error {
	key, err := client.UserKey("hums", userID, projectID, humID+".mp3")
	if err != nil {
		return err
	}
	if s.storage == nil {
		return nil
	}
	return s.storage.Delete(ctx, key)
}
