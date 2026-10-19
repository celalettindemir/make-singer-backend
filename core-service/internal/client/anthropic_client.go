package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/makeasinger/api/internal/config"
)

// anthropicFallbackBeta turns on server-side refusal fallbacks: a request the
// model's safeguards decline is re-served by a fallback model in the same call.
const anthropicFallbackBeta = "server-side-fallback-2026-07-01"

// AnthropicClient calls Anthropic's Messages API through the official SDK.
type AnthropicClient struct {
	client    anthropic.Client
	stream    anthropic.Client // no request timeout: the deadline comes from ctx
	apiKey    string
	model     string
	maxTokens int
	effort    string

	temperatureGenerate float64
	temperatureRewrite  float64
}

var _ ChatClient = (*AnthropicClient)(nil)

// NewAnthropicClient creates an Anthropic client. The SDK does not retry:
// the lyrics endpoints are not retried automatically (R-LYR-07).
func NewAnthropicClient(cfg *config.AnthropicConfig) *AnthropicClient {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(60 * time.Second),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	streamOpts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(0)}
	if cfg.BaseURL != "" {
		streamOpts = append(streamOpts, option.WithBaseURL(cfg.BaseURL))
	}
	return &AnthropicClient{
		client:              anthropic.NewClient(opts...),
		stream:              anthropic.NewClient(streamOpts...),
		apiKey:              cfg.APIKey,
		model:               cfg.Model,
		maxTokens:           cfg.MaxTokens,
		effort:              cfg.Effort,
		temperatureGenerate: cfg.TemperatureGenerate,
		temperatureRewrite:  cfg.TemperatureRewrite,
	}
}

// Temperatures returns the configured temperatures. They are not sent:
// current Claude models reject non-default sampling parameters.
func (c *AnthropicClient) Temperatures() (generate, rewrite float64) {
	return c.temperatureGenerate, c.temperatureRewrite
}

// IsConfigured returns true if the client has an API key.
func (c *AnthropicClient) IsConfigured() bool {
	return c.apiKey != ""
}

// params builds the request shared by ChatCompletion and ChatStream.
func (c *AnthropicClient) params(system, user string) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(c.maxTokens),
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	}
	if c.effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(c.effort)}
	}
	return params
}

// fallbackOpts turns on the server-side refusal fallback for one request.
func fallbackOpts() []option.RequestOption {
	return []option.RequestOption{
		option.WithHeaderAdd("anthropic-beta", anthropicFallbackBeta),
		option.WithJSONSet("fallbacks", "default"),
	}
}

// ChatCompletion sends one message and returns its text blocks joined. A
// refusal (after the server-side fallback) is an error.
func (c *AnthropicClient) ChatCompletion(ctx context.Context, system, user string, _ float64) (string, error) {
	msg, err := c.client.Messages.New(ctx, c.params(system, user), fallbackOpts()...)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return "", fmt.Errorf("anthropic API error (status %d): %w", apiErr.StatusCode, err)
		}
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return "", fmt.Errorf("anthropic refused the request (category %q)", msg.StopDetails.Category)
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("no text in response (stop reason %q)", msg.StopReason)
	}
	return b.String(), nil
}

// ChatStream streams the answer's text deltas. Thinking deltas are skipped.
// A refusal (after the server-side fallback) is an error.
func (c *AnthropicClient) ChatStream(ctx context.Context, system, user string, _ float64, onText func(string) error) error {
	stream := c.stream.Messages.NewStreaming(ctx, c.params(system, user), fallbackOpts()...)
	defer stream.Close()
	for stream.Next() {
		switch ev := stream.Current().AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			if td, ok := ev.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
				if err := onText(td.Text); err != nil {
					return err
				}
			}
		case anthropic.MessageDeltaEvent:
			if ev.Delta.StopReason == anthropic.StopReasonRefusal {
				return fmt.Errorf("anthropic refused the request (category %q)", ev.Delta.StopDetails.Category)
			}
		}
	}
	if err := stream.Err(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("stream cancelled: %w", ctx.Err())
		}
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return fmt.Errorf("anthropic API error (status %d): %w", apiErr.StatusCode, err)
		}
		return fmt.Errorf("failed to read stream: %w", err)
	}
	return nil
}
