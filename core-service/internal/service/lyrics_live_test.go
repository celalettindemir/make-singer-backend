package service

import (
	"context"
	"os"
	"sort"
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
		MaxTokens: 4000, ReasoningEffort: "medium", TemperatureGenerate: 0.8, TemperatureRewrite: 0.6,
	}
	if m := os.Getenv("OPENAI_MODEL"); m != "" {
		cfg.Model = m
	}
	if e := os.Getenv("OPENAI_REASONING_EFFORT"); e != "" {
		cfg.ReasoningEffort = e
	}
	svc := NewLyricsService(lyricsprovider.Fixed(client.NewOpenAIClient(cfg)))

	for _, section := range []model.SectionType{model.SectionChorus, model.SectionVerse} {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		res, err := svc.Generate(ctx, &model.LyricsGenerateRequest{
			Genre: model.Genre("pop"), SectionType: section,
			Vibes: []string{"melancholic", "romantic"}, Language: model.LanguageTR,
		})
		cancel()
		if err != nil {
			t.Fatalf("Generate %s: %v", section, err)
		}
		if len(res.Cues) != len(res.Drafts) {
			t.Fatalf("%s: %d drafts but %d cue lists", section, len(res.Drafts), len(res.Cues))
		}
		for d, lines := range res.Drafts {
			if len(res.Cues[d]) != len(lines) {
				t.Fatalf("%s draft %d: %d lines but %d cue lists", section, d, len(lines), len(res.Cues[d]))
			}
			t.Logf("--- %s draft %d", section, d+1)
			for i, line := range lines {
				t.Logf("%s    %v", renderLiveCues(line, res.Cues[d][i]), res.Cues[d][i])
			}
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
