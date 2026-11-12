package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// Transcription is the speech-to-text result. Segments is only filled by
// models that return them (whisper-1 verbose_json); entries are trimmed.
type Transcription struct {
	Text     string
	Segments []string
}

// Transcriber turns a short recording into text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, filename, language string) (*Transcription, error)
	IsConfigured() bool
}

// OpenAITranscriber calls OpenAI's /audio/transcriptions. The deadline comes
// from the caller's context, so the primary and fallback models share it.
type OpenAITranscriber struct {
	httpClient    *http.Client
	baseURL       string
	apiKey        string
	model         string
	fallbackModel string
}

var _ Transcriber = (*OpenAITranscriber)(nil)

// NewOpenAITranscriber creates a transcription client.
func NewOpenAITranscriber(openai *config.OpenAIConfig, hum *config.HumConfig) *OpenAITranscriber {
	return &OpenAITranscriber{
		httpClient:    &http.Client{},
		baseURL:       strings.TrimRight(openai.BaseURL, "/"),
		apiKey:        openai.APIKey,
		model:         hum.TranscribeModel,
		fallbackModel: hum.TranscribeFallbackModel,
	}
}

// IsConfigured reports whether an API key is set.
func (t *OpenAITranscriber) IsConfigured() bool { return t.apiKey != "" }

// Transcribe tries the primary model, then the fallback model when the
// primary failed and ctx is still alive.
func (t *OpenAITranscriber) Transcribe(ctx context.Context, audio []byte, filename, language string) (*Transcription, error) {
	res, err := t.transcribeWith(ctx, t.model, audio, filename, language)
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil || t.fallbackModel == "" || t.fallbackModel == t.model {
		return nil, err
	}
	res, fbErr := t.transcribeWith(ctx, t.fallbackModel, audio, filename, language)
	if fbErr != nil {
		return nil, fmt.Errorf("%w (fallback: %v)", err, fbErr)
	}
	return res, nil
}

func (t *OpenAITranscriber) transcribeWith(ctx context.Context, model string, audio []byte, filename, language string) (*Transcription, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	format := "json"
	if model == "whisper-1" {
		format = "verbose_json"
	}
	_ = w.WriteField("model", model)
	_ = w.WriteField("response_format", format)
	if language != "" {
		_ = w.WriteField("language", language)
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai transcribe (%s): %w", model, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("openai transcribe (%s): %w", model, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("openai transcribe (%s): status %d: %s", model, resp.StatusCode, truncateForLog(string(raw)))
	}

	var parsed struct {
		Text     string `json:"text"`
		Segments []struct {
			Text string `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("openai transcribe (%s): bad response: %w", model, err)
	}
	out := &Transcription{Text: strings.TrimSpace(parsed.Text)}
	for _, s := range parsed.Segments {
		if txt := strings.TrimSpace(s.Text); txt != "" {
			out.Segments = append(out.Segments, txt)
		}
	}
	return out, nil
}

func truncateForLog(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
