package client

import "context"

// ChatClient is one AI provider that turns a system and a user prompt into
// the model's text answer. The lyrics service asks for JSON in the prompt;
// each client uses its provider's JSON mode where it has one.
type ChatClient interface {
	ChatCompletion(ctx context.Context, system, user string, temperature float64) (string, error)
	// ChatStream asks for plain text and calls onText with each text delta in
	// order. It returns onText's error unchanged and stops the provider call.
	ChatStream(ctx context.Context, system, user string, temperature float64, onText func(text string) error) error
	// Temperatures returns the configured temperatures for generation and rewriting.
	Temperatures() (generate, rewrite float64)
	IsConfigured() bool
}

var _ ChatClient = (*GroqClient)(nil)
