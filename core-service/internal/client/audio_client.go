package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// AudioProcessor defines the interface for audio processing operations
type AudioProcessor interface {
	Master(ctx context.Context, req *MasterRequest) (*MasterResponse, error)
	Encode(ctx context.Context, req *EncodeRequest) (*EncodeResponse, error)
	CreateZip(ctx context.Context, req *ZipRequest) (*ZipResponse, error)
	HealthCheck(ctx context.Context) error
}

// AudioClient implements AudioProcessor for the Python microservice
type AudioClient struct {
	httpClient *http.Client
	baseURL    string
}

// MixChannel represents volume settings for a single channel
type MixChannel struct {
	StemURL string  `json:"stem_url"`
	Volume  float64 `json:"volume"`
	Pan     float64 `json:"pan,omitempty"`
	Mute    bool    `json:"mute,omitempty"`
	Solo    bool    `json:"solo,omitempty"`
}

// VocalTakeInput represents a vocal take for mixing
type VocalTakeInput struct {
	URL    string  `json:"url"`
	Volume float64 `json:"volume"`
	Pan    float64 `json:"pan,omitempty"`
}

// MasterRequest represents the request for mastering
type MasterRequest struct {
	StemURLs    []string         `json:"stem_urls"`
	MixSettings []MixChannel     `json:"mix_settings"`
	Profile     string           `json:"profile"`
	VocalTakes  []VocalTakeInput `json:"vocal_takes,omitempty"`
	OutputKey   string           `json:"output_key"`
}

// MasterResponse represents the response from mastering
type MasterResponse struct {
	OutputURL string  `json:"output_url"`
	Duration  float64 `json:"duration"`
	PeakDb    float64 `json:"peak_db"`
	LUFS      float64 `json:"lufs"`
}

// EncodeRequest represents the request for audio encoding
type EncodeRequest struct {
	InputURL   string            `json:"input_url"`
	Format     string            `json:"format"`
	Quality    int               `json:"quality,omitempty"`
	SampleRate int               `json:"sample_rate,omitempty"`
	BitDepth   int               `json:"bit_depth,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	OutputKey  string            `json:"output_key"`
}

// EncodeResponse represents the response from encoding
type EncodeResponse struct {
	OutputURL string `json:"output_url"`
	Format    string `json:"format"`
	Size      int64  `json:"size"`
}

// ZipRequest represents the request for creating a ZIP archive
type ZipRequest struct {
	Files     []ZipFileEntry `json:"files"`
	OutputKey string         `json:"output_key"`
}

// ZipFileEntry represents a file to include in the ZIP
type ZipFileEntry struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

// ZipResponse represents the response from ZIP creation
type ZipResponse struct {
	OutputURL string `json:"output_url"`
	Size      int64  `json:"size"`
	FileCount int    `json:"file_count"`
}

// NewAudioClient creates a new audio processing client
func NewAudioClient(cfg *config.AudioConfig) *AudioClient {
	return &AudioClient{
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.Timeout) * time.Second,
		},
		baseURL: cfg.ServiceURL,
	}
}

// Master sends audio to the mastering endpoint
func (c *AudioClient) Master(ctx context.Context, req *MasterRequest) (*MasterResponse, error) {
	var result MasterResponse
	if err := c.post(ctx, "/master", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Encode sends audio to the encoding endpoint
func (c *AudioClient) Encode(ctx context.Context, req *EncodeRequest) (*EncodeResponse, error) {
	var result EncodeResponse
	if err := c.post(ctx, "/encode", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateZip creates a ZIP archive from multiple files
func (c *AudioClient) CreateZip(ctx context.Context, req *ZipRequest) (*ZipResponse, error) {
	var result ZipResponse
	if err := c.post(ctx, "/zip", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HealthCheck checks if the audio service is available
func (c *AudioClient) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("audio service unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// post sends a POST request with JSON body and parses the response
func (c *AudioClient) post(ctx context.Context, endpoint string, body interface{}, result interface{}) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("audio service error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

// IsConfigured returns true if the client has valid configuration
func (c *AudioClient) IsConfigured() bool {
	return c.baseURL != ""
}
