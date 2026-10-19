package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/model"
)

// LyricsGenerator defines the interface for lyrics generation
type LyricsGenerator interface {
	Generate(ctx context.Context, req *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error)
	Rewrite(ctx context.Context, req *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error)
	// IsMock reports whether the generator answers with canned text instead
	// of calling the AI provider.
	IsMock() bool
}

// ClientSource hands out the AI client for one request (R-LYR-11).
type ClientSource interface {
	Active(ctx context.Context) (lyricsprovider.Name, client.ChatClient)
}

// LyricsService handles lyrics generation and rewriting with the active AI provider.
type LyricsService struct {
	source ClientSource
}

// NewLyricsService creates a lyrics service over a provider source.
func NewLyricsService(source ClientSource) *LyricsService {
	return &LyricsService{source: source}
}

// IsMock reports whether no AI provider is configured, so Generate and
// Rewrite answer with canned text.
func (s *LyricsService) IsMock() bool {
	_, c := s.source.Active(context.Background())
	return c == nil || !c.IsConfigured()
}

// Generate creates new lyrics based on the given parameters
func (s *LyricsService) Generate(ctx context.Context, req *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	language := req.Language
	if language == "" {
		language = model.LanguageEN
	}

	name, c := s.source.Active(ctx)
	if c == nil || !c.IsConfigured() {
		return s.generateMock(req)
	}

	temperature, _ := c.Temperatures()
	response, err := c.ChatCompletion(ctx, buildSystemPrompt(language), buildGeneratePrompt(req, language), temperature)
	if err != nil {
		return nil, fmt.Errorf("AI generation failed (%s): %w", name, err)
	}

	drafts, err := s.parseGenerateResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response (%s): %w", name, err)
	}

	return &model.LyricsGenerateResponse{
		Drafts: drafts,
	}, nil
}

// Rewrite rewrites existing lyrics based on the given parameters
func (s *LyricsService) Rewrite(ctx context.Context, req *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	name, c := s.source.Active(ctx)
	if c == nil || !c.IsConfigured() {
		return s.rewriteMock(req)
	}

	language := req.Language
	if language == "" {
		language = model.LanguageEN
	}

	_, temperature := c.Temperatures()
	response, err := c.ChatCompletion(ctx, buildSystemPrompt(language), buildRewritePrompt(req, language), temperature)
	if err != nil {
		return nil, fmt.Errorf("AI rewrite failed (%s): %w", name, err)
	}

	lines, err := s.parseRewriteResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response (%s): %w", name, err)
	}

	return &model.LyricsRewriteResponse{
		Lines: lines,
	}, nil
}

func (s *LyricsService) parseGenerateResponse(response string) ([][]string, error) {
	// Try to extract JSON from the response
	response = extractJSON(response)

	var result struct {
		Drafts [][]string `json:"drafts"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	if len(result.Drafts) == 0 {
		return nil, fmt.Errorf("no drafts in response")
	}

	return result.Drafts, nil
}

func (s *LyricsService) parseRewriteResponse(response string) ([]string, error) {
	response = extractJSON(response)

	var result struct {
		Lines []string `json:"lines"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	if len(result.Lines) == 0 {
		return nil, fmt.Errorf("no lines in response")
	}

	return result.Lines, nil
}

// extractJSON attempts to extract JSON from a response that may contain extra text
func extractJSON(s string) string {
	// Find the first { and last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")

	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return s
}

// Mock implementations for development/testing
func (s *LyricsService) generateMock(req *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	drafts := [][]string{
		{
			"Walking through the city lights",
			"Feeling like we own the night",
			"Nothing's gonna bring us down",
			"We're the kings without a crown",
		},
		{
			"Stars are shining up above",
			"This is what we're dreaming of",
			"Every moment feels so right",
			"Dancing till the morning light",
		},
	}

	return &model.LyricsGenerateResponse{
		Drafts: drafts,
	}, nil
}

func (s *LyricsService) rewriteMock(req *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	lines := []string{
		"Wandering through the silent night",
		"Memories fading from my sight",
		"Tears falling like the rain",
		"Searching for what will remain",
	}

	return &model.LyricsRewriteResponse{
		Lines: lines,
	}, nil
}
