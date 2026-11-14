package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// SeparateRequest is the separation-service /ayir request. Keys are R2 keys.
type SeparateRequest struct {
	InputKey     string `json:"input_key"`
	OutputPrefix string `json:"output_prefix"`
}

// SeparatedStem is one written stem: instrument is vocal, drums, bass or rest.
type SeparatedStem struct {
	Instrument string `json:"instrument"`
	Key        string `json:"key"`
}

// SeparateResponse: the stems are already written to R2.
type SeparateResponse struct {
	DurationMs int             `json:"duration_ms"`
	Stems      []SeparatedStem `json:"stems"`
}

// Separator splits a track into stems.
type Separator interface {
	Separate(ctx context.Context, req *SeparateRequest) (*SeparateResponse, error)
	IsConfigured() bool
}

// SeparationClient talks to the Demucs separation service.
type SeparationClient struct {
	httpClient *http.Client
	baseURL    string
}

var _ Separator = (*SeparationClient)(nil)

// NewSeparationClient creates a client; the HTTP timeout is SeparationTimeout.
func NewSeparationClient(cfg *config.ImportConfig) *SeparationClient {
	return &SeparationClient{
		httpClient: &http.Client{Timeout: cfg.SeparationTimeout},
		baseURL:    strings.TrimRight(cfg.SeparationURL, "/"),
	}
}

// IsConfigured reports whether a service URL is set.
func (c *SeparationClient) IsConfigured() bool { return c.baseURL != "" }

// Separate calls /ayir. A 422 maps to ErrAudioUnprocessable; a 503 (model not
// loaded) is a plain error.
func (c *SeparationClient) Separate(ctx context.Context, req *SeparateRequest) (*SeparateResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("separation: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/ayir", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("separation: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("separation: read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return nil, fmt.Errorf("%w: %s", ErrAudioUnprocessable, truncateForLog(string(raw)))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("separation service error (status %d): %s", resp.StatusCode, truncateForLog(string(raw)))
	}
	var out SeparateResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("separation: bad response: %w", err)
	}
	return &out, nil
}
