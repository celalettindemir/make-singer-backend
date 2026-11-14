package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// timedModel is the only OpenAI model that returns segment and word times.
const timedModel = "whisper-1"

// timedMaxResponse caps the response read: verbose_json with word times for an
// 8 minute song is large.
const timedMaxResponse = 8 << 20

// TimedSegment is one whisper segment with its confidence signals.
type TimedSegment struct {
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	AvgLogprob       float64 `json:"avg_logprob"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
	CompressionRatio float64 `json:"compression_ratio"`
}

// TimedWord is one word with its time in seconds.
type TimedWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// TimedTranscription is a whisper-1 verbose_json result. Text is not kept;
// callers build lines from Segments and Words.
type TimedTranscription struct {
	Language string
	Segments []TimedSegment
	Words    []TimedWord
}

// TimedTranscriber transcribes a recording with segment and word times.
type TimedTranscriber interface {
	TranscribeTimed(ctx context.Context, audio []byte, filename string) (*TimedTranscription, error)
	IsConfigured() bool
}

var _ TimedTranscriber = (*OpenAITranscriber)(nil)

// TranscribeTimed uses whisper-1 with segment and word timestamps. The language
// is auto-detected and the deadline comes from ctx.
func (t *OpenAITranscriber) TranscribeTimed(ctx context.Context, audio []byte, filename string) (*TimedTranscription, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	_ = w.WriteField("model", timedModel)
	_ = w.WriteField("response_format", "verbose_json")
	_ = w.WriteField("timestamp_granularities[]", "segment")
	_ = w.WriteField("timestamp_granularities[]", "word")
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
		return nil, fmt.Errorf("openai transcribe timed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, timedMaxResponse))
	if err != nil {
		return nil, fmt.Errorf("openai transcribe timed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("openai transcribe timed: status %d: %s", resp.StatusCode, truncateForLog(string(raw)))
	}

	var parsed struct {
		Language string         `json:"language"`
		Segments []TimedSegment `json:"segments"`
		Words    []TimedWord    `json:"words"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("openai transcribe timed: bad response: %w", err)
	}
	return &TimedTranscription{Language: parsed.Language, Segments: parsed.Segments, Words: parsed.Words}, nil
}
