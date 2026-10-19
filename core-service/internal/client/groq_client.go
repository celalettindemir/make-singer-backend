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
	streamHTTP *http.Client // no Timeout: the deadline comes from ctx
	baseURL    string
	apiKey     string
	model      string
	maxTokens  int

	temperatureGenerate float64
	temperatureRewrite  float64

	// retryDelay is the pause between attempts after a json_validate_failed.
	retryDelay time.Duration
}

const (
	// groqJSONValidateFailed is the error code Groq returns with HTTP 400 when
	// the model's JSON-mode output failed Groq's own validation. It is a
	// sampling flake, so the same request usually succeeds on a retry.
	groqJSONValidateFailed = "json_validate_failed"
	// groqJSONRetries is the number of extra attempts after json_validate_failed.
	groqJSONRetries = 2
)

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
	Stream         bool            `json:"stream,omitempty"`
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
		streamHTTP: &http.Client{},
		baseURL:    cfg.BaseURL,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,

		maxTokens:           cfg.MaxTokens,
		temperatureGenerate: cfg.TemperatureGenerate,
		temperatureRewrite:  cfg.TemperatureRewrite,
		retryDelay:          300 * time.Millisecond,
	}
}

// Temperatures returns the configured temperatures for generation and rewriting.
func (c *GroqClient) Temperatures() (generate, rewrite float64) {
	return c.temperatureGenerate, c.temperatureRewrite
}

// isJSONValidateFailed reports whether a Groq error body carries the
// json_validate_failed code.
func isJSONValidateFailed(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &e) == nil && e.Error.Code == groqJSONValidateFailed
}

// ChatCompletion sends a chat completion request to Groq in JSON mode. A 400
// json_validate_failed (the model produced output Groq could not validate) is
// retried up to groqJSONRetries more times; any other failure returns at once.
// The raw Groq body, including failed_generation, is only ever part of the
// returned error, which handlers map to a generic i18n message.
func (c *GroqClient) ChatCompletion(ctx context.Context, system, user string, temperature float64) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= groqJSONRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(c.retryDelay):
			}
		}
		out, retry, err := c.chatCompletionOnce(ctx, system, user, temperature)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return "", lastErr
}

func (c *GroqClient) chatCompletionOnce(ctx context.Context, system, user string, temperature float64) (content string, retry bool, err error) {
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
		return "", false, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", isJSONValidateFailed(resp.StatusCode, respBody), fmt.Errorf("groq API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", false, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", false, fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, false, nil
}

// IsConfigured returns true if the client has valid configuration
func (c *GroqClient) IsConfigured() bool {
	return c.apiKey != ""
}

// ChatStream streams the answer as plain text. There is no JSON mode, so
// there is no json_validate_failed retry.
func (c *GroqClient) ChatStream(ctx context.Context, system, user string, temperature float64, onText func(string) error) error {
	return streamChatCompletions(ctx, c.streamHTTP, c.baseURL+"/chat/completions", c.apiKey, ChatCompletionRequest{
		Model:       c.model,
		Messages:    []ChatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		Temperature: temperature,
		MaxTokens:   c.maxTokens,
		Stream:      true,
	}, onText)
}
