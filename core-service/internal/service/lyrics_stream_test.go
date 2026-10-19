package service

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/model"
)

// streamClient is a ChatClient that streams the given chunks.
type streamClient struct {
	chunks []string
	err    error
}

func (c *streamClient) ChatCompletion(context.Context, string, string, float64) (string, error) {
	return "", nil
}

func (c *streamClient) ChatStream(ctx context.Context, _, _ string, _ float64, onText func(string) error) error {
	for _, ch := range c.chunks {
		if err := onText(ch); err != nil {
			return err
		}
	}
	return c.err
}

func (c *streamClient) Temperatures() (float64, float64) { return 0.8, 0.6 }
func (c *streamClient) IsConfigured() bool               { return true }

var _ client.ChatClient = (*streamClient)(nil)

func gather() (*[]string, func(string) error) {
	var got []string
	return &got, func(l string) error { got = append(got, l); return nil }
}

func TestStreamPrompt_NoJSONOneDraft(t *testing.T) {
	g := genWith(contextSong(), model.SectionChorus)
	r := &model.LyricsRewriteRequest{CurrentLyrics: "NEW LINE", Genre: "pop", SectionType: model.SectionChorus,
		Vibes: []string{"sad"}, LyricsContext: contextSong()}
	min, max := targetLines(g.SectionType, sectionBars(g.LyricsContext))
	for name, p := range map[string]string{
		"system":   buildStreamSystemPrompt(model.LanguageTR),
		"generate": buildStreamGeneratePrompt(g, model.LanguageTR),
		"rewrite":  buildStreamRewritePrompt(r, model.LanguageTR),
	} {
		if strings.Contains(p, `"drafts"`) || strings.Contains(p, "JSON") || strings.Contains(p, `"lines"`) {
			t.Errorf("%s prompt mentions JSON:\n%s", name, p)
		}
	}
	gp := buildStreamGeneratePrompt(g, model.LanguageTR)
	for _, want := range []string{topicHeader, targetMark, lineRange(min, max)} {
		if !strings.Contains(gp, want) {
			t.Errorf("generate prompt lacks %q", want)
		}
	}
	if strings.Contains(gp, "2 different") {
		t.Error("generate prompt still asks for 2 drafts")
	}
	rp := buildStreamRewritePrompt(r, model.LanguageTR)
	for _, want := range []string{topicHeader, targetMark, "NEW LINE"} {
		if !strings.Contains(rp, want) {
			t.Errorf("rewrite prompt lacks %q", want)
		}
	}
	if !strings.Contains(buildStreamSystemPrompt(model.LanguageTR), "Output only the lyric lines, one per line, plain text.") {
		t.Error("system prompt lacks the plain text rule")
	}
}

func TestGenerateStream_EmitsCleanLines(t *testing.T) {
	s := NewLyricsService(lyricsprovider.Fixed(&streamClient{chunks: []string{"[Verse]\n1. Ge", "ce\nŞehir"}}))
	got, on := gather()
	n, err := s.GenerateStream(context.Background(), genWith(model.LyricsContext{}, model.SectionVerse), on)
	if err != nil || n != 2 || !reflect.DeepEqual(*got, []string{"Gece", "Şehir"}) {
		t.Fatalf("n=%d err=%v got=%v", n, err, *got)
	}
}

func TestRewriteStream_EmitsLines(t *testing.T) {
	s := NewLyricsService(lyricsprovider.Fixed(&streamClient{chunks: []string{"bir\niki\n"}}))
	got, on := gather()
	n, err := s.RewriteStream(context.Background(), &model.LyricsRewriteRequest{CurrentLyrics: "x", Genre: "pop", SectionType: model.SectionVerse}, on)
	if err != nil || n != 2 || !reflect.DeepEqual(*got, []string{"bir", "iki"}) {
		t.Fatalf("n=%d err=%v got=%v", n, err, *got)
	}
}

func TestGenerateStream_StopsAt16(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "satir %d\n", i)
	}
	s := NewLyricsService(lyricsprovider.Fixed(&streamClient{chunks: []string{sb.String()}}))
	got, on := gather()
	n, err := s.GenerateStream(context.Background(), genWith(model.LyricsContext{}, model.SectionVerse), on)
	if err != nil || n != maxStreamLines || len(*got) != maxStreamLines {
		t.Fatalf("n=%d err=%v lines=%d", n, err, len(*got))
	}
}

func TestGenerateStream_ProviderErrorKeepsCount(t *testing.T) {
	boom := fmt.Errorf("boom")
	s := NewLyricsService(lyricsprovider.Fixed(&streamClient{chunks: []string{"bir\n"}, err: boom}))
	got, on := gather()
	n, err := s.GenerateStream(context.Background(), genWith(model.LyricsContext{}, model.SectionVerse), on)
	if err == nil || n != 1 || len(*got) != 1 {
		t.Fatalf("n=%d err=%v got=%v", n, err, *got)
	}
}

func TestGenerateStream_MockStreams(t *testing.T) {
	s := NewLyricsService(lyricsprovider.Fixed(nil))
	s.mockDelay = time.Millisecond
	got, on := gather()
	n, err := s.GenerateStream(context.Background(), genWith(model.LyricsContext{}, model.SectionVerse), on)
	if err != nil || n != 4 || len(*got) != 4 {
		t.Fatalf("n=%d err=%v got=%v", n, err, *got)
	}
	got, on = gather()
	n, err = s.RewriteStream(context.Background(), &model.LyricsRewriteRequest{}, on)
	if err != nil || n != 4 || len(*got) != 4 {
		t.Fatalf("rewrite n=%d err=%v", n, err)
	}
}

func TestGenerateStream_MockStopsOnCancel(t *testing.T) {
	s := NewLyricsService(lyricsprovider.Fixed(nil))
	s.mockDelay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := s.GenerateStream(ctx, genWith(model.LyricsContext{}, model.SectionVerse), func(string) error { return nil })
	if err == nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
