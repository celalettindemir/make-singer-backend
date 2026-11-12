package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	// GenerateStream writes one draft line by line to onLine and returns how
	// many lines it emitted. An onLine error stops the provider call and is
	// returned.
	GenerateStream(ctx context.Context, req *model.LyricsGenerateRequest, onLine func(model.LyricLine) error) (int, error)
	// RewriteStream is GenerateStream for a rewrite.
	RewriteStream(ctx context.Context, req *model.LyricsRewriteRequest, onLine func(model.LyricLine) error) (int, error)
}

// streamTimeout is the deadline of one provider stream.
const streamTimeout = 60 * time.Second

// ClientSource hands out the AI client for one request (R-LYR-11).
type ClientSource interface {
	Active(ctx context.Context) (lyricsprovider.Name, client.ChatClient)
}

// LyricsService handles lyrics generation and rewriting with the active AI provider.
type LyricsService struct {
	source ClientSource
	// mockDelay is the pause before each line of a mock stream.
	mockDelay time.Duration
}

// NewLyricsService creates a lyrics service over a provider source.
func NewLyricsService(source ClientSource) *LyricsService {
	return &LyricsService{source: source, mockDelay: 300 * time.Millisecond}
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

	return splitDrafts(drafts, language), nil
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

	return splitRewrite(lines, language), nil
}

// splitCues strips the singing marks from raw model lines. Lines with no text
// left are dropped; Lines and Cues stay the same length.
func splitCues(raw []string, language model.Language) ([]string, [][]model.LyricCue) {
	lines, cues := []string{}, [][]model.LyricCue{}
	for _, r := range raw {
		l, ok := parseCueLine(r, language)
		if !ok {
			continue
		}
		lines = append(lines, l.Text)
		cues = append(cues, l.Cues)
	}
	return lines, cues
}

func splitDrafts(raw [][]string, language model.Language) *model.LyricsGenerateResponse {
	res := &model.LyricsGenerateResponse{Drafts: make([][]string, 0, len(raw)), Cues: make([][][]model.LyricCue, 0, len(raw))}
	for _, d := range raw {
		lines, cues := splitCues(d, language)
		res.Drafts = append(res.Drafts, lines)
		res.Cues = append(res.Cues, cues)
	}
	return res
}

func splitRewrite(raw []string, language model.Language) *model.LyricsRewriteResponse {
	lines, cues := splitCues(raw, language)
	return &model.LyricsRewriteResponse{Lines: lines, Cues: cues}
}

// GenerateStream streams one draft. Without a provider it streams the first
// mock draft.
func (s *LyricsService) GenerateStream(ctx context.Context, req *model.LyricsGenerateRequest, onLine func(model.LyricLine) error) (int, error) {
	language := req.Language
	if language == "" {
		language = model.LanguageEN
	}
	name, c := s.source.Active(ctx)
	if c == nil || !c.IsConfigured() {
		mock, _ := s.generateMock(req)
		return s.streamMock(ctx, mockLines(mock.Drafts[0], mock.Cues[0]), onLine)
	}
	temperature, _ := c.Temperatures()
	return streamChat(ctx, name, c, buildStreamSystemPrompt(language), buildStreamGeneratePrompt(req, language), temperature, language, onLine)
}

// RewriteStream streams the rewritten lines.
func (s *LyricsService) RewriteStream(ctx context.Context, req *model.LyricsRewriteRequest, onLine func(model.LyricLine) error) (int, error) {
	language := req.Language
	if language == "" {
		language = model.LanguageEN
	}
	name, c := s.source.Active(ctx)
	if c == nil || !c.IsConfigured() {
		mock, _ := s.rewriteMock(req)
		return s.streamMock(ctx, mockLines(mock.Lines, mock.Cues), onLine)
	}
	_, temperature := c.Temperatures()
	return streamChat(ctx, name, c, buildStreamSystemPrompt(language), buildStreamRewritePrompt(req, language), temperature, language, onLine)
}

// streamChat runs one provider stream under streamTimeout and returns the
// number of lines emitted. Hitting the line limit is a normal end.
func streamChat(ctx context.Context, name lyricsprovider.Name, c client.ChatClient, system, user string, temperature float64, language model.Language, onLine func(model.LyricLine) error) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	split := newLineSplitter(language, onLine)
	err := c.ChatStream(ctx, system, user, temperature, split.Write)
	if err == nil {
		err = split.Close()
	}
	if errors.Is(err, errLineLimit) {
		err = nil
	}
	if err != nil {
		return split.Count(), fmt.Errorf("AI stream failed (%s): %w", name, err)
	}
	return split.Count(), nil
}

func (s *LyricsService) streamMock(ctx context.Context, lines []model.LyricLine, onLine func(model.LyricLine) error) (int, error) {
	for i, l := range lines {
		select {
		case <-ctx.Done():
			return i, ctx.Err()
		case <-time.After(s.mockDelay):
		}
		if err := onLine(l); err != nil {
			return i, err
		}
	}
	return len(lines), nil
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

func mockLines(lines []string, cues [][]model.LyricCue) []model.LyricLine {
	out := make([]model.LyricLine, len(lines))
	for i, l := range lines {
		out[i] = model.LyricLine{Text: l, Cues: cues[i]}
	}
	return out
}

func mockLanguage(l model.Language) model.Language {
	if l == "" {
		return model.LanguageEN
	}
	return l
}

// Mock implementations for development/testing. The raw lines carry singing
// marks so the preview shows cues.
func (s *LyricsService) generateMock(req *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	drafts := [][]string{
		{
			"Walking through the city lights",
			"Feeling like we own the night~",
			"^Nothing's gonna bring us down",
			"We're the kings without a crown",
		},
		{
			"Stars are shining up above",
			"This is what we're dreaming of",
			"Every moment feels so right",
			"Dancing till the morning light",
		},
	}

	return splitDrafts(drafts, mockLanguage(req.Language)), nil
}

func (s *LyricsService) rewriteMock(req *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	lines := []string{
		"Wandering through the silent night",
		"Memories fading | from my sight~",
		"Tears falling like the rain",
		"Searching for what will remain",
	}

	return splitRewrite(lines, mockLanguage(req.Language)), nil
}
