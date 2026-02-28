package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// MusicGenerator defines the interface for music generation operations
type MusicGenerator interface {
	GenerateMusic(ctx context.Context, req *GenerateMusicRequest) (*GenerateMusicResponse, error)
	GetMusicStatus(ctx context.Context, taskID string) (*MusicResult, error)
	SeparateVocals(ctx context.Context, audioURL string) (*SeparationResult, error)
	SplitStems(ctx context.Context, audioURL string) (*StemSplitResult, error)
}

// SunoClient implements MusicGenerator for Suno API
type SunoClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// GenerateMusicRequest represents the request for music generation
type GenerateMusicRequest struct {
	Prompt           string `json:"prompt"`
	Style            string `json:"style,omitempty"`
	Title            string `json:"title,omitempty"`
	MakeInstrumental bool   `json:"make_instrumental,omitempty"`
}

// GenerateMusicResponse represents the response from music generation
type GenerateMusicResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// MusicResult represents a completed music generation result
type MusicResult struct {
	ID       string  `json:"id"`
	AudioURL string  `json:"audio_url"`
	Duration float64 `json:"duration"`
	Status   string  `json:"status"`
	Title    string  `json:"title,omitempty"`
	Style    string  `json:"style,omitempty"`
}

// SeparationResult represents vocal separation result
type SeparationResult struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	VocalURL   string `json:"vocal_url,omitempty"`
	BackingURL string `json:"backing_url,omitempty"`
}

// StemSplitResult represents stem splitting result
type StemSplitResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Stems  []Stem `json:"stems,omitempty"`
}

// Stem represents an individual stem from splitting
type Stem struct {
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Duration float64 `json:"duration"`
}

// NewSunoClient creates a new Suno API client
func NewSunoClient(cfg *config.SunoConfig) *SunoClient {
	return &SunoClient{
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
	}
}

// GenerateMusic initiates music generation
func (c *SunoClient) GenerateMusic(ctx context.Context, req *GenerateMusicRequest) (*GenerateMusicResponse, error) {
	var result GenerateMusicResponse
	if err := c.post(ctx, "/v1/music/generate", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetMusicStatus retrieves the status of a music generation task
func (c *SunoClient) GetMusicStatus(ctx context.Context, taskID string) (*MusicResult, error) {
	endpoint := fmt.Sprintf("/v1/music/status/%s", taskID)
	var result MusicResult
	if err := c.get(ctx, endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SeparateVocals initiates vocal separation for an audio file
func (c *SunoClient) SeparateVocals(ctx context.Context, audioURL string) (*SeparationResult, error) {
	req := map[string]string{"audio_url": audioURL}
	var result SeparationResult
	if err := c.post(ctx, "/v1/audio/separate-vocals", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SplitStems initiates stem splitting for an audio file
func (c *SunoClient) SplitStems(ctx context.Context, audioURL string) (*StemSplitResult, error) {
	req := map[string]string{"audio_url": audioURL}
	var result StemSplitResult
	if err := c.post(ctx, "/v1/audio/split-stems", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSeparationStatus retrieves the status of a vocal separation task
func (c *SunoClient) GetSeparationStatus(ctx context.Context, taskID string) (*SeparationResult, error) {
	endpoint := fmt.Sprintf("/v1/audio/separate-vocals/%s", taskID)
	var result SeparationResult
	if err := c.get(ctx, endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetStemSplitStatus retrieves the status of a stem split task
func (c *SunoClient) GetStemSplitStatus(ctx context.Context, taskID string) (*StemSplitResult, error) {
	endpoint := fmt.Sprintf("/v1/audio/split-stems/%s", taskID)
	var result StemSplitResult
	if err := c.get(ctx, endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// post sends a POST request with JSON body
func (c *SunoClient) post(ctx context.Context, endpoint string, body interface{}, result interface{}) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	return c.doRequest(req, result)
}

// get sends a GET request and parses JSON response
func (c *SunoClient) get(ctx context.Context, endpoint string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	return c.doRequest(req, result)
}

// doRequest executes an HTTP request and parses the response
func (c *SunoClient) doRequest(req *http.Request, result interface{}) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	log.Printf("[Suno API] → %s %s", req.Method, req.URL.String())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("[Suno API] ✗ %s %s — request failed: %v", req.Method, req.URL.String(), err)
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Suno API] ✗ %s %s — failed to read response: %v", req.Method, req.URL.String(), err)
		return fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("[Suno API] ← %d %s %s — %s", resp.StatusCode, req.Method, req.URL.String(), string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("suno API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		log.Printf("[Suno API] ✗ unmarshal error for %s %s: %v (body: %s)", req.Method, req.URL.String(), err, string(respBody))
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

// IsConfigured returns true if the client has valid configuration
func (c *SunoClient) IsConfigured() bool {
	return c.apiKey != ""
}

// PollMusicStatus polls for music generation completion
func (c *SunoClient) PollMusicStatus(ctx context.Context, taskID string, interval time.Duration, maxWait time.Duration) (*MusicResult, error) {
	deadline := time.Now().Add(maxWait)
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++
		result, err := c.GetMusicStatus(ctx, taskID)
		if err != nil {
			log.Printf("[Suno API] Poll music #%d (task=%s) — error: %v", attempt, taskID, err)
			return nil, err
		}

		log.Printf("[Suno API] Poll music #%d (task=%s) — status: %s", attempt, taskID, result.Status)

		switch result.Status {
		case "completed", "success":
			return result, nil
		case "failed", "error":
			return nil, fmt.Errorf("music generation failed: %s", result.Status)
		}

		select {
		case <-ctx.Done():
			log.Printf("[Suno API] Poll music (task=%s) — context cancelled", taskID)
			return nil, ctx.Err()
		case <-time.After(interval):
			continue
		}
	}

	return nil, fmt.Errorf("music generation timed out after %v", maxWait)
}

// PollStemSplitStatus polls for stem split completion
func (c *SunoClient) PollStemSplitStatus(ctx context.Context, taskID string, interval time.Duration, maxWait time.Duration) (*StemSplitResult, error) {
	deadline := time.Now().Add(maxWait)
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++
		result, err := c.GetStemSplitStatus(ctx, taskID)
		if err != nil {
			log.Printf("[Suno API] Poll stems #%d (task=%s) — error: %v", attempt, taskID, err)
			return nil, err
		}

		log.Printf("[Suno API] Poll stems #%d (task=%s) — status: %s", attempt, taskID, result.Status)

		switch result.Status {
		case "completed", "success":
			return result, nil
		case "failed", "error":
			return nil, fmt.Errorf("stem split failed: %s", result.Status)
		}

		select {
		case <-ctx.Done():
			log.Printf("[Suno API] Poll stems (task=%s) — context cancelled", taskID)
			return nil, ctx.Err()
		case <-time.After(interval):
			continue
		}
	}

	return nil, fmt.Errorf("stem split timed out after %v", maxWait)
}
