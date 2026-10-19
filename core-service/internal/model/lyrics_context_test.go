package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

func genReq(ctx LyricsContext) *LyricsGenerateRequest {
	return &LyricsGenerateRequest{Genre: "pop", SectionType: SectionChorus, Vibes: []string{"happy"}, LyricsContext: ctx}
}

func valid(t *testing.T, r *LyricsGenerateRequest) error {
	t.Helper()
	if err := validator.New().Struct(r); err != nil {
		return err
	}
	return r.CheckIndex(r.SectionType)
}

func TestLyricsContext_OldRequestValid(t *testing.T) {
	if err := valid(t, genReq(LyricsContext{})); err != nil {
		t.Fatalf("old request: %v", err)
	}
}

func TestLyricsContext_IndexRequiredWithSong(t *testing.T) {
	r := genReq(LyricsContext{Song: []LyricsSongSection{{Type: SectionChorus}}})
	if err := valid(t, r); err == nil {
		t.Fatal("song without sectionIndex must fail")
	}
	r.SectionIndex = intp(0)
	if err := valid(t, r); err != nil {
		t.Fatalf("with index: %v", err)
	}
}

func TestLyricsContext_IndexOutOfRange(t *testing.T) {
	r := genReq(LyricsContext{Song: []LyricsSongSection{{Type: SectionChorus}}, SectionIndex: intp(1)})
	if err := valid(t, r); err == nil {
		t.Fatal("index out of range must fail")
	}
	r.SectionIndex = intp(-1)
	if err := valid(t, r); err == nil {
		t.Fatal("negative index must fail")
	}
}

func TestLyricsContext_IndexTypeMismatch(t *testing.T) {
	r := genReq(LyricsContext{Song: []LyricsSongSection{{Type: SectionVerse}}, SectionIndex: intp(0)})
	if err := valid(t, r); err == nil {
		t.Fatal("type mismatch must fail")
	}
}

func TestLyricsContext_TopicLimitCountsRunes(t *testing.T) {
	if err := valid(t, genReq(LyricsContext{Topic: strings.Repeat("ş", 500)})); err != nil {
		t.Fatalf("500 runes: %v", err)
	}
	if err := valid(t, genReq(LyricsContext{Topic: strings.Repeat("ş", 501)})); err == nil {
		t.Fatal("501 runes must fail")
	}
}

func TestLyricsContext_SongLimits(t *testing.T) {
	song := make([]LyricsSongSection, 33)
	for i := range song {
		song[i] = LyricsSongSection{Type: SectionChorus}
	}
	if err := valid(t, genReq(LyricsContext{Song: song, SectionIndex: intp(0)})); err == nil {
		t.Error("33 sections must fail")
	}
	bad := []LyricsContext{
		{Song: []LyricsSongSection{{Type: SectionChorus, Bars: intp(65)}}, SectionIndex: intp(0)},
		{Song: []LyricsSongSection{{Type: SectionChorus, Lyrics: strings.Repeat("ğ", 2001)}}, SectionIndex: intp(0)},
		{Song: []LyricsSongSection{{Type: "solo"}}, SectionIndex: intp(0)},
		{BPM: intp(39)},
		{BPM: intp(221)},
	}
	for i, ctx := range bad {
		if err := valid(t, genReq(ctx)); err == nil {
			t.Errorf("case %d must fail", i)
		}
	}
	ok := LyricsContext{Song: []LyricsSongSection{{Type: SectionChorus, Bars: intp(64), Lyrics: strings.Repeat("ğ", 2000)}}, SectionIndex: intp(0), BPM: intp(220)}
	if err := valid(t, genReq(ok)); err != nil {
		t.Errorf("limits exactly: %v", err)
	}
}

func TestLyricsContext_EmbeddedJSONIsFlat(t *testing.T) {
	r := &LyricsRewriteRequest{}
	if err := jsonUnmarshal(`{"currentLyrics":"a","genre":"pop","sectionType":"verse","vibes":["x"],"topic":"t","bpm":96,"song":[{"type":"verse","bars":8,"lyrics":"a"}],"sectionIndex":0}`, r); err != nil {
		t.Fatal(err)
	}
	if r.Topic != "t" || *r.BPM != 96 || len(r.Song) != 1 || *r.SectionIndex != 0 || *r.Song[0].Bars != 8 {
		t.Errorf("decoded = %+v", r)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
