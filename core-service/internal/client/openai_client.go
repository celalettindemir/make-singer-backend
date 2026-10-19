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

// OpenAIClient calls OpenAI's chat completions API in JSON mode.
type OpenAIClient struct {
	httpClient      *http.Client
	baseURL         string
	apiKey          string
	model           string
	maxTokens       int
	reasoningEffort string

	temperatureGenerate float64
	temperatureRewrite  float64
}

var _ ChatClient = (*OpenAIClient)(nil)

// openAIRequest omits temperature on purpose: OpenAI's reasoning models
// reject any value but the default.
type openAIRequest struct {
	Model               string          `json:"model"`
	Messages            []ChatMessage   `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ResponseFormat      *ResponseFormat `json:"response_format"`
}

// NewOpenAIClient creates an OpenAI client.
func NewOpenAIClient(cfg *config.OpenAIConfig) *OpenAIClient {
	return &OpenAIClient{
		httpClient:          &http.Client{Timeout: 60 * time.Second},
		baseURL:             cfg.BaseURL,
		apiKey:              cfg.APIKey,
		model:               cfg.Model,
		maxTokens:           cfg.MaxTokens,
		reasoningEffort:     cfg.ReasoningEffort,
		temperatureGenerate: cfg.TemperatureGenerate,
		temperatureRewrite:  cfg.TemperatureRewrite,
	}
}

// Temperatures returns the configured temperatures. They are not sent (see
// openAIRequest); they only keep the ChatClient contract.
func (c *OpenAIClient) Temperatures() (generate, rewrite float64) {
	return c.temperatureGenerate, c.temperatureRewrite
}

// IsConfigured returns true if the client has an API key.
func (c *OpenAIClient) IsConfigured() bool {
	return c.apiKey != ""
}

// ChatCompletion sends one JSON-mode request and returns the first choice.
func (c *OpenAIClient) ChatCompletion(ctx context.Context, system, user string, _ float64) (string, error) {
	bodyBytes, err := json.Marshal(openAIRequest{
		Model:               c.model,
		Messages:            []ChatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		MaxCompletionTokens: c.maxTokens,
		ReasoningEffort:     c.reasoningEffort,
		ResponseFormat:      &ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return chatResp.Choices[0].Message.Content, nil
}
