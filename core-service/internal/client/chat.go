package client

import "context"

// ChatClient is one AI provider that turns a system and a user prompt into
// the model's text answer. The lyrics service asks for JSON in the prompt;
// each client uses its provider's JSON mode where it has one.
type ChatClient interface {
	ChatCompletion(ctx context.Context, system, user string, temperature float64) (string, error)
	// Temperatures returns the configured temperatures for generation and rewriting.
	Temperatures() (generate, rewrite float64)
	IsConfigured() bool
}

var _ ChatClient = (*GroqClient)(nil)
