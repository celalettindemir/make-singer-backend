package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
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
	// fetch downloads a signed URL; replaced in tests.
	fetch func(ctx context.Context, url string) ([]byte, error)
}

// maxHumFetch bounds the converted mp3 read back for transcription (a 30 s
// hum at 192 kbit/s is under 1 MB).
const maxHumFetch = 4 << 20

func fetchURL(ctx context.Context, url string) ([]byte, error) {
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
	return io.ReadAll(io.LimitReader(resp.Body, maxHumFetch))
}

func NewHumService(storage client.StorageClient, r2Cfg *config.R2Config, preparer client.HumPreparer, transcriber client.Transcriber, timeout time.Duration) *HumService {
	return &HumService{storage: storage, r2Cfg: r2Cfg, preparer: preparer, transcriber: transcriber, timeout: timeout, fetch: fetchURL}
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
		// audio-service may have written the mp3 before it failed (a timeout
		// on the response, say); remove it best-effort.
		s.deleteBestEffort(ctx, mp3Key)
		return nil, fmt.Errorf("could not prepare the hum: %w", err)
	}
	if prep.DurationMs < humAcceptMinMs || prep.DurationMs > humAcceptMaxMs {
		s.deleteBestEffort(ctx, mp3Key)
		return nil, &HumDurationError{DurationMs: prep.DurationMs}
	}

	fileURL, _, err := s.storage.URLFor(ctx, mp3Key)
	if err != nil {
		s.deleteBestEffort(ctx, mp3Key)
		return nil, fmt.Errorf("could not sign the hum URL: %w", err)
	}

	var transcript *model.HumTranscript
	if lines := s.transcribe(ctx, data, ext, fileURL, lang); len(lines) > 0 {
		transcript = &model.HumTranscript{Lines: lines}
	}
	log.Printf("hum upload: user=%s project=%s hum=%s durationMs=%d lines=%d",
		userID, projectID, humID, prep.DurationMs, len(transcriptLines(transcript)))
	return &model.HumUploadResponse{HumID: humID, FileURL: fileURL, DurationMs: prep.DurationMs, Transcript: transcript}, nil
}

func (s *HumService) deleteBestEffort(ctx context.Context, key string) {
	if err := s.storage.Delete(ctx, key); err != nil {
		log.Printf("hum upload: could not delete %s: %v", key, err)
	}
}

func transcriptLines(t *model.HumTranscript) []string {
	if t == nil {
		return nil
	}
	return t.Lines
}

// transcribe returns the lyric lines or nil when there are none, the
// transcriber is missing, or it fails or runs out of time.
func (s *HumService) transcribe(ctx context.Context, data []byte, ext, mp3URL string, lang model.Language) []string {
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
	// OpenAI does not accept raw AAC: transcribe the converted mp3 instead.
	if ext == "aac" {
		mp3, err := s.fetch(tctx, mp3URL)
		if err != nil || len(mp3) == 0 {
			log.Printf("hum upload: could not read the mp3 for transcription: %v", err)
			return nil
		}
		data, ext = mp3, "mp3"
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
