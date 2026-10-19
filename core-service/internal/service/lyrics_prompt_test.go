package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/model"
)

func ip(v int) *int { return &v }

func contextSong() model.LyricsContext {
	return model.LyricsContext{
		Topic: "Eski sevgiliye, özlem dolu",
		BPM:   ip(96),
		Song: []model.LyricsSongSection{
			{Type: model.SectionVerse, Bars: ip(16), Lyrics: "Eski sokakta yürüdüm\nKapında durdum"},
			{Type: model.SectionChorus, Bars: ip(8)},
			{Type: model.SectionVerse, Bars: ip(16)},
		},
		SectionIndex: ip(1),
	}
}

func genWith(ctx model.LyricsContext, section model.SectionType) *model.LyricsGenerateRequest {
	return &model.LyricsGenerateRequest{Genre: "pop", SectionType: section, Vibes: []string{"sad"}, Language: model.LanguageTR, LyricsContext: ctx}
}

func TestPrompt_NoContextHasNoSongBlock(t *testing.T) {
	p := buildGeneratePrompt(genWith(model.LyricsContext{}, model.SectionVerse), model.LanguageTR)
	if strings.Contains(p, songBlockHeader) || strings.Contains(p, topicHeader) {
		t.Errorf("old request must not get context blocks:\n%s", p)
	}
	if !strings.Contains(p, "Write ALL lyrics in Turkish") || !strings.Contains(p, "plain strings") {
		t.Errorf("old lines missing:\n%s", p)
	}
}

func TestPrompt_TopicIncluded(t *testing.T) {
	p := buildGeneratePrompt(genWith(contextSong(), model.SectionChorus), model.LanguageTR)
	if !strings.Contains(p, topicHeader) || !strings.Contains(p, "Eski sevgiliye, özlem dolu") {
		t.Errorf("topic missing:\n%s", p)
	}
	if !strings.Contains(p, "96 BPM") {
		t.Errorf("bpm missing:\n%s", p)
	}
}

func TestPrompt_SongBlockListsOtherSectionsAndMarksTarget(t *testing.T) {
	p := buildGeneratePrompt(genWith(contextSong(), model.SectionChorus), model.LanguageTR)
	for _, want := range []string{songBlockHeader, "1. verse (16 bars)", "Eski sokakta yürüdüm", "2. chorus (8 bars)", targetMark, "3. verse (16 bars)", emptyMark} {
		if !strings.Contains(p, want) {
			t.Errorf("song block lacks %q:\n%s", want, p)
		}
	}
	if strings.Index(p, targetMark) < strings.Index(p, "2. chorus") || strings.Index(p, targetMark) > strings.Index(p, "3. verse") {
		t.Errorf("target mark must be on the chorus line:\n%s", p)
	}
}

func TestPrompt_RewriteSkipsTargetSongLyrics(t *testing.T) {
	ctx := contextSong()
	ctx.Song[1].Lyrics = "OLD LINE"
	p := buildRewritePrompt(&model.LyricsRewriteRequest{CurrentLyrics: "NEW LINE", Genre: "pop", SectionType: model.SectionChorus,
		Vibes: []string{"sad"}, Language: model.LanguageTR, LyricsContext: ctx}, model.LanguageTR)
	if strings.Contains(p, "OLD LINE") || !strings.Contains(p, "NEW LINE") {
		t.Errorf("rewrite must use currentLyrics only:\n%s", p)
	}
}

func TestPrompt_TrimDropsFarthestFirst(t *testing.T) {
	ctx := model.LyricsContext{SectionIndex: ip(0)}
	for i := 0; i < 5; i++ {
		ctx.Song = append(ctx.Song, model.LyricsSongSection{Type: model.SectionVerse, Lyrics: strings.Repeat(string(rune('a'+i)), 2500)})
	}
	block := songBlock(ctx, false)
	if strings.Contains(block, strings.Repeat("e", 2500)) || strings.Contains(block, strings.Repeat("d", 2500)) {
		t.Error("the farthest sections must be dropped first")
	}
	if !strings.Contains(block, strings.Repeat("b", 2500)) || !strings.Contains(block, strings.Repeat("c", 2500)) {
		t.Error("the nearest sections must stay")
	}
	if utf8.RuneCountInString(block) > maxContextChars+2000 {
		t.Errorf("block too long: %d runes", utf8.RuneCountInString(block))
	}
}

func TestPrompt_TrimTiesDropLaterSectionFirst(t *testing.T) {
	ctx := model.LyricsContext{SectionIndex: ip(1)}
	for _, l := range []string{"x", "", "y"} {
		ctx.Song = append(ctx.Song, model.LyricsSongSection{Type: model.SectionVerse, Lyrics: strings.Repeat(l, 4500)})
	}
	ctx.Song[1].Type = model.SectionChorus
	block := songBlock(ctx, false)
	if !strings.Contains(block, strings.Repeat("x", 4500)) || strings.Contains(block, strings.Repeat("y", 4500)) {
		t.Error("on a tie the later section is dropped first")
	}
}

func TestPrompt_SecondChorusRepeatsFilledChorus(t *testing.T) {
	ctx := model.LyricsContext{
		Song: []model.LyricsSongSection{
			{Type: model.SectionChorus, Lyrics: "Gel desem gelmezsin"},
			{Type: model.SectionVerse},
			{Type: model.SectionChorus},
		},
		SectionIndex: ip(2),
	}
	p := buildGeneratePrompt(genWith(ctx, model.SectionChorus), model.LanguageTR)
	if !strings.Contains(p, repeatChorusRule) {
		t.Errorf("second chorus rule missing:\n%s", p)
	}
	first := buildGeneratePrompt(genWith(contextSong(), model.SectionChorus), model.LanguageTR)
	if strings.Contains(first, repeatChorusRule) {
		t.Error("first chorus must not get the repeat rule")
	}
}

func TestPrompt_TargetLinesFromBars(t *testing.T) {
	cases := []struct {
		section  model.SectionType
		bars     *int
		min, max int
	}{
		{model.SectionVerse, ip(8), 4, 4},
		{model.SectionVerse, ip(2), 2, 2},
		{model.SectionVerse, ip(64), 12, 12},
		{model.SectionVerse, nil, 6, 8},
		{model.SectionChorus, nil, 4, 6},
		{model.SectionIntro, nil, 2, 4},
		{model.SectionBridge, nil, 4, 4},
		{model.SectionInstrumental, nil, 4, 8},
	}
	for _, c := range cases {
		if mn, mx := targetLines(c.section, c.bars); mn != c.min || mx != c.max {
			t.Errorf("targetLines(%s, %v) = %d-%d, want %d-%d", c.section, c.bars, mn, mx, c.min, c.max)
		}
	}
	p := buildGeneratePrompt(genWith(contextSong(), model.SectionChorus), model.LanguageTR)
	if !strings.Contains(p, "4 lines") {
		t.Errorf("8-bar chorus must ask for 4 lines:\n%s", p)
	}
}

func TestPrompt_TurkishGuidance(t *testing.T) {
	if !strings.Contains(buildSystemPrompt(model.LanguageTR), turkishGuidance) {
		t.Error("tr system prompt lacks the Turkish guidance")
	}
	if strings.Contains(buildSystemPrompt(model.LanguageEN), turkishGuidance) {
		t.Error("en system prompt must not carry the Turkish guidance")
	}
}
