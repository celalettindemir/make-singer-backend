package service

import (
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func TestWriteHumLines_ListsTrimmedLines(t *testing.T) {
	var b strings.Builder
	writeHumLines(&b, []string{" Oy şeftaşım ", "", "Sarı sarı"})
	got := b.String()
	if !strings.Contains(got, "The user hummed the song's melody and sang these words in it:\n  Oy şeftaşım\n  Sarı sarı\n") {
		t.Errorf("output = %q", got)
	}
	if !strings.HasSuffix(got, "Use the words themselves only in the chorus.\n") {
		t.Errorf("output tail = %q", got)
	}
}

func TestWriteHumLines_EmptyWritesNothing(t *testing.T) {
	for _, lines := range [][]string{nil, {"  "}} {
		var b strings.Builder
		writeHumLines(&b, lines)
		if b.Len() != 0 {
			t.Errorf("lines %q wrote %q", lines, b.String())
		}
	}
}

func TestBuildGeneratePrompt_IncludesHumLines(t *testing.T) {
	ctx := model.LyricsContext{HumLines: []string{"Oy şeftaşım"}}
	if p := buildGeneratePrompt(genWith(ctx, model.SectionChorus), model.LanguageTR); !strings.Contains(p, "The user hummed") {
		t.Errorf("prompt has no hum block:\n%s", p)
	}
	if p := buildGeneratePrompt(genWith(model.LyricsContext{}, model.SectionChorus), model.LanguageTR); strings.Contains(p, "hummed") {
		t.Errorf("prompt without humLines mentions the hum")
	}
}

func TestBuildRewritePrompt_IncludesHumLines(t *testing.T) {
	req := &model.LyricsRewriteRequest{Genre: "pop", SectionType: model.SectionChorus, Vibes: []string{"sad"}, CurrentLyrics: "x",
		LyricsContext: model.LyricsContext{HumLines: []string{"la la"}}}
	if p := buildRewritePrompt(req, model.LanguageTR); !strings.Contains(p, "The user hummed") {
		t.Errorf("rewrite prompt has no hum block")
	}
}
