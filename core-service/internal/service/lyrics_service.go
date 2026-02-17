package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// LyricsGenerator defines the interface for lyrics generation
type LyricsGenerator interface {
	Generate(ctx context.Context, req *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error)
	Rewrite(ctx context.Context, req *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error)
}

// LyricsService handles lyrics generation and rewriting using Groq AI
type LyricsService struct {
	groqClient *client.GroqClient
}

// NewLyricsService creates a new lyrics service with Groq client
func NewLyricsService(groqClient *client.GroqClient) *LyricsService {
	return &LyricsService{
		groqClient: groqClient,
	}
}

// Generate creates new lyrics based on the given parameters
func (s *LyricsService) Generate(ctx context.Context, req *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	language := req.Language
	if language == "" {
		language = model.LanguageEN
	}

	// Use mock response if client is not configured
	if s.groqClient == nil || !s.groqClient.IsConfigured() {
		return s.generateMock(req)
	}

	systemPrompt := s.buildSystemPrompt(language)
	userPrompt := s.buildGeneratePrompt(req, language)

	response, err := s.groqClient.ChatCompletion(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("AI generation failed: %w", err)
	}

	drafts, err := s.parseGenerateResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return &model.LyricsGenerateResponse{
		Drafts: drafts,
	}, nil
}

// Rewrite rewrites existing lyrics based on the given parameters
func (s *LyricsService) Rewrite(ctx context.Context, req *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	// Use mock response if client is not configured
	if s.groqClient == nil || !s.groqClient.IsConfigured() {
		return s.rewriteMock(req)
	}

	systemPrompt := s.buildSystemPrompt(model.LanguageEN)
	userPrompt := s.buildRewritePrompt(req)

	response, err := s.groqClient.ChatCompletion(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("AI rewrite failed: %w", err)
	}

	lines, err := s.parseRewriteResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return &model.LyricsRewriteResponse{
		Lines: lines,
	}, nil
}

func (s *LyricsService) buildSystemPrompt(language model.Language) string {
	langName := "English"
	switch language {
	case model.LanguageTR:
		langName = "Turkish"
	case model.LanguageFR:
		langName = "French"
	}

	return fmt.Sprintf(`You are a professional %s songwriter with expertise in various music genres.
Your task is to write compelling, emotionally resonant lyrics that match the requested style and mood.
Always output your response as valid JSON in the exact format requested.
Do not include any text outside the JSON structure.`, langName)
}

func (s *LyricsService) buildGeneratePrompt(req *model.LyricsGenerateRequest, language model.Language) string {
	vibes := strings.Join(req.Vibes, ", ")

	return fmt.Sprintf(`Generate lyrics for a %s song's %s section.
Vibes/mood: %s
Language: %s

Create 2 different draft versions. Each draft should have 4-8 lines that fit the section type.
For a verse: tell a story or set the scene.
For a chorus: create a memorable, singable hook.
For a bridge: provide contrast or a new perspective.
For other sections: follow conventions of that section type.

Output as JSON: {"drafts": [["line1","line2","line3","line4"], ["line1","line2","line3","line4"]]}`,
		req.Genre, req.SectionType, vibes, language)
}

func (s *LyricsService) buildRewritePrompt(req *model.LyricsRewriteRequest) string {
	vibes := strings.Join(req.Vibes, ", ")

	instructions := ""
	if req.Instructions != "" {
		instructions = fmt.Sprintf("\nSpecific instructions: %s", req.Instructions)
	}

	return fmt.Sprintf(`Rewrite the following lyrics for a %s song's %s section.
Current vibes/mood: %s%s

Current lyrics:
%s

Keep the general meaning but improve the flow, rhyming, and emotional impact.
Maintain the same number of lines.

Output as JSON: {"lines": ["line1","line2","line3","line4"]}`,
		req.Genre, req.SectionType, vibes, instructions, req.CurrentLyrics)
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
