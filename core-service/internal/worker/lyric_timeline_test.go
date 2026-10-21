package worker

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// timedWords builds consecutive 0.5s words starting at startS; each word lasts 0.4s.
func timedWords(startS float64, text ...string) []client.AlignedWord {
	var out []client.AlignedWord
	for i, w := range text {
		s := startS + float64(i)*0.5
		out = append(out, client.AlignedWord{Word: w, StartS: s, EndS: s + 0.4, Success: true})
	}
	return out
}

func eq(t *testing.T, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Errorf("want %s, got %s", dump(want), dump(got))
	}
}

func isNil(t *testing.T, got []model.LyricTimelineSection) {
	t.Helper()
	if got != nil {
		t.Errorf("want nil, got %s", dump(got))
	}
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func iptr(v int) *int { return &v }

func TestLyricTimeline_SimpleSong(t *testing.T) {
	sections := []model.GuideSection{
		{ID: "v1", Type: "verse", Lines: []string{"Hello world", "good night"}},
		{ID: "c1", Type: "chorus", Lines: []string{"la la la"}},
	}
	// 0.5s apart, each 0.4s long
	words := timedWords(0, "Hello", "world", "good", "night", "la", "la", "la")

	got := BuildLyricTimeline(sections, words, 10)

	want := []model.LyricTimelineSection{
		{SectionID: "v1", StartMs: 0, EndMs: 2000, Lines: []model.LyricTimelineLine{
			{Index: 0, StartMs: iptr(0), EndMs: iptr(900)},
			{Index: 1, StartMs: iptr(1000), EndMs: iptr(1900)},
		}},
		{SectionID: "c1", StartMs: 2000, EndMs: 10000, Lines: []model.LyricTimelineLine{
			{Index: 0, StartMs: iptr(2000), EndMs: iptr(3400)},
		}},
	}
	eq(t, want, got)
}

func TestLyricTimeline_ExtraChorusAndTagsIgnored(t *testing.T) {
	sections := []model.GuideSection{
		{ID: "v1", Type: "verse", Lines: []string{"one two", "", "three four"}},
		{ID: "c1", Type: "chorus", Lines: []string{"sing along now"}},
	}
	var texts []string
	texts = append(texts, "[Verse]\n", "one", "two", "three", "four", "[Chorus]\n", "sing", "along", "now")
	texts = append(texts, "[Chorus]\n")
	for i := 0; i < 13; i++ { // extra chorus: 14 words with the tag
		texts = append(texts, []string{"sing", "along", "now"}[i%3])
	}
	words := timedWords(1, texts...)

	got := BuildLyricTimeline(sections, words, 30)

	if len(got) != 2 {
		t.Fatalf("got %d sections, want 2: %+v", len(got), got)
	}
	eq(t, "v1", got[0].SectionID)
	eq(t, 1500, got[0].StartMs) // "one" is the 2nd word (tag first at 1.0s)
	eq(t, 4000, got[0].EndMs)   // chorus start
	eq(t, []model.LyricTimelineLine{
		{Index: 0, StartMs: iptr(1500), EndMs: iptr(2400)},
		{Index: 2, StartMs: iptr(2500), EndMs: iptr(3400)}, // original index kept
	}, got[0].Lines)
	eq(t, "c1", got[1].SectionID)
	eq(t, 4000, got[1].StartMs)
	eq(t, 30000, got[1].EndMs)
	eq(t, []model.LyricTimelineLine{
		{Index: 0, StartMs: iptr(4000), EndMs: iptr(5400)},
	}, got[1].Lines)
}

func TestLyricTimeline_TurkishCase(t *testing.T) {
	sections := []model.GuideSection{
		{ID: "v1", Type: "verse", Lines: []string{"IŞIK İstanbul"}},
	}
	words := timedWords(2, "ışık", "istanbul")

	got := BuildLyricTimeline(sections, words, 5)

	if len(got) != 1 {
		t.Fatalf("got %d sections, want 1: %+v", len(got), got)
	}
	eq(t, []model.LyricTimelineLine{
		{Index: 0, StartMs: iptr(2000), EndMs: iptr(2900)},
	}, got[0].Lines)
}

func TestLyricTimeline_MissingLineIsNull(t *testing.T) {
	sections := []model.GuideSection{
		{ID: "v1", Type: "verse", Lines: []string{"alpha beta", "gamma delta", "epsilon zeta"}},
	}
	// Suno skipped the middle line.
	words := timedWords(0, "alpha", "beta", "epsilon", "zeta")

	got := BuildLyricTimeline(sections, words, 4)

	if len(got) != 1 {
		t.Fatalf("got %d sections, want 1: %+v", len(got), got)
	}
	eq(t, []model.LyricTimelineLine{
		{Index: 0, StartMs: iptr(0), EndMs: iptr(900)},
		{Index: 1},
		{Index: 2, StartMs: iptr(1000), EndMs: iptr(1900)},
	}, got[0].Lines)
}

func TestLyricTimeline_Unreliable_Nil(t *testing.T) {
	var sections []model.GuideSection
	for i := 0; i < 4; i++ {
		sections = append(sections, model.GuideSection{
			ID: fmt.Sprintf("s%d", i), Type: "verse", Lines: []string{fmt.Sprintf("word%d other%d", i, i)},
		})
	}
	// Only the first section is in the words.
	words := timedWords(0, "word0", "other0", "zzz", "yyy")

	isNil(t, BuildLyricTimeline(sections, words, 10))
}

func TestLyricTimeline_NoWords_Nil(t *testing.T) {
	sections := []model.GuideSection{{ID: "v1", Type: "verse", Lines: []string{"hello"}}}
	isNil(t, BuildLyricTimeline(sections, nil, 10))
	tags := timedWords(0, "[Verse]", "  ")
	isNil(t, BuildLyricTimeline(sections, tags, 10))
}

func TestLyricTimeline_TagGluedToFirstWord(t *testing.T) {
	sections := []model.GuideSection{
		{ID: "v1", Type: "verse", Lines: []string{"la si"}},
		{ID: "c1", Type: "chorus", Lines: []string{"bir iki"}},
	}
	words := []client.AlignedWord{
		{Word: "[Verse]\nla ", StartS: 1.0, EndS: 1.5, Success: true},
		{Word: "si ", StartS: 1.5, EndS: 2.0, Success: true},
		{Word: "[Chorus]\nbir ", StartS: 3.0, EndS: 3.5, Success: true},
		{Word: "iki", StartS: 3.5, EndS: 4.0, Success: true},
	}

	got := BuildLyricTimeline(sections, words, 6)

	eq(t, []model.LyricTimelineSection{
		{SectionID: "v1", StartMs: 1000, EndMs: 3000, Lines: []model.LyricTimelineLine{
			{Index: 0, StartMs: iptr(1000), EndMs: iptr(2000)},
		}},
		{SectionID: "c1", StartMs: 3000, EndMs: 6000, Lines: []model.LyricTimelineLine{
			{Index: 0, StartMs: iptr(3000), EndMs: iptr(4000)},
		}},
	}, got)
}

func TestLyricTimeline_UnsuccessfulWordSkipped(t *testing.T) {
	sections := []model.GuideSection{{ID: "v1", Type: "verse", Lines: []string{"ay dede"}}}
	words := []client.AlignedWord{
		{Word: "ay", StartS: 0, EndS: 0.5, Success: true},
		{Word: "dede", StartS: 9, EndS: 9.5, Success: false},
		{Word: "dede", StartS: 1, EndS: 1.5, Success: true},
	}

	got := BuildLyricTimeline(sections, words, 3)

	eq(t, []model.LyricTimelineLine{{Index: 0, StartMs: iptr(0), EndMs: iptr(1500)}}, got[0].Lines)
}
