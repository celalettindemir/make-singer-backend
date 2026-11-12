package service

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/model"
)

// TestLyricsLive_TurkishCues writes a Turkish pop chorus and verse with the
// real OpenAI client and logs the lines with their cues. Paid manual gate:
// skipped unless LYRICS_LIVE=1 and OPENAI_API_KEY are set.
func TestLyricsLive_TurkishCues(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if os.Getenv("LYRICS_LIVE") != "1" || key == "" {
		t.Skip("set LYRICS_LIVE=1 and OPENAI_API_KEY to run the live lyrics sample")
	}
	cfg := &config.OpenAIConfig{
		APIKey: key, BaseURL: "https://api.openai.com/v1", Model: "gpt-6.1-sol",
		MaxTokens: 8000, ReasoningEffort: "medium", TemperatureGenerate: 0.8, TemperatureRewrite: 0.6,
	}
	if m := os.Getenv("OPENAI_MODEL"); m != "" {
		cfg.Model = m
	}
	if e := os.Getenv("OPENAI_REASONING_EFFORT"); e != "" {
		cfg.ReasoningEffort = e
	}
	if v := os.Getenv("OPENAI_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxTokens = n
		}
	}
	svc := NewLyricsService(lyricsprovider.Fixed(client.NewOpenAIClient(cfg)))

	for _, section := range []model.SectionType{model.SectionChorus, model.SectionVerse} {
		// The apps write lyrics through the stream path, so sample that one.
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		var lines []model.LyricLine
		n, err := svc.GenerateStream(ctx, &model.LyricsGenerateRequest{
			Genre: model.Genre("pop"), SectionType: section,
			Vibes: []string{"melancholic", "romantic"}, Language: model.LanguageTR,
		}, func(l model.LyricLine) error {
			lines = append(lines, l)
			return nil
		})
		cancel()
		if err != nil {
			t.Fatalf("GenerateStream %s: %v", section, err)
		}
		if n != len(lines) {
			t.Fatalf("%s: stream reported %d lines but emitted %d", section, n, len(lines))
		}
		t.Logf("--- %s (stream)", section)
		for _, line := range lines {
			t.Logf("%s    %v", renderLiveCues(line.Text, line.Cues), line.Cues)
		}
	}
}

// renderLiveCues shows cues readably: a hold triples the syllable vowel, a
// rise puts an up arrow before the word, a breath puts a breve after it.
func renderLiveCues(text string, cues []model.LyricCue) string {
	r := []rune(text)
	isWord := func(c rune) bool { return unicode.IsLetter(c) || c == '\'' || c == '’' }
	wordStart := func(at int) int {
		for at > 0 && isWord(r[at-1]) {
			at--
		}
		return at
	}
	wordEnd := func(at int) int {
		for at < len(r) && isWord(r[at]) {
			at++
		}
		return at
	}
	type edit struct {
		pos int
		ins string
	}
	var edits []edit
	for _, c := range cues {
		if c.At < 0 || c.At >= len(r) {
			continue
		}
		switch c.Kind {
		case model.CueHold:
			edits = append(edits, edit{c.At + 1, strings.Repeat(string(r[c.At]), 2)})
		case model.CueRise:
			edits = append(edits, edit{wordStart(c.At), "↑"})
		case model.CueBreath:
			edits = append(edits, edit{wordEnd(c.At), "˘"})
		}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].pos > edits[j].pos })
	for _, e := range edits {
		r = append(r[:e.pos], append([]rune(e.ins), r[e.pos:]...)...)
	}
	return string(r)
}
