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

// GroqClient handles communication with Groq API
type GroqClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	maxTokens  int

	temperatureGenerate float64
	temperatureRewrite  float64
}

// ChatMessage represents a message in the chat completion request
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest represents the request body for chat completion
type ChatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat turns on Groq's JSON mode. json_object guarantees valid JSON
// syntax, not the schema; the schema is described in the prompt.
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatCompletionResponse represents the response from chat completion
type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// NewGroqClient creates a new Groq API client
func NewGroqClient(cfg *config.GroqConfig) *GroqClient {
	return &GroqClient{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,

		maxTokens:           cfg.MaxTokens,
		temperatureGenerate: cfg.TemperatureGenerate,
		temperatureRewrite:  cfg.TemperatureRewrite,
	}
}

// Temperatures returns the configured temperatures for generation and rewriting.
func (c *GroqClient) Temperatures() (generate, rewrite float64) {
	return c.temperatureGenerate, c.temperatureRewrite
}

// ChatCompletion sends a chat completion request to Groq in JSON mode
func (c *GroqClient) ChatCompletion(ctx context.Context, system, user string, temperature float64) (string, error) {
	messages := []ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	reqBody := ChatCompletionRequest{
		Model:          c.model,
		Messages:       messages,
		Temperature:    temperature,
		MaxTokens:      c.maxTokens,
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	}

	bodyBytes, err := json.Marshal(reqBody)
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
		return "", fmt.Errorf("groq API error (status %d): %s", resp.StatusCode, string(respBody))
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

// IsConfigured returns true if the client has valid configuration
func (c *GroqClient) IsConfigured() bool {
	return c.apiKey != ""
}
